package agents

// End-to-end verification of the combined behavior of the save_review hard stop and the task-aware StopGuard (build.go's editor
// configuration is wired for real: StopAfterToolResult matches save_review/save_*_summary,
// StopGuardFactory uses a real guard, and tools persist real checkpoints).
//
// Scenario 1 (summary task reviews first): the editor is dispatched to write an arc summary but calls save_review first —
// the hard stop fires but the guard vetoes it, and after the nudge is injected the editor only truly exits at save_arc_summary.
// This is the safety premise for restoring the save_review hard stop, and it prevents a regression of the livelock where the arc summary never lands.
//
// Scenario 2 (review task finishes in one step): the editor is dispatched to review and save_review landing passes the hard stop right away,
// with no extra LLM turn spent on wrapping up.

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"

	"github.com/CTKiet2006/kietnovel/internal/agents/guard"
	"github.com/CTKiet2006/kietnovel/internal/domain"
	"github.com/CTKiet2006/kietnovel/internal/store"
	"github.com/voocel/agentcore"
	"github.com/voocel/agentcore/subagent"
)

// editorStopAfterToolResult keeps the same criterion as the editor configuration in build.go.
func editorStopAfterToolResult(toolName string, _ json.RawMessage) bool {
	return toolName == "save_review" || toolName == "save_arc_summary" || toolName == "save_volume_summary"
}

func checkpointTool(t *testing.T, st *store.Store, name, step string) agentcore.Tool {
	t.Helper()
	return agentcore.NewFuncTool(name, "fake "+name, map[string]any{"type": "object"},
		func(context.Context, json.RawMessage) (json.RawMessage, error) {
			if _, err := st.Checkpoints.Append(domain.ArcScope(1, 1), step, "artifact", "digest"); err != nil {
				t.Fatalf("append checkpoint %s: %v", step, err)
			}
			return json.RawMessage(`"saved"`), nil
		})
}

func runEditorLike(t *testing.T, st *store.Store, task string, model agentcore.ChatModel, tools []agentcore.Tool) {
	t.Helper()
	cfg := subagent.Config{
		Name:                "editor",
		Description:         "test editor",
		Model:               model,
		SystemPrompt:        "test",
		Tools:               tools,
		MaxTurns:            10,
		StopAfterToolResult: editorStopAfterToolResult,
		StopGuardFactory: func(_, task string) agentcore.StopGuard {
			return guard.NewEditorStopGuard(st, task, nil)
		},
	}
	tool := subagent.NewRunner(cfg).AsTool()
	args, _ := json.Marshal(map[string]string{"agent": "editor", "task": task})
	if _, err := tool.Execute(context.Background(), args); err != nil {
		t.Fatalf("subagent execute: %v", err)
	}
}

func TestEditorFlow_SummaryTaskSurvivesEarlyReview(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatalf("init store: %v", err)
	}

	var calls atomic.Int32
	model := &contractModel{fn: func(i int, _ []agentcore.Message) (*agentcore.LLMResponse, error) {
		switch i {
		case 0:
			// Off track: a summary task reviews first.
			return &agentcore.LLMResponse{Message: assistantToolCall("save_review", `{}`)}, nil
		default:
			// Only after the guard vetoes the hard stop and injects the nudge does this turn produce the summary.
			calls.Add(1)
			return &agentcore.LLMResponse{Message: assistantToolCall("save_arc_summary", `{}`)}, nil
		}
	}}

	runEditorLike(t, st, "生成第 1 卷第 1 弧摘要（save_arc_summary）", model, []agentcore.Tool{
		checkpointTool(t, st, "save_review", "review"),
		checkpointTool(t, st, "save_arc_summary", "arc_summary"),
	})

	if calls.Load() == 0 {
		t.Fatal("save_review 硬停被 guard 否决后，editor 应继续走到 save_arc_summary——若 run 在复核后直接结束，说明终态退出绕过了 guard，弧摘要死循环会回归")
	}
	all := st.Checkpoints.All()
	var hasSummary bool
	for _, cp := range all {
		if cp.Step == "arc_summary" {
			hasSummary = true
		}
	}
	if !hasSummary {
		t.Fatal("弧摘要必须最终落盘")
	}
}

func TestEditorFlow_ReviewTaskStopsAtSaveReview(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatalf("init store: %v", err)
	}

	model := &contractModel{fn: func(i int, _ []agentcore.Message) (*agentcore.LLMResponse, error) {
		if i == 0 {
			return &agentcore.LLMResponse{Message: assistantToolCall("save_review", `{}`)}, nil
		}
		t.Fatal("评审任务 save_review 落盘后应硬停，模型不应获得额外轮次")
		return nil, nil
	}}

	runEditorLike(t, st, "对第 1 卷第 1 弧做弧级评审（scope=arc）", model, []agentcore.Tool{
		checkpointTool(t, st, "save_review", "review"),
	})

	if got := model.calls(); got != 1 {
		t.Fatalf("评审任务应恰好一次模型调用后收尾，got %d", got)
	}
}
