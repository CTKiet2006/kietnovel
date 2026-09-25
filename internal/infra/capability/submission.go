package capability

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/voocel/ainovel-cli/internal/domain/model"
	"github.com/voocel/ainovel-cli/internal/infra/workspace"
)

func (r *Runtime) validateSubmissionArtifact(
	ctx context.Context,
	operation model.Operation,
	workspaceKeys []string, reviewKey string,
	patches []model.Patch,
) error {
	switch operation.Kind {
	case model.OperationWriteChapter, model.OperationRewriteChapter:
		if len(workspaceKeys) != 1 {
			return fmt.Errorf("single-chapter submission requires exactly one workspace_key, got %d; use the key returned by workspace_put_chapter: %w", len(workspaceKeys), model.ErrInvalid)
		}
		return r.validateWorkspaceChapters(ctx, operation, workspaceKeys, patches)
	case model.OperationRewriteAffected:
		if len(workspaceKeys) == 0 {
			return fmt.Errorf("affected rewrite submission requires workspace_keys: %w", model.ErrInvalid)
		}
		return r.validateWorkspaceChapters(ctx, operation, workspaceKeys, patches)
	case model.OperationReviewRange:
		if reviewKey == "" {
			return fmt.Errorf("review submission requires review_key: %w", model.ErrInvalid)
		}
		if _, err := r.store.GetWorkspaceArtifact(ctx, operation.ID, reviewKey); err != nil {
			return fmt.Errorf("read submitted workspace review: %w", err)
		}
	}
	return nil
}

// Reference submission copies the exact versioned draft into the proposal. Other
// patches still pass the same Canon, directive, scope and structural validation.
func (r *Runtime) materializeWorkspaceChapters(ctx context.Context, operation model.Operation, keys []string, versions map[string]int64, patches []model.Patch) ([]model.Patch, error) {
	switch operation.Kind {
	case model.OperationWriteChapter, model.OperationRewriteChapter:
		if len(keys) != 1 {
			return nil, fmt.Errorf("single-chapter reference submission requires one workspace_key: %w", model.ErrInvalid)
		}
	case model.OperationRewriteAffected:
	default:
		return nil, fmt.Errorf("workspace versions are only supported for chapter writing and rewriting: %w", model.ErrInvalid)
	}
	if len(keys) == 0 || len(versions) != len(keys) {
		return nil, fmt.Errorf("provide one workspace version for every submitted key: %w", model.ErrInvalid)
	}
	for _, patch := range patches {
		if patch.Document.Kind == model.DocumentManuscript {
			return nil, fmt.Errorf("versioned workspace submission assembles manuscript patches automatically; omit manuscript patches and submit Canon/other changes only: %w", model.ErrInvalid)
		}
	}
	result := append([]model.Patch(nil), patches...)
	for _, key := range keys {
		version, ok := versions[key]
		if !ok || version < 1 {
			return nil, fmt.Errorf("workspace %q requires a positive version from workspace_read or workspace_put_chapter: %w", key, model.ErrInvalid)
		}
		artifact, err := r.store.GetWorkspaceArtifact(ctx, operation.ID, key)
		if err != nil {
			return nil, fmt.Errorf("read workspace %q: %w", key, err)
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
		result = append(result, model.Patch{Document: model.DocumentRef{Kind: model.DocumentManuscript, ID: chapter.ID}, Operation: model.PatchPut, Content: append(json.RawMessage(nil), artifact.Content...)})
	}
	return result, nil
}

// validateWorkspaceChapters 核对提交的正文就是工作区里的草稿：键不重复、每章逐字一致、
// 正文补丁恰好对应提交的草稿。章节是否是任务目标、字数与 Canon Delta 等契约由
// changes.Validate 统一执行（D64）。
func (r *Runtime) validateWorkspaceChapters(
	ctx context.Context,
	operation model.Operation,
	workspaceKeys []string,
	patches []model.Patch,
) error {
	seenKeys := make(map[string]struct{}, len(workspaceKeys))
	seenChapters := make(map[string]struct{}, len(workspaceKeys))
	for _, workspaceKey := range workspaceKeys {
		if workspaceKey == "" {
			return fmt.Errorf("workspace chapter key is required: %w", model.ErrInvalid)
		}
		if _, exists := seenKeys[workspaceKey]; exists {
			return fmt.Errorf("duplicate workspace chapter key %q: %w", workspaceKey, model.ErrInvalid)
		}
		seenKeys[workspaceKey] = struct{}{}
		artifact, err := r.store.GetWorkspaceArtifact(ctx, operation.ID, workspaceKey)
		if err != nil {
			return fmt.Errorf("read submitted workspace chapter: %w", err)
		}
		if artifact.MediaType != workspace.ChapterMediaType {
			return fmt.Errorf("workspace artifact %q is not a chapter: %w", workspaceKey, model.ErrInvalid)
		}
		var workspaceChapter model.ManuscriptChapter
		if err := decodeToolArgs(artifact.Content, &workspaceChapter); err != nil {
			return fmt.Errorf("decode submitted workspace chapter: %w", err)
		}
		if _, exists := seenChapters[workspaceChapter.ID]; exists {
			return fmt.Errorf("duplicate submitted workspace chapter %q: %w", workspaceChapter.ID, model.ErrInvalid)
		}
		seenChapters[workspaceChapter.ID] = struct{}{}
		workspaceContent, err := json.Marshal(workspaceChapter)
		if err != nil {
			return fmt.Errorf("encode submitted workspace chapter: %w", err)
		}
		manuscriptMatches := false
		mismatch := "missing manuscript put patch; submit workspace_key + workspace_version to assemble it automatically"
		for _, patch := range patches {
			if patch.Document.Kind != model.DocumentManuscript || patch.Document.ID != workspaceChapter.ID || patch.Operation != model.PatchPut {
				continue
			}
			var proposedChapter model.ManuscriptChapter
			if err := decodeToolArgs(patch.Content, &proposedChapter); err != nil {
				return fmt.Errorf("decode proposed manuscript: %w", err)
			}
			proposedContent, err := json.Marshal(proposedChapter)
			if err != nil {
				return fmt.Errorf("encode proposed manuscript: %w", err)
			}
			if bytes.Equal(workspaceContent, proposedContent) {
				manuscriptMatches = true
			} else {
				mismatch = manuscriptDifference(workspaceChapter, proposedChapter)
			}
		}
		if !manuscriptMatches {
			return fmt.Errorf("proposal manuscript must match workspace artifact %q: %s; read the draft or submit its key and version without a manuscript patch: %w", workspaceKey, mismatch, model.ErrInvalid)
		}
	}
	for _, patch := range patches {
		if patch.Document.Kind != model.DocumentManuscript {
			continue
		}
		if _, exists := seenChapters[patch.Document.ID]; !exists {
			return fmt.Errorf("writer proposal includes unverified manuscript %q; submit it through its workspace draft: %w", patch.Document.ID, model.ErrInvalid)
		}
	}
	return nil
}

func manuscriptDifference(saved, proposed model.ManuscriptChapter) string {
	if len(saved.Blocks) != len(proposed.Blocks) {
		return fmt.Sprintf("blocks count differs: workspace %d, submitted %d", len(saved.Blocks), len(proposed.Blocks))
	}
	for i, block := range saved.Blocks {
		other := proposed.Blocks[i]
		if block.ID != other.ID {
			return fmt.Sprintf("blocks[%d].id differs: workspace %q, submitted %q", i, block.ID, other.ID)
		}
		if block.Text != other.Text {
			a, b := []rune(block.Text), []rune(other.Text)
			pos := 0
			for pos < len(a) && pos < len(b) && a[pos] == b[pos] {
				pos++
			}
			start := max(0, pos-12)
			return fmt.Sprintf("block %q text differs at character %d: workspace %q, submitted %q", block.ID, pos+1, string(a[start:min(len(a), pos+20)]), string(b[start:min(len(b), pos+20)]))
		}
	}
	a, _ := json.Marshal(saved)
	b, _ := json.Marshal(proposed)
	var left, right map[string]json.RawMessage
	_ = json.Unmarshal(a, &left)
	_ = json.Unmarshal(b, &right)
	for _, field := range []string{"id", "plan_node_id", "number", "title", "author", "depends_on"} {
		if !bytes.Equal(left[field], right[field]) {
			return field + " differs from workspace"
		}
	}
	return "chapter representation differs from workspace"
}
