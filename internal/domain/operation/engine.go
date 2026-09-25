package operation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/voocel/ainovel-cli/internal/domain/change"
	"github.com/voocel/ainovel-cli/internal/domain/model"
)

// Executor 是执行器契约（D45）：Identity 是冻结进快照、领取时过滤的身份；
// Execute 按快照自行加载配置并产出统一的 OperationOutcome（D30）。
type Executor interface {
	Identity() string
	Execute(context.Context, model.Operation) (model.OperationOutcome, error)
}

// SemanticComplianceAnalyzer 由 capability 层实现：对 AI 正文做独立于创作
// Worker 的语义合规裁定。接口定义在消费方（Go 惯例），报告类型定义在 domain，
// 二者共同保证生产方无需反向依赖 operation。
type SemanticComplianceAnalyzer interface {
	AnalyzeSemanticCompliance(
		context.Context,
		model.Operation,
		model.Proposal,
		[]model.OwnershipRule,
	) (model.SemanticComplianceReport, error)
}

type Engine struct {
	store    Store
	changes  *change.Engine
	verdicts map[model.OperationKind]VerdictValidator
}

var errLeaseLost = errors.New("operation lease renewal failed")

type RunResult struct {
	Operation model.Operation  `json:"operation"`
	Proposal  model.Proposal   `json:"proposal,omitempty"`
	ChangeSet *model.ChangeSet `json:"change_set,omitempty"`
	Verdict   json.RawMessage  `json:"verdict,omitempty"`
	Artifacts []model.Artifact `json:"artifacts,omitempty"`
}

func NewEngine(authorityStore Store, changes *change.Engine, contracts ...VerdictContract) *Engine {
	e := &Engine{store: authorityStore, changes: changes, verdicts: make(map[model.OperationKind]VerdictValidator)}
	for _, contract := range contracts {
		if _, err := model.KindSpec(contract.Kind); err != nil {
			panic(err)
		}
		if _, exists := e.verdicts[contract.Kind]; exists || contract.Validate == nil {
			panic("invalid or duplicate evidence contract: " + string(contract.Kind))
		}
		e.verdicts[contract.Kind] = contract.Validate
	}
	return e
}

// RunNextWithExecutors 在所有已装配执行器之间按同一队列优先级原子领取，
// 再把任务交给其冻结身份对应的执行器。
func (e *Engine) RunNextWithExecutors(ctx context.Context, executors []Executor, workerID string, leaseDuration time.Duration, now time.Time) (RunResult, error) {
	byID := make(map[string]Executor, len(executors))
	identities := make([]string, 0, len(executors))
	for _, executor := range executors {
		if executor == nil || strings.TrimSpace(executor.Identity()) == "" {
			return RunResult{}, fmt.Errorf("executor identity is required: %w", model.ErrInvalid)
		}
		id := executor.Identity()
		if _, exists := byID[id]; exists {
			return RunResult{}, fmt.Errorf("duplicate executor identity %q: %w", id, model.ErrInvalid)
		}
		byID[id] = executor
		identities = append(identities, id)
	}
	if len(identities) == 0 {
		return RunResult{}, fmt.Errorf("operation executor is required: %w", model.ErrInvalid)
	}
	op, err := e.store.ClaimNextOperationForExecutors(ctx, workerID, identities, leaseDuration, now)
	if err != nil {
		return RunResult{}, err
	}
	return e.runClaimed(ctx, byID[op.Snapshot.Executor], op, workerID, leaseDuration, now)
}

func (e *Engine) Run(
	ctx context.Context,
	executor Executor,
	operationID, workerID string,
	leaseDuration time.Duration,
	now time.Time,
) (RunResult, error) {
	if executor == nil {
		return RunResult{}, fmt.Errorf("operation executor is required: %w", model.ErrInvalid)
	}
	operation, err := e.store.ClaimOperationForExecutor(ctx, operationID, workerID, executor.Identity(), leaseDuration, now)
	if err != nil {
		stored, err := e.persisted(ctx, operationID, err)
		return RunResult{Operation: stored}, err
	}
	return e.runClaimed(ctx, executor, operation, workerID, leaseDuration, now)
}

func (e *Engine) runClaimed(
	ctx context.Context,
	executor Executor,
	operation model.Operation,
	workerID string,
	leaseDuration time.Duration,
	now time.Time,
) (RunResult, error) {
	storedProposal, err := e.store.GetProposalByOperation(ctx, operation.ID)
	if err == nil {
		return e.finalize(ctx, executor, workerID, leaseDuration, operation, storedProposal, now)
	}
	if !errors.Is(err, model.ErrNotFound) {
		return e.fail(ctx, operation, err, now)
	}
	// 裁定恢复：崩溃后重入时已落盘的 Verdict 不重跑模型，直接收尾。
	if stored, err := e.store.GetDerivedDocument(
		ctx, operation.Target.ID, operation.Snapshot.BaseRevision, model.DerivedVerdictKind, operation.ID,
	); err == nil {
		return e.finalizeVerdict(ctx, operation, stored.Content, now)
	} else if !errors.Is(err, model.ErrNotFound) {
		return e.fail(ctx, operation, err, now)
	}
	var outcome model.OperationOutcome
	err = e.withLease(ctx, operation, workerID, leaseDuration, func(callContext context.Context) error {
		var executeErr error
		outcome, executeErr = executor.Execute(callContext, operation)
		return executeErr
	})
	if err != nil {
		return e.fail(ctx, operation, err, now)
	}
	if err := outcome.Validate(); err != nil {
		return e.fail(ctx, operation, err, now)
	}
	// 工件元数据先于提案落盘（D47）：附件校验才查得到；对象与元数据都幂等，
	// 重入不为仅工件的产出加捷径。
	for _, artifact := range outcome.Artifacts {
		required, err := model.OperationBasis(operation)
		if err != nil {
			return e.fail(ctx, operation, err, now)
		}
		if !artifact.Basis.Covers(required) {
			return e.fail(ctx, operation, fmt.Errorf("artifact %q omits task evidence: %w", artifact.ID, model.ErrInvalid), now)
		}
		if err := e.verifyBasis(ctx, operation, artifact.Basis); err != nil {
			return e.fail(ctx, operation, fmt.Errorf("artifact %q: %w", artifact.ID, err), now)
		}
	}
	if len(outcome.Artifacts) > 0 {
		if err := e.store.SaveExecutionArtifacts(ctx, outcome.Artifacts, operation.ID, operation.Attempt); err != nil {
			return e.fail(ctx, operation, err, now)
		}
	}
	result, err := e.deliver(ctx, executor, workerID, leaseDuration, operation, outcome, now)
	result.Artifacts = outcome.Artifacts
	return result, err
}

// deliver 收尾执行产出：无产出直接成功，裁定走证据收尾，提案经宿主规范化后保存并裁决。
func (e *Engine) deliver(
	ctx context.Context,
	executor Executor,
	workerID string,
	leaseDuration time.Duration,
	operation model.Operation,
	outcome model.OperationOutcome,
	now time.Time,
) (RunResult, error) {
	if outcome.Proposal == nil && len(outcome.Verdict) == 0 {
		succeeded, err := e.conclude(ctx, operation, model.OperationSucceeded, "", now)
		return RunResult{Operation: succeeded}, err
	}
	if outcome.Proposal == nil {
		return e.finalizeVerdict(ctx, operation, outcome.Verdict, now)
	}
	proposal := *outcome.Proposal
	if proposal.OperationID != operation.ID {
		return e.fail(ctx, operation, fmt.Errorf(
			"proposal operation %q does not match %q: %w",
			proposal.OperationID, operation.ID, model.ErrInvalid,
		), now)
	}
	if proposal.Target != operation.Target || proposal.BaseRevision != operation.Snapshot.BaseRevision {
		return e.fail(ctx, operation, fmt.Errorf("proposal does not belong to the frozen operation target and revision: %w", model.ErrInvalid), now)
	}
	if err := proposal.Validate(); err != nil {
		return e.fail(ctx, operation, err, now)
	}
	if len(proposal.Impact.Semantic) != 0 || len(proposal.Impact.Compliance) != 0 {
		return e.fail(ctx, operation, fmt.Errorf("executor cannot self-assert semantic compliance: %w", model.ErrInvalid), now)
	}
	// 宿主规范化提交（D40：故事依赖由宿主写入，执行器声明的 depends_on 不作数）。
	patches, err := model.NormalizeSubmission(proposal.Patches)
	if err != nil {
		return e.fail(ctx, operation, err, now)
	}
	proposal.Patches = patches
	prepared, err := e.changes.PrepareExecution(ctx, proposal, operation.Attempt)
	if err != nil {
		return e.settle(ctx, operation, err, now)
	}
	return e.finalize(ctx, executor, workerID, leaseDuration, operation, prepared, now)
}

// settle 把收尾错误落为任务结局：基线冲突（无法重定位、提交前又有提交）转 stale，由
// 后继继承工作区重做；其余失败。
func (e *Engine) settle(ctx context.Context, operation model.Operation, cause error, now time.Time) (RunResult, error) {
	if errors.Is(cause, model.ErrRevisionConflict) {
		return e.stale(ctx, operation, cause, now)
	}
	return e.fail(ctx, operation, cause, now)
}

// stale 收尾为 stale（§5.5）：基线无法重定位，由后继继承工作区重做。
func (e *Engine) stale(ctx context.Context, operation model.Operation, cause error, now time.Time) (RunResult, error) {
	stale, err := e.conclude(ctx, operation, model.OperationStale, cause.Error(), now)
	return RunResult{Operation: stale}, errors.Join(cause, err)
}

// finalizeVerdict 收尾无 Proposal 的 Operation（D30）：裁定绑定启动快照的 Revision
// （§6.4），有效性按基线判定（D48）——基线在当前 Revision 不再成立即转 stale，
// 不相干的变化不作废裁定；落盘为派生文档后成功收尾。
func (e *Engine) finalizeVerdict(
	ctx context.Context,
	operation model.Operation,
	payload json.RawMessage,
	now time.Time,
) (RunResult, error) {
	basis, err := e.ValidateEvidence(ctx, operation, payload)
	if err != nil {
		return e.fail(ctx, operation, err, now)
	}
	currentRevision, err := e.store.CurrentRevision(ctx, operation.Target)
	if errors.Is(err, model.ErrNotFound) {
		currentRevision = model.InitialRevision
	} else if err != nil {
		return e.fail(ctx, operation, err, now)
	}
	if err := e.changes.VerifyBasis(ctx, operation.Target, basis, currentRevision); errors.Is(err, change.ErrBasisMismatch) {
		return e.stale(ctx, operation, fmt.Errorf("%w: %w", model.ErrRevisionConflict, err), now)
	} else if err != nil {
		return e.fail(ctx, operation, err, now)
	}
	if _, err := e.store.SaveExecutionDerivedDocument(ctx, model.DerivedDocument{
		ProjectID: operation.Target.ID, Revision: operation.Snapshot.BaseRevision,
		Kind: model.DerivedVerdictKind, Key: operation.ID,
		Content: payload, CreatedAt: now,
	}, operation.ID, operation.Attempt); err != nil {
		return e.fail(ctx, operation, err, now)
	}
	succeeded, err := e.conclude(ctx, operation, model.OperationSucceeded, "", now)
	return RunResult{Operation: succeeded, Verdict: payload}, err
}

func (e *Engine) withLease(
	ctx context.Context,
	operation model.Operation,
	workerID string,
	leaseDuration time.Duration,
	call func(context.Context) error,
) error {
	callContext, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	interval := leaseDuration / 3
	if interval <= 0 {
		interval = time.Nanosecond
	}
	stop := make(chan struct{})
	stopped := make(chan struct{})
	heartbeatErr := make(chan error, 1)
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-callContext.Done():
				return
			case <-ticker.C:
				_, err := e.store.RenewOperationLease(
					callContext, operation.ID, workerID, operation.Attempt, leaseDuration, time.Now().UTC(),
				)
				if err != nil {
					leaseErr := fmt.Errorf("%w: %v", errLeaseLost, err)
					heartbeatErr <- leaseErr
					cancel(leaseErr)
					return
				}
			}
		}
	}()
	callErr := call(callContext)
	close(stop)
	<-stopped
	select {
	case leaseErr := <-heartbeatErr:
		return errors.Join(callErr, leaseErr)
	default:
		return callErr
	}
}

// finalize 裁决已保存的执行提案（首次收尾与崩溃恢复共用）；已裁决的提案直接落结局。
func (e *Engine) finalize(
	ctx context.Context,
	executor Executor,
	workerID string,
	leaseDuration time.Duration,
	operation model.Operation,
	prepared model.Proposal,
	now time.Time,
) (RunResult, error) {
	switch prepared.ApprovalState {
	case model.ApprovalApproved:
		committed, err := e.store.GetChangeSet(ctx, prepared.ID)
		if err != nil {
			return e.fail(ctx, operation, err, now)
		}
		succeeded, err := e.conclude(ctx, operation, model.OperationSucceeded, "", now)
		return RunResult{Operation: succeeded, Proposal: prepared, ChangeSet: &committed}, err
	case model.ApprovalRejected:
		return e.fail(ctx, operation, fmt.Errorf("proposal %q was rejected", prepared.ID), now)
	}
	result, err := e.adjudicate(ctx, executor, workerID, leaseDuration, operation, &prepared, now)
	result.Proposal = prepared
	return result, err
}

// adjudicate 先按 D51 重定位，再问 Change Engine 能否按策略放行；需要独立合规分析时
// 只重做检查、保留已生成的候选，证据写回后重新判定。放行则以系统身份裁决提交，授权
// 层面的等待（locked、用户专属等）由提交返回 ErrUnauthorized。proposal 随写回更新。
func (e *Engine) adjudicate(
	ctx context.Context,
	executor Executor,
	workerID string,
	leaseDuration time.Duration,
	operation model.Operation,
	proposal *model.Proposal,
	now time.Time,
) (RunResult, error) {
	relocated, err := e.changes.RelocatePending(ctx, *proposal, operation.Attempt, now)
	if err != nil {
		return e.settle(ctx, operation, err, now)
	}
	*proposal = relocated
	admission, err := e.changes.Admit(ctx, *proposal)
	if err != nil {
		return e.fail(ctx, operation, err, now)
	}
	if admission.Analyze {
		report, err := e.analyzeCompliance(ctx, executor, workerID, leaseDuration, operation, *proposal, admission)
		if err != nil {
			return e.fail(ctx, operation, err, now)
		}
		recorded, err := e.changes.RecordCompliance(ctx, *proposal, report, operation.Attempt, now)
		if err != nil {
			return e.fail(ctx, operation, err, now)
		}
		*proposal = recorded
		if admission, err = e.changes.Admit(ctx, *proposal); err != nil {
			return e.fail(ctx, operation, err, now)
		}
	}
	if admission.Hold != "" {
		awaiting, err := e.conclude(ctx, operation, model.OperationAwaitingApproval, admission.Hold, now)
		return RunResult{Operation: awaiting}, err
	}
	approved, err := change.Decide(
		*proposal, model.ApprovalApproved,
		model.Author{Kind: model.AuthorSystem, ID: "approval-policy:" + string(admission.Policy)}, now,
	)
	if err != nil {
		return e.fail(ctx, operation, err, now)
	}
	committed, err := e.changes.CommitExecution(ctx, approved, operation.Attempt)
	if errors.Is(err, change.ErrUnauthorized) {
		awaiting, transitionErr := e.conclude(ctx, operation, model.OperationAwaitingApproval, err.Error(), now)
		return RunResult{Operation: awaiting}, transitionErr
	}
	if err != nil {
		return e.settle(ctx, operation, err, now)
	}
	succeeded, err := e.conclude(ctx, operation, model.OperationSucceeded, "", now)
	return RunResult{Operation: succeeded, ChangeSet: &committed}, err
}

func unavailableComplianceReport(
	constraints []model.OwnershipRule,
	explanation string,
) model.SemanticComplianceReport {
	report := model.SemanticComplianceReport{Status: model.SemanticComplianceUnavailable}
	for _, constraint := range constraints {
		report.Findings = append(report.Findings, model.SemanticComplianceFinding{
			Constraint: constraint.Target, Explanation: explanation,
		})
	}
	return report
}

// fail 收尾失败并按原因打码（D46）：结果未知不是普通失败，用户对账或重提前不会自动重试。
// fail 落盘执行失败。进程正在退出（ctx 已取消）时不是失败：任务放回队列、释放租约，
// 下次进入接着对话与工作区续跑，不消耗失败预算。
func (e *Engine) fail(ctx context.Context, operation model.Operation, cause error, now time.Time) (RunResult, error) {
	if ctx.Err() != nil {
		return e.release(ctx, operation, cause)
	}
	failed, err := e.failed(ctx, operation, cause, now)
	return RunResult{Operation: failed}, errors.Join(cause, err)
}

// releaseTimeout 是释放写入的上限：取消后执行器已停手，释放只是一次本地写入。
const releaseTimeout = 5 * time.Second

// releasedMessage 记在任务上，诊断与续跑提示都能看到中断原因。
const releasedMessage = "进程退出，任务已放回队列等待续跑"

// release 用脱离取消的短上下文把任务 running → queued（attempt 围栏内），旧执行的
// 对话与工作区保留；释放失败就交给租约到期回收。
func (e *Engine) release(ctx context.Context, operation model.Operation, cause error) (RunResult, error) {
	detached, cancel := context.WithTimeout(context.WithoutCancel(ctx), releaseTimeout)
	defer cancel()
	released, err := e.conclude(detached, operation, model.OperationQueued, releasedMessage, time.Now().UTC())
	return RunResult{Operation: released}, errors.Join(cause, err)
}

// conclude 落盘任务结局。围栏拒绝写入（用户在执行期间暂停或取消了它，或执行已被
// 取代）时回读持久化状态一并返回：协调器按真实状态落点，而不是按执行前的副本。
func (e *Engine) conclude(ctx context.Context, operation model.Operation, to model.OperationState, message string, now time.Time) (model.Operation, error) {
	concluded, err := e.store.ConcludeOperation(ctx, operation.ID, operation.Attempt, to, message, now)
	if err != nil {
		return e.persisted(ctx, operation.ID, err)
	}
	return concluded, nil
}

// failed 落盘执行失败并按原因打码（D46），写入被拒时同 conclude 回读。
func (e *Engine) failed(ctx context.Context, operation model.Operation, cause error, now time.Time) (model.Operation, error) {
	failed, err := e.store.FailOperation(ctx, operation.ID, operation.Attempt, model.FailureCodeFor(cause), cause.Error(), now)
	if err != nil {
		return e.persisted(ctx, operation.ID, err)
	}
	return failed, nil
}

func (e *Engine) persisted(ctx context.Context, id string, cause error) (model.Operation, error) {
	stored, err := e.store.GetOperation(ctx, id)
	if err != nil {
		return model.Operation{}, errors.Join(cause, err)
	}
	return stored, cause
}

// verifyBasis 核对证据基线在启动快照上属实（D48）：文档 revision、要求作用域摘要与
// 工件摘要都必须与 BaseRevision 上的状态一致，不实的基线是执行器的无效产出。
func (e *Engine) verifyBasis(ctx context.Context, operation model.Operation, basis model.EvidenceBasis) error {
	err := e.changes.VerifyBasis(ctx, operation.Target, basis, operation.Snapshot.BaseRevision)
	if errors.Is(err, change.ErrBasisMismatch) {
		return fmt.Errorf("%w: %w", model.ErrInvalid, err)
	}
	return err
}

// analyzeCompliance 由宿主把报告绑定到 Admit 给出的基线；分析失败沿 D14 记为不可用，
// 显式等待裁决。
func (e *Engine) analyzeCompliance(ctx context.Context, executor Executor, workerID string, leaseDuration time.Duration, operation model.Operation, proposal model.Proposal, admission change.Admission) (model.SemanticComplianceReport, error) {
	report := unavailableComplianceReport(admission.Constraints, "configured executor does not provide independent semantic compliance analysis")
	if analyzer, ok := executor.(SemanticComplianceAnalyzer); ok {
		err := e.withLease(ctx, operation, workerID, leaseDuration, func(callContext context.Context) error {
			var analyzeErr error
			report, analyzeErr = analyzer.AnalyzeSemanticCompliance(callContext, operation, proposal, admission.Constraints)
			return analyzeErr
		})
		if errors.Is(err, errLeaseLost) {
			return model.SemanticComplianceReport{}, err
		}
		if err != nil {
			report = unavailableComplianceReport(admission.Constraints, err.Error())
		} else if err := report.Validate(); err != nil {
			return model.SemanticComplianceReport{}, err
		}
	}
	report.BasisDigest = admission.Basis
	return report, nil
}
