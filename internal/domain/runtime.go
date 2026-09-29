package domain

import (
	"fmt"
	"strings"
)

// Phase represents the stage of the novel writing.
type Phase string

const (
	PhaseInit     Phase = "init"
	PhasePremise  Phase = "premise"
	PhaseOutline  Phase = "outline"
	PhaseWriting  Phase = "writing"
	PhaseComplete Phase = "complete"
)

// FlowState is the type of the currently active flow, used for checkpoint recovery.
type FlowState string

const (
	FlowWriting   FlowState = "writing"
	FlowReviewing FlowState = "reviewing"
	FlowRewriting FlowState = "rewriting"
	FlowPolishing FlowState = "polishing"
	FlowSteering  FlowState = "steering"
)

// PlanningTier represents the length tier of the planned work.
type PlanningTier string

const (
	PlanningTierShort PlanningTier = "short"
	PlanningTierMid   PlanningTier = "mid"
	PlanningTierLong  PlanningTier = "long"
)

// Progress tracks the progress and is persisted to meta/progress.json.
type Progress struct {
	Phase          Phase `json:"phase"`
	CurrentChapter int   `json:"current_chapter"`
	// In non-layered mode TotalChapters is the chapter count of the detailed outline; in layered mode it is only an
	// internal capacity value containing a rough estimate, used by the context policy and not a fixed total chapter count of the whole book.
	TotalChapters     int         `json:"total_chapters"`
	CompletedChapters []int       `json:"completed_chapters"`
	TotalWordCount    int         `json:"total_word_count"`
	ChapterWordCounts map[int]int `json:"chapter_word_counts,omitempty"` // word count per chapter, so the total can be corrected when a chapter is rewritten
	InProgressChapter int         `json:"in_progress_chapter,omitempty"` // the chapter currently being written (scene-level recovery)
	CompletedScenes   []int       `json:"completed_scenes,omitempty"`    // the scene numbers already completed in the current chapter
	Flow              FlowState   `json:"flow,omitempty"`                // the current flow
	PendingRewrites   []int       `json:"pending_rewrites,omitempty"`    // the queue of chapters pending a rewrite
	RewriteReason     string      `json:"rewrite_reason,omitempty"`      // the reason for the rewrite
	StrandHistory     []string    `json:"strand_history,omitempty"`      // records dominant_strand in chapter order
	HookHistory       []string    `json:"hook_history,omitempty"`        // records hook_type in chapter order
	// long-form layered tracking (only used in long-form mode, the zero value in short/medium mode)
	CurrentVolume int  `json:"current_volume,omitempty"`
	CurrentArc    int  `json:"current_arc,omitempty"`
	Layered       bool `json:"layered,omitempty"`
	// ReopenedFromComplete marks that this book was reopened from the finished state through reopen and entered rework. A rework only changes existing chapters,
	// it does not add or remove structure, so once drained it should be allowed to finish again "as soon as the structure is complete" (this avoids a writing -> out-of-range continue-writing deadlock when the final volume's last setup is disturbed by the rework);
	// forward writing does not set this flag, and the finish decision keeps the conservative semantics of closing the threads.
	ReopenedFromComplete bool `json:"reopened_from_complete,omitempty"`
	// ReopenCount records how many times this book has been reopened from the finished state (a /reopen audit fact). It also guarantees
	// that the progress.json content of the re-finish after a reopen differs from the previous finish: a checkpoint is idempotent for the same digest and
	// dedupes it, so a byte-identical re-finish would not produce a new checkpoint and StopGuard would misjudge the successful complete_book
	// as idling and escalate the termination.
	ReopenCount int `json:"reopen_count,omitempty"`
}

// IsResumable reports whether it can resume from a breakpoint.
func (p *Progress) IsResumable() bool {
	return p.Phase == PhaseWriting && p.CurrentChapter > 0
}

// NextChapter returns the number of the next chapter to write.
func (p *Progress) NextChapter() int {
	return p.LatestCompleted() + 1
}

// LatestCompleted returns the largest completed chapter number; it returns 0 when no chapter is completed.
func (p *Progress) LatestCompleted() int {
	max := 0
	for _, ch := range p.CompletedChapters {
		if ch > max {
			max = ch
		}
	}
	return max
}

// ContextProfile is the context loading policy, adapting itself to the total chapter count.
type ContextProfile struct {
	SummaryWindow  int  // load the summaries of the most recent N chapters
	TimelineWindow int  // load the timeline of the most recent N chapters
	Layered        bool // true = enable layered summary loading (volume + arc + chapter summaries)
}

// MemoryPolicy represents the memory usage policy shared at runtime.
// It is used both for the context output and for the handoff / reminder decisions of the host layer.
type MemoryPolicy struct {
	Mode                string `json:"mode,omitempty"`
	SummaryWindow       int    `json:"summary_window,omitempty"`
	TimelineWindow      int    `json:"timeline_window,omitempty"`
	LayeredSummaries    bool   `json:"layered_summaries,omitempty"`
	SummaryStrategy     string `json:"summary_strategy,omitempty"`
	WorkingRefresh      string `json:"working_refresh,omitempty"`
	EpisodicRefresh     string `json:"episodic_refresh,omitempty"`
	PlanningRefresh     string `json:"planning_refresh,omitempty"`
	FoundationRefresh   string `json:"foundation_refresh,omitempty"`
	PlanningFocus       string `json:"planning_focus,omitempty"`
	FoundationFocus     string `json:"foundation_focus,omitempty"`
	PreviousTailChars   int    `json:"previous_tail_chars,omitempty"`
	ChapterPlanEnabled  bool   `json:"chapter_plan_enabled,omitempty"`
	RelatedLookup       bool   `json:"related_chapter_lookup,omitempty"`
	CurrentOutlineBound bool   `json:"current_outline_bound,omitempty"`
	HandoffPreferred    bool   `json:"handoff_preferred,omitempty"`
	ReadOnlyThreshold   int    `json:"read_only_threshold,omitempty"`
}

// NewContextProfile computes the context policy from the total chapter count.
func NewContextProfile(totalChapters int) ContextProfile {
	switch {
	case totalChapters <= 15:
		return ContextProfile{SummaryWindow: 10, TimelineWindow: 10}
	case totalChapters <= 50:
		return ContextProfile{SummaryWindow: 5, TimelineWindow: 8}
	default:
		return ContextProfile{SummaryWindow: 3, TimelineWindow: 5, Layered: true}
	}
}

// NewChapterMemoryPolicy generates the chapter runtime memory policy from the progress and the context policy.
func NewChapterMemoryPolicy(progress *Progress, profile ContextProfile, currentOutlineBound bool) MemoryPolicy {
	policy := MemoryPolicy{
		Mode:                "chapter",
		SummaryWindow:       profile.SummaryWindow,
		TimelineWindow:      profile.TimelineWindow,
		LayeredSummaries:    profile.Layered,
		WorkingRefresh:      "每次按章节加载时刷新",
		EpisodicRefresh:     "随章节提交、评审和长篇状态变更刷新",
		PreviousTailChars:   800,
		ChapterPlanEnabled:  true,
		CurrentOutlineBound: currentOutlineBound,
		ReadOnlyThreshold:   5,
	}
	if profile.Layered {
		policy.SummaryStrategy = "卷摘要+弧摘要+最近章节摘要"
	} else {
		policy.SummaryStrategy = "最近章节摘要"
	}
	if progress != nil {
		if progress.TotalChapters > 30 {
			policy.RelatedLookup = true
		}
		if progress.Flow == FlowReviewing || progress.Flow == FlowRewriting || progress.Flow == FlowPolishing {
			policy.HandoffPreferred = true
		}
		if progress.Layered && len(progress.CompletedChapters) >= 6 {
			policy.HandoffPreferred = true
		}
		if len(progress.CompletedChapters) >= 12 {
			policy.HandoffPreferred = true
		}
		if progress.Layered && len(progress.CompletedChapters) >= 6 {
			policy.ReadOnlyThreshold = 4
		}
		if len(progress.CompletedChapters) >= 12 {
			policy.ReadOnlyThreshold = 4
		}
	}
	return policy
}

// NewArchitectMemoryPolicy returns the memory policy used during the planning phase.
func NewArchitectMemoryPolicy() MemoryPolicy {
	return MemoryPolicy{
		Mode:               "architect",
		PlanningRefresh:    "卷弧结构、指南针或摘要更新时刷新",
		FoundationRefresh:  "角色、伏笔、设定变更时刷新",
		PlanningFocus:      "分层大纲、指南针、卷摘要",
		FoundationFocus:    "角色设定、角色快照、伏笔台账",
		HandoffPreferred:   true,
		ChapterPlanEnabled: false,
		ReadOnlyThreshold:  4,
	}
}

// RunMeta is the run metadata, persisted to meta/run.json.
type RunMeta struct {
	StartedAt            string             `json:"started_at"`
	Provider             string             `json:"provider,omitempty"`
	Style                string             `json:"style"`
	Model                string             `json:"model"`
	PlanningTier         PlanningTier       `json:"planning_tier,omitempty"`
	StartPrompt          string             `json:"start_prompt,omitempty"`           // the user's original writing request (an input fact, written to disk before the start ruling; the fallback ruling after a failed ruling is based on it)
	PlanStart            *PlanStartRecord   `json:"plan_start,omitempty"`             // the start-ruling fact, the only basis for recovering from a crash during the planning phase
	PendingSteer         string             `json:"pending_steer,omitempty"`          // the unfinished Steer instruction, re-injected when recovering from an interruption
	AdvanceMode          ChapterAdvanceMode `json:"advance_mode"`                     // the chapter advance mode: auto / review
	AdvancePermitChapter int                `json:"advance_permit_chapter,omitempty"` // the forward chapter of the one-shot permit in review mode
	AdvanceHold          *AdvanceHold       `json:"advance_hold,omitempty"`           // the one-shot pause intent signed by the current intervention
}

// ChapterAdvanceMode decides whether each new chapter needs a per-chapter permit.
type ChapterAdvanceMode string

const (
	ChapterAdvanceAuto   ChapterAdvanceMode = "auto"
	ChapterAdvanceReview ChapterAdvanceMode = "review"
)

// Valid reports whether the chapter advance mode is supported by the current version.
func (m ChapterAdvanceMode) Valid() bool {
	return m == ChapterAdvanceAuto || m == ChapterAdvanceReview
}

// UnsupportedAdvanceModeError means the control mode of the book is not supported by the current binary.
// The caller must stop constructing a writable Host and tell the user to use a matching version; guessing a downgrade is forbidden.
type UnsupportedAdvanceModeError struct {
	Mode ChapterAdvanceMode
}

func (e *UnsupportedAdvanceModeError) Error() string {
	return fmt.Sprintf("不支持的章节推进模式 %q，请使用创建该项目的新版 ainovel", e.Mode)
}

// AdvanceHoldAfter is the deterministic trigger condition of a one-shot pause.
type AdvanceHoldAfter string

const (
	AdvanceHoldAtBoundary           AdvanceHoldAfter = "boundary"
	AdvanceHoldAfterRewritesDrained AdvanceHoldAfter = "rewrites_drained"
	AdvanceHoldAtChapter            AdvanceHoldAfter = "chapter"
)

// Valid reports whether the pause condition is supported by the current version.
func (a AdvanceHoldAfter) Valid() bool {
	return a == AdvanceHoldAtBoundary || a == AdvanceHoldAfterRewritesDrained || a == AdvanceHoldAtChapter
}

// AdvanceHold is the one-shot pause intent signed by the current intervention, consumed at the Host boundary.
type AdvanceHold struct {
	After         AdvanceHoldAfter `json:"after"`
	TargetChapter int              `json:"target_chapter,omitempty"`
	Reason        string           `json:"reason"`
}

// Validate checks the structural constraints of the one-shot pause intent itself.
func (h AdvanceHold) Validate() error {
	if !h.After.Valid() {
		return fmt.Errorf("不支持的一次性暂停条件 %q", h.After)
	}
	if h.After == AdvanceHoldAtChapter {
		if h.TargetChapter <= 0 {
			return fmt.Errorf("目标章节必须大于 0")
		}
	} else if h.TargetChapter != 0 {
		return fmt.Errorf("暂停条件 %q 不能设置目标章节", h.After)
	}
	if strings.TrimSpace(h.Reason) == "" {
		return fmt.Errorf("一次性暂停原因不能为空")
	}
	return nil
}

// PlanStartRecord is the persisted fact of the start ruling (the ruling is written as a fact first, then execution starts; recovery does not rule again).
// Once the first save_foundation has persisted the scale, recovery during the planning phase derives it from PlanningTier instead, so this record
// only covers the window "from the completed ruling to the first write". DecisionID links to the decisions.jsonl audit.
type PlanStartRecord struct {
	RawPrompt   string `json:"raw_prompt"`
	Planner     string `json:"planner"`
	PlannerTask string `json:"planner_task"`
	DecisionID  string `json:"decision_id,omitempty"`
}
