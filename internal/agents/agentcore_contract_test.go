package agents

// agentcore contract tests: pin the framework behavior this project depends on into executable assertions.
// Each test names its dependents; all of them must be green before bumping agentcore — comments go stale, tests do not.
// Everything is driven through subagent.Runner.Run — the Engine's actual dispatch channel.
//
// The pinned contracts:
//  1. A terminal StopAfterTools/StopAfterToolResult exit passes through StopGuard (StopTriggerAfterTool),
//     and a guard veto (InjectMessage) can pull the run back to continuing — the task-aware
//     EditorStopGuard in guard/subagent_guards.go relies on this behavior to catch the "dispatched to write a summary but only reviewed" early exit.
//  2. StopReasonError / StopReasonAborted terminate the run outright and never reach StopGuard —
//     so hardStopReasons in guard/subagent_guards.go only needs to list safety/content_filter.
//  3. A provider refusal (safety and other non-error stops) reaches StopGuard via the end_turn path,
//     and info.Message.StopReason keeps its original value — hardStopReasons' immediate escalation depends on this path.
//  4. After StopGuard returns InjectMessage the model gets a new turn; returning Escalate terminates immediately,
//     and the error chain is matchable by errors.Is(err, agentcore.ErrStopGuard) —
//     guard/stop_guard.go's "physically unable to stop" and over-limit escalation rely on this semantics.
//  5. Runner.Run's errors keep a typed chain: an unregistered agent matches subagent.ErrUnknownAgent —
//     host/engine.go's isDeterministicWorkerError depends on this classification rather than on error text.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/voocel/agentcore"
	"github.com/voocel/agentcore/subagent"
)

// contractModel is a mock model that returns preset responses by call ordinal.
type contractModel struct {
	fn  func(i int, msgs []agentcore.Message) (*agentcore.LLMResponse, error)
	idx int64
}

func (m *contractModel) take(msgs []agentcore.Message) (*agentcore.LLMResponse, error) {
	i := int(atomic.AddInt64(&m.idx, 1) - 1)
	return m.fn(i, msgs)
}

func (m *contractModel) calls() int { return int(atomic.LoadInt64(&m.idx)) }

func (m *contractModel) Generate(_ context.Context, msgs []agentcore.Message, _ []agentcore.ToolSpec, _ ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	return m.take(msgs)
}

func (m *contractModel) GenerateStream(_ context.Context, msgs []agentcore.Message, _ []agentcore.ToolSpec, _ ...agentcore.CallOption) (<-chan agentcore.StreamEvent, error) {
	resp, err := m.take(msgs)
	if err != nil {
		return nil, err
	}
	ch := make(chan agentcore.StreamEvent, 1)
	ch <- agentcore.StreamEvent{Type: agentcore.StreamEventDone, Message: resp.Message, StopReason: resp.Message.StopReason}
	close(ch)
	return ch, nil
}

func (m *contractModel) SupportsTools() bool { return true }

func assistantText(text string, stop agentcore.StopReason) agentcore.Message {
	return agentcore.Message{
		Role:       agentcore.RoleAssistant,
		Content:    []agentcore.ContentBlock{agentcore.TextBlock(text)},
		StopReason: stop,
	}
}

func assistantToolCall(name string, args string) agentcore.Message {
	return agentcore.Message{
		Role: agentcore.RoleAssistant,
		Content: []agentcore.ContentBlock{agentcore.ToolCallBlock(agentcore.ToolCall{
			ID: "tc-" + name, Name: name, Args: json.RawMessage(args),
		})},
		StopReason: agentcore.StopReasonToolUse,
	}
}

func okTool(name string) agentcore.Tool {
	return agentcore.NewFuncTool(name, "contract test tool", map[string]any{"type": "object"},
		func(context.Context, json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(`"ok"`), nil
		})
}

// runSubagent runs a single dispatch with the given config through Runner.Run (the Engine's dispatch channel).
// It returns execution errors — a StopGuard escalation termination surfaces as an error (that is itself a contract),
// so cases that expect a normal ending assert nil themselves.
func runSubagent(t *testing.T, cfg subagent.Config) error {
	t.Helper()
	_, err := subagent.NewRunner(cfg).Run(context.Background(), cfg.Name, "contract")
	return err
}

// Contract 1: a terminal tool exit passes through StopGuard; after a guard veto (InjectMessage) the run continues.
// Dependent: EditorStopGuard — once a terminal tool such as save_review matches, the task-aware guard must
// get a chance to pull back an early exit where "the artifact was not persisted".
func TestContract_TerminalToolExitConsultsStopGuard(t *testing.T) {
	var guardCalls atomic.Int32
	var trigger atomic.Value

	model := &contractModel{fn: func(i int, _ []agentcore.Message) (*agentcore.LLMResponse, error) {
		switch i {
		case 0:
			return &agentcore.LLMResponse{Message: assistantToolCall("finish", `{}`)}, nil
		default:
			// After the guard vetoes a terminal exit the model must get a new turn; that turn ends normally.
			return &agentcore.LLMResponse{Message: assistantText("done", agentcore.StopReasonStop)}, nil
		}
	}}

	if err := runSubagent(t, subagent.Config{
		Name:           "editorish",
		Description:    "contract",
		Model:          model,
		SystemPrompt:   "test",
		Tools:          []agentcore.Tool{okTool("finish")},
		MaxTurns:       5,
		StopAfterTools: []string{"finish"},
		StopGuardFactory: func(_, _ string) agentcore.StopGuard {
			return func(_ context.Context, info agentcore.StopInfo) agentcore.StopDecision {
				n := guardCalls.Add(1)
				if n == 1 {
					trigger.Store(info.Trigger)
					return agentcore.StopDecision{Allow: false, InjectMessage: "还没落盘，继续"}
				}
				return agentcore.StopDecision{Allow: true}
			}
		},
	}); err != nil {
		t.Fatalf("subagent execute: %v", err)
	}

	if guardCalls.Load() < 2 {
		t.Fatalf("终态工具退出必须触达 StopGuard 且否决后继续（期望 ≥2 次咨询），got %d", guardCalls.Load())
	}
	if got := trigger.Load(); got != agentcore.StopTriggerAfterTool {
		t.Fatalf("终态退出的 Trigger 应为 StopTriggerAfterTool，got %v", got)
	}
	if model.calls() < 2 {
		t.Fatalf("guard 否决后模型应获得新一轮，got %d calls", model.calls())
	}
}

// Contract 2: StopReasonError / StopReasonAborted terminate directly without reaching StopGuard.
// Dependent: the hardStopReasons comment — it only has to handle refusal semantics that actually reach the guard.
func TestContract_ErrorAndAbortedStopSkipStopGuard(t *testing.T) {
	for _, stop := range []agentcore.StopReason{agentcore.StopReasonError, agentcore.StopReasonAborted} {
		t.Run(string(stop), func(t *testing.T) {
			var guardCalls atomic.Int32
			model := &contractModel{fn: func(int, []agentcore.Message) (*agentcore.LLMResponse, error) {
				return &agentcore.LLMResponse{Message: assistantText("dead", stop)}, nil
			}}
			_ = runSubagent(t, subagent.Config{
				Name: "dying", Description: "contract", Model: model,
				SystemPrompt: "test", MaxTurns: 5,
				StopGuardFactory: func(_, _ string) agentcore.StopGuard {
					return func(context.Context, agentcore.StopInfo) agentcore.StopDecision {
						guardCalls.Add(1)
						return agentcore.StopDecision{Allow: true}
					}
				},
			}) // the error semantics of error/aborted stops are defined by the subagent layer; here we only care whether the guard is reached
			if guardCalls.Load() != 0 {
				t.Fatalf("%s 停机不应触达 StopGuard，got %d 次咨询", stop, guardCalls.Load())
			}
		})
	}
}

// Contract 3: a provider refusal (safety and friends) reaches StopGuard via the end_turn path,
// and info.Message.StopReason keeps its original value. Dependent: hardStopReasons' immediate escalation.
func TestContract_SafetyStopReachesStopGuardWithReason(t *testing.T) {
	var seen atomic.Value
	model := &contractModel{fn: func(int, []agentcore.Message) (*agentcore.LLMResponse, error) {
		return &agentcore.LLMResponse{Message: assistantText("refused", agentcore.StopReason("safety"))}, nil
	}}
	err := runSubagent(t, subagent.Config{
		Name: "refused", Description: "contract", Model: model,
		SystemPrompt: "test", MaxTurns: 5,
		StopGuardFactory: func(_, _ string) agentcore.StopGuard {
			return func(_ context.Context, info agentcore.StopInfo) agentcore.StopDecision {
				seen.Store(info.Message.StopReason)
				return agentcore.StopDecision{Allow: false, Escalate: true}
			}
		},
	})
	if got := seen.Load(); got != agentcore.StopReason("safety") {
		t.Fatalf("StopGuard 应看到原始 stop reason safety，got %v", got)
	}
	if !errors.Is(err, agentcore.ErrStopGuard) {
		t.Fatalf("Escalate 应以可 errors.Is(agentcore.ErrStopGuard) 的错误浮出，got %v", err)
	}
}

// Contract 4: on end_turn, InjectMessage gives the model a new turn with the injected content present;
// Escalate terminates immediately and the model is not called again. Dependent: the Worker StopGuard's
// "physically unable to stop + repeated over-limit escalation".
func TestContract_StopGuardInjectContinuesEscalateTerminates(t *testing.T) {
	var sawInject atomic.Bool
	model := &contractModel{fn: func(i int, msgs []agentcore.Message) (*agentcore.LLMResponse, error) {
		if i > 0 {
			for _, m := range msgs {
				if strings.Contains(m.TextContent(), "禁止结束-契约") {
					sawInject.Store(true)
				}
			}
		}
		return &agentcore.LLMResponse{Message: assistantText("try stop", agentcore.StopReasonStop)}, nil
	}}

	var guardCalls atomic.Int32
	err := runSubagent(t, subagent.Config{
		Name: "stubborn", Description: "contract", Model: model,
		SystemPrompt: "test", MaxTurns: 10,
		StopGuardFactory: func(_, _ string) agentcore.StopGuard {
			return func(context.Context, agentcore.StopInfo) agentcore.StopDecision {
				switch guardCalls.Add(1) {
				case 1:
					return agentcore.StopDecision{Allow: false, InjectMessage: "禁止结束-契约"}
				default:
					return agentcore.StopDecision{Allow: false, Escalate: true}
				}
			}
		},
	})
	if !errors.Is(err, agentcore.ErrStopGuard) {
		t.Fatalf("Escalate 应以可 errors.Is(agentcore.ErrStopGuard) 的错误浮出，got %v", err)
	}

	if !sawInject.Load() {
		t.Fatal("InjectMessage 后模型的下一轮请求里应包含注入消息")
	}
	if guardCalls.Load() != 2 {
		t.Fatalf("期望 guard 恰被咨询 2 次（1 注入 + 1 升级），got %d", guardCalls.Load())
	}
	if model.calls() != 2 {
		t.Fatalf("Escalate 后模型不应再被调用，期望恰 2 次，got %d", model.calls())
	}
}

// Contract 5: Runner.Run's errors keep a typed chain — an unregistered agent surfaces as subagent.ErrUnknownAgent
// Dependents: host/engine.go's isDeterministicWorkerError ("a retry must fail identically →
// pause outright" classification relies on errors.Is, not on error text matching).
func TestContract_RunUnknownAgentIsTyped(t *testing.T) {
	runner := subagent.NewRunner(subagent.Config{
		Name: "writer", Description: "contract",
		Model: &contractModel{fn: func(int, []agentcore.Message) (*agentcore.LLMResponse, error) {
			return &agentcore.LLMResponse{Message: assistantText("ok", agentcore.StopReasonStop)}, nil
		}},
		SystemPrompt: "test", MaxTurns: 3,
	})
	_, err := runner.Run(context.Background(), "ghost", "contract")
	if !errors.Is(err, subagent.ErrUnknownAgent) {
		t.Fatalf("未注册 agent 应匹配 subagent.ErrUnknownAgent，got %v", err)
	}
}
