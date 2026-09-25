package novel

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/voocel/ainovel-cli/internal/domain/model"
	"github.com/voocel/ainovel-cli/internal/infra/store"
)

// ReviewEvidence 是审阅任务的证据契约（§6.4），由组合根装配进执行内核：裁定对得上任务
// 输入，发现与工作区审阅记录逐条一致；返回的基线由内核按冻结依赖核验、落盘与收尾。
func ReviewEvidence(s *store.Store) func(context.Context, model.Operation, json.RawMessage) (model.EvidenceBasis, error) {
	return func(ctx context.Context, operation model.Operation, payload json.RawMessage) (model.EvidenceBasis, error) {
		var verdict model.ReviewVerdict
		if err := model.DecodeStrict(payload, &verdict); err != nil {
			return model.EvidenceBasis{}, fmt.Errorf("decode review verdict: %w", err)
		}
		if err := model.ValidateReviewVerdictForOperation(operation, verdict); err != nil {
			return model.EvidenceBasis{}, err
		}
		artifact, err := s.GetWorkspaceArtifact(ctx, operation.ID, verdict.ReviewKey)
		if err != nil {
			return model.EvidenceBasis{}, fmt.Errorf("read review artifact %q: %w", verdict.ReviewKey, err)
		}
		if artifact.MediaType != model.ReviewArtifactMediaType {
			return model.EvidenceBasis{}, fmt.Errorf("workspace artifact %q is not a review record: %w", verdict.ReviewKey, model.ErrInvalid)
		}
		var findings []model.ReviewFinding
		if err := model.DecodeStrict(artifact.Content, &findings); err != nil {
			return model.EvidenceBasis{}, fmt.Errorf("decode review artifact %q: %w", verdict.ReviewKey, err)
		}
		if findings == nil {
			return model.EvidenceBasis{}, fmt.Errorf("review artifact %q must contain a findings array: %w", verdict.ReviewKey, model.ErrInvalid)
		}
		if !slices.Equal(findings, verdict.Findings) {
			return model.EvidenceBasis{}, fmt.Errorf("verdict findings do not match review artifact %q: %w", verdict.ReviewKey, model.ErrInvalid)
		}
		return verdict.Basis, nil
	}
}
