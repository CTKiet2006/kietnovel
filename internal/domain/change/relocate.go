package change

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/voocel/ainovel-cli/internal/domain/model"
)

// VerifyBasis 核对证据基线在 at 上成立（D48）：文档最后变化 revision 相等、要求作用域
// 成员摘要相等、工件已发布且摘要一致。不成立返回 ErrBasisMismatch 并说明条目。
func (e *Engine) VerifyBasis(ctx context.Context, target model.AuthorityTarget, basis model.EvidenceBasis, at model.Revision) error {
	for _, entry := range basis.Documents {
		version, err := e.store.GetDocument(ctx, target, entry.Ref, at)
		if errors.Is(err, model.ErrNotFound) {
			return fmt.Errorf("basis document %s is absent at revision %d: %w", entry.Ref.Key(), at, ErrBasisMismatch)
		}
		if err != nil {
			return err
		}
		if version.Revision != entry.Revision {
			return fmt.Errorf("basis document %s claims revision %d, stored %d at revision %d: %w",
				entry.Ref.Key(), entry.Revision, version.Revision, at, ErrBasisMismatch)
		}
	}
	if len(basis.Scopes) > 0 {
		directives, revisionOf, err := e.directivesAt(ctx, target, at)
		if err != nil {
			return err
		}
		for _, scope := range basis.Scopes {
			if model.ScopeDigest(model.ScopeMembers(directives, scope.Target, revisionOf)) != scope.Digest {
				return fmt.Errorf("directives covering chapter %d changed by revision %d: %w",
					scope.Target.ChapterNumber, at, ErrBasisMismatch)
			}
		}
	}
	for _, ref := range basis.Artifacts {
		artifact, err := e.store.GetArtifact(ctx, ref.ID)
		if errors.Is(err, model.ErrNotFound) {
			return fmt.Errorf("basis artifact %s is not published: %w", ref.ID, ErrBasisMismatch)
		}
		if err != nil {
			return err
		}
		if artifact.Digest != ref.Digest {
			return fmt.Errorf("basis artifact %s digest mismatch: %w", ref.ID, ErrBasisMismatch)
		}
	}
	return nil
}

// BasisHolds 是 VerifyBasis 的布尔视图：基线不成立返回 false 而非错误，其它错误照常上抛。
// 证据有效性查询（审阅裁定、工件、用户裁决）共用这一处，不各自翻译 ErrBasisMismatch。
func (e *Engine) BasisHolds(ctx context.Context, target model.AuthorityTarget, basis model.EvidenceBasis, at model.Revision) (bool, error) {
	err := e.VerifyBasis(ctx, target, basis, at)
	if errors.Is(err, ErrBasisMismatch) {
		return false, nil
	}
	return err == nil, err
}

func (e *Engine) directivesAt(
	ctx context.Context,
	target model.AuthorityTarget,
	at model.Revision,
) ([]model.Directive, func(model.DocumentRef) model.Revision, error) {
	revisions := make(map[string]model.Revision)
	var directives []model.Directive
	if at > model.InitialRevision {
		documents, err := e.store.ListDocuments(ctx, target, model.DocumentDirective, at)
		if err != nil {
			return nil, nil, err
		}
		for _, document := range documents {
			var directive model.Directive
			if err := json.Unmarshal(document.Content, &directive); err != nil {
				return nil, nil, fmt.Errorf("decode directive %q: %w", document.Document.ID, err)
			}
			directives = append(directives, directive)
			revisions[document.Document.Key()] = document.Revision
		}
	}
	return directives, func(ref model.DocumentRef) model.Revision { return revisions[ref.Key()] }, nil
}

// Relocate 落实提案重定位规则（D51 / §5.5 第 4 条）：提案基线落后于当前 Revision 时，
// 仅当中间各 Revision 只改过用户专属文档、没碰本提案的文档，且所属任务的基线在当前仍
// 成立，才把基线搬到当前并重算结构影响；否则返回 model.ErrRevisionConflict 并说明原因。
// 不落库；没有任务的提案（用户直接变更）没有可核对的基线，原样返回。
func (e *Engine) Relocate(ctx context.Context, proposal model.Proposal) (model.Proposal, bool, error) {
	task, err := e.task(ctx, proposal)
	if err != nil || task == nil {
		return proposal, false, err
	}
	basis, err := model.OperationBasis(*task)
	if err != nil {
		return model.Proposal{}, false, err
	}
	return e.relocate(ctx, proposal, basis)
}

// RelocatePending 重定位已保存的待裁决提案并落库；attempt 为正时受执行归属围栏保护（D42）。
func (e *Engine) RelocatePending(ctx context.Context, proposal model.Proposal, attempt int, now time.Time) (model.Proposal, error) {
	relocated, moved, err := e.Relocate(ctx, proposal)
	if err != nil || !moved {
		return relocated, err
	}
	if err := e.store.UpdatePendingProposal(ctx, relocated, attempt, now); err != nil {
		return model.Proposal{}, err
	}
	return relocated, nil
}

func (e *Engine) relocate(ctx context.Context, proposal model.Proposal, basis model.EvidenceBasis) (model.Proposal, bool, error) {
	current, err := e.currentRevision(ctx, proposal.Target)
	if err != nil {
		return model.Proposal{}, false, err
	}
	if current == proposal.BaseRevision {
		return proposal, false, nil
	}
	if current < proposal.BaseRevision {
		return model.Proposal{}, false, fmt.Errorf("base revision %d is ahead of current revision %d: %w",
			proposal.BaseRevision, current, model.ErrRevisionConflict)
	}
	own := make(map[string]struct{}, len(proposal.Patches))
	for _, patch := range proposal.Patches {
		own[patch.Document.Key()] = struct{}{}
	}
	for revision := proposal.BaseRevision + 1; revision <= current; revision++ {
		changeSet, err := e.store.GetChangeSetByRevision(ctx, proposal.Target, revision)
		if err != nil {
			return model.Proposal{}, false, err
		}
		for _, patch := range changeSet.Patches {
			spec, err := model.DocumentType(patch.Document.Kind)
			if err != nil {
				return model.Proposal{}, false, err
			}
			if _, touched := own[patch.Document.Key()]; !spec.UserOnly || touched {
				return model.Proposal{}, false, fmt.Errorf("%s changed at revision %d: %w",
					patch.Document.Key(), revision, model.ErrRevisionConflict)
			}
		}
	}
	if err := e.VerifyBasis(ctx, proposal.Target, basis, current); errors.Is(err, ErrBasisMismatch) {
		return model.Proposal{}, false, fmt.Errorf("%w: %w", model.ErrRevisionConflict, err)
	} else if err != nil {
		return model.Proposal{}, false, err
	}
	relocated := proposal
	relocated.BaseRevision = current
	checked, err := e.validateAndAnalyze(ctx, relocated)
	if err != nil {
		return model.Proposal{}, false, err
	}
	relocated, err = withImpact(relocated, checked.impact)
	return relocated, err == nil, err
}
