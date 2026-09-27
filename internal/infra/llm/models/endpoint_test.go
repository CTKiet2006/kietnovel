package models

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/voocel/agentcore"
	"github.com/voocel/litellm"
)

func TestVerifyUsesSelectedOpenAIEndpoint(t *testing.T) {
	for _, endpoint := range []string{"chat", "responses"} {
		t.Run(endpoint, func(t *testing.T) {
			path := ""
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				path = r.URL.Path
				if r.Header.Get("Authorization") != "Bearer test-key" {
					t.Error("key not forwarded")
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":{"message":"test rejection","type":"authentication_error"}}`))
			}))
			defer server.Close()
			err := Verify(context.Background(), Config{Provider: "openai", Model: "custom", API: endpoint, APIKey: "test-key", BaseURL: server.URL + "/v1"})
			expected := "/v1/chat/completions"
			if endpoint == "responses" {
				expected = "/v1/responses"
			}
			if err == nil || path != expected {
				t.Fatalf("path=%q want=%q err=%v", path, expected, err)
			}
		})
	}
}

func TestNewNormalizesInvalidToolUseIDsInPairs(t *testing.T) {
	const raw = "call_3543acedc27c4404bfe7317c#235532d85de245bda8ff64f6a683623a"
	bodies := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		bodies <- string(body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"stub","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()
	chat, err := New(Config{Provider: "openai", Model: "custom", APIKey: "k", BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	messages := []agentcore.Message{
		agentcore.UserMsg("hi"),
		{Role: agentcore.RoleAssistant, Content: []agentcore.ContentBlock{
			agentcore.ToolCallBlock(agentcore.ToolCall{ID: raw, Name: "read", Args: json.RawMessage(`{}`)}),
		}},
		agentcore.ToolResultMsg(raw, json.RawMessage(`"ok"`), false),
	}
	if _, err := chat.Generate(context.Background(), messages, nil); err != nil {
		t.Fatalf("request rejected before reaching provider: %v", err)
	}
	body := <-bodies
	if strings.Contains(body, raw) || strings.Count(body, litellm.NormalizeToolUseID(raw)) != 2 {
		t.Fatalf("tool_use and tool_result ids must be normalized in pairs: %s", body)
	}
}
