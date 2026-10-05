package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/voocel/agentcore/schema"
	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/errs"
	"github.com/voocel/ainovel-cli/internal/store"
)

// ReviseOutlineTool 让 Architect 用完整替换内容修订尚未发生的大纲尾段。
type ReviseOutlineTool struct {
	store *store.Store
}

func NewReviseOutlineTool(store *store.Store) *ReviseOutlineTool {
	return &ReviseOutlineTool{store: store}
}

func (t *ReviseOutlineTool) Name() string  { return "revise_outline" }
func (t *ReviseOutlineTool) Label() string { return "修订大纲" }
func (t *ReviseOutlineTool) Description() string {
	return "修订尚未发生的大纲。从 from_chapter 起，用 replacement 完整替换后续计划：" +
		"扁平大纲替换全书尾段，分层大纲替换该章所在弧的尾段；已完成或正在写作的章节不可移动。" +
		"需要保留的后续章节必须一并放入 replacement。"
}

func (t *ReviseOutlineTool) ReadOnly(json.RawMessage) bool        { return false }
func (t *ReviseOutlineTool) ConcurrencySafe(json.RawMessage) bool { return false }
func (t *ReviseOutlineTool) StrictSchema() bool                   { return true }

func (t *ReviseOutlineTool) Schema() map[string]any {
	entry := schema.Object(
		schema.Property("title", schema.String("章节标题")).Required(),
		schema.Property("core_event", schema.String("本章核心事件")).Required(),
		schema.Property("hook", schema.String("章末钩子")).Required(),
		schema.Property("scenes", schema.Array("计划场景；无则为空数组", schema.String(""))).Required(),
	)
	return schema.Object(
		schema.Property("from_chapter", schema.Int("从这一章开始替换尚未发生的计划")).Required(),
		schema.Property("replacement", schema.Array("完整替换尾段；需要保留的后续章节也必须包含", entry)).Required(),
		schema.Property("reason", schema.String("本次修订原因")).Required(),
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
