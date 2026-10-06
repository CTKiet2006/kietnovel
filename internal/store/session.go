package store

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/voocel/agentcore"
)

// SessionStore records the LLM conversation history into a JSONL file in append-only fashion.
// Large payloads (novel body text, full context) are replaced by a [session_compact: ...] placeholder marker.
type SessionStore struct {
	io      *IO
	mu      sync.Mutex
	seq     map[string]int    // agent run sequence number (used when no chapter number can be extracted)
	taskKey map[string]string // "agentName|task" -> suffix, the same run reuses the same file
}

func NewSessionStore(io *IO) *SessionStore {
	return &SessionStore{io: io, seq: make(map[string]int), taskKey: make(map[string]string)}
}

// ModelLookup resolves the provider/model "in effect at the time" by agent name while the logger writes.
// It is a func type rather than an interface so that the caller can inject the normalization rule as a closure (e.g. architect_short -> architect).
// An empty string means unknown: the caller still writes as usual but without _meta, and replay falls back to ModelSet.
type ModelLookup func(agentName string) (provider, model string)

// SubAgentLogger returns the OnMessage callback of a sub-agent.
func (s *SessionStore) SubAgentLogger(lookup ModelLookup) func(agentName, task string, msg agentcore.AgentMessage) {
	return func(agentName, task string, msg agentcore.AgentMessage) {
		rel, err := s.subAgentPath(agentName, task)
		if err != nil {
			slog.Warn("session log failed", "agent", agentName, "err", err)
			return
		}
		var meta *sessionLogMeta
		if lookup != nil {
			meta = lookupMeta(lookup, agentName)
		}
		if err := s.logEntry(rel, msg, meta); err != nil {
			slog.Warn("session log failed", "agent", agentName, "err", err)
		}
	}
}

func lookupMeta(lookup ModelLookup, agentName string) *sessionLogMeta {
	provider, model := lookup(agentName)
	if provider == "" && model == "" {
		return nil
	}
	return &sessionLogMeta{Provider: provider, Model: model}
}

// LogCoCreate appends one co-creation conversation log to meta/sessions/cocreate.jsonl.
// During the co-creation phase no concrete novel is bound yet, so everything lands under the OutputDir default root (output/novel),
// at the same level as agents/* of the real writing, which makes troubleshooting easy.
func (s *SessionStore) LogCoCreate(entry any) error {
	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshal cocreate session: %w", err)
	}
	data = append(data, '\n')
	return s.io.AppendLine("meta/sessions/cocreate.jsonl", data)
}

// Log appends one message to the given path, compacting large content automatically.
// It carries no _meta; only role-less paths such as cocreate use it.
func (s *SessionStore) Log(rel string, msg agentcore.AgentMessage) error {
	return s.logEntry(rel, msg, nil)
}

// sessionLogEntry embeds agentcore.Message plus an optional _meta.
// agentcore.Message is a plain struct (no MarshalJSON), so once embedded the json marshal
// expands it to the top level automatically; _meta is governed by omitempty - it is injected only when
// assistant + Usage != nil, user/tool messages carry no _meta, and _meta=nil while parsing old jsonl is a no-op.
type sessionLogEntry struct {
	agentcore.Message
	Meta *sessionLogMeta `json:"_meta,omitempty"`
}

type sessionLogMeta struct {
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
}

// logEntry serializes a message and appends _meta when needed. The meta already computed by lookupMeta is passed in;
// the function itself writes the meta only for messages that "consumed LLM usage" (assistant + Usage != nil),
// while every other message keeps the pure agentcore.Message serialization shape.
func (s *SessionStore) logEntry(rel string, msg agentcore.AgentMessage, meta *sessionLogMeta) error {
	m, ok := msg.(agentcore.Message)
	if !ok {
		return nil // non-LLM messages (e.g. custom types) are skipped
	}
	compacted := compactMessage(m)
	entry := sessionLogEntry{Message: compacted}
	if compacted.Role == agentcore.RoleAssistant && compacted.Usage != nil {
		entry.Meta = usageMeta(compacted.Usage)
		if entry.Meta == nil {
			entry.Meta = meta
		}
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshal session message: %w", err)
	}
	data = append(data, '\n')
	return s.io.AppendLine(rel, data)
}

func usageMeta(usage *agentcore.Usage) *sessionLogMeta {
	if usage == nil || (usage.Provider == "" && usage.Model == "") {
		return nil
	}
	return &sessionLogMeta{
		Provider: usage.Provider,
		Model:    usage.Model,
	}
}

// subAgentPath builds the file path from agentName+task.
func (s *SessionStore) subAgentPath(agentName, task string) (string, error) {
	suffix := extractChapter(task)
	if suffix != "" {
		return fmt.Sprintf("meta/sessions/agents/%s-%s.jsonl", agentName, suffix), nil
	}
	key := agentName + "|" + task
	s.mu.Lock()
	defer s.mu.Unlock()
	if cached, ok := s.taskKey[key]; ok {
		return fmt.Sprintf("meta/sessions/agents/%s-%s.jsonl", agentName, cached), nil
	}
	if _, ok := s.seq[agentName]; !ok {
		seq, err := s.maxAgentSequence(agentName)
		if err != nil {
			return "", err
		}
		s.seq[agentName] = seq
	}
	s.seq[agentName]++
	suffix = fmt.Sprintf("%03d", s.seq[agentName])
	s.taskKey[key] = suffix
	return fmt.Sprintf("meta/sessions/agents/%s-%s.jsonl", agentName, suffix), nil
}

func (s *SessionStore) maxAgentSequence(agentName string) (int, error) {
	entries, err := os.ReadDir(s.io.path("meta/sessions/agents"))
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read agent sessions: %w", err)
	}

	prefix := agentName + "-"
	maxSeq := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		seq, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(name, prefix), ".jsonl"))
		if err == nil && seq > maxSeq {
			maxSeq = seq
		}
	}
	return maxSeq, nil
}

var chapterRe = regexp.MustCompile(`第\s*(\d+)\s*章`)

func extractChapter(task string) string {
	m := chapterRe.FindStringSubmatch(task)
	if len(m) < 2 {
		return ""
	}
	n, _ := strconv.Atoi(m[1])
	if n <= 0 {
		return ""
	}
	return fmt.Sprintf("ch%02d", n)
}

// compactMessage clones the message and replaces large content.
func compactMessage(m agentcore.Message) agentcore.Message {
	if len(m.Content) == 0 {
		return m
	}
	blocks := make([]agentcore.ContentBlock, len(m.Content))
	copy(blocks, m.Content)

	toolName := toolNameFromMeta(m.Metadata)

	for i := range blocks {
		switch blocks[i].Type {
		case agentcore.ContentText:
			blocks[i].Text = compactText(m.Role, toolName, blocks[i].Text)
		case agentcore.ContentToolCall:
			if blocks[i].ToolCall != nil {
				blocks[i].ToolCall = compactToolCall(blocks[i].ToolCall)
			}
		}
	}
	m.Content = blocks
	return m
}

func toolNameFromMeta(meta map[string]any) string {
	if meta == nil {
		return ""
	}
	if v, ok := meta["tool_name"].(string); ok {
		return v
	}
	return ""
}

// compactText compacts the text content of a tool result.
func compactText(role agentcore.Role, toolName, text string) string {
	if role != agentcore.RoleTool || len(text) < 4096 {
		return text
	}
	switch toolName {
	case "novel_context":
		summary := extractJSONField(text, "_loading_summary")
		keep := extractNovelContextKeep(text)
		return fmt.Sprintf("[session_compact: novel_context %dB | %s]\n%s", len(text), summary, keep)
	case "read_chapter":
		chars := utf8.RuneCountInString(text)
		return fmt.Sprintf("[session_compact: read_chapter %d字 | 见 chapters/]", chars)
	default:
		if len(text) > 8192 {
			chars := utf8.RuneCountInString(text)
			return fmt.Sprintf("[session_compact: %s %d字]", toolName, chars)
		}
		return text
	}
}

// compactToolCall compacts the large content fields in the args of a tool call.
func compactToolCall(tc *agentcore.ToolCall) *agentcore.ToolCall {
	switch tc.Name {
	case "draft_chapter":
		return compactArgsContent(tc, "第N章正文", "drafts/")
	case "save_foundation":
		return compactFoundationArgs(tc)
	default:
		return tc
	}
}

func compactArgsContent(tc *agentcore.ToolCall, label, ref string) *agentcore.ToolCall {
	var args map[string]json.RawMessage
	if err := json.Unmarshal(tc.Args, &args); err != nil {
		return tc
	}
	contentRaw, ok := args["content"]
	if !ok || len(contentRaw) < 4096 {
		return tc
	}
	var content string
	if err := json.Unmarshal(contentRaw, &content); err != nil {
		// content is not a string (it may be a JSON object), so the byte size is used
		placeholder := fmt.Sprintf("[session_compact: %s %dB | 见 %s]", label, len(contentRaw), ref)
		args["content"], _ = json.Marshal(placeholder)
	} else {
		chars := utf8.RuneCountInString(content)
		ch := extractJSONFieldInt(tc.Args, "chapter")
		if ch > 0 {
			label = fmt.Sprintf("第%d章正文", ch)
			ref = fmt.Sprintf("drafts/%02d.draft.md", ch)
		}
		placeholder := fmt.Sprintf("[session_compact: %s %d字 | 见 %s]", label, chars, ref)
		args["content"], _ = json.Marshal(placeholder)
	}
	clone := *tc
	clone.Args, _ = json.Marshal(args)
	return &clone
}

func compactFoundationArgs(tc *agentcore.ToolCall) *agentcore.ToolCall {
	var args map[string]json.RawMessage
	if err := json.Unmarshal(tc.Args, &args); err != nil {
		return tc
	}
	contentRaw, ok := args["content"]
	if !ok || len(contentRaw) < 4096 {
		return tc
	}
	typeName := "foundation"
	var t string
	if json.Unmarshal(args["type"], &t) == nil && t != "" {
		typeName = t
	}
	placeholder := fmt.Sprintf("[session_compact: %s %dB | 见 store]", typeName, len(contentRaw))
	args["content"], _ = json.Marshal(placeholder)
	clone := *tc
	clone.Args, _ = json.Marshal(args)
	return &clone
}

// extractNovelContextKeep trích phần hành động được từ novel_context JSON:
// dàn ý chương hiện tại + đuôi chương trước + contract. Placeholder cũ chỉ
// giữ _loading_summary nên model đói nội dung, cứ gọi lại novel_context
// hoài không sang draft. Giữ dưới ~2500 ký tự để không vỡ budget.
func extractNovelContextKeep(jsonStr string) string {
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(jsonStr), &m); err != nil {
		return ""
	}
	var sb strings.Builder
	write := func(label, s string, cap int) {
		s = strings.TrimSpace(s)
		if s == "" {
			return
		}
		if len(s) > cap {
			s = s[:cap] + "..."
		}
		sb.WriteString(label + ": " + s + "\n")
	}
	// working_memory: dàn ý chương, contract, đuôi chương trước, checkpoint.
	if raw, ok := m["working_memory"]; ok {
		var wm map[string]json.RawMessage
		if json.Unmarshal(raw, &wm) == nil {
			for _, k := range []string{"current_chapter_outline", "chapter_contract", "previous_tail", "checkpoint", "next_chapter_outline"} {
				if v, ok := wm[k]; ok {
					var s string
					if json.Unmarshal(v, &s) == nil {
						write(k, s, 800)
					} else {
						write(k, string(v), 800)
					}
				}
			}
			// chapter_plan: chỉ giữ khế ước + beats chính.
			if v, ok := wm["chapter_plan"]; ok {
				var cp map[string]json.RawMessage
				if json.Unmarshal(v, &cp) == nil {
					var beats []string
					for _, k := range []string{"goal", "beats", "emotion_target", "hook_goal"} {
						if b, ok := cp[k]; ok {
							var s string
							if json.Unmarshal(b, &s) == nil {
								beats = append(beats, k+"="+s)
							} else if len(b) < 600 {
								beats = append(beats, k+"="+string(b))
							}
						}
					}
					if len(beats) > 0 {
						write("chapter_plan", strings.Join(beats, "; "), 800)
					}
				}
			}
		}
	}
	// Cảnh báo tool (ví dụ tự bỏ volume/arc) phải lộ ra để model không gọi sai lại.
	if raw, ok := m["_warnings"]; ok {
		write("warnings", string(raw), 500)
	}
	out := strings.TrimSpace(sb.String())
	if len(out) > 2500 {
		out = out[:2500] + "..."
	}
	return out
}

// extractJSONField extracts the string value of a given field from a JSON string.
func extractJSONField(jsonStr, field string) string {
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(jsonStr), &m); err != nil {
		return ""
	}
	raw, ok := m[field]
	if !ok {
		return ""
	}
	var val string
	if err := json.Unmarshal(raw, &val); err != nil {
		return string(raw)
	}
	return val
}

func extractJSONFieldInt(data json.RawMessage, field string) int {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		return 0
	}
	raw, ok := m[field]
	if !ok {
		return 0
	}
	var val int
	if err := json.Unmarshal(raw, &val); err != nil {
		return 0
	}
	return val
}

// CompactTag is the prefix of the placeholder marker, which makes searching and restoring easy.
const CompactTag = "[session_compact:"
