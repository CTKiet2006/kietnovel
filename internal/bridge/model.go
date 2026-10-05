// Package bridge là provider chatgpt-web riêng của kietnovel: nói chuyện
// thẳng với codex-chatgpt-web bridge qua Responses API native shape
// (client_metadata + input item có turn_id), không qua litellm vì body wire
// của litellm không có chỗ nhét client_metadata nên bridge luôn từ chối.
//
// Bridge ở chế độ browser-only KHÔNG forward tools cho model — model chỉ
// trả text. Provider này vì vậy chỉ dùng cho vai trò viết text thuần
// (Writer viết nháp, Arbiter JSON text). Các vai trò cần tool calling
// (Architect/Editor dùng tool) phải dùng provider API cloud.
package bridge

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/voocel/agentcore"
)

// Model là agentcore.ChatModel nói thẳng với bridge.
type Model struct {
	baseURL    string
	model      string
	httpClient *http.Client
	threadID   string
}

// New tạo Model mới. baseURL dạng http://127.0.0.1:17841/v1 (không trailing slash).
func New(baseURL, model string, timeout time.Duration) *Model {
	if timeout <= 0 {
		timeout = 60 * time.Minute
	}
	return &Model{
		baseURL:    strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		model:      strings.TrimSpace(model),
		httpClient: &http.Client{Timeout: timeout},
		threadID:   "kietnovel-" + shortID(),
	}
}

// ProviderName để agent loop biết đây là chatgpt-web.
func (m *Model) ProviderName() string { return "chatgpt-web" }

// ModelName để hiển thị/telemetry.
func (m *Model) ModelName() string { return m.model }

// SupportsTools: giả lập tool calling qua JSON object trong text (bridge
// browser-only không forward tools native). Loop vẫn chạy tool local.
func (m *Model) SupportsTools() bool { return true }

func shortID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// --- wire shape native của bridge ---

type inputTextPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type inputItem struct {
	Type     string            `json:"type"`
	ID       string            `json:"id"`
	Role     string            `json:"role"`
	Content  []inputTextPart   `json:"content"`
	TurnMeta map[string]string `json:"internal_chat_message_metadata_passthrough"`
}

type turnMeta struct {
	ThreadID    string         `json:"thread_id"`
	TurnID      string         `json:"turn_id"`
	RequestKind string         `json:"request_kind"`
	Sandbox     string         `json:"sandbox"`
	Workspaces  map[string]any `json:"workspaces"`
}

type responsesReq struct {
	Model          string         `json:"model"`
	ClientMetadata map[string]any `json:"client_metadata,omitempty"`
	Input          []inputItem    `json:"input"`
	Stream         bool           `json:"stream"`
	Store          bool           `json:"store"`
}

type outputTextPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type outputItem struct {
	Type    string           `json:"type"`
	Role    string           `json:"role"`
	Phase   string           `json:"phase"`
	Content []outputTextPart `json:"content"`
}

type responsesResp struct {
	Status string       `json:"status"`
	Output []outputItem `json:"output"`
	Error  *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    string `json:"code"`
	} `json:"error"`
}

// buildBody dựng request native: mỗi lượt là turn_id mới, thread giữ nguyên
// để bridge replay đúng session browser.
func (m *Model) buildBody(prompt string) (*bytes.Buffer, string, error) {
	turnID := "turn-" + shortID()
	itemText := prompt
	body := responsesReq{
		Model: m.model,
		ClientMetadata: map[string]any{
			"x-codex-turn-metadata": turnMeta{
				ThreadID:    m.threadID,
				TurnID:      turnID,
				RequestKind: "turn",
				Sandbox:     "none",
				Workspaces:  map[string]any{},
			},
		},
		Input: []inputItem{{
			Type:     "message",
			ID:       "msg-" + shortID(),
			Role:     "user",
			Content:  []inputTextPart{{Type: "input_text", Text: itemText}},
			TurnMeta: map[string]string{"turn_id": turnID},
		}},
		Stream: false,
		Store:  false,
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, "", err
	}
	return bytes.NewBuffer(raw), turnID, nil
}

func flattenMessages(messages []agentcore.Message) string {
	var sb strings.Builder
	for _, msg := range messages {
		text := msg.TextContent()
		think := msg.ThinkingContent()
		switch msg.Role {
		case agentcore.RoleSystem:
			sb.WriteString(text)
			sb.WriteString("\n\n")
		case agentcore.RoleAssistant:
			if think != "" {
				sb.WriteString("[Suy nghĩ trước đó]\n" + think + "\n\n")
			}
			if text != "" {
				sb.WriteString(text + "\n\n")
			}
			for _, tc := range msg.ToolCalls() {
				sb.WriteString(fmt.Sprintf("[Đã gọi tool %s args=%s]\n\n", tc.Name, string(tc.Args)))
			}
		case agentcore.RoleTool:
			sb.WriteString("[Kết quả tool]\n" + text + "\n\n")
		default:
			if text != "" {
				sb.WriteString(text + "\n\n")
			}
		}
	}
	return strings.TrimSpace(sb.String())
}

func (m *Model) doRequest(ctx context.Context, prompt string) (string, error) {
	buf, _, err := m.buildBody(prompt)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.baseURL+"/responses", buf)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer local")
	resp, err := m.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("chatgpt-web bridge: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("chatgpt-web bridge HTTP %d: %.300s", resp.StatusCode, string(raw))
	}
	var out responsesResp
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("chatgpt-web bridge JSON lỗi: %w", err)
	}
	if out.Error != nil {
		return "", fmt.Errorf("chatgpt-web bridge: %s (%s/%s)", out.Error.Message, out.Error.Type, out.Error.Code)
	}
	if out.Status != "completed" {
		return "", fmt.Errorf("chatgpt-web bridge status=%s", out.Status)
	}
	var finals, others []string
	for _, item := range out.Output {
		if item.Type != "message" || (item.Role != "assistant" && item.Role != "") {
			continue
		}
		var sb strings.Builder
		for _, part := range item.Content {
			if part.Type == "output_text" || part.Type == "text" {
				sb.WriteString(part.Text)
			}
		}
		t := strings.TrimSpace(sb.String())
		if t == "" {
			continue
		}
		// Bỏ block cảnh báo "Local tools unavailable" của bridge.
		if strings.Contains(t, "Local tools unavailable") {
			continue
		}
		if item.Phase == "final_answer" {
			finals = append(finals, t)
		} else {
			others = append(others, t)
		}
	}
	picked := finals
	if len(picked) == 0 {
		picked = others
	}
	text := strings.TrimSpace(strings.Join(picked, "\n\n"))
	if text == "" {
		return "", fmt.Errorf("chatgpt-web bridge trả output rỗng")
	}
	return text, nil
}

// toolCatalog dựng danh sách tool cho vào prompt khi bridge không forward
// tools native. Model chỉ được trả đúng 1 JSON object.
func toolCatalog(tools []agentcore.ToolSpec) string {
	var sb strings.Builder
	for _, t := range tools {
		params, _ := json.Marshal(t.Parameters)
		if string(params) == "" || string(params) == "null" {
			params = []byte(`{"type":"object"}`)
		}
		fmt.Fprintf(&sb, "- %s: %s\n  args schema: %s\n", t.Name, t.Description, string(params))
	}
	return sb.String()
}

const toolcallSystemHint = `Bạn đang viết truyện qua cầu text-only, KHÔNG có function-calling native.
Khi cần dùng công cụ, CHỈ trả về đúng 1 JSON object thuần (không markdown fence, không giải thích thêm ngoài text ngắn trước JSON):
{"tool":"<tên tool>","arguments":{...}}
Tên tool và arguments phải khớp catalog dưới đây. Khi không cần tool, trả text thường, hoặc {"tool":"__text__","text":"..."}.
Catalog công cụ:
`

// buildPrompt gộp messages + catalog tool (nếu có) thành prompt gửi bridge.
func buildPrompt(messages []agentcore.Message, tools []agentcore.ToolSpec, opts []agentcore.CallOption) string {
	base := flattenMessages(messages)
	var sb strings.Builder
	sb.WriteString(base)
	if len(tools) > 0 {
		sb.WriteString("\n\n" + toolcallSystemHint + toolCatalog(tools))
	} else {
		cfg := agentcore.ResolveCallConfig(opts)
		if cfg.ResponseFormat != nil {
			sb.WriteString("\n\nCHỈ trả về JSON object thuần, không markdown fence, không giải thích.")
		}
	}
	return strings.TrimSpace(sb.String())
}

// splitToolJSON tách phần text dẫn và JSON object cuối trong reply.
func splitToolJSON(text string) (lead, raw string) {
	t := strings.TrimSpace(text)
	// Bóc markdown fence nếu model vẫn bọc.
	if strings.HasPrefix(t, "```") {
		if i := strings.Index(t, "\n"); i >= 0 {
			t = t[i+1:]
		}
		if j := strings.LastIndex(t, "```"); j >= 0 {
			t = t[:j]
		}
		t = strings.TrimSpace(t)
	}
	// Tìm JSON object cuối: từ '{' đầu tiên cân bằng ngoặc tới hết.
	start := strings.Index(t, "{")
	if start < 0 {
		return t, ""
	}
	depth, inStr, esc := 0, false, false
	end := -1
	for i := start; i < len(t); i++ {
		c := t[i]
		if inStr {
			if esc {
				esc = false
			} else if c == '\\' {
				esc = true
			} else if c == '"' {
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				end = i + 1
			}
		}
	}
	if end < 0 {
		return t, ""
	}
	return strings.TrimSpace(t[:start]), strings.TrimSpace(t[start:end])
}

// unescapeMarkdown gỡ escape markdown (draft\_chapter → draft_chapter)
// mà ChatGPT Web hay chèn vào tên tool trong JSON.
func unescapeMarkdown(s string) string {
	r := strings.NewReplacer(
		`\_`, "_",
		`\*`, "*",
		"\\`", "`",
		`\{`, "{",
		`\}`, "}",
		`\[`, "[",
		`\]`, "]",
	)
	return r.Replace(s)
}

// parseToolCall parse reply thành blocks. Trả blocks + true nếu là tool call hợp lệ.
func parseToolCall(text string, tools []agentcore.ToolSpec) ([]agentcore.ContentBlock, bool) {
	byName := make(map[string]bool, len(tools))
	for _, t := range tools {
		byName[t.Name] = true
	}
	lead, raw := splitToolJSON(text)
	if raw == "" {
		return nil, false
	}
	// ChatGPT Web hay escape markdown trong tên tool (draft\_chapter) —
	// `\_` không phải escape JSON hợp lệ nên phải gỡ trước khi parse.
	raw = unescapeMarkdown(raw)
	var env struct {
		Tool      string          `json:"tool"`
		Arguments json.RawMessage `json:"arguments"`
		Text      string          `json:"text"`
	}
	if err := json.Unmarshal([]byte(raw), &env); err != nil {
		return nil, false
	}
	if env.Tool == "" || env.Tool == "__text__" {
		return nil, false
	}
	if !byName[env.Tool] {
		return nil, false
	}
	args := env.Arguments
	if len(args) == 0 {
		args = json.RawMessage(`{}`)
	}
	var probe any
	if err := json.Unmarshal(args, &probe); err != nil {
		return nil, false
	}
	if _, ok := probe.(map[string]any); !ok {
		return nil, false
	}
	var blocks []agentcore.ContentBlock
	if lead != "" {
		blocks = append(blocks, agentcore.TextBlock(lead))
	}
	blocks = append(blocks, agentcore.ToolCallBlock(agentcore.ToolCall{
		ID:   "call-" + shortID(),
		Name: env.Tool,
		Args: args,
	}))
	return blocks, true
}

// Generate gọi bridge đồng bộ. Có tools → ép JSON tool-call và parse thành
// ToolCallBlock để agent loop chạy tool local như provider cloud.
func (m *Model) Generate(ctx context.Context, messages []agentcore.Message, tools []agentcore.ToolSpec, opts ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	prompt := buildPrompt(messages, tools, opts)
	if prompt == "" {
		return nil, fmt.Errorf("chatgpt-web bridge: prompt rỗng")
	}
	text, err := m.doRequest(ctx, prompt)
	if err != nil {
		return nil, err
	}
	msg := agentcore.Message{Role: agentcore.RoleAssistant}
	if len(tools) > 0 {
		if blocks, ok := parseToolCall(text, tools); ok {
			msg.Content = blocks
			msg.StopReason = agentcore.StopReasonToolUse
			return &agentcore.LLMResponse{Message: msg}, nil
		}
		msg.Content = []agentcore.ContentBlock{agentcore.TextBlock(text)}
		msg.StopReason = agentcore.StopReasonStop
		return &agentcore.LLMResponse{Message: msg}, nil
	}
	msg.Content = []agentcore.ContentBlock{agentcore.TextBlock(text)}
	msg.StopReason = agentcore.StopReasonStop
	return &agentcore.LLMResponse{Message: msg}, nil
}

// GenerateStream: bridge không SSE nên chạy Generate rồi phát lại events
// mirror adapter chuẩn để TUI hiển thị + loop ráp tool call: TextStart,
// (TextDelta)*, TextEnd, rồi (ToolCallStart, ToolCallEnd)*, cuối là Done
// mang Message hoàn chỉnh (kể cả ToolCallBlock) để loop chạy tool local.
func (m *Model) GenerateStream(ctx context.Context, messages []agentcore.Message, tools []agentcore.ToolSpec, opts ...agentcore.CallOption) (<-chan agentcore.StreamEvent, error) {
	ch := make(chan agentcore.StreamEvent, 32)
	go func() {
		defer close(ch)
		resp, err := m.Generate(ctx, messages, tools, opts...)
		if err != nil {
			ch <- agentcore.StreamEvent{Type: agentcore.StreamEventError, Err: err}
			return
		}
		partial := agentcore.Message{Role: agentcore.RoleAssistant}
		textIdx := -1
		for _, b := range resp.Message.Content {
			switch b.Type {
			case agentcore.ContentText:
				partial.Content = append(partial.Content, agentcore.TextBlock(""))
				textIdx = len(partial.Content) - 1
				ch <- agentcore.StreamEvent{Type: agentcore.StreamEventTextStart, ContentIndex: textIdx, Message: partial}
				const chunk = 4000
				for i := 0; i < len(b.Text); i += chunk {
					end := i + chunk
					if end > len(b.Text) {
						end = len(b.Text)
					}
					select {
					case <-ctx.Done():
						ch <- agentcore.StreamEvent{Type: agentcore.StreamEventError, Err: ctx.Err()}
						return
					case ch <- agentcore.StreamEvent{Type: agentcore.StreamEventTextDelta, ContentIndex: textIdx, Delta: b.Text[i:end], Message: partial}:
					}
					partial.Content[textIdx].Text += b.Text[i:end]
				}
				ch <- agentcore.StreamEvent{Type: agentcore.StreamEventTextEnd, ContentIndex: textIdx, Message: partial}
			case agentcore.ContentToolCall:
				if b.ToolCall == nil {
					continue
				}
				partial.Content = append(partial.Content, agentcore.ToolCallBlock(agentcore.ToolCall{ID: b.ToolCall.ID, Name: b.ToolCall.Name}))
				idx := len(partial.Content) - 1
				ch <- agentcore.StreamEvent{Type: agentcore.StreamEventToolCallStart, ToolID: b.ToolCall.ID, Message: partial}
				raw := string(b.ToolCall.Args)
				if raw != "" {
					ch <- agentcore.StreamEvent{Type: agentcore.StreamEventToolCallDelta, ToolID: b.ToolCall.ID, Delta: raw, Message: partial}
				}
				done := *b.ToolCall
				partial.Content[idx] = agentcore.ToolCallBlock(done)
				ch <- agentcore.StreamEvent{Type: agentcore.StreamEventToolCallEnd, ToolID: done.ID, ContentIndex: idx, Message: partial, CompletedToolCall: &done}
			}
		}
		partial.StopReason = resp.Message.StopReason
		ch <- agentcore.StreamEvent{Type: agentcore.StreamEventDone, Message: partial, StopReason: partial.StopReason}
	}()
	return ch, nil
}
