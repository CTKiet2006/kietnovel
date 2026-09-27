package novel

import (
	"context"
	"errors"

	projectdoc "github.com/voocel/ainovel-cli/internal/app/project"
	"github.com/voocel/ainovel-cli/internal/domain/change"
	"github.com/voocel/ainovel-cli/internal/domain/model"
)

func manuscriptRef(chapterID string) model.DocumentRef {
	return model.DocumentRef{Kind: model.DocumentManuscript, ID: chapterID}
}

// directiveTarget 是章节的要求作用域匹配对象：章号加该章 Plan 节点及其祖先。
func directiveTarget(project projectdoc.Snapshot, number int, planID string) model.DirectiveTarget {
	return model.DirectiveTarget{ChapterNumber: number, PlanNodeIDs: model.PlanAncestry(project.Plan, planID)}
}

// directiveScope 把命中 target 的 active 要求集合（含各自版本）钉成作用域基线：
// 相干要求的增删改改变摘要，不相干要求的变化不影响。
func directiveScope(project projectdoc.Snapshot, target model.DirectiveTarget) model.ScopeBasis {
	members := model.ScopeMembers(project.Directives, target, func(ref model.DocumentRef) model.Revision {
		return project.Index[ref.Key()].Revision
	})
	return model.ScopeBasis{Kind: model.ScopeDirective, Target: target, Digest: model.ScopeDigest(members)}
}

// basisValid delegates validity to the same contract used by change submission.
func (s *Reviews) basisValid(ctx context.Context, project projectdoc.Snapshot, basis model.EvidenceBasis) (bool, error) {
	target := model.AuthorityTarget{Kind: model.AuthorityProject, ID: project.ID}
	err := s.changes.VerifyBasis(ctx, target, basis, project.Revision)
	if errors.Is(err, change.ErrBasisMismatch) {
		return false, nil
	}
	return err == nil, err
}
