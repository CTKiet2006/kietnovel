package change

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/voocel/ainovel-cli/internal/domain/model"
)

type Engine struct {
	store    Store
	semantic SemanticAnalyzer
}

type SemanticAnalyzer interface {
	Analyze(context.Context, model.Proposal, StructuralImpact) (json.RawMessage, error)
}

type StructuralImpact struct {
	Direct   []model.DocumentRef `json:"direct"`
	Affected []model.DocumentRef `json:"affected"`
}

func New(authorityStore Store) *Engine {
	return &Engine{store: authorityStore}
}

func NewWithSemanticAnalyzer(authorityStore Store, analyzer SemanticAnalyzer) *Engine {
	return &Engine{store: authorityStore, semantic: analyzer}
}

func (e *Engine) Prepare(ctx context.Context, proposal model.Proposal) (model.Proposal, error) {
	return e.prepare(ctx, proposal, false, 0)
}

// Validate 只做确定性结构校验、不落库：执行器在工具边界调用它，把冲突当场回给模型
// 自纠（§11 第 7 条），收尾的 PrepareExecution 仍是兜底。基线按提案自己的 BaseRevision
// 读取而不核对当前 Revision——漂移留给收尾时的重定位（D51）处理。
func (e *Engine) Validate(ctx context.Context, proposal model.Proposal) error {
	if err := proposal.Validate(); err != nil {
		return err
	}
	_, err := e.analyze(ctx, proposal)
	return err
}

// PrepareExecution 校验并保存执行提案（attempt 围栏内）：基线落后时先按 D51 重定位，
// 不能重定位返回 model.ErrRevisionConflict。
func (e *Engine) PrepareExecution(ctx context.Context, proposal model.Proposal, attempt int) (model.Proposal, error) {
	if attempt <= 0 {
		return model.Proposal{}, fmt.Errorf("execution proposal requires an attempt: %w", model.ErrInvalid)
	}
	return e.prepare(ctx, proposal, false, attempt)
}

func (e *Engine) PrepareWithSemantic(ctx context.Context, proposal model.Proposal) (model.Proposal, error) {
	return e.prepare(ctx, proposal, true, 0)
}

func (e *Engine) prepare(ctx context.Context, proposal model.Proposal, analyzeSemantic bool, attempt int) (model.Proposal, error) {
	if proposal.ApprovalState != model.ApprovalPending || proposal.DecidedBy != nil || proposal.DecidedAt != nil {
		return model.Proposal{}, fmt.Errorf("prepare requires a pending proposal: %w", ErrInvalidState)
	}
	if err := proposal.Validate(); err != nil {
		return model.Proposal{}, err
	}
	var err error
	if attempt > 0 {
		if proposal, _, err = e.Relocate(ctx, proposal); err != nil {
			return model.Proposal{}, err
		}
	}
	checked, err := e.validateAndAnalyze(ctx, proposal)
	if err != nil {
		return model.Proposal{}, err
	}
	if proposal, err = withImpact(proposal, checked.impact); err != nil {
		return model.Proposal{}, err
	}
	if analyzeSemantic {
		if e.semantic == nil {
			return model.Proposal{}, ErrSemanticUnavailable
		}
		semantic, err := e.semantic.Analyze(ctx, proposal, checked.impact)
		if err != nil {
			return model.Proposal{}, fmt.Errorf("analyze semantic impact: %w", err)
		}
		if len(semantic) == 0 || !json.Valid(semantic) {
			return model.Proposal{}, fmt.Errorf("semantic analyzer returned invalid JSON: %w", model.ErrInvalid)
		}
		if _, err := model.DecodeSemanticImpact(semantic, checked.base); err != nil {
			return model.Proposal{}, err
		}
		proposal.Impact.Semantic = append(json.RawMessage(nil), semantic...)
	}
	if attempt > 0 {
		return e.store.SaveExecutionProposal(ctx, proposal, attempt)
	}
	return e.store.SaveProposal(ctx, proposal)
}

func Decide(proposal model.Proposal, state model.ApprovalState, decider model.Author, at time.Time) (model.Proposal, error) {
	if proposal.ApprovalState != model.ApprovalPending || proposal.DecidedBy != nil || proposal.DecidedAt != nil {
		return model.Proposal{}, fmt.Errorf("proposal is not pending: %w", ErrInvalidState)
	}
	if state != model.ApprovalApproved && state != model.ApprovalRejected {
		return model.Proposal{}, fmt.Errorf("decision must approve or reject: %w", ErrInvalidState)
	}
	if at.IsZero() {
		return model.Proposal{}, fmt.Errorf("decision time is required: %w", model.ErrInvalid)
	}
	proposal.ApprovalState = state
	proposal.DecidedBy = &decider
	proposal.DecidedAt = &at
	if err := proposal.Validate(); err != nil {
		return model.Proposal{}, err
	}
	return proposal, nil
}

func (e *Engine) Commit(ctx context.Context, approved model.Proposal) (model.ChangeSet, error) {
	ready, err := e.prepareCommit(ctx, approved)
	if err != nil {
		return model.ChangeSet{}, err
	}
	return e.store.CommitProposal(ctx, ready)
}

// CommitExecution 提交执行实例自动批准的提案：校验与授权同 Commit，落库时在同一事务内
// 确认 attempt 仍是 approved.OperationID 的当前执行（D42）。
func (e *Engine) CommitExecution(ctx context.Context, approved model.Proposal, attempt int) (model.ChangeSet, error) {
	ready, err := e.prepareCommit(ctx, approved)
	if err != nil {
		return model.ChangeSet{}, err
	}
	return e.store.CommitExecutionProposal(ctx, ready, attempt)
}

func (e *Engine) prepareCommit(ctx context.Context, approved model.Proposal) (model.Proposal, error) {
	if approved.ApprovalState != model.ApprovalApproved {
		return model.Proposal{}, model.ErrNotApproved
	}
	if err := approved.Validate(); err != nil {
		return model.Proposal{}, err
	}
	checked, err := e.validateAndAnalyze(ctx, approved)
	if err != nil {
		return model.Proposal{}, err
	}
	constraints, err := e.constraints(ctx, checked.task, approved)
	if err != nil {
		return model.Proposal{}, err
	}
	basis := ""
	if len(constraints) > 0 {
		if basis, err = complianceBasis(approved, constraints); err != nil {
			return model.Proposal{}, err
		}
	}
	if err := authorize(approved, checked.base, basis); err != nil {
		return model.Proposal{}, err
	}
	return withImpact(approved, checked.impact)
}

func (e *Engine) Reject(ctx context.Context, rejected model.Proposal) (model.Proposal, error) {
	if rejected.ApprovalState != model.ApprovalRejected {
		return model.Proposal{}, fmt.Errorf("reject requires a rejected proposal: %w", ErrInvalidState)
	}
	return e.store.RejectProposal(ctx, rejected)
}

// PrepareRevert creates a new Proposal that restores an earlier authority
// snapshot. History remains append-only; committing it creates a new revision.
func (e *Engine) PrepareRevert(
	ctx context.Context,
	id string,
	target model.AuthorityTarget,
	to model.Revision,
	author model.Author,
	reason string,
	createdAt time.Time,
) (model.Proposal, error) {
	current, err := e.currentRevision(ctx, target)
	if err != nil {
		return model.Proposal{}, err
	}
	if to < model.InitialRevision || to >= current {
		return model.Proposal{}, fmt.Errorf("revert revision %d must be before current revision %d: %w", to, current, model.ErrInvalid)
	}
	currentState, err := e.loadState(ctx, target, current)
	if err != nil {
		return model.Proposal{}, err
	}
	targetState, err := e.loadState(ctx, target, to)
	if err != nil {
		return model.Proposal{}, err
	}

	keys := make([]string, 0, len(currentState)+len(targetState))
	seen := make(map[string]struct{}, len(currentState)+len(targetState))
	for key := range currentState {
		seen[key] = struct{}{}
		keys = append(keys, key)
	}
	for key := range targetState {
		if _, ok := seen[key]; !ok {
			keys = append(keys, key)
		}
	}
	slices.Sort(keys)
	patches := make([]model.Patch, 0, len(keys))
	for _, key := range keys {
		currentDocument, inCurrent := currentState[key]
		targetDocument, inTarget := targetState[key]
		ref := currentDocument.Document
		if !inCurrent {
			ref = targetDocument.Document
		}
		// 只追加文档（裁决）不随回滚变化：历史记录不是可回退的内容。
		if spec, _ := model.DocumentType(ref.Kind); spec.AppendOnly {
			continue
		}
		switch {
		case !inTarget:
			patches = append(patches, model.Patch{Document: currentDocument.Document, Operation: model.PatchDelete})
		case !inCurrent || !bytes.Equal(currentDocument.Content, targetDocument.Content):
			content, err := model.RevertContent(ref, currentDocument.Content, targetDocument.Content)
			if err != nil {
				return model.Proposal{}, err
			}
			patches = append(patches, model.Patch{
				Document:  targetDocument.Document,
				Operation: model.PatchPut,
				Content:   content,
			})
		}
	}
	if len(patches) == 0 {
		return model.Proposal{}, fmt.Errorf("revision %d already matches current state: %w", to, ErrInvalidState)
	}
	return e.Prepare(ctx, model.Proposal{
		ID:            id,
		Target:        target,
		BaseRevision:  current,
		Author:        author,
		Reason:        reason,
		Patches:       patches,
		ApprovalState: model.ApprovalPending,
		CreatedAt:     createdAt,
	})
}

// analysis 是一次确定性校验的结果：结构影响、基线状态与提案所属任务。
type analysis struct {
	impact StructuralImpact
	base   model.DocumentSet
	task   *model.Operation
}

func withImpact(proposal model.Proposal, impact StructuralImpact) (model.Proposal, error) {
	payload, err := json.Marshal(impact)
	if err != nil {
		return model.Proposal{}, fmt.Errorf("marshal structural impact: %w", err)
	}
	proposal.Impact.Structural = payload
	return proposal, nil
}

func (e *Engine) validateAndAnalyze(ctx context.Context, change model.Proposal) (analysis, error) {
	current, err := e.currentRevision(ctx, change.Target)
	if err != nil {
		return analysis{}, err
	}
	if current != change.BaseRevision {
		return analysis{}, fmt.Errorf("base revision %d, current revision %d: %w", change.BaseRevision, current, model.ErrRevisionConflict)
	}
	return e.analyze(ctx, change)
}

// analyze 在提案自己的 BaseRevision 上执行全部确定性校验并计算结构影响，不落库：先是
// 通用结构（种类、内容、只追加、工件、依赖），再是创作领域规则与所属任务的提交契约。
func (e *Engine) analyze(ctx context.Context, change model.Proposal) (analysis, error) {
	if err := validateTargetDocuments(change.Target, change.Patches); err != nil {
		return analysis{}, err
	}
	base, err := e.loadState(ctx, change.Target, change.BaseRevision)
	if err != nil {
		return analysis{}, err
	}
	projected := maps.Clone(base) // 已存文档内容不可变，浅拷贝即可
	direct := make([]model.DocumentRef, 0, len(change.Patches))
	for i, patch := range change.Patches {
		key := patch.Document.Key()
		direct = append(direct, patch.Document)
		switch patch.Operation {
		case model.PatchPut:
			if err := model.ValidateDocumentContent(patch.Document, patch.Content); err != nil {
				return analysis{}, fmt.Errorf("patch %d content: %w", i, err)
			}
			projected[key] = model.DocumentVersion{
				Target: change.Target, Document: patch.Document, Revision: change.BaseRevision + 1,
				Content: append(json.RawMessage(nil), patch.Content...), ChangeSetID: change.ID,
			}
		case model.PatchDelete:
			if _, ok := projected[key]; !ok {
				return analysis{}, fmt.Errorf("delete missing document %q: %w", key, model.ErrNotFound)
			}
			delete(projected, key)
		}
	}
	if err := validateAppendOnly(base, change.Patches); err != nil {
		return analysis{}, err
	}
	if err := e.validateArtifacts(ctx, change); err != nil {
		return analysis{}, err
	}
	graph, err := dependenciesOf(projected)
	if err != nil {
		return analysis{}, err
	}
	task, err := e.task(ctx, change)
	if err != nil {
		return analysis{}, err
	}
	if err := model.ValidateChange(model.ChangeCheck{Proposal: change, Task: task, Base: base, Projected: projected}); err != nil {
		return analysis{}, err
	}
	return analysis{impact: graph.impact(projected, direct), base: base, task: task}, nil
}

// task 载入提案所属的任务：任务提交契约随领域规则一起执行（D64）。用户直接发起的
// 变更没有任务。
func (e *Engine) task(ctx context.Context, change model.Proposal) (*model.Operation, error) {
	if change.OperationID == "" {
		return nil, nil
	}
	operation, err := e.store.GetOperation(ctx, change.OperationID)
	if err != nil {
		return nil, fmt.Errorf("load task %q of proposal %q: %w", change.OperationID, change.ID, err)
	}
	return &operation, nil
}

func (e *Engine) currentRevision(ctx context.Context, target model.AuthorityTarget) (model.Revision, error) {
	revision, err := e.store.CurrentRevision(ctx, target)
	if errors.Is(err, model.ErrNotFound) {
		return model.InitialRevision, nil
	}
	return revision, err
}

func (e *Engine) loadState(ctx context.Context, target model.AuthorityTarget, at model.Revision) (model.DocumentSet, error) {
	state := make(model.DocumentSet)
	if at == model.InitialRevision {
		return state, nil
	}
	for _, kind := range model.DocumentKindsFor(target.Kind) {
		documents, err := e.store.ListDocuments(ctx, target, kind, at)
		if err != nil {
			return nil, err
		}
		for _, document := range documents {
			if err := model.ValidateDocumentContent(document.Document, document.Content); err != nil {
				return nil, fmt.Errorf("stored document %q is invalid: %w", document.Document.Key(), err)
			}
			state[document.Document.Key()] = document
		}
	}
	return state, nil
}
