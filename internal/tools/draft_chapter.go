package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/CTKiet2006/kietnovel/internal/domain"
	"github.com/CTKiet2006/kietnovel/internal/errs"
	"github.com/CTKiet2006/kietnovel/internal/store"
	"github.com/voocel/agentcore/schema"
)

// DraftChapterTool writes a whole chapter draft, replacing the old write_scene + polish_chapter pipeline.
// The Agent decides on its own whether to write it in one go or continue in batches.
type DraftChapterTool struct {
	store *store.Store
}

func NewDraftChapterTool(store *store.Store) *DraftChapterTool {
	return &DraftChapterTool{store: store}
}

func (t *DraftChapterTool) Name() string { return "draft_chapter" }
func (t *DraftChapterTool) Description() string {
	switch toolLang(t.store) {
	case "vi":
		return "Ghi nội dung chương. mode=write để ghi đè toàn bộ chương, mode=append để nối thêm vào bản nháp hiện có (viết tiếp/sửa đổi)"
	case "en":
		return "Write chapter prose. mode=write overwrites the full chapter, mode=append appends to existing draft (continue/edit)"
	default:
		return "写入章节正文。mode=write 覆盖写入整章，mode=append 追加到现有草稿（续写/修改）"
	}
}
func (t *DraftChapterTool) Label() string {
	switch toolLang(t.store) {
	case "vi":
		return "Ghi chương"
	case "en":
		return "Draft chapter"
	default:
		return "写入章节"
	}
}

// A writing tool; concurrency is forbidden (read-modify-write race).
func (t *DraftChapterTool) ReadOnly(_ json.RawMessage) bool        { return false }
func (t *DraftChapterTool) ConcurrencySafe(_ json.RawMessage) bool { return false }

func (t *DraftChapterTool) Schema() map[string]any {
	chapterDesc := "章节号"
	contentDesc := "章节正文"
	modeDesc := "写入模式"
	switch toolLang(t.store) {
	case "vi":
		chapterDesc = "Số chương"
		contentDesc = "Nội dung chính văn của chương"
		modeDesc = "Chế độ ghi (write hoặc append)"
	case "en":
		chapterDesc = "Chapter number"
		contentDesc = "Chapter prose content"
		modeDesc = "Write mode (write or append)"
	}
	return schema.Object(
		schema.Property("chapter", schema.Int(chapterDesc)).Required(),
		schema.Property("content", schema.String(contentDesc)).Required(),
		schema.Property("mode", schema.Enum(modeDesc, "write", "append")).Required(),
	)
}

// StrictSchema requires the Provider to guarantee that the tool arguments conform to the schema.
func (t *DraftChapterTool) StrictSchema() bool { return true }

func (t *DraftChapterTool) Execute(_ context.Context, args json.RawMessage) (json.RawMessage, error) {
	var a struct {
		Chapter int    `json:"chapter"`
		Content string `json:"content"`
		Mode    string `json:"mode"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, fmt.Errorf("invalid args: %w: %w", errs.ErrToolArgs, err)
	}
	if a.Chapter <= 0 {
		return nil, fmt.Errorf("chapter must be > 0: %w", errs.ErrToolArgs)
	}
	if a.Content == "" {
		return nil, fmt.Errorf("content must not be empty: %w", errs.ErrToolArgs)
	}
	if err := t.store.Progress.ValidateChapterWork(a.Chapter); err != nil {
		return nil, err
	}
	if err := EnsureChapterExpanded(t.store, a.Chapter); err != nil {
		return nil, err
	}
	completed, err := t.store.Progress.IsChapterCompleted(a.Chapter)
	if err != nil {
		return nil, fmt.Errorf("load progress: %w: %w", errs.ErrStoreRead, err)
	}
	if completed {
		// Polish/rewrite path: the chapter is already complete but is still in pending_rewrites, so overwriting the draft is allowed
		progress, err := t.store.Progress.Load()
		if err != nil {
			return nil, fmt.Errorf("load progress: %w: %w", errs.ErrStoreRead, err)
		}
		inRewriteQueue := progress != nil && slices.Contains(progress.PendingRewrites, a.Chapter)
		if !inRewriteQueue {
			reason := fmt.Sprintf("第 %d 章已提交完成，不能覆盖", a.Chapter)
			switch toolLang(t.store) {
			case "vi":
				reason = fmt.Sprintf("Chương %d đã nộp hoàn thành, không thể ghi đè", a.Chapter)
			case "en":
				reason = fmt.Sprintf("Chapter %d is already committed, cannot overwrite", a.Chapter)
			}
			return json.Marshal(map[string]any{
				"chapter":   a.Chapter,
				"skipped":   true,
				"completed": true,
				"reason":    reason,
			})
		}
	}
	if err := t.store.Progress.StartChapter(a.Chapter); err != nil {
		return nil, fmt.Errorf("mark chapter in progress: %w", err)
	}

	switch a.Mode {
	case "append":
		if err := t.store.Drafts.AppendDraft(a.Chapter, a.Content); err != nil {
			return nil, fmt.Errorf("append draft: %w", err)
		}
		full, err := t.store.Drafts.LoadDraft(a.Chapter)
		if err != nil {
			return nil, fmt.Errorf("load draft after append: %w", err)
		}
		if _, err := t.store.Checkpoints.AppendArtifact(
			domain.ChapterScope(a.Chapter), "draft",
			fmt.Sprintf("drafts/%02d.draft.md", a.Chapter),
		); err != nil {
			return nil, fmt.Errorf("checkpoint draft: %w", err)
		}
		return json.Marshal(map[string]any{
			"written":    true,
			"chapter":    a.Chapter,
			"mode":       "append",
			"word_count": domain.WordCount(full),
			"next_step":  nextStepHint(t.store),
		})
	default: // write
		if err := t.store.Drafts.SaveDraft(a.Chapter, a.Content); err != nil {
			return nil, fmt.Errorf("save draft: %w", err)
		}
		if _, err := t.store.Checkpoints.AppendArtifact(
			domain.ChapterScope(a.Chapter), "draft",
			fmt.Sprintf("drafts/%02d.draft.md", a.Chapter),
		); err != nil {
			return nil, fmt.Errorf("checkpoint draft: %w", err)
		}
		return json.Marshal(map[string]any{
			"written":    true,
			"chapter":    a.Chapter,
			"mode":       "write",
			"word_count": domain.WordCount(a.Content),
			"next_step":  nextStepHint(t.store),
		})
	}
}
