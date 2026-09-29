package diag

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/CTKiet2006/kietnovel/internal/store"
	"github.com/voocel/agentcore"
)

// SkelEvent is the behaviour skeleton of a redacted session message: it keeps the structural signals (role / tool / error /
// repeat fingerprint) and masks every free text (body, prompt, thinking). This is a stricter projection than
// store.compactMessage - the latter compacts by size (>4KB), while here the size is irrelevant and
// no text at all leaves the package.
type SkelEvent struct {
	Agent    string     // the source session: writer-ch07 / architect-arc02 …
	Role     string     // assistant / tool / user
	Tools    []SkelTool // the tool calls inside this message
	ErrClass string     // role=tool and is_error: the first line of the error (a framework error string, no body text)
	TextSha  string     // a short hash of the masked body text; the same sha = the same text is generated repeatedly (a loop signal)
	Redacted int        // the number of text/thinking blocks masked in this entry (used by the redaction self-check)
}

// SkelTool is the redacted projection of one tool call.
type SkelTool struct {
	Name     string            // the tool name (a structural signal, no body text)
	Args     map[string]string // key -> raw scalar / short string quoted / "<redacted len sha>"
	Invalid  bool              // ArgsInvalid: the arguments sent by the model cannot be parsed (a #34 signal)
	ParseErr string            // ArgsParseError: the reason the parse failed
}

// redactMessage projects an agentcore.Message into a behaviour skeleton.
func redactMessage(agent string, m agentcore.Message) SkelEvent {
	ev := SkelEvent{Agent: agent, Role: string(m.Role)}
	isErr, _ := m.Metadata["is_error"].(bool)

	var text strings.Builder
	for _, b := range m.Content {
		switch b.Type {
		case agentcore.ContentText:
			// The first line of a tool error result is kept: it is our own error string (e.g. InputValidationError),
			// it holds no body text and it is the key to locating a loop. All other text goes into the masking pool.
			if m.Role == agentcore.RoleTool && isErr && ev.ErrClass == "" {
				ev.ErrClass = firstLine(b.Text, 160)
				continue
			}
			if strings.TrimSpace(b.Text) != "" {
				text.WriteString(b.Text)
				ev.Redacted++
			}
		case agentcore.ContentThinking:
			if strings.TrimSpace(b.Thinking) != "" {
				text.WriteString(b.Thinking)
				ev.Redacted++
			}
		case agentcore.ContentToolCall:
			if b.ToolCall != nil {
				ev.Tools = append(ev.Tools, redactToolCall(b.ToolCall))
			}
		}
	}
	if t := text.String(); t != "" {
		ev.TextSha = shortHash(t)
	}
	return ev
}

// redactToolCall projects one tool call: tool name + arguments (values redacted) + a parse-anomaly flag.
func redactToolCall(tc *agentcore.ToolCall) SkelTool {
	return SkelTool{
		Name:     tc.Name,
		Args:     redactArgs(tc.Args),
		Invalid:  tc.ArgsInvalid,
		ParseErr: tc.ArgsParseError,
	}
}

// redactArgs projects a tool-arguments object into key -> redacted value. A non-object argument returns nil
// (ArgsInvalid/ParseErr are recorded separately in SkelTool).
func redactArgs(raw json.RawMessage) map[string]string {
	if len(raw) == 0 {
		return nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = projectValue(v)
	}
	return out
}

// projectValue projects a single argument value by its JSON type:
//   - scalar (number / bool / null): the raw value is itself a structural signal and is kept (chapter: 7)
//   - short identifier-like string: kept with quotes, which exposes the type (chapter: "7" <- the stringified-number signal of #34)
//   - string containing Chinese / spaces / long text, object, array: masked into <redacted …> (zero body text out of the package)
//   - already a [session_compact: …] placeholder: safe and informative, kept as is
func projectValue(raw json.RawMessage) string {
	s := strings.TrimSpace(string(raw))
	if s == "" {
		return ""
	}
	switch s[0] {
	case '"':
		var str string
		if err := json.Unmarshal(raw, &str); err != nil {
			return redactPlaceholder(s)
		}
		if strings.HasPrefix(str, store.CompactTag) {
			return str
		}
		// only short values that "look like an identifier/number/enum" are kept (chapter:"7", type:"premise", agent:"writer");
		// any string containing Chinese, spaces or other symbols counts as body text and is always masked.
		if utf8.RuneCountInString(str) <= 32 && isStructuralToken(str) {
			return strconv.Quote(str)
		}
		return redactPlaceholder(str)
	case '{':
		return fmt.Sprintf("<redacted object len=%d>", len(raw))
	case '[':
		return fmt.Sprintf("<redacted array len=%d>", len(raw))
	default:
		return s
	}
}

// isStructuralToken reports whether a string "looks like an identifier" - pure-ASCII letters / digits / `_-.:/`,
// no spaces, no Chinese. It separates structural signals (kept) from body-text fragments (masked).
func isStructuralToken(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '_' || r == '-' || r == '.' || r == ':' || r == '/':
		default:
			return false
		}
	}
	return true
}

func redactPlaceholder(s string) string {
	return fmt.Sprintf("<redacted len=%d sha=%s>", utf8.RuneCountInString(s), shortHash(s))
}

// shortHash takes a short hash of the text; it is only used to judge "whether the same text appears repeatedly", not for cryptography.
func shortHash(s string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(s))
	return fmt.Sprintf("%08x", h.Sum32())
}

// firstLine takes the first line and truncates it by rune, for an error-string summary.
func firstLine(s string, max int) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\n\r"); i >= 0 {
		s = s[:i]
	}
	if utf8.RuneCountInString(s) > max {
		r := []rune(s)
		s = string(r[:max]) + "…"
	}
	return s
}
