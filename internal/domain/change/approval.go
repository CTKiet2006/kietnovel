package change

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/voocel/ainovel-cli/internal/domain/model"
)

// 审批（§5.4、D23、D14）：执行收尾只问 Admit——这份提案能否由系统按策略放行、是否
// 需要独立合规分析；授权时的 guided 检查与这里共用同一个 complianceGap。

// Admission 是执行提案的放行结论。
type Admission struct {
	Policy model.ApprovalPolicy
	// Constraints 是在途正文受的 locked/guided 约束，Basis 是合规证据必须绑定的摘要
	// （候选补丁 + 约束），二者都交给独立合规分析。
	Constraints []model.OwnershipRule
	Basis       string
	// Analyze 表示证据缺失或不覆盖当前候选，独立分析可能消除等待；Hold 非空即须用户
	// 裁决，值为原因。
	Analyze bool
	Hold    string
}

// Admit 给出执行提案的放行结论：生效策略取任务快照与提案基线上权威设置中更严格的一方
// （D23），manual 或 milestone 下的重大变化等待用户；受 locked/guided 约束的正文只有
// 覆盖当前候选与约束且为 pass 的独立合规证据才能放行（D14、D51）。
func (e *Engine) Admit(ctx context.Context, proposal model.Proposal) (Admission, error) {
	task, err := e.task(ctx, proposal)
	if err != nil {
		return Admission{}, err
	}
	if task == nil {
		return Admission{}, fmt.Errorf("admission requires a task proposal: %w", model.ErrInvalid)
	}
	policy, err := e.effectivePolicy(ctx, task.Snapshot.ApprovalPolicy, proposal)
	if err != nil {
		return Admission{}, err
	}
	switch {
	case policy == model.ApprovalCustom:
		return Admission{}, fmt.Errorf("custom approval policy requires an explicit policy contract: %w", model.ErrInvalid)
	case policy == model.ApprovalManual || policy == model.ApprovalMilestone && model.IsMilestone(proposal):
		return Admission{Policy: policy, Hold: "proposal awaits user approval"}, nil
	}
	admission := Admission{Policy: policy}
	if admission.Constraints, err = e.constraints(ctx, task, proposal); err != nil || len(admission.Constraints) == 0 {
		return admission, err
	}
	if admission.Basis, err = complianceBasis(proposal, admission.Constraints); err != nil {
		return Admission{}, err
	}
	admission.Hold, admission.Analyze, err = complianceGap(proposal, admission.Basis)
	return admission, err
}

// effectivePolicy：收紧立即生效来自提案基线上的权威设置，放松不追溯来自快照下限（D23）。
func (e *Engine) effectivePolicy(
	ctx context.Context,
	snapshot model.ApprovalPolicy,
	proposal model.Proposal,
) (model.ApprovalPolicy, error) {
	if proposal.Target.Kind != model.AuthorityProject || proposal.BaseRevision == model.InitialRevision {
		return snapshot, nil
	}
	document, err := e.store.GetDocument(ctx, proposal.Target,
		model.DocumentRef{Kind: model.DocumentApproval, ID: model.SingletonDocumentID}, proposal.BaseRevision)
	if errors.Is(err, model.ErrNotFound) {
		return snapshot, nil
	}
	if err != nil {
		return "", err
	}
	var setting model.ApprovalSetting
	if err := json.Unmarshal(document.Content, &setting); err != nil {
		return "", fmt.Errorf("decode approval setting: %w", err)
	}
	return model.StricterApproval(snapshot, setting.Policy), nil
}

// constraints 取提案所受的 locked/guided 约束：任务快照（用户直接变更取提案基线）与提案
// 基线中更严格的一方（D23）。哪些提案受语义约束由创作领域判定（model.ComplianceApplies）。
func (e *Engine) constraints(ctx context.Context, task *model.Operation, proposal model.Proposal) ([]model.OwnershipRule, error) {
	if !model.ComplianceApplies(proposal) || proposal.Target.Kind != model.AuthorityProject {
		return nil, nil
	}
	snapshotRevision := proposal.BaseRevision
	if task != nil {
		snapshotRevision = task.Snapshot.BaseRevision
	}
	constraints, err := e.ownershipRules(ctx, proposal.Target, snapshotRevision)
	if err != nil {
		return nil, err
	}
	if proposal.BaseRevision != snapshotRevision {
		latest, err := e.ownershipRules(ctx, proposal.Target, proposal.BaseRevision)
		if err != nil {
			return nil, err
		}
		constraints = mergeStricterRules(constraints, latest)
	}
	constraints = slices.DeleteFunc(constraints, func(rule model.OwnershipRule) bool {
		return rule.Control != model.ControlLocked && rule.Control != model.ControlGuided
	})
	slices.SortFunc(constraints, func(left, right model.OwnershipRule) int {
		if compared := strings.Compare(left.Target.Key(), right.Target.Key()); compared != 0 {
			return compared
		}
		return strings.Compare(strings.Join(left.Guidance, "\n"), strings.Join(right.Guidance, "\n"))
	})
	return constraints, nil
}

func (e *Engine) ownershipRules(
	ctx context.Context,
	target model.AuthorityTarget,
	revision model.Revision,
) ([]model.OwnershipRule, error) {
	if revision == model.InitialRevision {
		return nil, nil
	}
	documents, err := e.store.ListDocuments(ctx, target, model.DocumentOwnership, revision)
	if err != nil {
		return nil, err
	}
	rules := make([]model.OwnershipRule, 0, len(documents))
	for _, document := range documents {
		var rule model.OwnershipRule
		if err := json.Unmarshal(document.Content, &rule); err != nil {
			return nil, fmt.Errorf("decode ownership constraint %q: %w", document.Document.ID, err)
		}
		rules = append(rules, rule)
	}
	return rules, nil
}

func controlRank(level model.ControlLevel) int {
	switch level {
	case model.ControlLocked:
		return 2
	case model.ControlGuided:
		return 1
	default:
		return 0
	}
}

// mergeStricterRules 对同一 target 取控制级别更严格的一方；两侧同为 guided 且
// guidance 不同时二者都保留——在途任务必须同时通过新旧约束校验（§5.5）。
func mergeStricterRules(snapshot, latest []model.OwnershipRule) []model.OwnershipRule {
	byTarget := make(map[string][]model.OwnershipRule)
	for _, rule := range snapshot {
		byTarget[rule.Target.Key()] = append(byTarget[rule.Target.Key()], rule)
	}
	for _, rule := range latest {
		key := rule.Target.Key()
		existing := byTarget[key]
		if len(existing) == 0 {
			byTarget[key] = []model.OwnershipRule{rule}
			continue
		}
		strongest := existing[0]
		switch {
		case controlRank(rule.Control) > controlRank(strongest.Control):
			byTarget[key] = []model.OwnershipRule{rule}
		case controlRank(rule.Control) < controlRank(strongest.Control):
		case rule.Control == model.ControlGuided && !slices.Equal(rule.Guidance, strongest.Guidance):
			byTarget[key] = append(existing, rule)
		}
	}
	merged := make([]model.OwnershipRule, 0, len(byTarget))
	for _, rules := range byTarget {
		merged = append(merged, rules...)
	}
	return merged
}

// complianceBasis 把合规证据绑定到候选补丁与实际检查的约束（D51）：恢复或重定位后
// 任一方变化，旧证据即不可复用。
func complianceBasis(proposal model.Proposal, constraints []model.OwnershipRule) (string, error) {
	payload, err := json.Marshal(struct {
		Patches     []model.Patch         `json:"patches"`
		Constraints []model.OwnershipRule `json:"constraints"`
	}{proposal.Patches, constraints})
	if err != nil {
		return "", fmt.Errorf("encode compliance basis: %w", err)
	}
	return model.Digest(payload), nil
}

// complianceGap 兑现 D14：只有绑定 basis 且结论为 pass 的独立合规证据才能自动放行。
// 返回等待用户的原因（空串为放行）；uncovered 表示证据缺失或不覆盖当前候选。
func complianceGap(proposal model.Proposal, basis string) (reason string, uncovered bool, err error) {
	if len(proposal.Impact.Compliance) == 0 {
		return "semantic compliance evidence is required for locked or guided story constraints", true, nil
	}
	var report model.SemanticComplianceReport
	if err := json.Unmarshal(proposal.Impact.Compliance, &report); err != nil {
		return "", false, fmt.Errorf("decode semantic compliance report: %w", err)
	}
	if err := report.Validate(); err != nil {
		return "", false, err
	}
	if basis == "" || report.BasisDigest != basis {
		return "semantic compliance evidence does not cover the candidate and current constraints", true, nil
	}
	if report.Status != model.SemanticCompliancePass {
		return fmt.Sprintf("semantic compliance is %s; user approval is required", report.Status), false, nil
	}
	return "", false, nil
}

// RecordCompliance 把独立合规报告写回待裁决的执行提案（attempt 围栏内）。报告必须绑定
// Admit 给出的 Basis，放行与否由再次 Admit 判定。
func (e *Engine) RecordCompliance(
	ctx context.Context,
	proposal model.Proposal,
	report model.SemanticComplianceReport,
	attempt int,
	now time.Time,
) (model.Proposal, error) {
	if err := report.Validate(); err != nil {
		return model.Proposal{}, err
	}
	if report.BasisDigest == "" {
		return model.Proposal{}, fmt.Errorf("semantic compliance report must bind its basis: %w", model.ErrInvalid)
	}
	payload, err := json.Marshal(report)
	if err != nil {
		return model.Proposal{}, fmt.Errorf("encode semantic compliance report: %w", err)
	}
	proposal.Impact.Compliance = payload
	if err := e.store.UpdatePendingProposal(ctx, proposal, attempt, now); err != nil {
		return model.Proposal{}, err
	}
	return proposal, nil
}
