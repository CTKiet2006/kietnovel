package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/CTKiet2006/kietnovel/internal/domain"
	"github.com/CTKiet2006/kietnovel/internal/errs"
	"github.com/CTKiet2006/kietnovel/internal/store"
	"github.com/voocel/agentcore/schema"
)

// ExpandNextArcTool expands the skeleton after the currently completed arc into detailed chapters.
type ExpandNextArcTool struct {
	store *store.Store
}

func NewExpandNextArcTool(store *store.Store) *ExpandNextArcTool {
	return &ExpandNextArcTool{store: store}
}

func (t *ExpandNextArcTool) Name() string { return "expand_next_arc" }
func (t *ExpandNextArcTool) Label() string {
	switch toolLang(t.store) {
	case "vi":
		return "Mở rộng arc tiếp theo"
	case "en":
		return "Expand next arc"
	default:
		return "展开下一弧"
	}
}
func (t *ExpandNextArcTool) Description() string {
	switch toolLang(t.store) {
	case "vi":
		return "Mở rộng arc khung sườn tiếp theo sau arc vừa hoàn thành. Quyển và arc đích được xác định tự động theo tiến độ và dàn ý; chỉ cần nộp title, goal và chapters đã được căn chỉnh theo dữ kiện thực tế."
	case "en":
		return "Expand the next skeleton arc following the completed arc. Target volume and arc are determined automatically by progress and outline; submit title, goal, and chapters calibrated against established facts."
	default:
		return "展开当前已完成弧之后的下一骨架弧。目标卷弧由系统根据进度和大纲确定；只需提交结合已完成事实校准后的 title、goal 和 chapters。"
	}
}

func (t *ExpandNextArcTool) ReadOnly(json.RawMessage) bool        { return false }
func (t *ExpandNextArcTool) ConcurrencySafe(json.RawMessage) bool { return false }
func (t *ExpandNextArcTool) StrictSchema() bool                   { return true }

func (t *ExpandNextArcTool) Schema() map[string]any {
	titleDesc := "章节标题"
	eventDesc := "本章核心事件"
	hookDesc := "章末钩子"
	scenesDesc := "计划场景；无则为空数组"
	arcTitleDesc := "结合已完成事实校准后的弧标题"
	arcGoalDesc := "结合已完成事实校准后的弧目标"
	chapsDesc := "该弧的详细章节计划"

	switch toolLang(t.store) {
	case "vi":
		titleDesc = "Tiêu đề chương"
		eventDesc = "Sự kiện cốt lõi của chương"
		hookDesc = "Móc câu treo cuối chương"
		scenesDesc = "Các phân cảnh dự kiến; mảng rỗng nếu không có"
		arcTitleDesc = "Tiêu đề arc đã căn chỉnh theo thực tế đã viết"
		arcGoalDesc = "Mục tiêu arc đã căn chỉnh theo thực tế đã viết"
		chapsDesc = "Kế hoạch chi tiết các chương trong arc"
	case "en":
		titleDesc = "Chapter title"
		eventDesc = "Core chapter event"
		hookDesc = "End-of-chapter hook"
		scenesDesc = "Planned scenes; empty array if none"
		arcTitleDesc = "Calibrated arc title"
		arcGoalDesc = "Calibrated arc goal"
		chapsDesc = "Detailed chapter plans for this arc"
	}

	chapter := schema.Object(
		schema.Property("title", schema.String(titleDesc)).Required(),
		schema.Property("core_event", schema.String(eventDesc)).Required(),
		schema.Property("hook", schema.String(hookDesc)).Required(),
		schema.Property("scenes", schema.Array(scenesDesc, schema.String(""))).Required(),
	)
	return schema.Object(
		schema.Property("title", schema.String(arcTitleDesc)).Required(),
		schema.Property("goal", schema.String(arcGoalDesc)).Required(),
		schema.Property("chapters", schema.Array(chapsDesc, chapter)).Required(),
	)
}

func (t *ExpandNextArcTool) Execute(_ context.Context, args json.RawMessage) (json.RawMessage, error) {
	var expansion domain.ArcExpansion
	if err := json.Unmarshal(args, &expansion); err != nil {
		return nil, fmt.Errorf("invalid args: %w: %w", errs.ErrToolArgs, err)
	}
	position, err := t.store.ExpandNextArc(expansion)
	if err != nil {
		return nil, fmt.Errorf("expand next arc: %w: %w", errs.ErrStoreWrite, err)
	}
	if err := consumeWriterFeedback(t.store); err != nil {
		return nil, err
	}
	if _, err := t.store.Checkpoints.AppendArtifact(domain.ArcScope(position.Volume, position.Arc), t.Name(), "layered_outline.json"); err != nil {
		return nil, fmt.Errorf("checkpoint %s: %w: %w", t.Name(), errs.ErrStoreWrite, err)
	}
	return json.Marshal(map[string]any{
		"saved":    true,
		"type":     t.Name(),
		"volume":   position.Volume,
		"arc":      position.Arc,
		"title":    expansion.Title,
		"goal":     expansion.Goal,
		"chapters": len(expansion.Chapters),
	})
}
