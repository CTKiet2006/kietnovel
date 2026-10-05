package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/CTKiet2006/kietnovel/internal/domain"
	"github.com/CTKiet2006/kietnovel/internal/errs"
	"github.com/CTKiet2006/kietnovel/internal/store"
	"github.com/voocel/agentcore/schema"
)

// ReviseOutlineTool lets the Architect revise the not-yet-written tail of the outline with full replacement content.
type ReviseOutlineTool struct {
	store *store.Store
}

func NewReviseOutlineTool(store *store.Store) *ReviseOutlineTool {
	return &ReviseOutlineTool{store: store}
}

func (t *ReviseOutlineTool) Name() string { return "revise_outline" }
func (t *ReviseOutlineTool) Label() string {
	switch toolLang(t.store) {
	case "vi":
		return "Tu chỉnh dàn ý"
	case "en":
		return "Revise outline"
	default:
		return "修订大纲"
	}
}
func (t *ReviseOutlineTool) Description() string {
	switch toolLang(t.store) {
	case "vi":
		return "Tu chỉnh các chương chưa diễn ra trong dàn ý. Bắt đầu từ from_chapter, dùng replacement thay thế toàn bộ kế hoạch phía sau: " +
			"dàn ý phẳng thay thế đoạn đuôi của toàn bộ sách, dàn ý phân tầng thay thế đoạn đuôi của arc chứa chương đó; các chương đã hoàn thành hoặc đang viết không được xê dịch. " +
			"Các chương tiếp theo muốn giữ lại phải được đưa vào replacement."
	case "en":
		return "Revise unwritten sections of the outline. Starting from from_chapter, replacement fully overwrites subsequent plans: " +
			"flat outline replaces tail of whole book, layered outline replaces tail of containing arc; completed/in-progress chapters cannot be moved. " +
			"Subsequent chapters to preserve must be included in replacement."
	default:
		return "修订尚未发生的大纲。从 from_chapter 起，用 replacement 完整替换后续计划：" +
			"扁平大纲替换全书尾段，分层大纲替换该章所在弧的尾段；已完成或正在写作的章节不可移动。" +
			"需要保留的后续章节必须一并放入 replacement。"
	}
}

func (t *ReviseOutlineTool) ReadOnly(json.RawMessage) bool        { return false }
func (t *ReviseOutlineTool) ConcurrencySafe(json.RawMessage) bool { return false }
func (t *ReviseOutlineTool) StrictSchema() bool                   { return true }

func (t *ReviseOutlineTool) Schema() map[string]any {
	titleDesc := "章节标题"
	eventDesc := "本章核心事件"
	hookDesc := "章末钩子"
	scenesDesc := "计划场景；无则为空数组"
	fromDesc := "从这一章开始替换尚未发生的计划"
	repDesc := "完整替换尾段；需要保留的后续章节也必须包含"
	reasonDesc := "本次修订原因"

	switch toolLang(t.store) {
	case "vi":
		titleDesc = "Tiêu đề chương"
		eventDesc = "Sự kiện cốt lõi của chương"
		hookDesc = "Móc câu treo cuối chương"
		scenesDesc = "Các phân cảnh dự kiến; mảng rỗng nếu không có"
		fromDesc = "Bắt đầu thay thế kế hoạch chưa diễn ra từ chương này"
		repDesc = "Thay thế hoàn toàn phần đuôi; các chương muốn giữ lại cũng phải bao gồm"
		reasonDesc = "Lý do tu chỉnh lần này"
	case "en":
		titleDesc = "Chapter title"
		eventDesc = "Core chapter event"
		hookDesc = "End-of-chapter hook"
		scenesDesc = "Planned scenes; empty array if none"
		fromDesc = "Start replacing unwritten plans from this chapter onward"
		repDesc = "Full replacement tail; subsequent chapters to keep must be included"
		reasonDesc = "Reason for revision"
	}

	entry := schema.Object(
		schema.Property("title", schema.String(titleDesc)).Required(),
		schema.Property("core_event", schema.String(eventDesc)).Required(),
		schema.Property("hook", schema.String(hookDesc)).Required(),
		schema.Property("scenes", schema.Array(scenesDesc, schema.String(""))).Required(),
	)
	return schema.Object(
		schema.Property("from_chapter", schema.Int(fromDesc)).Required(),
		schema.Property("replacement", schema.Array(repDesc, entry)).Required(),
		schema.Property("reason", schema.String(reasonDesc)).Required(),
	)
}

func (t *ReviseOutlineTool) Execute(_ context.Context, args json.RawMessage) (json.RawMessage, error) {
	var input struct {
		FromChapter int                   `json:"from_chapter"`
		Replacement []domain.OutlineEntry `json:"replacement"`
		Reason      string                `json:"reason"`
	}
	if err := json.Unmarshal(args, &input); err != nil {
		return nil, fmt.Errorf("invalid args: %w: %w", errs.ErrToolArgs, err)
	}
	if input.FromChapter <= 0 {
		return nil, fmt.Errorf("from_chapter must be > 0: %w", errs.ErrToolArgs)
	}
	if strings.TrimSpace(input.Reason) == "" {
		return nil, fmt.Errorf("reason 不能为空: %w", errs.ErrToolArgs)
	}

	total, err := t.store.ReviseOutline(input.FromChapter, input.Replacement)
	if err != nil {
		return nil, fmt.Errorf("revise outline: %w", err)
	}
	artifact := "outline.json"
	result := map[string]any{
		"revised":      true,
		"from_chapter": input.FromChapter,
		"replacement":  len(input.Replacement),
		"reason":       strings.TrimSpace(input.Reason),
	}
	progress, err := t.store.Progress.Load()
	if err != nil {
		return nil, fmt.Errorf("load progress after revise: %w: %w", errs.ErrStoreRead, err)
	}
	if progress != nil && progress.Layered {
		artifact = "layered_outline.json"
		outline, outlineErr := t.store.Outline.LoadOutline()
		if outlineErr != nil {
			return nil, fmt.Errorf("load outlined chapters after revise: %w: %w", errs.ErrStoreRead, outlineErr)
		}
		result["dynamic_planning"] = true
		result["outlined_chapters"] = len(outline)
	} else {
		result["total_chapters"] = total
	}
	if _, err := t.store.Checkpoints.AppendArtifact(domain.GlobalScope(), "revise_outline", artifact); err != nil {
		return nil, fmt.Errorf("checkpoint revise_outline: %w: %w", errs.ErrStoreWrite, err)
	}
	if err := t.store.Outline.ClearOutlineFeedback(); err != nil {
		return nil, fmt.Errorf("clear outline feedback: %w: %w", errs.ErrStoreWrite, err)
	}

	return json.Marshal(result)
}
