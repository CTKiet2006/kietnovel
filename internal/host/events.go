package host

import (
	"time"
)

// Event is a structured event consumed by the TUI.
//
// For call events (MODEL / TOOL / DISPATCH / DECISION) the start and the end of one call share a single ID:
// at the start an event with a zero FinishedAt is emitted first (the TUI renders it in the "in progress" style);
// at the end another event with the same ID is emitted, filling in FinishedAt + Duration (+ Failed),
// and the TUI locates the original row by ID and updates it in place, avoiding the redundancy of
//
// "a row for the start and another row for the finish". Non-call events (SYSTEM / ERROR / CONTEXT) have an
type Event struct {
	ID         string    // shared by the start and the end of one call; empty for non-call events
	Time       time.Time // when it was first emitted (the start moment)
	FinishedAt time.Time // zero value = in progress; non-zero = finished
	Failed     bool      // finished but failed (only meaningful in the finished state)
	Category   string    // DISPATCH / MODEL / TOOL / DECISION / SYSTEM / REVIEW / CHECK / ERROR / CONTEXT
	Agent      string    // the agent that produced the event
	Summary    string
	// SummaryMsg là bản CHƯA dịch của Summary, dùng khi Summary là thông điệp hiển thị
	// có tham số: TUI dịch Key rồi mới điền Args, nên hiện đúng ở cả vi/en/zh.
	// Summary vẫn được giữ (đã điền sẵn) cho log và cho code đọc trực tiếp.
	// Nil nghĩa là Summary là dữ liệu thô, không dịch — ví dụ tên tool, thông điệp provider.
	SummaryMsg *Msg
	Detail     string        // full text, written to the log untruncated for troubleshooting; falls back to Summary when empty. The UI only reads Summary
	Kind       string        // error classification (e.g. stream_idle), emitted with the log for filtering/alerting; not emitted when empty
	Level      string        // info / warn / error / success
	Depth      int           // 0 = Engine layer, 1 = Worker layer
	Duration   time.Duration // how long the execution took, once finished
	RetryAt    time.Time     // retry events: the deadline of the next retry; the UI counts down second by second from it and clears at zero (the request is already in flight)
}

// empty ID and are appended as independent rows. Running reports whether the event is in progress.
// Only call events (MODEL / TOOL / DISPATCH / DECISION, the ones with an ID) can be in progress; every other type always returns false.
func (e Event) Running() bool {
	return e.hasLifecycle() && e.FinishedAt.IsZero()
}

func (e Event) hasLifecycle() bool {
	if e.ID == "" {
		return false
	}
	switch e.Category {
	case "MODEL", "TOOL", "DISPATCH", "DECISION":
		return true
	default:
		return false
	}
}

// UISnapshot is the aggregate state snapshot the TUI needs for rendering.
type UISnapshot struct {
	Provider             string
	BookTitle            string
	ModelName            string
	ModelContextWindow   int // context window of the current default model (resolved live on /model switch)
	ThinkingLevel        string
	Style                string
	RuntimeState         string // idle / running / pausing / paused / completed
	StatusLabel          string
	Phase                string
	Flow                 string
	CurrentChapter       int
	TotalChapters        int
	CompletedCount       int
	TotalWordCount       int
	InProgressChapter    int
	PendingRewrites      []int
	RewriteReason        string
	PendingSteer         string
	AdvanceMode          string
	AdvancePermitChapter int
	HasAdvanceHold       bool
	AdvanceHoldReason    string
	RecoveryLabel        Msg // chưa dịch; TUI gọi i18n.Tf(RecoveryLabel.Key, RecoveryLabel.Args...)
	IsRunning            bool
	Agents               []AgentSnapshot

	// cumulative usage (whole session, across all agents and model switches)
	TotalInputTokens      int
	TotalOutputTokens     int
	TotalCacheReadTokens  int
	TotalCacheWriteTokens int
	TotalCostUSD          float64
	TotalSavedUSD         float64 // USD saved thanks to CacheRead hits (relative to billing every input at the non-cached input price)
	BudgetLimitUSD        float64 // budget cap (config budget.book_usd); 0 = not enabled

	// cache diagnostics
	OverallCacheCapable    bool // at least one role has run a model that supports prompt cache (distinguishes "not enabled" from "0% hit rate")
	OverallRecentCacheRead int  // sum of cacheRead over the most recent N samples in the sliding window
	OverallRecentInput     int  // sum of input over the most recent N samples in the sliding window
	OverallRecentSamples   int  // number of samples in the sliding window (<= recentSampleCap)
	TotalCacheBreaks       int  // number of cache-chain breaks detected live (the prefix did not shorten while the hit rate dropped), see noteCacheBreak in usage.go

	// MissingAssistantUsage > 0 usually means the upstream streaming did not send the final usage
	// chunk per the OpenAI stream_options.include_usage protocol (common with self-hosted proxies),
	// so the UsageTracker receives no cumulative data at all. The UI tells the user to inspect the backend,
	// rather than letting them think the cache module itself is broken.
	MissingAssistantUsage int

	// cache per-role dimension, sorted by CacheRead descending, with roles that never consumed tokens filtered out
	CachePerAgent []AgentCacheStat
	CachePerModel []AgentCacheStat

	// base settings
	Synopsis         string
	Premise          string
	Outline          []OutlineSnapshot
	Characters       []string
	SupportingCount  int      // total number of supporting characters recorded for the chapter
	RecentSupporting []string // most recently active supporting characters (at most 5, sorted by LastSeenChapter descending)
	Layered          bool
	CurrentVolumeArc string
	NextVolumeTitle  string
	CompassDirection string
	CompassScale     string

	// details
	LastCommitSummary  string
	LastReviewSummary  string
	LastCheckpointName string
	RecentSummaries    []string
}

// OutlineSnapshot is the display summary of an outline entry.
type OutlineSnapshot struct {
	Chapter   int
	Title     string
	CoreEvent string
}

// AgentSnapshot is the display projection of an Agent's state.
type AgentSnapshot struct {
	Name      string
	State     string
	TaskID    string
	TaskKind  string
	Summary   string
	Tool      string
	Turn      int
	Context   AgentContextSnapshot
	UpdatedAt time.Time
}

// AgentCacheStat is a single agent's cumulative cache hits (projected into the left column).
// HitRate = CacheRead / Input; Input already carries the "includes CacheRead" semantics at the litellm layer.
//
// CacheCapable distinguishes the two kinds of 0% hit rate:
//   - true  -> the model supports prompt cache, 0% means a badly designed prompt or an unstable prefix, and needs optimizing
//   - false -> the model/provider does not support prompt cache, 0% is expected and needs no investigation
//
// Recent* is the hit data of the sliding window (the last N calls); comparing it with the cumulative total identifies "an early-drag problem" vs "a steady low hit rate".
type AgentCacheStat struct {
	Role            string
	Model           string
	Input           int
	Output          int
	CacheRead       int
	CacheWrite      int
	Cost            float64
	Saved           float64
	CacheCapable    bool
	RecentCacheRead int
	RecentInput     int
	RecentSamples   int
}

// AgentContextSnapshot is the Agent context usage.
type AgentContextSnapshot struct {
	Tokens          int
	ContextWindow   int
	Percent         float64
	Scope           string
	Strategy        string
	ActiveMessages  int
	SummaryMessages int
	CompactedCount  int
	KeptCount       int
}

// CoCreateMessage is a message of the co-create conversation.
type CoCreateMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// CoCreateReply is the LLM reply of the co-create conversation. Raw keeps the model's complete four-part original text,
// which is written back into history so the next round sees its own previous [DRAFT], and therefore really
// accumulates updates on top of the existing draft (with only Message, the model would re-summarise from the conversation every round).
// Suggestions is the AI's proactive "here is what you might want to say next"; when the user is stuck, a number key fills it into the input box in one keystroke.
type CoCreateReply struct {
	Message     string
	Prompt      string
	Ready       bool
	Suggestions []string
	Raw         string
}
