package host

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/CTKiet2006/kietnovel/internal/domain"
	storepkg "github.com/CTKiet2006/kietnovel/internal/store"
	"github.com/voocel/agentcore"
)

// errorKind classifies a runtime error into a stable, short label for log
// filtering and alert routing. Returns "" when no special tag applies.
//
// err is the live error chain (may be nil after JSON serialization); msg is
// the rendered string fallback used when the chain has been flattened
// (e.g. inside sub-agent JSON results).
func errorKind(err error, msg string) string {
	if kind := agentcore.ErrorKind(err); kind != "" && kind != "unknown" {
		return kind
	}
	if msg == "" {
		return ""
	}
	if kind := agentcore.ErrorKind(errors.New(msg)); kind != "unknown" {
		return kind
	}
	lower := strings.ToLower(msg)
	switch {
	case strings.Contains(lower, "tool argument validation failed"):
		return "tool_validation"
	case strings.Contains(lower, "too many concurrent requests"):
		return "overloaded"
	// providerError appends litellm's structured type to the end of the text.
	// HTTP/2 INTERNAL_ERROR has no classifiable keyword of its own, so keeping this explicit network marker is enough.
	case strings.Contains(lower, "[network,"):
		return "network"
	}
	return ""
}

// A monotonically increasing event ID counter; combined with a timestamp it yields stable IDs.
var eventIDCounter uint64

func nextEventID() string {
	return fmt.Sprintf("e%d", atomic.AddUint64(&eventIDCounter, 1))
}

// activeCall records the ID, start time and summary of one in-flight call.
// The summary is back-filled into the finish Event so that a replay (from the runtime queue) can restore the row content.
type activeCall struct {
	id      string
	start   time.Time
	summary string
	// summaryMsg giữ dạng chưa dịch của summary khi đó là nhãn hiển thị có tham số;
	// rỗng khi summary là dữ liệu thô (tên tool, tên agent) — thì không cần dịch.
	summaryMsg Msg
	depth      int
}

// observer projects Engine dispatches and Worker progress onto the Host's output channel.
// It is a pure observer and takes part in no control decision.
// isAborting báo vòng lặp hiện tại có bị dừng chủ ý không. Observer dùng để phân
// biệt "bị dừng giữa chừng" với lỗi thật: hủy context khi người dùng tạm dừng hay
// vào đồng sáng tác là việc bình thường, không nên hiện thành lỗi đỏ.
// Nil nghĩa là không biết, coi như lỗi thật (thiên về báo lỗi hơn là bỏ sót).
type observer struct {
	isAborting func() bool

	emitEv  func(Event)
	emitD   func(string)
	emitC   func()
	store   *storepkg.Store // used for runtime queue persistence (consumed by ReplayQueue)
	agents  map[string]*agentState
	agentMu sync.Mutex

	streamThinking      bool
	lastThinkingByAgent map[string]string          // agent -> the most recent accumulated thinking text (used to extract the incremental delta)
	dispatchStarts      map[string]*activeCall     // dispatched agent -> the in-flight DISPATCH call
	modelStarts         map[string]*activeCall     // agent -> the in-flight model response
	toolStarts          map[string]*activeCall     // agent -> the in-flight TOOL call
	streamExtractors    map[string]*agentExtractor // agent -> the content extractor for the current tool call JSON argument
	retryEvents         map[string]string          // retry scope -> event ID, updated in place on the same row (2/7)
	streamHasContent    bool                       // whether the current streamRound has already emitted content (decides whether a paragraph break is needed)
	streamLastByte      byte                       // last byte of the most recent streaming output (used to pad the newline exactly)
}

// agentExtractor records the tool name currently being extracted for a given agent, plus the extractor instance.
// The tool name is used to detect "a new tool call has started", so the cache is not polluted by leftovers from the previous round.
type agentExtractor struct {
	tool       string
	ext        *jsonFieldExtractor
	emittedAny bool // whether this extractor has already emitted content; used to insert a paragraph break before the first output
}

type agentState struct {
	name    string
	state   string
	tool    string
	summary string
	turn    int
	context AgentContextSnapshot
	updated time.Time
}

func newObserver(s *storepkg.Store, emitEv func(Event), emitD func(string), emitC func()) *observer {
	return &observer{
		emitEv:              emitEv,
		emitD:               emitD,
		emitC:               emitC,
		store:               s,
		agents:              make(map[string]*agentState),
		lastThinkingByAgent: make(map[string]string),
		dispatchStarts:      make(map[string]*activeCall),
		modelStarts:         make(map[string]*activeCall),
		toolStarts:          make(map[string]*activeCall),
		streamExtractors:    make(map[string]*agentExtractor),
		retryEvents:         make(map[string]string),
	}
}

// stoppedOnPurpose báo lỗi này chỉ là hệ quả của việc dừng chủ ý, không phải hỏng.
// Chỉ đúng khi thông điệp là do hủy context; lỗi khác vẫn là lỗi thật dù có dừng.
func (o *observer) stoppedOnPurpose(msg string) bool {
	if o.isAborting == nil {
		return false
	}
	return o.isAborting() && strings.Contains(msg, "context canceled")
}

// ── Engine direct-drive entry points ──
//
// The Engine runs Workers directly, and events come from two sources:
//  1. dispatchStart/dispatchFinish - called by the Engine right at the dispatch boundary (DISPATCH rows)
//  2. workerProgress - the Worker's progress relay (ctx ToolProgress),
//     handled uniformly by handleToolUpdate: TOOL / streamed body / thinking / retry / context
//     (TOOL rows / streamed body / thinking / retry / context).

// dispatchStart records the start of a Worker dispatch and emits the DISPATCH row.
func (o *observer) dispatchStart(agent, task, reason string) {
	summary := dispatchSummary(agent, task)
	o.updateAgent(agent, func(a *agentState) {
		a.state = "working"
		a.tool = ""
		a.summary = fmt.Sprintf("engine → %s", summary)
	})
	id := nextEventID()
	o.dispatchStarts[agent] = &activeCall{id: id, start: time.Now(), summary: summary}
	o.emitAndLog(Event{
		ID:       id,
		Time:     time.Now(),
		Category: "DISPATCH",
		Agent:    agent,
		Summary:  summary,
		Detail:   dispatchDetail(task, reason),
		Level:    "info",
	})
}

// dispatchFinish settles the DISPATCH row into its finished state and resets the Worker state;
// it cleans up the unfinished MODEL / TOOL rows under that Worker.
func (o *observer) dispatchFinish(agent string, runErr error) {
	o.updateAgent(agent, func(a *agentState) {
		a.state = "idle"
		a.tool = ""
	})
	delete(o.lastThinkingByAgent, agent)
	if call, ok := o.modelStarts[agent]; ok {
		delete(o.modelStarts, agent)
		o.emitCallFinish(call, "MODEL", agent, runErr)
	}
	if call, ok := o.toolStarts[agent]; ok {
		delete(o.toolStarts, agent)
		delete(o.streamExtractors, agent)
		o.emitCallFinish(call, "TOOL", agent, runErr)
	}
	if call, ok := o.dispatchStarts[agent]; ok {
		delete(o.dispatchStarts, agent)
		o.emitCallFinish(call, "DISPATCH", agent, runErr)
	}
	o.streamClear()
}

// workerProgress adapts the Worker progress relay into the existing ToolExecUpdate handling.
func (o *observer) workerProgress(p agentcore.ProgressPayload) {
	payload := p
	o.handleToolUpdate(agentcore.Event{Type: agentcore.EventToolExecUpdate, Progress: &payload})
}

func (o *observer) finalize() {
	o.agentMu.Lock()
	defer o.agentMu.Unlock()
	for _, a := range o.agents {
		a.state = "idle"
		a.tool = ""
	}
}

func (o *observer) retryEventID(scope string, attempt int) string {
	if strings.TrimSpace(scope) == "" {
		scope = "engine"
	}
	if o.retryEvents == nil {
		o.retryEvents = make(map[string]string)
	}
	if attempt <= 1 || o.retryEvents[scope] == "" {
		o.retryEvents[scope] = nextEventID()
	}
	return o.retryEvents[scope]
}

// emitAndLog serves the "start" state of call events: it sends to the TUI but does not write to the runtime queue,
// avoiding a "start row plus finish row" duplication on replay. slog is logged uniformly by host.emitEvent.
func (o *observer) emitAndLog(ev Event) {
	o.emitEv(ev)
}

// persistEvent writes the event to the runtime queue (slog is logged uniformly by host.emitEvent).
func (o *observer) persistEvent(ev Event) {
	if o.store == nil || o.store.Runtime == nil {
		return
	}
	priority := domain.RuntimePriorityBackground
	switch {
	case ev.Level == "error":
		priority = domain.RuntimePriorityControl
	case ev.Category == "SYSTEM" || ev.Category == "ERROR":
		priority = domain.RuntimePriorityControl
	}
	if _, err := o.store.Runtime.AppendQueue(domain.RuntimeQueueItem{
		Time:     ev.Time,
		Priority: priority,
		Category: ev.Category,
		Summary:  ev.Summary,
		Payload:  ev,
	}); err != nil {
		slog.Warn("运行事件持久化失败", "module", "observer", "category", ev.Category, "err", err)
	}
}

func (o *observer) updateAgent(name string, fn func(*agentState)) {
	if name == "" {
		return
	}
	o.agentMu.Lock()
	defer o.agentMu.Unlock()
	a, ok := o.agents[name]
	if !ok {
		a = &agentState{name: name, state: "idle"}
		o.agents[name] = a
	}
	fn(a)
	a.updated = time.Now()
}

func (o *observer) agentSnapshots() []AgentSnapshot {
	o.agentMu.Lock()
	defer o.agentMu.Unlock()
	snaps := make([]AgentSnapshot, 0, len(o.agents))
	for _, a := range o.agents {
		snaps = append(snaps, AgentSnapshot{
			Name:      a.name,
			State:     a.state,
			Summary:   a.summary,
			Tool:      a.tool,
			Turn:      a.turn,
			Context:   a.context,
			UpdatedAt: a.updated,
		})
	}
	return snaps
}
