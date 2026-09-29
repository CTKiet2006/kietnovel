// Package flow implements vertical routing: the Host decides from the facts which subagent to call next and what to do.
//
// Design principles:
//   - Route is a pure function: it takes a State and returns an *Instruction. No IO, no Store calls, so it is unit-testable.
//   - State is built by LoadState (not pure) from the Store, reading every fact routing needs in one pass.
//   - Returning nil is legal: it means there is currently no Worker instruction derivable from the deterministic facts;
//     the Engine then handles it by terminal state, start-up fallback arbitration or waiting for user intervention.
//
// Router covers "table lookup" decisions (the next step of each chapter, post-arc-end handling, queue-driven work),
// and not "semantic understanding" decisions (picking a planner, handling a user Steer, producing a summary).
package flow

import (
	"fmt"
	"strings"

	"github.com/CTKiet2006/kietnovel/internal/domain"
	storepkg "github.com/CTKiet2006/kietnovel/internal/store"
)

// plannerForTier derives the planner identity from the persisted planning tier: short maps to the short-form planner,
// mid/long map to the long-form planner (consistent with the selection used by the start-up Arbiter).
func plannerForTier(tier domain.PlanningTier) string {
	if tier == domain.PlanningTierShort {
		return "architect_short"
	}
	return "architect_long"
}

// Instruction tells the Engine which Worker to run next and with what task.
type Instruction struct {
	Agent   string // architect_long / architect_short / writer / editor
	Task    string // the task description handed to the subagent
	Reason  string // the routing rationale (used for events, logs and failure arbitration)
	Chapter int    // the chapter number a writer task touches (continue / rewrite / polish); 0 means none (editor/architect tasks)
}

type AggregateKind string

const (
	AggregateArcReview     AggregateKind = "arc_review"
	AggregateArcSummary    AggregateKind = "arc_summary"
	AggregateVolumeSummary AggregateKind = "volume_summary"
	AggregateGlobalReview  AggregateKind = "global_review"
)

type AggregateRefresh struct {
	Kind         AggregateKind
	Volume       int
	Arc          int
	StartChapter int
	EndChapter   int
}

// State is the input of Route: every fact must be declared here explicitly, and Route is forbidden from reading the Store.
type State struct {
	Progress *domain.Progress

	// the largest chapter number among completed chapters; 0 means writing has not started yet.
	LastCompleted int

	// arc boundary info of the previous chapter; the other fields are meaningless when IsArcEnd=false.
	// should be nil when LastCompleted=0 or in non-Layered mode.
	ArcBoundary *storepkg.ArcBoundary

	// the three post-arc-end facts: whether the review / arc summary / volume summary are done.
	HasArcReview     bool
	HasArcSummary    bool
	HasVolumeSummary bool

	// missing foundation entries (the completion signal of the planning phase).
	FoundationMissing []string

	// the persisted planning tier (written to RunMeta when save_foundation persists scale).
	// empty = the first planning round has not produced any setting yet, so the planner identity cannot be determined.
	PlanningTier domain.PlanningTier

	// non-layered book: whether the most recently completed chapter already has a global review with scope=global
	// (only meaningful at the ShouldReview trigger point; always false for a layered book).
	HasGlobalReview bool

	// external revision impacts the Architect must handle before continuing. Ordinary writer feedback is left to the next
	// natural structural operation to absorb together, instead of dispatching a planner for every chapter.
	ImmediateFeedbackCount int

	// the earliest arc/volume artifact the Editor has to regenerate after an external revision.
	AggregateRefresh *AggregateRefresh
}

// Route returns the deterministic next-step instruction from the facts; returning nil is handled by the Engine per call context.
//
// Decision priority (mutually exclusive, matching the first hit from the top):
//  1. Phase=Complete        → nil (the Host deterministically produces the summary)
//  2. planning-phase settings missing and the planner is determinable → the same planner completes them; otherwise nil (the Engine applies the start-up fallback)
//  3. PendingRewrites non-empty  → writer rewrites/polishes per the queue
//  4. Flow=Reviewing        → nil (dormant: there is no writer right now, and during a review the Flow is really writing)
//  5. Flow=Steering         → nil (a user intervention is being handled)
//  6. an external revision invalidated aggregate artifacts → editor rebuilds them
//  7. an external revision affects later planning     → architect handles it
//  8. a layered book reaches the end of an arc          → review, summarize, expand the arc or continue into the next volume
//  9. the non-layered global review is due       → editor(global review)
//
// 10. the non-layered outline is exhausted        → architect(decides to finish or to continue from an outline)
// 11. everything else                   → writer(write next_chapter)
func Route(s State) *Instruction {
	p := s.Progress
	if p == nil {
		return nil
	}

	// 1. Terminal state: the Host generates a deterministic summary from the store facts
	if p.Phase == domain.PhaseComplete {
		return nil
	}

	// 2. Planning-phase completion: a table-lookup decision — what is missing lives in the store and the planner identity
	//    is derived from the persisted scale (short → architect_short, everything else → architect_long). An empty tier means the first
	//    planning round has not persisted any setting yet (choosing is a semantic judgement), so the Engine's planStartFallback arbitrates it.
	if p.Phase != domain.PhaseWriting {
		if len(s.FoundationMissing) > 0 && s.PlanningTier != "" {
			task := fmt.Sprintf("补齐基础设定与作品信息缺项：%s；book 使用 save_book，其余基础设定使用 save_foundation 落盘", strings.Join(s.FoundationMissing, "、"))
			if len(s.FoundationMissing) == 1 && s.FoundationMissing[0] == "foundation_audit" {
				task = "基础设定已齐全：重新调用 novel_context 读取全部已落盘工件与 foundation_status.fingerprint，审查跨文件语义一致性后调用 audit_foundation；有问题先修正并重新审查"
			}
			return &Instruction{
				Agent:  plannerForTier(s.PlanningTier),
				Task:   task,
				Reason: "基础设定缺项未齐，照缺项续派同一规划师",
			}
		}
		return nil
	}

	// 3. The rewrite/polish queue takes priority (the facts are already persisted at the tool layer, the Router just dispatches them)
	if len(p.PendingRewrites) > 0 {
		ch := p.PendingRewrites[0]
		verb := "重写"
		if p.Flow == domain.FlowPolishing {
			verb = "打磨"
		}
		return &Instruction{
			Agent:   "writer",
			Task:    fmt.Sprintf("%s第 %d 章", verb, ch),
			Reason:  fmt.Sprintf("PendingRewrites 队列剩余 %d 章", len(p.PendingRewrites)),
			Chapter: ch,
		}
	}

	// 4. Reviewing → hand back to the LLM. Currently a dormant branch: save_review only sets Flow to
	//    writing/rewriting/polishing, and no production path sets reviewing (during a review the Flow is really writing, and
	//    "review before continuing" is guaranteed by the agentcore steering priority rather than by this branch). It is kept for symmetry
	//    with Steering, and so that if the editor ever explicitly sets reviewing in a future review phase, routing yields to the LLM.
	if p.Flow == domain.FlowReviewing {
		return nil
	}

	// 5. A user intervention is being handled: the Arbiter is arbitrating and the Engine must not preempt it
	if p.Flow == domain.FlowSteering {
		return nil
	}
	if refresh := s.AggregateRefresh; refresh != nil {
		switch refresh.Kind {
		case AggregateArcReview:
			return &Instruction{
				Agent: "editor",
				Task: fmt.Sprintf(
					"审阅第 %d 卷第 %d 弧（第 %d-%d 章）：调用 novel_context(chapter=%d)，save_review 使用 scope=arc、chapter=%d",
					refresh.Volume, refresh.Arc, refresh.StartChapter, refresh.EndChapter, refresh.EndChapter, refresh.EndChapter,
				),
				Reason: "弧级审阅缺失",
			}
		case AggregateArcSummary:
			return &Instruction{
				Agent:  "editor",
				Task:   fmt.Sprintf("生成第 %d 卷第 %d 弧摘要、角色快照与写作规则（save_arc_summary）", refresh.Volume, refresh.Arc),
				Reason: "弧级摘要缺失",
			}
		case AggregateVolumeSummary:
			return &Instruction{
				Agent:  "editor",
				Task:   fmt.Sprintf("生成第 %d 卷卷摘要（save_volume_summary）", refresh.Volume),
				Reason: "卷摘要缺失",
			}
		case AggregateGlobalReview:
			return &Instruction{
				Agent:  "editor",
				Task:   fmt.Sprintf("审阅前 %d 章：调用 novel_context(chapter=%d)，save_review 使用 scope=global、chapter=%d", refresh.EndChapter, refresh.EndChapter, refresh.EndChapter),
				Reason: "全局审阅缺失",
			}
		}
	}

	if s.ImmediateFeedbackCount > 0 {
		return &Instruction{
			Agent:  plannerForTier(s.PlanningTier),
			Task:   "仅处理 novel_context 中的外部修订 writer_feedback：核对已发生剧情与后续计划，需要调整时调用 revise_outline 或相应结构工具，无需调整时调用 resolve_outline_feedback；不得处理 foundation_status 或其它规划，落盘后用一句话结束",
			Reason: fmt.Sprintf("有 %d 条外部修订影响尚未传播到后续规划", s.ImmediateFeedbackCount),
		}
	}

	// 8. Post-arc-end handling in layered mode
	if p.Layered && s.ArcBoundary != nil && s.ArcBoundary.IsArcEnd {
		b := s.ArcBoundary
		switch {
		case !s.HasArcReview:
			return &Instruction{
				Agent: "editor",
				Task: fmt.Sprintf(
					"对第 %d 卷第 %d 弧（第 %d-%d 章）做弧级评审：调用 novel_context(chapter=%d)，save_review 使用 scope=arc、chapter=%d；issues[].chapters 只能落在该区间",
					b.Volume, b.Arc, b.StartChapter, b.EndChapter, b.EndChapter, b.EndChapter,
				),
				Reason: "弧末评审未完成",
			}
		case !s.HasArcSummary:
			return &Instruction{
				Agent:  "editor",
				Task:   fmt.Sprintf("生成第 %d 卷第 %d 弧摘要、角色快照与写作规则（save_arc_summary）", b.Volume, b.Arc),
				Reason: "弧摘要未完成",
			}
		case b.IsVolumeEnd && !s.HasVolumeSummary:
			return &Instruction{
				Agent:  "editor",
				Task:   fmt.Sprintf("生成第 %d 卷卷摘要（save_volume_summary）", b.Volume),
				Reason: "卷摘要未完成",
			}
		case b.NeedsExpansion && b.NextArc > 0:
			return &Instruction{
				Agent:  "architect_long",
				Task:   fmt.Sprintf("展开第 %d 卷第 %d 弧（expand_next_arc）", b.NextVolume, b.NextArc),
				Reason: "下一弧骨架待展开",
			}
		case b.NeedsNewVolume:
			return &Instruction{
				Agent:  "architect_long",
				Task:   "创建下一卷：按完结判定清单评估后调用 save_foundation——故事继续 → type=append_volume；故事接近终点 → type=append_volume 且卷 JSON 顶层带 \"final\": true（收官卷，整卷收线，写完自动完结）；全部完结条件当下已满足 → type=complete_book。三选一均须附 reason 参数写明判定理由",
				Reason: "卷末需决定追加新卷、收官卷或结束全书",
			}
		}
	}

	// 11. Non-layered global review: once every ReviewInterval chapters (fact: that chapter's global review is not persisted).
	//     This used to be the review_required signal in commit_chapter's return value and is now derived from the facts —
	//     the return value is only a mirror of the fact, and Route looks at the same fact directly in the store.
	if !p.Layered && s.LastCompleted > 0 {
		if due, reason := domain.ShouldReview(len(p.CompletedChapters)); due && !s.HasGlobalReview {
			return &Instruction{
				Agent:  "editor",
				Task:   fmt.Sprintf("对前 %d 章做全局审阅（save_review scope=global, chapter=%d）", s.LastCompleted, s.LastCompleted),
				Reason: reason,
			}
		}
	}

	// 12. When the non-layered outline is exhausted, out-of-range chapters must not be dispatched. Let the Architect decide
	// to finish based on the current story facts, or use revise_outline to continue the plan from the next chapter.
	next := p.NextChapter()
	if next <= 0 {
		return nil
	}
	if !p.Layered && p.TotalChapters > 0 && next > p.TotalChapters {
		return &Instruction{
			Agent: plannerForTier(s.PlanningTier),
			Task: fmt.Sprintf(
				"非分层大纲已写完（已完成 %d 章，共 %d 章）：若故事已收束，调用 save_foundation(type=complete_book)；若仍需继续，用 revise_outline 从第 %d 章续接后续计划",
				len(p.CompletedChapters), p.TotalChapters, next,
			),
			Reason: "非分层大纲已耗尽，需决定完结或续接",
		}
	}

	// 13. Normal continuation
	return &Instruction{
		Agent:   "writer",
		Task:    fmt.Sprintf("写第 %d 章", next),
		Reason:  "续写下一章",
		Chapter: next,
	}
}
