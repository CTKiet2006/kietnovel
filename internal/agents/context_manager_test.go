package agents

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/voocel/agentcore"
	corecontext "github.com/voocel/agentcore/context"
)

type stubSummaryModel struct{ calls int }

func (m *stubSummaryModel) Generate(context.Context, []agentcore.Message, []agentcore.ToolSpec, ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	m.calls++
	return &agentcore.LLMResponse{Message: agentcore.Message{
		Role:    agentcore.RoleAssistant,
		Content: []agentcore.ContentBlock{agentcore.TextBlock("<summary>checkpoint</summary>")},
	}}, nil
}

func (m *stubSummaryModel) GenerateStream(context.Context, []agentcore.Message, []agentcore.ToolSpec, ...agentcore.CallOption) (<-chan agentcore.StreamEvent, error) {
	return nil, nil
}

func (m *stubSummaryModel) SupportsTools() bool { return true }

func toolCallResult(id, name, args, result string) []agentcore.AgentMessage {
	return []agentcore.AgentMessage{
		agentcore.Message{
			Role:    agentcore.RoleAssistant,
			Content: []agentcore.ContentBlock{agentcore.ToolCallBlock(agentcore.ToolCall{ID: id, Name: name, Args: json.RawMessage(args)})},
		},
		agentcore.ToolResultMsg(id, json.RawMessage(strconv.Quote(result)), false),
	}
}

// Worker 一次运行只有一条任务消息，其余全是工具组。Editor 的章节原文不走微压缩，
// 只能靠全量摘要，它必须在这种形态里真的切开，且最新原文原样留在尾部。
func TestRoleContextManagerSummarizesToolLoop(t *testing.T) {
	msgs := []agentcore.AgentMessage{agentcore.UserMsg("生成第 1 卷卷摘要")}
	msgs = append(msgs, toolCallResult("ctx", "novel_context", `{}`, strings.Repeat("x", 8000))...)
	for i := 1; i <= 8; i++ {
		chapter := strconv.Itoa(i)
		msgs = append(msgs, toolCallResult("ch"+chapter, "read_chapter", `{"chapter":`+chapter+`}`, strings.Repeat("章", 4000))...)
	}

	model := &stubSummaryModel{}
	projection, err := newRoleContextManager(editorContextProfile, model, 32000, "novel_context").Project(context.Background(), msgs)
	if err != nil {
		t.Fatal(err)
	}
	if model.calls == 0 || !projection.ShouldCommit {
		t.Fatalf("expected an LLM summary to run and commit, calls=%d commit=%v", model.calls, projection.ShouldCommit)
	}
	if summary, ok := projection.Messages[0].(corecontext.ContextSummary); !ok || !strings.Contains(summary.Summary, "checkpoint") {
		t.Fatalf("projection must start with the summary, got %T", projection.Messages[0])
	}
	last := projection.Messages[len(projection.Messages)-1].(agentcore.Message)
	if !strings.Contains(last.TextContent(), "章") {
		t.Fatal("the newest read_chapter evidence must stay verbatim in the kept suffix")
	}
}
