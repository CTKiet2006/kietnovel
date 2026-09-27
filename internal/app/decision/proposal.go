package decision

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	projectdoc "github.com/voocel/ainovel-cli/internal/app/project"
	"github.com/voocel/ainovel-cli/internal/app/resource"
	"github.com/voocel/ainovel-cli/internal/domain/change"
	"github.com/voocel/ainovel-cli/internal/domain/model"

	"github.com/voocel/ainovel-cli/internal/app/task"
)

func (s *Review) Propose(ctx context.Context, proposal model.Proposal) (model.Proposal, error) {
	return s.changes.Prepare(ctx, proposal)
}

func (s *Review) ProposeWithSemantic(ctx context.Context, proposal model.Proposal) (model.Proposal, error) {
	return s.changes.PrepareWithSemantic(ctx, proposal)
}

func (s *Review) Proposal(ctx context.Context, id string) (model.Proposal, error) {
	return s.store.GetProposal(ctx, id)
}

func (s *Review) ProposalForOperation(ctx context.Context, operationID string) (model.Proposal, error) {
	return s.store.GetProposalByOperation(ctx, operationID)
}

type ResolveProposalCommand struct {
	ProposalID          string
	UserID              string
	Strategy            string
	Reason              string
	Packs               []resource.PackRef
	CreatorProfiles     []resource.CreatorProfileRef
	CoreProtocolVersion string
	RunID               string
	CreatedAt           time.Time
}

type ResolveProposalResult struct {
	Strategy   string            `json:"strategy"`
	Proposal   *model.Proposal   `json:"proposal,omitempty"`
	ChangeSet  *model.ChangeSet  `json:"change_set,omitempty"`
	Operations []model.Operation `json:"operations,omitempty"`
}

func (s *Review) ResolveProposal(ctx context.Context, command ResolveProposalCommand) (ResolveProposalResult, error) {
	if command.ProposalID == "" || command.UserID == "" || command.CreatedAt.IsZero() {
		return ResolveProposalResult{}, fmt.Errorf("proposal resolution requires proposal, user and time: %w", model.ErrInvalid)
	}
	proposal, err := s.store.GetProposal(ctx, command.ProposalID)
	if err != nil {
		return ResolveProposalResult{}, err
	}
	strategy := model.ResolutionStrategy(command.Strategy)
	report, option, err := semanticResolutionOption(proposal, strategy)
	if err != nil {
		return ResolveProposalResult{}, err
	}
	if report.Status == model.SemanticImpactConsistent {
		return ResolveProposalResult{}, fmt.Errorf("consistent proposal does not require a resolution strategy; approve it directly: %w", model.ErrInvalid)
	}
	result := ResolveProposalResult{Strategy: command.Strategy}
	if strategy == model.ResolutionAbandon {
		rejected, err := s.Reject(ctx, proposal.ID, command.UserID, command.Reason, command.CreatedAt)
		if err != nil {
			return ResolveProposalResult{}, err
		}
		result.Proposal = &rejected
		return result, nil
	}
	if strategy == model.ResolutionRewriteAffected && !s.tasks.HasLLM() {
		return ResolveProposalResult{}, fmt.Errorf("affected rewrite requires a configured model: %w", model.ErrInvalid)
	}
	if strategy == model.ResolutionRewriteAffected && strings.TrimSpace(command.RunID) == "" {
		return ResolveProposalResult{}, fmt.Errorf("affected rewrite requires a creation run: %w", model.ErrInvalid)
	}
	if strategy == model.ResolutionRewriteAffected {
		for _, ref := range command.Packs {
			if _, err := resource.LoadPack(ctx, s.store, ref); err != nil {
				return ResolveProposalResult{}, err
			}
		}
		for _, ref := range command.CreatorProfiles {
			if _, err := resource.LoadCreatorProfile(ctx, s.store, ref); err != nil {
				return ResolveProposalResult{}, err
			}
		}
	}
	committed, err := s.Approve(ctx, proposal.ID, command.UserID, command.CreatedAt)
	if err != nil {
		return ResolveProposalResult{}, err
	}
	result.ChangeSet = &committed
	if strategy == model.ResolutionReinterpretFuture {
		return result, nil
	}
	operationID := proposal.ID + ":rewrite-affected"
	existing, err := s.store.GetOperation(ctx, operationID)
	if err == nil {
		if err := validateResolutionOperation(existing, proposal.ID, option.ChapterIDs, committed.NewRevision, command.RunID); err != nil {
			return result, err
		}
		result.Operations = []model.Operation{existing}
		return result, nil
	}
	if !errors.Is(err, model.ErrNotFound) {
		return result, err
	}

	coreVersion := command.CoreProtocolVersion
	if coreVersion == "" {
		coreVersion = "core-v1"
	}
	input, err := json.Marshal(model.RewriteAffectedInput{
		ChapterIDs: option.ChapterIDs, BaseRevision: committed.NewRevision,
		ResolutionProposalID: proposal.ID, Reason: option.Explanation,
	})
	if err != nil {
		return ResolveProposalResult{}, fmt.Errorf("encode affected rewrite task: %w", err)
	}
	operation, err := s.tasks.StartOperation(ctx, task.StartOperationCommand{
		OperationID: operationID, ProjectID: proposal.Target.ID,
		Kind:  model.OperationRewriteAffected,
		Input: input, Packs: command.Packs, CreatorProfiles: command.CreatorProfiles,
		CoreProtocolVersion: coreVersion, ApprovalPolicy: model.ApprovalManual,
		RunID: command.RunID, CreatedAt: *committed.DecidedAt,
	})
	if err != nil {
		return result, fmt.Errorf("proposal was committed but affected rewrite operation could not be created; retry the same resolution: %w", err)
	}
	result.Operations = []model.Operation{operation}
	return result, nil
}

func validateResolutionOperation(
	operation model.Operation,
	proposalID string,
	chapterIDs []string,
	baseRevision model.Revision,
	runID string,
) error {
	if operation.Kind != model.OperationRewriteAffected || operation.Snapshot.BaseRevision != baseRevision ||
		operation.RunID != runID {
		return fmt.Errorf("operation %q is not the requested semantic resolution: %w", operation.ID, model.ErrIdempotencyConflict)
	}
	input, err := model.TaskInputAs[model.RewriteAffectedInput](operation)
	if err != nil || input.ResolutionProposalID != proposalID ||
		input.BaseRevision != baseRevision || !slices.Equal(input.ChapterIDs, chapterIDs) {
		return fmt.Errorf("operation %q is not the requested semantic resolution: %w", operation.ID, model.ErrIdempotencyConflict)
	}
	return nil
}

func semanticResolutionOption(
	proposal model.Proposal,
	strategy model.ResolutionStrategy,
) (model.SemanticImpactReport, model.ResolutionOption, error) {
	if strategy != model.ResolutionRewriteAffected && strategy != model.ResolutionReinterpretFuture && strategy != model.ResolutionAbandon {
		return model.SemanticImpactReport{}, model.ResolutionOption{}, fmt.Errorf("unknown resolution strategy %q: %w", strategy, model.ErrInvalid)
	}
	if len(proposal.Impact.Semantic) == 0 {
		return model.SemanticImpactReport{}, model.ResolutionOption{}, fmt.Errorf("proposal %q has no semantic impact report: %w", proposal.ID, model.ErrInvalid)
	}
	var report model.SemanticImpactReport
	if err := model.DecodeStrict(proposal.Impact.Semantic, &report); err != nil {
		return model.SemanticImpactReport{}, model.ResolutionOption{}, fmt.Errorf("decode semantic impact report: %w", err)
	}
	if err := report.Validate(); err != nil {
		return model.SemanticImpactReport{}, model.ResolutionOption{}, err
	}
	// 一致的报告没有候选策略，先返回让调用方给出“直接批准即可”，不要误报成策略不存在。
	if report.Status == model.SemanticImpactConsistent {
		return report, model.ResolutionOption{}, nil
	}
	for _, option := range report.Options {
		if option.Strategy == strategy {
			return report, option, nil
		}
	}
	return model.SemanticImpactReport{}, model.ResolutionOption{}, fmt.Errorf("semantic impact does not offer strategy %q: %w", strategy, model.ErrInvalid)
}

func (s *Review) Approve(ctx context.Context, proposalID, userID string, at time.Time) (model.ChangeSet, error) {
	proposal, err := s.store.GetProposal(ctx, proposalID)
	if err != nil {
		return model.ChangeSet{}, err
	}
	switch proposal.ApprovalState {
	case model.ApprovalApproved:
		return s.store.GetChangeSet(ctx, proposal.ID)
	case model.ApprovalRejected:
		return model.ChangeSet{}, fmt.Errorf("proposal %q was rejected: %w", proposal.ID, model.ErrStateConflict)
	default:
		return s.commitPending(ctx, proposal, userID, at)
	}
}

// commitPending 由用户批准并提交待裁决提案：任务提案只在任务仍等待审批时可批准（存储在
// 提交事务内再断言一次），先按 D51 重定位到当前 Revision，不能重定位的原样返回
// ErrRevisionConflict，由用户按工作台指引重写。
func (s *Review) commitPending(ctx context.Context, proposal model.Proposal, userID string, at time.Time) (model.ChangeSet, error) {
	if proposal.OperationID != "" {
		operation, err := s.store.GetOperation(ctx, proposal.OperationID)
		if err != nil {
			return model.ChangeSet{}, err
		}
		if operation.State != model.OperationAwaitingApproval {
			return model.ChangeSet{}, fmt.Errorf("operation %q is %s and no longer awaits approval: %w", operation.ID, operation.State, model.ErrStateConflict)
		}
	}
	proposal, err := s.changes.RelocatePending(ctx, proposal, 0, at)
	if err != nil {
		return model.ChangeSet{}, err
	}
	approved, err := change.Decide(proposal, model.ApprovalApproved, model.Author{Kind: model.AuthorUser, ID: userID}, at)
	if err != nil {
		return model.ChangeSet{}, err
	}
	return s.changes.Commit(ctx, approved)
}

// Relocate 按所属任务的基线重定位待裁决提案（D51），不落库：工作台据此判断候选是否
// 仍是当前稿件。
func (s *Review) Relocate(ctx context.Context, proposal model.Proposal) (model.Proposal, bool, error) {
	return s.changes.Relocate(ctx, proposal)
}

// Reject 否决提案并把理由入账为要求。等待审批的任务随之失败；已被取代（stale）或取消的
// 任务只否决旧稿、不再转移状态。
func (s *Review) Reject(ctx context.Context, proposalID, userID, reason string, at time.Time) (model.Proposal, error) {
	proposal, err := s.store.GetProposal(ctx, proposalID)
	if err != nil {
		return model.Proposal{}, err
	}
	var operation model.Operation
	if proposal.OperationID != "" {
		if operation, err = s.store.GetOperation(ctx, proposal.OperationID); err != nil {
			return model.Proposal{}, err
		}
		switch operation.State {
		case model.OperationAwaitingApproval, model.OperationFailed, model.OperationStale, model.OperationCancelled:
		default:
			return model.Proposal{}, fmt.Errorf("operation %q is %s: %w", operation.ID, operation.State, model.ErrStateConflict)
		}
	}
	rejected := proposal
	switch proposal.ApprovalState {
	case model.ApprovalApproved:
		return model.Proposal{}, fmt.Errorf("proposal %q was approved: %w", proposal.ID, model.ErrStateConflict)
	case model.ApprovalPending:
		rejected, err = change.Decide(proposal, model.ApprovalRejected, model.Author{Kind: model.AuthorUser, ID: userID}, at)
		if err == nil {
			rejected.DecisionReason = strings.TrimSpace(reason)
			rejected, err = s.changes.Reject(ctx, rejected)
		}
		if err != nil {
			return model.Proposal{}, err
		}
	}
	if proposal.OperationID == "" {
		return rejected, nil
	}
	// 否决后任务只可能被同时取代或取消，冲突即以现状为准。
	if operation.State == model.OperationAwaitingApproval {
		if _, err := s.store.TransitionOperation(ctx, operation.ID, operation.State, model.OperationFailed,
			"proposal rejected by user", at); err != nil && !errors.Is(err, model.ErrStateConflict) {
			return model.Proposal{}, err
		}
	}
	if err := s.rejectionDirective(ctx, rejected, operation, userID, at); err != nil {
		return model.Proposal{}, err
	}
	return rejected, nil
}

// rejectionDirective 把否决理由入账为 Directive（§4.9 / S10），作用域见 rejectionScope；
// 后继按当前快照装配到它，审阅逐条核验。幂等：同一提案重复否决沿用同一 ChangeID 与
// 已记录的理由。
func (s *Review) rejectionDirective(
	ctx context.Context,
	rejected model.Proposal,
	operation model.Operation,
	userID string,
	at time.Time,
) error {
	if rejected.DecisionReason == "" || rejected.Target.Kind != model.AuthorityProject {
		return nil
	}
	scope, err := s.rejectionScope(ctx, operation, rejected.Target.ID)
	if err != nil {
		return err
	}
	_, err = s.projects.AddDirective(ctx, projectdoc.AddDirectiveCommand{
		ProjectID: rejected.Target.ID, ChangeID: rejected.ID + ":directive", UserID: userID,
		DirectiveID: rejected.ID + ":directive", Scope: scope, Text: rejected.DecisionReason,
		Reason: "否决候选 " + rejected.ID + " 的理由", CreatedAt: at,
	})
	return err
}

// chapterPlanIDOf 取章节任务对应的 Plan 节点；非章节任务为空。
// rejectionScope 是否决理由的作用域（D71）：章节稿件落到该章；规划方案说的是还没写的
// 部分，从下一章起——已写章节不因规划重写，理由若覆盖它们，每个已审窗口的要求作用域
// 都会变、全书重审。其余任务（事实核验、受影响重写）落到整书。
func (s *Review) rejectionScope(ctx context.Context, operation model.Operation, projectID string) (string, error) {
	input, err := model.DecodeTaskInput(operation.Kind, operation.Input)
	if err != nil {
		return "", err
	}
	switch input := input.(type) {
	case *model.WriteChapterInput:
		return model.DirectiveScopePlanNode(input.ChapterPlanID), nil
	case *model.RewriteChapterInput:
		return model.DirectiveScopePlanNode(input.ChapterPlanID), nil
	case *model.DevelopPlanInput, *model.RevisePlanInput:
		project, err := s.projects.Project(ctx, projectID, model.InitialRevision)
		if err != nil {
			return "", err
		}
		return model.DirectiveScopeFromChapter(len(project.Manuscript) + 1), nil
	}
	return model.DirectiveScopeProject, nil
}

func (s *Review) PrepareRevert(
	ctx context.Context,
	proposalID, projectID, userID, reason string,
	to model.Revision,
	createdAt time.Time,
) (model.Proposal, error) {
	return s.changes.PrepareRevert(
		ctx, proposalID,
		model.AuthorityTarget{Kind: model.AuthorityProject, ID: projectID}, to,
		model.Author{Kind: model.AuthorUser, ID: userID}, reason, createdAt,
	)
}
