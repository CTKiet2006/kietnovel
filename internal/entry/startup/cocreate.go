package startup

import (
	"fmt"
	"strings"

	"github.com/CTKiet2006/kietnovel/internal/host"
)

// CoCreateSession holds the non-UI state of co-creation mode.
type CoCreateSession struct {
	history        []host.CoCreateMessage
	draftPrompt    string
	ready          bool
	streamReply    string
	streamThinking string
	suggestions    []string
}

func NewCoCreateSession(initial string) *CoCreateSession {
	return &CoCreateSession{
		history: []host.CoCreateMessage{
			{Role: "user", Content: strings.TrimSpace(initial)},
		},
	}
}

func (s *CoCreateSession) History() []host.CoCreateMessage {
	if s == nil {
		return nil
	}
	return append([]host.CoCreateMessage(nil), s.history...)
}

func (s *CoCreateSession) ApplyReply(reply host.CoCreateReply) {
	if s == nil {
		return
	}
	s.streamReply = ""
	s.streamThinking = ""
	// history stores the assistant turn as the full three-part Raw (including [DRAFT]) so that the model can see
	// its own draft from the previous turn and build on it; storing only Message would keep [DRAFT] entirely
	// out of context, forcing the model to re-derive it from the conversation every turn and easily losing early detail. On the degraded path
	// Raw == Message, so the two are equivalent.
	text := strings.TrimSpace(reply.Raw)
	if text == "" {
		text = strings.TrimSpace(reply.Message)
	}
	if text != "" {
		s.history = append(s.history, host.CoCreateMessage{Role: "assistant", Content: text})
	}
	// draft is overwritten only when Prompt is non-empty: the degraded parse path returns Prompt="", and then the
	// previous draft must be kept, otherwise the user's accumulated "current writing directive" is wiped by a truncated reply.
	if prompt := strings.TrimSpace(reply.Prompt); prompt != "" {
		s.draftPrompt = prompt
	}
	s.ready = reply.Ready
	// suggestions are overwritten outright (including with an empty value): each round's prompts only matter for that round.
	s.suggestions = append(s.suggestions[:0], reply.Suggestions...)
}

func (s *CoCreateSession) AppendUser(text string) {
	if s == nil {
		return
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	// once the user has decided what to say next, suggestions are voided immediately, so stale advice cannot
	// linger in the input box and mislead while the AI has not replied yet.
	s.suggestions = nil
	s.history = append(s.history, host.CoCreateMessage{Role: "user", Content: text})
}

// ApplyDelta receives the streaming accumulation; kind="thinking" feeds the reasoning stream, "reply" the reply preview.
// The two accumulate separately so the UI can colour and render them in chunks, letting the user see the LLM working during the thinking phase too.
func (s *CoCreateSession) ApplyDelta(kind, text string) {
	if s == nil {
		return
	}
	text = strings.TrimSpace(text)
	switch kind {
	case host.CoCreateProgressThinking:
		s.streamThinking = text
	case host.CoCreateProgressReply:
		s.streamReply = text
	}
}

func (s *CoCreateSession) StreamReply() string {
	if s == nil {
		return ""
	}
	return s.streamReply
}

func (s *CoCreateSession) StreamThinking() string {
	if s == nil {
		return ""
	}
	return s.streamThinking
}

func (s *CoCreateSession) DraftPrompt() string {
	if s == nil {
		return ""
	}
	return s.draftPrompt
}

func (s *CoCreateSession) Suggestions() []string {
	if s == nil {
		return nil
	}
	return s.suggestions
}

func (s *CoCreateSession) Ready() bool {
	if s == nil {
		return false
	}
	return s.ready
}

func (s *CoCreateSession) CanStart() bool {
	return strings.TrimSpace(s.DraftPrompt()) != ""
}

func (s *CoCreateSession) InitialInput() string {
	if s == nil || len(s.history) == 0 {
		return ""
	}
	return strings.TrimSpace(s.history[0].Content)
}

func (s *CoCreateSession) BuildPrompt() (string, error) {
	if s == nil || !s.CanStart() {
		return "", fmt.Errorf("cocreate draft prompt is required")
	}
	return s.DraftPrompt(), nil
}
