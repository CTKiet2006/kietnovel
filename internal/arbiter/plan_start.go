package arbiter

import (
	"context"
	"fmt"
	"strings"

	"github.com/CTKiet2006/kietnovel/internal/llmcontract"
	"github.com/voocel/agentcore"
	"github.com/voocel/agentcore/schema"
)

// PlanStartDecision is a start-up arbitration: pick a planner and produce the (possibly expanded) task text.
type PlanStartDecision struct {
	Planner string `json:"planner"` // architect_long | architect_short
	Task    string `json:"task"`    // the full task handed to the planner (including the expanded requirements)
	Reason  string `json:"reason"`
}

func (d *PlanStartDecision) Validate() error {
	if d.Planner != "architect_long" && d.Planner != "architect_short" {
		return fmt.Errorf("planner 非法: %q（可选 architect_long / architect_short）", d.Planner)
	}
	if strings.TrimSpace(d.Task) == "" {
		return fmt.Errorf("task 不能为空")
	}
	if strings.TrimSpace(d.Reason) == "" {
		return fmt.Errorf("reason 不能为空")
	}
	return nil
}

// planStartContract sits next to PlanStartDecision: all fields are required and planner is a closed enum.
var planStartContract = llmcontract.Contract{
	Name:        "arbiter_plan_start",
	Description: "启动裁定:选规划师并产出完整任务文本",
	Schema: schema.Object(
		schema.Property("planner", schema.Enum("规划师", "architect_long", "architect_short")).Required(),
		schema.Property("task", schema.String("交给规划师的完整任务(含扩充后的需求)")).Required(),
		schema.Property("reason", schema.String("选择理由")).Required(),
	),
}

// planStartPayload is the user payload of plan_start (the facts are the input, there is no store state — a new book).
type planStartPayload struct {
	Requirement string `json:"requirement"`
	Style       string `json:"style,omitempty"`
}

// DecidePlanStart arbitrates the start-up: pick a planner from the user requirements; when the requirement is too short (<20 characters) it autonomously
// adds differentiating directions, the target reader and core consumption hook, and at least one unconventional hook to the task.
// Failure semantics: returning an error → the caller raises the error explicitly and aborts the start-up (the user is present at start-up, so an error beats a guess).
func DecidePlanStart(ctx context.Context, model agentcore.ChatModel, systemPrompt, requirement, style string) (PlanStartDecision, error) {
	payload, err := marshalPayload(planStartPayload{Requirement: requirement, Style: style})
	if err != nil {
		return PlanStartDecision{}, err
	}
	return decide(ctx, model, planStartContract, systemPrompt, payload, (*PlanStartDecision).Validate)
}
