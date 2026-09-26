package capability

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"github.com/voocel/ainovel-cli/internal/domain/model"
	"github.com/voocel/ainovel-cli/internal/infra/workspace"
)

// workspaceDrafts 按工作稿键与版本逐字取回章节：正文只经工作稿提交。单章任务恰好
// 引用一份工作稿，受影响重写至少一份；章节是否是任务目标、字数与事实变化等契约由
// changes.Validate 统一执行（D64）。
func (r *Runtime) workspaceDrafts(ctx context.Context, operation model.Operation, versions map[string]int64) ([]model.ManuscriptChapter, error) {
	switch operation.Kind {
	case model.OperationWriteChapter, model.OperationRewriteChapter:
		if len(versions) != 1 {
			return nil, fmt.Errorf("single-chapter submission requires workspace_key and workspace_version returned by workspace_put_chapter: %w", model.ErrInvalid)
		}
	case model.OperationRewriteAffected:
		if len(versions) == 0 {
			return nil, fmt.Errorf("affected rewrite submission requires workspace_versions covering every rewritten chapter: %w", model.ErrInvalid)
		}
	default:
		if len(versions) > 0 {
			return nil, fmt.Errorf("this task does not submit chapter drafts: %w", model.ErrInvalid)
		}
		return nil, nil
	}
	drafts := make([]model.ManuscriptChapter, 0, len(versions))
	for _, key := range slices.Sorted(maps.Keys(versions)) {
		version := versions[key]
		if version < 1 {
			return nil, fmt.Errorf("workspace %q requires a positive version from workspace_read or workspace_put_chapter: %w", key, model.ErrInvalid)
		}
		artifact, err := r.store.GetWorkspaceArtifact(ctx, operation.ID, key)
		if err != nil {
			return nil, fmt.Errorf("read workspace %q (workspace_list lists existing keys): %w", key, err)
		}
		if artifact.Version != version {
			return nil, fmt.Errorf("workspace %q version mismatch: submitted %d, current %d; read the current draft before resubmitting: %w", key, version, artifact.Version, model.ErrStateConflict)
		}
		if artifact.MediaType != workspace.ChapterMediaType {
			return nil, fmt.Errorf("workspace %q is not a chapter: %w", key, model.ErrInvalid)
		}
		var chapter model.ManuscriptChapter
		if err := decodeToolArgs(artifact.Content, &chapter); err != nil {
			return nil, fmt.Errorf("decode workspace %q: %w", key, err)
		}
		drafts = append(drafts, chapter)
	}
	return drafts, nil
}
