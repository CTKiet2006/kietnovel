package novel

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	projectdoc "github.com/voocel/ainovel-cli/internal/app/project"
	"github.com/voocel/ainovel-cli/internal/domain/model"
)

// StoredVerdict 是一条已校验的原始裁定及其派生记录坐标（选"当前"用）；Accepted 是
// 仍然有效的用户裁决所接受的发现（D43），生效裁定由 Effective 套用得到。
type StoredVerdict struct {
	Verdict   model.ReviewVerdict
	Key       string
	CreatedAt time.Time
	Accepted  map[string]struct{}
}

func (v StoredVerdict) Effective() model.ReviewVerdict {
	return v.Verdict.Adjudicated(v.Key, v.Accepted)
}

// ListVerdicts 取当前快照下仍然有效的全部裁定并附上有效裁决（D43）：协调器与工作台
// 共用，两处"当前裁定"（CurrentVerdicts）与"生效裁定"语义一致。
func (s *Reviews) ListVerdicts(ctx context.Context, project projectdoc.Snapshot) ([]StoredVerdict, error) {
	verdicts, _, err := s.listVerdicts(ctx, project)
	return verdicts, err
}

// listVerdicts 另外返回已失效的上一轮裁定（D68）：推导器只在章节正文未变时沿用其中
// 对该章的结论，所以套用全部未撤回的裁决，不按基线过滤。
func (s *Reviews) listVerdicts(ctx context.Context, project projectdoc.Snapshot) (valid, prior []StoredVerdict, err error) {
	valid, prior, err = s.rawVerdicts(ctx, project)
	if err != nil {
		return nil, nil, err
	}
	adjudications, err := s.ValidAdjudications(ctx, project)
	if err != nil {
		return nil, nil, err
	}
	accepted, all := model.AcceptedFindings(adjudications), model.AcceptedFindings(project.Adjudications)
	for index := range valid {
		valid[index].Accepted = accepted
	}
	for index := range prior {
		prior[index].Accepted = all
	}
	return valid, prior, nil
}

// rawVerdicts 取全部原始裁定，按当前快照下基线是否成立（D48）分为有效与已失效：操作
// 必须已成功、内容可解码且结构合法——缺失或损坏是数据错误，一律上抛，不得静默当作
// "没有裁定"。
func (s *Reviews) rawVerdicts(ctx context.Context, project projectdoc.Snapshot) (valid, stale []StoredVerdict, err error) {
	derived, err := s.store.ListDerivedDocumentsByKind(ctx, project.ID, model.DerivedVerdictKind)
	if err != nil {
		return nil, nil, err
	}
	for _, document := range derived {
		operation, err := s.store.GetOperation(ctx, document.Key)
		if err != nil {
			return nil, nil, fmt.Errorf("review verdict %q has no operation: %w", document.Key, err)
		}
		if operation.State != model.OperationSucceeded || operation.Kind != model.OperationReviewRange {
			continue
		}
		var verdict model.ReviewVerdict
		if err := json.Unmarshal(document.Content, &verdict); err != nil {
			return nil, nil, fmt.Errorf("decode review verdict %q: %w", document.Key, err)
		}
		if err := verdict.Validate(); err != nil {
			return nil, nil, fmt.Errorf("review verdict %q: %w", document.Key, err)
		}
		basisValid, err := s.basisValid(ctx, project, verdict.Basis)
		if err != nil {
			return nil, nil, err
		}
		stored := StoredVerdict{Verdict: verdict, Key: document.Key, CreatedAt: document.CreatedAt}
		if basisValid {
			valid = append(valid, stored)
		} else {
			stale = append(stale, stored)
		}
	}
	return valid, stale, nil
}
