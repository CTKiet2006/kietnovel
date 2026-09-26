package capability

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/voocel/agentcore"
	agentllm "github.com/voocel/agentcore/llm"
	domainmodel "github.com/voocel/ainovel-cli/internal/domain/model"
	"github.com/voocel/ainovel-cli/internal/infra/capability/prompt"
	"github.com/voocel/ainovel-cli/internal/infra/llm/models"
	"github.com/voocel/ainovel-cli/internal/infra/store"
)

// thinkingModel 记录循环传来的思考强度；noThinking 时对外声明不支持思考。
type thinkingModel struct {
	*planRuntimeModel
	noThinking bool
	seen       []agentcore.ThinkingLevel
}

func (m *thinkingModel) GenerateStream(ctx context.Context, messages []agentcore.Message, tools []agentcore.ToolSpec, opts ...agentcore.CallOption) (<-chan agentcore.StreamEvent, error) {
	m.seen = append(m.seen, agentcore.ResolveCallConfig(opts).ThinkingLevel)
	return m.planRuntimeModel.GenerateStream(ctx, messages, tools, opts...)
}

func (m *thinkingModel) Capabilities() (agentllm.Capabilities, bool) {
	if m.noThinking {
		return agentllm.Capabilities{}, true
	}
	return agentllm.Capabilities{Thinking: true, DisableThinking: true, ThinkingEffort: true}, true
}

// TestExecuteUsesRoleBindingAndRecordsRunStart：模型按 Worker 角色取绑定（D57），思考
// 强度按模型能力折算后进循环，每次尝试首条事件记录实际使用的模型。
func TestExecuteUsesRoleBindingAndRecordsRunStart(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 21, 9, 0, 0, 0, time.UTC)
	authorityStore, err := store.Open(ctx, filepath.Join(t.TempDir(), "ainovel.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer authorityStore.Close()
	worker, err := prompt.BuiltinWorkerProfile("architect.design")
	if err != nil {
		t.Fatal(err)
	}
	plan := func() json.RawMessage {
		return json.RawMessage(`{"reason":"规划","volumes":[{"volume":1,"title":"卷","summary":"卷"}],` +
			`"arcs":[{"arc":1,"volume":1,"title":"弧","summary":"弧"}],"chapters":[{"chapter":1,"arc":1,"title":"第一章","summary":"开篇"}]}`)
	}
	task := json.RawMessage(`{"intent":"规划开篇","fixed_chapters":3,"requested_chapters":1}`)
	runID := createRuntimeTestRun(t, ctx, authorityStore, "book-plan", now)
	profile := seedRuntimeProfile(t, ctx, authorityStore, "book-plan", worker, task, 1)

	for _, tc := range []struct {
		name         string
		noThinking   bool
		wantThinking agentcore.ThinkingLevel
	}{
		{name: "supported level reaches the loop", wantThinking: agentcore.ThinkingHigh},
		{name: "unsupported model falls back to auto", noThinking: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			operation := domainmodel.Operation{
				ID: "plan-" + tc.name, Kind: domainmodel.OperationDevelopPlan,
				Target: domainmodel.AuthorityTarget{Kind: domainmodel.AuthorityProject, ID: "book-plan"},
				State:  domainmodel.OperationQueued, RunID: runID,
				Snapshot: runtimeSnapshot(task, 1, domainmodel.ApprovalAuto, profile),
				Input:    task, CreatedAt: now, UpdatedAt: now,
			}
			if _, err := authorityStore.CreateOperation(ctx, operation); err != nil {
				t.Fatal(err)
			}
			if operation, err = authorityStore.ClaimOperationForExecutor(ctx, operation.ID, "worker-1", prompt.ExecutorIdentity, time.Minute, now); err != nil {
				t.Fatal(err)
			}
			defaultModel := &thinkingModel{planRuntimeModel: &planRuntimeModel{steps: []json.RawMessage{plan()}, now: now}}
			architect := &thinkingModel{planRuntimeModel: &planRuntimeModel{steps: []json.RawMessage{plan()}, now: now}, noThinking: tc.noThinking}
			runtime := NewRuntime(authorityStore)
			if _, err := runtime.Execute(ctx, operation); !errors.Is(err, domainmodel.ErrInvalid) {
				t.Fatalf("unbound runtime executed: %v", err)
			}
			runtime.Bind(models.Bindings{
				Default: models.Binding{Provider: "test", Model: "default-model", Thinking: agentcore.ThinkingHigh, Digest: "d0", Chat: defaultModel},
				Roles:   map[string]models.Binding{"architect": {Provider: "test", Model: "architect-model", Thinking: agentcore.ThinkingHigh, Digest: "d1", Chat: architect}},
			})
			if _, err := runtime.Execute(ctx, operation); err != nil {
				t.Fatalf("execute: %v", err)
			}
			if defaultModel.requests != 0 || architect.requests == 0 {
				t.Fatalf("architect task must use the architect binding: default=%d architect=%d", defaultModel.requests, architect.requests)
			}
			if len(architect.seen) == 0 || architect.seen[0] != tc.wantThinking {
				t.Fatalf("thinking level seen by the model = %v, want %q", architect.seen, tc.wantThinking)
			}
			events, err := authorityStore.ListOperationEvents(ctx, operation.ID)
			if err != nil {
				t.Fatal(err)
			}
			var started struct {
				Role         string                  `json:"role"`
				Provider     string                  `json:"provider"`
				Model        string                  `json:"model"`
				ConfigDigest string                  `json:"config_digest"`
				Thinking     agentcore.ThinkingLevel `json:"thinking"`
			}
			if len(events) < 2 || events[1].Kind != "agent.run_started" || json.Unmarshal(events[1].Payload, &started) != nil {
				t.Fatalf("first agent event = %+v, want agent.run_started after the claim", events)
			}
			if started.Role != "architect" || started.Model != "architect-model" || started.Provider != "test" || started.ConfigDigest != "d1" || started.Thinking != tc.wantThinking {
				t.Fatalf("run_started payload = %+v", started)
			}
			if events[1].IdempotencyKey != "agent-start:1" || events[1].Attempt != 1 {
				t.Fatalf("run_started keyed by attempt: %+v", events[1])
			}
		})
	}
}
