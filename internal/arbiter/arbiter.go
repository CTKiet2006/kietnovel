// Package arbiter is the semantic arbitration layer: an LLM-as-function woken up on demand.
//
// The two planes are symmetric (docs/engine-arbiter.md §2):
//
//	Deterministic plane:  flow.LoadState   → flow.Route     → Instruction
//	Semantic plane:       arbiter.Collect* → arbiter.Decide* → XxxDecision
//
// Discipline: Collect concentrates the IO (reading all the facts from the store); Decide has no IO other than the model
// requests managed by the unified executor, so it can replay offline from historical facts; execution belongs to the Engine.
// One pair of functions plus a dedicated Decision type per scenario; actions that do not match the scenario are inexpressible at the type
// level, and the remaining legality is rejected by each type's Validate — Arbiter output is just as untrustworthy as any other LLM output, so fact validation is the last gate.
package arbiter

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/CTKiet2006/kietnovel/internal/llmcontract"
	"github.com/voocel/agentcore"
	"github.com/voocel/agentcore/schema"
)

// decideMaxTokens caps the output of a single arbitration; the arbitration JSON is tiny, so most of the budget is left to the
// reasoning model's thinking budget (same reasoning as userrules.normalizeMaxTokens).
const decideMaxTokens = 8192

// decide hands the scenario contract and business validation to the unified structured executor. No IO other than the model call.
func decide[T any](ctx context.Context, model agentcore.ChatModel, contract llmcontract.Contract, systemPrompt, payload string, validate func(*T) error) (T, error) {
	out, err := llmcontract.Execute(ctx, model, llmcontract.Request[T]{
		Contract:     contract,
		SystemPrompt: systemPrompt,
		Payload:      payload,
		Options:      []agentcore.CallOption{agentcore.WithMaxTokens(decideMaxTokens)},
		Validate:     validate,
		Agent:        "arbiter",
		Hooks: llmcontract.Hooks{
			Resolved: func(res llmcontract.Resolution) {
				slog.Debug("裁定协议选择", "module", "arbiter",
					"contract", contract.Name, "structured_mode", res.Mode,
					"capability_source", res.Source, "provider", res.Provider,
					"model", res.Model, "schema_fingerprint", contract.Fingerprint())
			},
			Correction: func(ev llmcontract.Correction) {
				slog.Warn("裁定输出自愈", "module", "arbiter", "attempt", ev.Attempt,
					"layer", ev.Layer, "structured_mode", ev.Mode, "err", ev.Err)
			},
		},
	})
	if err != nil {
		return out, fmt.Errorf("arbiter: %w", err)
	}
	return out, nil
}

// DispatchOp is the dispatch action shared by every scenario.
type DispatchOp struct {
	Agent string `json:"agent"`
	Task  string `json:"task"`
}

// workerNames are the legal dispatch targets (consistent with what agents.BuildWorkers registers). An ordered slice:
// it doubles as the schema enum (a fixed order keeps the fingerprint stable) and as the validation allowlist.
var workerNames = []string{"architect_long", "architect_short", "writer", "editor"}

func (d *DispatchOp) validate() error {
	if d == nil {
		return nil
	}
	if !slices.Contains(workerNames, d.Agent) {
		return fmt.Errorf("dispatch.agent 非法: %q", d.Agent)
	}
	if strings.TrimSpace(d.Task) == "" {
		return fmt.Errorf("dispatch.task 不能为空")
	}
	return nil
}

// dispatchSchema is the nullable schema slot of DispatchOp: actions that need a dispatch supply an object,
// everything else is null (strict mode requires all fields, so optional semantics are expressed as null).
func dispatchSchema(desc string) map[string]any {
	return llmcontract.Nullable(schema.Object(
		schema.Property("agent", schema.Enum(desc, workerNames...)).Required(),
		schema.Property("task", schema.String("交给该 worker 的完整任务描述")).Required(),
	))
}

// marshalPayload serializes the fact packet; failure is a programming error and must surface — silently faking empty facts
// would make the model misjudge based on false input.
func marshalPayload(v any) (string, error) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "", fmt.Errorf("arbiter: 事实包序列化失败: %w", err)
	}
	return string(data), nil
}
