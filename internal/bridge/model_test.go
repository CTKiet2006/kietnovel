package bridge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/voocel/agentcore"
)

func fakeBridge(t *testing.T, reply string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("bridge fake: body lỗi: %v", err)
		}
		// Bắt buộc: body native phải có client_metadata + turn_id trong item.
		cmd, _ := body["client_metadata"].(map[string]any)
		tm, _ := cmd["x-codex-turn-metadata"].(map[string]any)
		turnID, _ := tm["turn_id"].(string)
		if turnID == "" {
			t.Errorf("bridge fake: thiếu client_metadata turn_id")
		}
		items, _ := body["input"].([]any)
		if len(items) == 0 {
			t.Errorf("bridge fake: input rỗng")
		} else {
			first, _ := items[0].(map[string]any)
			pm, _ := first["internal_chat_message_metadata_passthrough"].(map[string]any)
			if pm["turn_id"] != turnID {
				t.Errorf("bridge fake: item turn_id %v != header turn %v", pm["turn_id"], turnID)
			}
		}
		resp := map[string]any{
			"status": "completed",
			"model":  body["model"],
			"output": []any{map[string]any{
				"type": "message", "role": "assistant",
				"content": []any{map[string]any{"type": "output_text", "text": reply}},
			}},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
}

func TestGenerateReturnsText(t *testing.T) {
	srv := fakeBridge(t, "Biển đêm lặng im.")
	defer srv.Close()
	m := New(srv.URL, "chatgpt-web/gpt-5.6-luna", time.Minute)
	resp, err := m.Generate(context.Background(), []agentcore.Message{
		{Role: agentcore.RoleSystem, Content: []agentcore.ContentBlock{agentcore.TextBlock("Bạn là writer.")}},
		{Role: agentcore.RoleUser, Content: []agentcore.ContentBlock{agentcore.TextBlock("Viết 1 câu về biển.")}},
	}, nil)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if got := resp.Message.TextContent(); !strings.Contains(got, "Biển đêm") {
		t.Fatalf("text lạ: %q", got)
	}
}

func TestSupportsToolsFalse(t *testing.T) {
	m := New("http://127.0.0.1:17841/v1", "chatgpt-web/gpt-5.6-luna", 0)
	// Giả lập tool calling qua JSON nên phải báo true để loop chạy tool.
	if !m.SupportsTools() {
		t.Fatalf("bridge JSON tool-call phải báo SupportsTools=true")
	}
	if m.ProviderName() != "chatgpt-web" {
		t.Fatalf("ProviderName=%q", m.ProviderName())
	}
}

func TestGenerateStreamReplays(t *testing.T) {
	srv := fakeBridge(t, "hello world")
	defer srv.Close()
	m := New(srv.URL, "chatgpt-web/gpt-5.6-luna", time.Minute)
	ch, err := m.GenerateStream(context.Background(), []agentcore.Message{
		{Role: agentcore.RoleUser, Content: []agentcore.ContentBlock{agentcore.TextBlock("hi")}},
	}, nil)
	if err != nil {
		t.Fatalf("GenerateStream: %v", err)
	}
	var sb strings.Builder
	var done bool
	for ev := range ch {
		switch ev.Type {
		case agentcore.StreamEventTextDelta:
			sb.WriteString(ev.Delta)
		case agentcore.StreamEventDone:
			done = true
		case agentcore.StreamEventError:
			t.Fatalf("stream lỗi: %v", ev.Err)
		}
	}
	if !done || sb.String() != "hello world" {
		t.Fatalf("stream thiếu: done=%v text=%q", done, sb.String())
	}
}

func TestBridgeErrorSurfaced(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":"failed","error":{"message":"nope","type":"server_error","code":"x"}}`))
	}))
	defer srv.Close()
	m := New(srv.URL, "chatgpt-web/gpt-5.6-luna", time.Minute)
	_, err := m.Generate(context.Background(), []agentcore.Message{
		{Role: agentcore.RoleUser, Content: []agentcore.ContentBlock{agentcore.TextBlock("hi")}},
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("lỗi bridge phải lộ ra, được: %v", err)
	}
}

func toolSpecsForTest() []agentcore.ToolSpec {
	return []agentcore.ToolSpec{
		{Name: "draft_chapter", Description: "Write chapter", Parameters: map[string]any{"type": "object"}},
		{Name: "commit_chapter", Description: "Commit chapter", Parameters: map[string]any{"type": "object"}},
	}
}

func TestParseToolCallValid(t *testing.T) {
	text := "Tôi sẽ ghi chương 2.\n{\"tool\":\"draft_chapter\",\"arguments\":{\"chapter\":2,\"mode\":\"write\",\"content\":\"abc\"}}"
	blocks, ok := parseToolCall(text, toolSpecsForTest())
	if !ok || len(blocks) != 2 {
		t.Fatalf("phải parse ra text+toolcall, được ok=%v blocks=%d", ok, len(blocks))
	}
	tc := blocks[1].ToolCall
	if tc == nil || tc.Name != "draft_chapter" {
		t.Fatalf("tool sai: %+v", tc)
	}
	var args map[string]any
	if err := json.Unmarshal(tc.Args, &args); err != nil || args["chapter"] != float64(2) {
		t.Fatalf("args sai: %s", string(tc.Args))
	}
}

func TestParseToolCallRejectUnknown(t *testing.T) {
	for _, text := range []string{
		`{"tool":"xoa_db","arguments":{}}`,
		`{"tool":"__text__","text":"hello"}`,
		`chỉ là text thường không có JSON`,
		`{"tool":"draft_chapter","arguments":[1,2]}`,
		`{"tool": broken`,
		"```json\n{\"tool\":\"commit_chapter\",\"arguments\":{\"chapter\":3}}\n```",
	} {
		blocks, ok := parseToolCall(text, toolSpecsForTest())
		// Case fence bọc commit hợp lệ thì phải parse được.
		if strings.Contains(text, "```json") {
			if !ok || len(blocks) != 1 || blocks[0].ToolCall.Name != "commit_chapter" {
				t.Fatalf("fence JSON hợp lệ phải parse được: %q", text)
			}
			continue
		}
		if ok {
			t.Fatalf("phải từ chối: %q", text)
		}
	}
}

func TestGenerateWithToolsParsesJSON(t *testing.T) {
	srv := fakeBridge(t, `{"tool":"commit_chapter","arguments":{"chapter":2}}`)
	defer srv.Close()
	m := New(srv.URL, "chatgpt-web/gpt-5.6-luna", time.Minute)
	resp, err := m.Generate(context.Background(), []agentcore.Message{
		{Role: agentcore.RoleUser, Content: []agentcore.ContentBlock{agentcore.TextBlock("nộp chương")}},
	}, toolSpecsForTest())
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if resp.Message.StopReason != agentcore.StopReasonToolUse {
		t.Fatalf("phải StopReasonToolUse, được %q", resp.Message.StopReason)
	}
	calls := resp.Message.ToolCalls()
	if len(calls) != 1 || calls[0].Name != "commit_chapter" {
		t.Fatalf("tool calls sai: %+v", calls)
	}
}

// ChatGPT Web escape markdown trong tên tool (draft\_chapter) —
// `\_` không phải escape JSON hợp lệ, parse phải gỡ ra.
func TestParseToolCallMarkdownEscape(t *testing.T) {
	text := `{"tool":"draft\_chapter","arguments":{"chapter":2,"mode":"write","content":"abc"}}`
	blocks, ok := parseToolCall(text, toolSpecsForTest())
	if !ok || len(blocks) != 1 || blocks[0].ToolCall.Name != "draft_chapter" {
		t.Fatalf("phải gỡ escape markdown, được ok=%v %+v", ok, blocks)
	}
}

// Bridge trả block commentary "Local tools unavailable" + final_answer —
// doRequest phải bỏ block cảnh báo, lấy final.
func TestDoRequestPrefersFinalAnswer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":"completed","output":[{"type":"message","role":"assistant","phase":"commentary","content":[{"type":"output_text","text":"> **Local tools unavailable**\nfoo"}]},{"type":"message","role":"assistant","phase":"final_answer","content":[{"type":"output_text","text":"hello"}]}],"end_turn":true}`))
	}))
	defer srv.Close()
	m := New(srv.URL, "chatgpt-web/gpt-5.6-luna", time.Minute)
	got, err := m.doRequest(context.Background(), "hi")
	if err != nil {
		t.Fatalf("doRequest: %v", err)
	}
	if got != "hello" {
		t.Fatalf("phải lấy final_answer, được %q", got)
	}
}

func mkMsgs(n int, size int) []agentcore.Message {
	msgs := []agentcore.Message{
		{Role: agentcore.RoleSystem, Content: []agentcore.ContentBlock{agentcore.TextBlock("system prompt")}},
	}
	for i := 0; i < n; i++ {
		msgs = append(msgs, agentcore.Message{
			Role:    agentcore.RoleUser,
			Content: []agentcore.ContentBlock{agentcore.TextBlock(strings.Repeat("x", size))},
		})
	}
	return msgs
}

// trimMessages phải giữ system + cắt cũ nhất, và buildPrompt không bao giờ
// vượt perTurnBudget dù lịch sử dài cỡ nào.
func TestTrimMessagesWithinBudget(t *testing.T) {
	msgs := mkMsgs(30, 3000) // ~90k chars ≈ 30k tokens ước cao
	trimmed := trimMessages(msgs, 22000)
	if trimmed[0].Role != agentcore.RoleSystem {
		t.Fatalf("phải giữ system prompt đầu")
	}
	if len(trimmed) >= len(msgs) {
		t.Fatalf("phải cắt bớt, được %d/%d", len(trimmed), len(msgs))
	}
	prompt := buildPrompt(msgs, toolSpecsForTest(), nil)
	if estimateTokens(prompt) > perTurnBudget {
		t.Fatalf("prompt vượt budget: %d > %d", estimateTokens(prompt), perTurnBudget)
	}
}

// compactSchema phải giữ câu ràng buộc loại trừ để model không gộp sai
// (novel_context cấm gộp chapter với volume/arc).
func TestCompactSchemaKeepsExclusiveNote(t *testing.T) {
	params := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"chapter": map[string]any{"type": "integer", "description": "Số chương."},
			"volume":  map[string]any{"type": "integer", "description": "Số quyển; phải truyền kèm arc và không dùng cùng lúc với chapter"},
			"mode":    map[string]any{"type": "string", "description": "Chế độ", "enum": []string{"write", "append"}},
		},
		"required": []string{"chapter"},
	}
	got := compactSchema(params)
	if !strings.Contains(got, "không dùng cùng lúc với chapter") {
		t.Fatalf("mất ràng buộc loại trừ: %q", got)
	}
	if !strings.Contains(got, "write|append") {
		t.Fatalf("mất enum: %q", got)
	}
}
