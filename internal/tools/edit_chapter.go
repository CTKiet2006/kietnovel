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
	agentcoretools "github.com/voocel/agentcore/tools"
)

// EditChapterTool does targeted string replacement on a chapter draft, for polish scenarios.
// Compared with draft_chapter rewriting a whole chapter, it saves 10x+ in tokens.
//
// Persistence contract: it only modifies drafts/{ch:02d}.draft.md; writing straight to chapters/ is forbidden (the final version is owned exclusively by commit_chapter).
// Seed semantics: when drafts does not exist but chapters does -> chapters is copied into drafts automatically as the starting point.
// Ownership check: only chapters that are already complete and currently in the PendingRewrites queue may be edited.
//
// This tool is a thin wrapper around agentcore.EditTool; the find-and-replace logic (multi-level tolerant matching, diff output, line-ending/BOM preservation)
// is reused entirely from the upstream implementation.
type EditChapterTool struct {
	store *store.Store
	edit  *agentcoretools.EditTool
}

func NewEditChapterTool(s *store.Store) *EditChapterTool {
	return &EditChapterTool{
		store: s,
		edit:  agentcoretools.NewEdit(s.Dir(), nil),
	}
}

func (t *EditChapterTool) Name() string { return "edit_chapter" }
func (t *EditChapterTool) Label() string {
	switch toolLang(t.store) {
	case "vi":
		return "Sửa đổi chương"
	case "en":
		return "Edit chapter"
	default:
		return "编辑章节"
	}
}

// ReadOnly explicitly declares this a writing tool (together with ConcurrencySafeTool it keeps it from being scheduled concurrently).
func (t *EditChapterTool) ReadOnly(_ json.RawMessage) bool { return false }

// ConcurrencySafe explicitly forbids concurrency: several edit_chapter calls on the same chapter in parallel would race read-modify-write,
// and even parallel calls on different chapters would interleave the checkpoint order. A single serial lane is the most robust.
func (t *EditChapterTool) ConcurrencySafe(_ json.RawMessage) bool { return false }

// ActivityDescription supplies the activity description of the current tool for the UI / log.
func (t *EditChapterTool) ActivityDescription(_ json.RawMessage) string {
	switch toolLang(t.store) {
	case "vi":
		return "Sửa đổi bản nháp chương"
	case "en":
		return "Edit chapter draft"
	default:
		return "编辑章节草稿"
	}
}

func (t *EditChapterTool) Description() string {
	switch toolLang(t.store) {
	case "vi":
		return "Chỉ dùng để thay thế chuỗi định vị trên bản nháp của chương đã hoàn thành và đang nằm trong hàng đợi PendingRewrites (lựa chọn hàng đầu khi gọt giũa, tiết kiệm token hơn draft_chapter viết lại cả chương). " +
			"Cấm dùng công cụ này khi viết bản thảo đầu tiên của chương mới. " +
			"Tìm old_string và thay bằng new_string, yêu cầu khớp chính xác và duy nhất (nhiều nơi cần replace_all=true). " +
			"old_string phải copy chính xác từng chữ từ kết quả read_chapter(source=\"draft\")."
	case "en":
		return "Perform targeted string replacement on completed chapter drafts currently in the PendingRewrites queue (preferred for polishing, saves tokens vs draft_chapter). " +
			"Forbidden for initial chapter drafts. " +
			"Locates old_string and replaces with new_string, requiring exact and unique match (multiple occurrences require replace_all=true). " +
			"old_string must be copied verbatim from read_chapter(source=\"draft\")."
	default:
		return "仅对已完成且进入 PendingRewrites 队列的章节草稿做定点字符串替换（打磨场景首选，比 draft_chapter 整章重写省 token）。" +
			"新章初稿禁止使用本工具；初稿有硬伤请调用 draft_chapter(mode=\"write\") 整章覆盖。" +
			"找到 old_string 并替换为 new_string，要求精确匹配且唯一（多处匹配需 replace_all=true）。" +
			"old_string 必须从最近一次 read_chapter(source=\"draft\") 的返回中逐字复制，禁止凭记忆重构原文；" +
			"注意返回值是 JSON 字符串，\\n 须还原为真实换行。draft_chapter 改写过草稿后必须先重新 read_chapter 再编辑。" +
			"匹配失败的报错会附上草稿中最接近的候选片段，请从候选逐字复制后重试。" +
			"写入 drafts/{ch}.draft.md；drafts 不存在时自动从 chapters 播种。" +
			"章节已完成且不在 PendingRewrites 队列中时拒绝执行。每次调用只改一处，多处修改请多次调用。"
	}
}

func (t *EditChapterTool) Schema() map[string]any {
	chapDesc := "章节号"
	oldDesc := "要替换的原文精确片段，多行需包含换行；不加 replace_all 时必须在草稿中唯一出现"
	newDesc := "替换后的新文本"
	repAllDesc := "替换所有匹配（默认 false）"

	switch toolLang(t.store) {
	case "vi":
		chapDesc = "Số chương"
		oldDesc = "Đoạn văn bản gốc chính xác cần thay thế, nhiều dòng cần bao gồm ký tự xuống dòng; bắt buộc phải xuất hiện duy nhất trong bản nháp nếu không bật replace_all"
		newDesc = "Văn bản mới sau khi thay thế"
		repAllDesc = "Thay thế tất cả các vị trí trùng khớp (mặc định false)"
	case "en":
		chapDesc = "Chapter number"
		oldDesc = "Exact snippet to replace; must be unique in draft unless replace_all=true"
		newDesc = "Replacement text"
		repAllDesc = "Replace all occurrences (default false)"
	}

	return schema.Object(
		schema.Property("chapter", schema.Int(chapDesc)).Required(),
		schema.Property("old_string", schema.String(oldDesc)).Required(),
		schema.Property("new_string", schema.String(newDesc)).Required(),
		schema.Property("replace_all", schema.Bool(repAllDesc)),
	)
}

func (t *EditChapterTool) Execute(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
	var a struct {
		Chapter    int    `json:"chapter"`
		OldString  string `json:"old_string"`
		NewString  string `json:"new_string"`
		ReplaceAll bool   `json:"replace_all"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, fmt.Errorf("invalid args: %w: %w", errs.ErrToolArgs, err)
	}
	if a.Chapter <= 0 {
		return nil, fmt.Errorf("chapter must be > 0: %w", errs.ErrToolArgs)
	}
	if a.OldString == "" {
		return nil, fmt.Errorf("old_string 不能为空: %w", errs.ErrToolArgs)
	}
	if a.OldString == a.NewString {
		return nil, fmt.Errorf("old_string 与 new_string 相同，无需修改: %w", errs.ErrToolArgs)
	}
	if err := t.store.Progress.ValidateChapterWork(a.Chapter); err != nil {
		return nil, err
	}

	// Ownership check: mechanically enforcing the writer protocol. A brand new chapter's first draft may only be replaced wholesale; it must not
	// rely on the model obeying the prompt while still exposing fragile precise editing as an executable path.
	completed, err := t.store.Progress.IsChapterCompleted(a.Chapter)
	if err != nil {
		return nil, fmt.Errorf("load progress: %w: %w", errs.ErrStoreRead, err)
	}
	if !completed {
		switch toolLang(t.store) {
		case "vi":
			return nil, fmt.Errorf("Chương %d đang trong giai đoạn sơ thảo, cấm dùng edit_chapter. Nếu bản nháp đã xong, hãy gọi NGAY commit_chapter(chapter=%d) để nộp chương; chỉ khi nội dung có lỗi cứng mới dùng draft_chapter(mode=\"write\", chapter=%d) để ghi đè: %w", a.Chapter, a.Chapter, a.Chapter, errs.ErrToolPrecondition)
		case "en":
			return nil, fmt.Errorf("Chapter %d is a first draft; edit_chapter is forbidden. Call commit_chapter(chapter=%d) to submit; only call draft_chapter(mode=\"write\", chapter=%d) if flawed: %w", a.Chapter, a.Chapter, a.Chapter, errs.ErrToolPrecondition)
		default:
			return nil, fmt.Errorf("第 %d 章尚未完成，初稿禁止使用 edit_chapter；有硬伤请调用 draft_chapter(mode=\"write\", chapter=%d) 整章覆盖: %w", a.Chapter, a.Chapter, errs.ErrToolPrecondition)
		}
	}
	progress, err := t.store.Progress.Load()
	if err != nil {
		return nil, fmt.Errorf("load progress: %w: %w", errs.ErrStoreRead, err)
	}
	if progress == nil || !slices.Contains(progress.PendingRewrites, a.Chapter) {
		switch toolLang(t.store) {
		case "vi":
			return nil, fmt.Errorf("Chương %d đã hoàn thành và không nằm trong hàng đợi PendingRewrites, không thể sửa; muốn đổi phải chờ editor review kích hoạt viết lại/gọt giũa trước: %w", a.Chapter, errs.ErrToolPrecondition)
		case "en":
			return nil, fmt.Errorf("Chapter %d is completed and not in the PendingRewrites queue, cannot edit; ask the editor review to trigger a rewrite/polish first: %w", a.Chapter, errs.ErrToolPrecondition)
		default:
			return nil, fmt.Errorf("第 %d 章已完成且不在 PendingRewrites 队列中，不能编辑；需修改请先由 editor 评审触发重写/打磨: %w", a.Chapter, errs.ErrToolPrecondition)
		}
	}
	if err := EnsureChapterExpanded(t.store, a.Chapter); err != nil {
		return nil, err
	}

	// Seed: when drafts does not exist, copy one from chapters as the starting point
	if err := t.ensureDraft(a.Chapter); err != nil {
		return nil, err
	}

	// Delegate the find-and-replace to agentcore.EditTool
	subArgs, _ := json.Marshal(map[string]any{
		"path":        fmt.Sprintf("drafts/%02d.draft.md", a.Chapter),
		"file_path":   fmt.Sprintf("drafts/%02d.draft.md", a.Chapter),
		"old_text":    a.OldString,
		"old_string":  a.OldString,
		"new_text":    a.NewString,
		"new_string":  a.NewString,
		"replace_all": a.ReplaceAll,
	})
	result, err := t.edit.Execute(ctx, subArgs)
	if err != nil {
		return nil, fmt.Errorf("apply edit: %w: %w", errs.ErrToolPrecondition, err)
	}

	if _, err := t.store.Checkpoints.AppendArtifact(
		domain.ChapterScope(a.Chapter), "edit",
		fmt.Sprintf("drafts/%02d.draft.md", a.Chapter),
	); err != nil {
		return nil, fmt.Errorf("checkpoint edit: %w: %w", errs.ErrStoreWrite, err)
	}

	// Additional guidance: tell the writer what the next steps are, so check_consistency / commit_chapter is not missed
	var passthrough map[string]any
	if err := json.Unmarshal(result, &passthrough); err != nil {
		return result, nil
	}
	passthrough["chapter"] = a.Chapter
	passthrough["next_step"] = editNextStepHint(t.store)
	return json.Marshal(passthrough)
}

// ensureDraft guarantees that drafts/{ch}.draft.md exists:
//   - a draft already exists -> return directly
//   - no draft but a final version exists -> copy the final version into drafts as the starting point for edits (common in polish scenarios)
//   - neither exists -> error, telling the user to create the first draft with draft_chapter
func (t *EditChapterTool) ensureDraft(chapter int) error {
	draft, err := t.store.Drafts.LoadDraft(chapter)
	if err != nil {
		return fmt.Errorf("load draft: %w: %w", errs.ErrStoreRead, err)
	}
	if draft != "" {
		return nil
	}
	text, err := t.store.Drafts.LoadChapterText(chapter)
	if err != nil {
		return fmt.Errorf("load chapter: %w: %w", errs.ErrStoreRead, err)
	}
	if text == "" {
		switch toolLang(t.store) {
		case "vi":
			return fmt.Errorf("Chương %d chưa có bản nháp lẫn bản chính, hãy gọi draft_chapter(mode=write, chapter=%d) để tạo bản thảo trước: %w", chapter, chapter, errs.ErrToolPrecondition)
		case "en":
			return fmt.Errorf("Chapter %d has neither draft nor final text, call draft_chapter(mode=write, chapter=%d) to create the first draft: %w", chapter, chapter, errs.ErrToolPrecondition)
		default:
			return fmt.Errorf("第 %d 章无草稿也无终稿，请先调 draft_chapter(mode=write, chapter=%d) 创建初稿: %w", chapter, chapter, errs.ErrToolPrecondition)
		}
	}
	if err := t.store.Drafts.SaveDraft(chapter, text); err != nil {
		return fmt.Errorf("seed draft from chapter: %w: %w", errs.ErrStoreWrite, err)
	}
	return nil
}
