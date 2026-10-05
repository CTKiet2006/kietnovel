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

// ResolveOutlineFeedbackTool 落盘“现有计划仍适用”的审查结论并消费反馈。
type ResolveOutlineFeedbackTool struct{ store *store.Store }

func NewResolveOutlineFeedbackTool(store *store.Store) *ResolveOutlineFeedbackTool {
	return &ResolveOutlineFeedbackTool{store: store}
}

func (t *ResolveOutlineFeedbackTool) Name() string  { return "resolve_outline_feedback" }
func (t *ResolveOutlineFeedbackTool) Label() string { return "确认大纲无需调整" }
func (t *ResolveOutlineFeedbackTool) Description() string {
	return "确认已审查全部 writer_feedback，且现有后续计划仍然适用。只有无需修改大纲时调用；需要修改时使用 revise_outline 或结构工具。"
}
func (t *ResolveOutlineFeedbackTool) ReadOnly(json.RawMessage) bool        { return false }
func (t *ResolveOutlineFeedbackTool) ConcurrencySafe(json.RawMessage) bool { return false }
func (t *ResolveOutlineFeedbackTool) StrictSchema() bool                   { return true }
func (t *ResolveOutlineFeedbackTool) Schema() map[string]any {
	return schema.Object(schema.Property("reason", schema.String("现有计划仍然适用的理由")).Required())
}

func (t *ResolveOutlineFeedbackTool) Execute(_ context.Context, args json.RawMessage) (json.RawMessage, error) {
	var input struct {
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(args, &input); err != nil {
		return nil, fmt.Errorf("invalid args: %w: %w", errs.ErrToolArgs, err)
	}
	input.Reason = strings.TrimSpace(input.Reason)
	if input.Reason == "" {
		return nil, fmt.Errorf("reason is required: %w", errs.ErrToolArgs)
	}
	feedback, err := t.store.Outline.LoadPendingOutlineFeedback()
	if err != nil {
		return nil, fmt.Errorf("load outline feedback: %w: %w", errs.ErrStoreRead, err)
	}
	if len(feedback) == 0 {
		return nil, fmt.Errorf("没有待处理的大纲反馈: %w", errs.ErrToolPrecondition)
	}
	if err := t.store.Outline.SaveOutlineFeedbackResolution(input.Reason, len(feedback)); err != nil {
		return nil, fmt.Errorf("save outline feedback resolution: %w: %w", errs.ErrStoreWrite, err)
	}
	if _, err := t.store.Checkpoints.AppendArtifact(domain.GlobalScope(), "resolve_outline_feedback", "meta/outline_feedback_resolution.json"); err != nil {
		return nil, fmt.Errorf("checkpoint outline feedback resolution: %w: %w", errs.ErrStoreWrite, err)
	}
	if err := t.store.Outline.ClearOutlineFeedback(); err != nil {
		return nil, fmt.Errorf("clear outline feedback: %w: %w", errs.ErrStoreWrite, err)
	}
	return json.Marshal(map[string]any{"resolved": len(feedback), "outline_changed": false, "reason": input.Reason})
}
