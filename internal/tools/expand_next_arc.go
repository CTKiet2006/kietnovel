package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/voocel/agentcore/schema"
	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/errs"
	"github.com/voocel/ainovel-cli/internal/store"
)

// ExpandNextArcTool 将当前已完成弧之后的骨架展开为详细章节。
type ExpandNextArcTool struct {
	store *store.Store
}

func NewExpandNextArcTool(store *store.Store) *ExpandNextArcTool {
	return &ExpandNextArcTool{store: store}
}

func (t *ExpandNextArcTool) Name() string  { return "expand_next_arc" }
func (t *ExpandNextArcTool) Label() string { return "展开下一弧" }
func (t *ExpandNextArcTool) Description() string {
	return "展开当前已完成弧之后的下一骨架弧。目标卷弧由系统根据进度和大纲确定；只需提交结合已完成事实校准后的 title、goal 和 chapters。"
}

func (t *ExpandNextArcTool) ReadOnly(json.RawMessage) bool        { return false }
func (t *ExpandNextArcTool) ConcurrencySafe(json.RawMessage) bool { return false }
func (t *ExpandNextArcTool) StrictSchema() bool                   { return true }

func (t *ExpandNextArcTool) Schema() map[string]any {
	chapter := schema.Object(
		schema.Property("title", schema.String("章节标题")).Required(),
		schema.Property("core_event", schema.String("本章核心事件")).Required(),
		schema.Property("hook", schema.String("章末钩子")).Required(),
		schema.Property("scenes", schema.Array("计划场景；无则为空数组", schema.String(""))).Required(),
	)
	return schema.Object(
		schema.Property("title", schema.String("结合已完成事实校准后的弧标题")).Required(),
		schema.Property("goal", schema.String("结合已完成事实校准后的弧目标")).Required(),
		schema.Property("chapters", schema.Array("该弧的详细章节计划", chapter)).Required(),
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
