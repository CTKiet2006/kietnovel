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
	ID         string    // 同一次调用的开始/结束共用；非调用事件为空
	Time       time.Time // 首次发出时间（开始时刻）
	FinishedAt time.Time // 零值 = 进行中；非零 = 已完成
	Failed     bool      // 已完成但失败（仅完成态有意义）
	Category   string    // DISPATCH / MODEL / TOOL / DECISION / SYSTEM / REVIEW / CHECK / ERROR / CONTEXT
	Agent      string    // 产生事件的 agent
	Summary    string
	Detail     string        // 完整文案，写入日志不截断供排查；为空回退 Summary。UI 只读 Summary
	Kind       string        // 错误分类（如 stream_idle），随日志输出供过滤/告警；为空不输出
	Level      string        // info / warn / error / success
	Depth      int           // 0 = Engine 层, 1 = Worker 层
	Duration   time.Duration // 完成时的执行耗时
	RetryAt    time.Time     // 重试类事件：下次重试的截止时刻；UI 据此逐秒倒计时，到点即清（请求已在途）
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
	ModelContextWindow   int // 当前默认模型的上下文窗口（随 /model 切换实时解析）
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
	RecoveryLabel        string
	IsRunning            bool
	Agents               []AgentSnapshot

	// cumulative usage (whole session, across all agents and model switches)
	TotalInputTokens      int
	TotalOutputTokens     int
	TotalCacheReadTokens  int
	TotalCacheWriteTokens int
	TotalCostUSD          float64
	TotalSavedUSD         float64 // 因 CacheRead 命中省下的美元（相对全按非缓存输入价计费）
	BudgetLimitUSD        float64 // 预算上限（config budget.book_usd）；0 = 未启用

	// cache diagnostics
	OverallCacheCapable    bool // 至少一个 role 跑过支持 prompt cache 的模型（区分"未启用"和"0% 命中"）
	OverallRecentCacheRead int  // 滑动窗最近 N 次的 cacheRead 总和
	OverallRecentInput     int  // 滑动窗最近 N 次的 input 总和
	OverallRecentSamples   int  // 滑动窗内的样本数（≤ recentSampleCap）
	TotalCacheBreaks       int  // live 检测到的缓存链断裂次数（前缀未缩短而命中骤降），详见 usage.go noteCacheBreak

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
	SupportingCount  int      // 章节记录中的次要角色总数
	RecentSupporting []string // 最近活跃的次要角色（最多 5 个，按 LastSeenChapter 倒序）
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
