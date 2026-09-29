package host

import (
	"strings"
	"unicode/utf8"
)

// toolDisplays configures how each tool is presented on the stream panel. Tools not in this table take no part
// in streaming rendering (the observer drops their DeltaToolCall outright).
//
// Generic mode (nakedKey empty): the tokenizer renders the args JSON the LLM emits as indented
// "key: value" text, with nested objects/arrays indented per level and string/number/bool streamed out.
// Fully decoupled from the schema - one extra field out of the LLM is one extra line on the panel, with no code change at all.
//
// Naked mode (nakedKey non-empty): only the string value of the target top-level field is emitted as is, and
// all other fields are skipped. This is for draft_chapter, so a whole chapter of markdown is not decorated as "content: # …".
// A header always starts with "✻ ": this is the conventional prefix for the TUI's renderStreamContent to take
// the renderAgentBlock highlighting path (gold ✻ + cyan-background blue-underlined label + dim divider), and it
// stays consistent with the fallback header (streamHeaderFallback). Turning it into ordinary text would drop
// it to the body path with the terminal default color, and the title would no longer stand out.
var toolDisplays = map[string]toolDisplay{
	"draft_chapter": {nakedKey: "content"},

	"plan_chapter":        {header: "✻ 规划"},
	"edit_chapter":        {header: "✻ 打磨"},
	"commit_chapter":      {header: "✻ 章节提交"},
	"save_review":         {header: "✻ 审阅"},
	"save_arc_summary":    {header: "✻ 弧摘要"},
	"save_volume_summary": {header: "✻ 卷摘要"},
	"save_foundation":     {header: "✻ 设定"},
	"revise_outline":      {header: "✻ 修订大纲"},
	"read_chapter":        {header: "✻ 读章节"},
	"check_consistency":   {header: "✻ 一致性检查"},
	"novel_context":       {header: "✻ 查询上下文"},
}

type toolDisplay struct {
	header   string
	nakedKey string
}

// jsonFieldExtractor is a streaming JSON tokenizer. A byte-by-byte state machine that turns the LLM's tool
// args stream into readable text. One instance serves exactly one tool call; once the top-level container closes, Done()=true.
type jsonFieldExtractor struct {
	cfg toolDisplay

	state pState
	stack []byte // container stack: 'O' obj / 'A' arr

	keyBuf strings.Builder

	escape bool
	uHex   []byte

	started bool // whether any character has already been emitted (used for the newline between the header and the first key)

	done bool
}

type pState int

const (
	psRoot         pState = iota
	psBeforeKey           // inside obj: waiting for the next key or }
	psInKey               // inside obj: parsing the key
	psAfterKey            // inside obj: waiting for :
	psBeforeValue         // waiting for the first character of the value
	psStringStream        // string value, streaming the cooked characters out
	psStringSkip          // string value, skipped (a non-target field in bare-stream mode)
	psNumberStream        // number, streamed out
	psNumberSkip          // number, skipped
	psPrimStream          // true/false/null, streamed out
	psPrimSkip            // true/false/null, skipped
	psDone                // the top-level container has closed
)

func newToolExtractor(tool string) *jsonFieldExtractor {
	cfg, ok := toolDisplays[tool]
	if !ok {
		return nil
	}
	return &jsonFieldExtractor{cfg: cfg}
}

func (e *jsonFieldExtractor) Done() bool { return e.done }

func (e *jsonFieldExtractor) Feed(chunk string) string {
	if e.done || chunk == "" {
		return ""
	}
	var out strings.Builder
	for i := 0; i < len(chunk); i++ {
		e.step(chunk[i], &out)
		if e.done {
			break
		}
	}
	return out.String()
}

// ── Container stack / indentation ──

func (e *jsonFieldExtractor) push(kind byte) {
	e.stack = append(e.stack, kind)
}

func (e *jsonFieldExtractor) pop() {
	if len(e.stack) == 0 {
		return
	}
	e.stack = e.stack[:len(e.stack)-1]
}

func (e *jsonFieldExtractor) parent() byte {
	if len(e.stack) == 0 {
		return 0
	}
	return e.stack[len(e.stack)-1]
}

// writeIndent writes the current indent. Depth = nesting level = len(stack)-1 (inside the root container there is no indent).
func (e *jsonFieldExtractor) writeIndent(out *strings.Builder) {
	depth := len(e.stack) - 1
	for range depth {
		out.WriteString("  ")
	}
}

// ── State machine ──

func (e *jsonFieldExtractor) step(c byte, out *strings.Builder) {
	switch e.state {
	case psRoot:
		switch c {
		case '{':
			e.push('O')
			e.state = psBeforeKey
		case '[':
			// this cannot actually happen (tool args are always an obj); tolerate it: when the root is an arr
			e.push('A')
			e.state = psBeforeValue
		}
	case psBeforeKey:
		switch c {
		case '"':
			e.keyBuf.Reset()
			e.escape = false
			e.state = psInKey
		case '}':
			e.closeContainer(out)
		case ' ', '\t', '\n', '\r', ',':
		}
	case psInKey:
		if e.escape {
			e.keyBuf.WriteByte(c)
			e.escape = false
			return
		}
		if c == '\\' {
			e.escape = true
			return
		}
		if c == '"' {
			e.emitKeyLine(out, e.keyBuf.String())
			e.state = psAfterKey
			return
		}
		e.keyBuf.WriteByte(c)
	case psAfterKey:
		if c == ':' {
			e.state = psBeforeValue
		}
	case psBeforeValue:
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == ',' {
			return
		}
		switch c {
		case '"':
			e.beginString(out)
		case '{':
			e.beginNested('O', out)
		case '[':
			e.beginNested('A', out)
		case ']', '}':
			e.closeContainer(out)
		case 't', 'f', 'n':
			e.beginPrim(c, out)
		default:
			if c == '-' || (c >= '0' && c <= '9') {
				e.beginNumber(c, out)
			}
		}
	case psStringStream:
		e.handleStringByte(c, out, false)
	case psStringSkip:
		e.handleStringByte(c, out, true)
	case psNumberStream:
		if isNumberByte(c) {
			out.WriteByte(c)
			return
		}
		e.afterValueChar(c, out)
	case psNumberSkip:
		if isNumberByte(c) {
			return
		}
		e.afterValueChar(c, out)
	case psPrimStream:
		if c >= 'a' && c <= 'z' {
			out.WriteByte(c)
			return
		}
		e.afterValueChar(c, out)
	case psPrimSkip:
		if c >= 'a' && c <= 'z' {
			return
		}
		e.afterValueChar(c, out)
	case psDone:
	}
}

// ── Line rendering ──

// emitKeyLine is called inside an obj once a key has been parsed, writing the "<lf><indent>key:" prefix.
// In naked mode no key prefix is written (the key is recorded in keyBuf for beginString to inspect).
func (e *jsonFieldExtractor) emitKeyLine(out *strings.Builder, key string) {
	if e.cfg.nakedKey != "" {
		return
	}
	if !e.started {
		if e.cfg.header != "" {
			out.WriteString(e.cfg.header)
			out.WriteByte('\n')
		}
		e.started = true
	} else {
		out.WriteByte('\n')
	}
	e.writeIndent(out)
	out.WriteString(key)
	out.WriteByte(':')
}

// emitArrayItem is called at the start of every element inside an arr, writing "<lf><indent>-". A primitive
// element is followed by a space and then its emitted value; a struct element is handled by the following nesting wrapping naturally.
func (e *jsonFieldExtractor) emitArrayItem(out *strings.Builder) {
	if e.cfg.nakedKey != "" {
		return
	}
	if !e.started {
		if e.cfg.header != "" {
			out.WriteString(e.cfg.header)
			out.WriteByte('\n')
		}
		e.started = true
	} else {
		out.WriteByte('\n')
	}
	e.writeIndent(out)
	out.WriteByte('-')
}

// ── value start ──

func (e *jsonFieldExtractor) beginString(out *strings.Builder) {
	if e.cfg.nakedKey != "" {
		// naked: only the string value of the target key in the top-level obj is output
		if e.cfg.nakedKey == e.keyBuf.String() && len(e.stack) == 1 && e.stack[0] == 'O' {
			e.state = psStringStream
		} else {
			e.state = psStringSkip
		}
		e.escape = false
		e.uHex = nil
		return
	}
	// generic: an obj field is followed by "key: " ("key:" is already emitted, the space is added here); an arr element by "- "
	if e.parent() == 'A' {
		e.emitArrayItem(out)
		out.WriteByte(' ')
	} else {
		out.WriteByte(' ')
	}
	e.state = psStringStream
	e.escape = false
	e.uHex = nil
}

func (e *jsonFieldExtractor) beginNumber(first byte, out *strings.Builder) {
	if e.cfg.nakedKey != "" {
		e.state = psNumberSkip
		return
	}
	if e.parent() == 'A' {
		e.emitArrayItem(out)
		out.WriteByte(' ')
	} else {
		out.WriteByte(' ')
	}
	out.WriteByte(first)
	e.state = psNumberStream
}

func (e *jsonFieldExtractor) beginPrim(first byte, out *strings.Builder) {
	if e.cfg.nakedKey != "" {
		e.state = psPrimSkip
		return
	}
	if e.parent() == 'A' {
		e.emitArrayItem(out)
		out.WriteByte(' ')
	} else {
		out.WriteByte(' ')
	}
	out.WriteByte(first)
	e.state = psPrimStream
}

func (e *jsonFieldExtractor) beginNested(kind byte, out *strings.Builder) {
	if e.cfg.nakedKey != "" {
		// naked mode does not expand nesting; the stack depth is tracked to the matching } / ]
		e.push(kind)
		if kind == 'O' {
			e.state = psBeforeKey
		} else {
			e.state = psBeforeValue
		}
		return
	}
	// generic mode: when an arr element is a nested structure, first emit a standalone "<indent>-" line
	// (no space after the obj key's ":", so a nested child key wraps naturally onto the next line)
	if e.parent() == 'A' {
		e.emitArrayItem(out)
	}
	e.push(kind)
	if kind == 'O' {
		e.state = psBeforeKey
	} else {
		e.state = psBeforeValue
	}
}

// closeContainer handles } or ].
func (e *jsonFieldExtractor) closeContainer(out *strings.Builder) {
	e.pop()
	if len(e.stack) == 0 {
		// empty args fallback (e.g. novel_context passes no parameters): emitKeyLine never had a chance to
		// output a header, so add one here to avoid ending up with "neither a title nor content".
		if !e.started && e.cfg.nakedKey == "" && e.cfg.header != "" {
			out.WriteString(e.cfg.header)
			out.WriteByte('\n')
			e.started = true
		}
		// the trailing newline gives the panel a clear boundary before the next segment of output
		if e.started {
			out.WriteByte('\n')
		}
		e.state = psDone
		e.done = true
		return
	}
	if e.parent() == 'O' {
		e.state = psBeforeKey
	} else {
		e.state = psBeforeValue
	}
}

// ── string streaming ──

func (e *jsonFieldExtractor) handleStringByte(c byte, out *strings.Builder, skipping bool) {
	if e.uHex != nil {
		e.uHex = append(e.uHex, c)
		if len(e.uHex) == 4 {
			if r, ok := parseHex4(e.uHex); ok && !skipping {
				var buf [4]byte
				n := utf8.EncodeRune(buf[:], r)
				out.Write(buf[:n])
			}
			e.uHex = nil
		}
		return
	}
	if e.escape {
		e.escape = false
		if !skipping {
			writeEscapedByte(out, c)
		}
		if c == 'u' {
			e.uHex = make([]byte, 0, 4)
		}
		return
	}
	if c == '\\' {
		e.escape = true
		return
	}
	if c == '"' {
		e.afterValueDone()
		return
	}
	if !skipping {
		out.WriteByte(c)
	}
}

func writeEscapedByte(out *strings.Builder, c byte) {
	switch c {
	case 'n':
		out.WriteByte('\n')
	case 't':
		out.WriteByte('\t')
	case 'r':
		out.WriteByte('\r')
	case '"':
		out.WriteByte('"')
	case '\\':
		out.WriteByte('\\')
	case '/':
		out.WriteByte('/')
	case 'b', 'f':
		// backspace / form feed: ignored
	case 'u':
		// the caller builds the uHex buffer; nothing is emitted here
	default:
		out.WriteByte('\\')
		out.WriteByte(c)
	}
}

// ── Wrap-up ──

// afterValueDone transitions to the next state once a string closes (its trailing `"` was read).
func (e *jsonFieldExtractor) afterValueDone() {
	e.escape = false
	e.uHex = nil
	if len(e.stack) == 0 {
		e.state = psDone
		e.done = true
		return
	}
	if e.parent() == 'O' {
		e.state = psBeforeKey
	} else {
		e.state = psBeforeValue
	}
}

// afterValueChar decides the next state by character once the "terminating character" of a number / primitive was read.
// That character may be , / } / ] or whitespace, and this function forwards and dispatches on it.
func (e *jsonFieldExtractor) afterValueChar(c byte, out *strings.Builder) {
	switch c {
	case '}', ']':
		e.closeContainer(out)
	case ',', ' ', '\t', '\n', '\r':
		if len(e.stack) == 0 {
			e.state = psDone
			e.done = true
			return
		}
		if e.parent() == 'O' {
			e.state = psBeforeKey
		} else {
			e.state = psBeforeValue
		}
	}
}

// ── Tools ──

func isNumberByte(c byte) bool {
	switch c {
	case '0', '1', '2', '3', '4', '5', '6', '7', '8', '9',
		'-', '+', '.', 'e', 'E':
		return true
	}
	return false
}

func parseHex4(b []byte) (rune, bool) {
	var r rune
	for _, d := range b {
		var v rune
		switch {
		case d >= '0' && d <= '9':
			v = rune(d - '0')
		case d >= 'a' && d <= 'f':
			v = rune(d-'a') + 10
		case d >= 'A' && d <= 'F':
			v = rune(d-'A') + 10
		default:
			return 0, false
		}
		r = r*16 + v
	}
	return r, true
}
