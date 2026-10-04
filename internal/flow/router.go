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

	// Language is the writing language ("vi", "en", "zh") of the book.
	Language string
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
			task, reason := formatFoundationMissingTask(s.Language, s.FoundationMissing)
			return &Instruction{
				Agent:  plannerForTier(s.PlanningTier),
				Task:   task,
				Reason: reason,
			}
		}
		return nil
	}

	// 3. The rewrite/polish queue takes priority (the facts are already persisted at the tool layer, the Router just dispatches them)
	if len(p.PendingRewrites) > 0 {
		ch := p.PendingRewrites[0]
		task, reason := formatRewriteTask(s.Language, ch, p.Flow == domain.FlowPolishing, len(p.PendingRewrites))
		return &Instruction{
			Agent:   "writer",
			Task:    task,
			Reason:  reason,
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
		task, reason := formatAggregateRefreshTask(s.Language, refresh)
		return &Instruction{
			Agent:  refreshAgent(refresh.Kind),
			Task:   task,
			Reason: reason,
		}
	}

	if s.ImmediateFeedbackCount > 0 {
		task := "仅处理 novel_context 中的外部修订 writer_feedback：核对已发生剧情与后续计划，需要调整时调用 revise_outline 或相应结构工具，无需调整时调用 resolve_outline_feedback；不得处理 foundation_status 或其它规划，落盘后用一句话结束"
		reason := fmt.Sprintf("有 %d 条外部修订影响尚未传播到后续规划", s.ImmediateFeedbackCount)
		if strings.EqualFold(s.Language, "vi") {
			task = "Chỉ xử lý writer_feedback từ hiệu đính bên ngoài trong novel_context: đối chiếu tình tiết đã diễn ra với kế hoạch tiếp theo, khi cần điều chỉnh thì gọi revise_outline hoặc công cụ cấu trúc tương ứng, không cần điều chỉnh thì gọi resolve_outline_feedback; không xử lý foundation_status hay quy hoạch khác, lưu đĩa xong kết thúc bằng một câu"
			reason = fmt.Sprintf("Có %d phản hồi hiệu đính bên ngoài chưa cập nhật vào kế hoạch", s.ImmediateFeedbackCount)
		} else if strings.EqualFold(s.Language, "en") {
			task = "Process only external revision writer_feedback in novel_context: reconcile occurred plot with subsequent plan, call revise_outline or appropriate structural tools when adjustment is needed, call resolve_outline_feedback when no adjustment is needed; do not touch foundation_status or other planning, finish in one sentence after persisting"
			reason = fmt.Sprintf("%d external revision items not yet propagated to subsequent planning", s.ImmediateFeedbackCount)
		}
		return &Instruction{
			Agent:  plannerForTier(s.PlanningTier),
			Task:   task,
			Reason: reason,
		}
	}

	// 8. Post-arc-end handling in layered mode
	if p.Layered && s.ArcBoundary != nil && s.ArcBoundary.IsArcEnd {
		b := s.ArcBoundary
		switch {
		case !s.HasArcReview:
			task := fmt.Sprintf(
				"对第 %d 卷第 %d 弧（第 %d-%d 章）做弧级评审：调用 novel_context(chapter=%d)，save_review 使用 scope=arc、chapter=%d；issues[].chapters 只能落在该区间",
				b.Volume, b.Arc, b.StartChapter, b.EndChapter, b.EndChapter, b.EndChapter,
			)
			reason := "弧末评审未完成"
			if strings.EqualFold(s.Language, "vi") {
				task = fmt.Sprintf(
					"Đánh giá cấp arc cho quyển %d arc %d (chương %d-%d): gọi novel_context(chapter=%d), save_review dùng scope=arc, chapter=%d; issues[].chapters chỉ thuộc khoảng này",
					b.Volume, b.Arc, b.StartChapter, b.EndChapter, b.EndChapter, b.EndChapter,
				)
				reason = "Đánh giá cuối arc chưa hoàn thành"
			} else if strings.EqualFold(s.Language, "en") {
				task = fmt.Sprintf(
					"Perform arc-level review for volume %d arc %d (chapters %d-%d): call novel_context(chapter=%d), save_review with scope=arc, chapter=%d; issues[].chapters within range",
					b.Volume, b.Arc, b.StartChapter, b.EndChapter, b.EndChapter, b.EndChapter,
				)
				reason = "Arc-end review not completed"
			}
			return &Instruction{
				Agent:  "editor",
				Task:   task,
				Reason: reason,
			}
		case !s.HasArcSummary:
			task := fmt.Sprintf("生成第 %d 卷第 %d 弧摘要、角色快照与写作规则（save_arc_summary）", b.Volume, b.Arc)
			reason := "弧摘要未完成"
			if strings.EqualFold(s.Language, "vi") {
				task = fmt.Sprintf("Tạo tóm tắt quyển %d arc %d, snapshot nhân vật và quy tắc viết (save_arc_summary)", b.Volume, b.Arc)
				reason = "Tóm tắt arc chưa hoàn thành"
			} else if strings.EqualFold(s.Language, "en") {
				task = fmt.Sprintf("Generate volume %d arc %d summary, character snapshot, and writing rules (save_arc_summary)", b.Volume, b.Arc)
				reason = "Arc summary not completed"
			}
			return &Instruction{
				Agent:  "editor",
				Task:   task,
				Reason: reason,
			}
		case b.IsVolumeEnd && !s.HasVolumeSummary:
			task := fmt.Sprintf("生成第 %d 卷卷摘要（save_volume_summary）", b.Volume)
			reason := "卷摘要未完成"
			if strings.EqualFold(s.Language, "vi") {
				task = fmt.Sprintf("Tạo tóm tắt quyển %d (save_volume_summary)", b.Volume)
				reason = "Tóm tắt quyển chưa hoàn thành"
			} else if strings.EqualFold(s.Language, "en") {
				task = fmt.Sprintf("Generate volume %d summary (save_volume_summary)", b.Volume)
				reason = "Volume summary not completed"
			}
			return &Instruction{
				Agent:  "editor",
				Task:   task,
				Reason: reason,
			}
		case b.NeedsExpansion && b.NextArc > 0:
			task := fmt.Sprintf("展开第 %d 卷第 %d 弧（expand_next_arc）", b.NextVolume, b.NextArc)
			reason := "下一弧骨架待展开"
			if strings.EqualFold(s.Language, "vi") {
				task = fmt.Sprintf("Mở rộng quyển %d arc %d (expand_next_arc)", b.NextVolume, b.NextArc)
				reason = "Khung sườn arc tiếp theo cần mở rộng"
			} else if strings.EqualFold(s.Language, "en") {
				task = fmt.Sprintf("Expand volume %d arc %d (expand_next_arc)", b.NextVolume, b.NextArc)
				reason = "Next arc skeleton needs expansion"
			}
			return &Instruction{
				Agent:  "architect_long",
				Task:   task,
				Reason: reason,
			}
		case b.NeedsNewVolume:
			task := "创建下一卷：按完结判定清单评估后调用 save_foundation——故事继续 → type=append_volume；故事接近终点 → type=append_volume 且卷 JSON 顶层带 \"final\": true（收官卷，整卷收线，写完自动完结）；全部完结条件当下已满足 → type=complete_book。三选一均须附 reason 参数写明判定理由"
			reason := "卷末需决定追加新卷、收官卷或结束全书"
			if strings.EqualFold(s.Language, "vi") {
				task = "Tạo quyển tiếp theo: đánh giá theo checklist kết thúc rồi gọi save_foundation — truyện tiếp tục → type=append_volume; truyện gần kết → type=append_volume và JSON quyển có \"final\": true (quyển kết, thu tuyến toàn quyển, viết xong tự động kết thúc); đã thỏa mãn mọi điều kiện kết thúc → type=complete_book. Cả 3 lựa chọn đều phải kèm tham số reason nêu rõ lý do"
				reason = "Cuối quyển cần quyết định thêm quyển mới, quyển kết hoặc hoàn thành truyện"
			} else if strings.EqualFold(s.Language, "en") {
				task = "Create next volume: evaluate per ending checklist then call save_foundation — continuing → type=append_volume; approaching end → type=append_volume with \"final\": true; all ending conditions satisfied → type=complete_book. Include reason parameter for all choices"
				reason = "End of volume decision: append volume, final volume, or complete book"
			}
			return &Instruction{
				Agent:  "architect_long",
				Task:   task,
				Reason: reason,
			}
		}
	}

	// 11. Non-layered global review: once every ReviewInterval chapters (fact: that chapter's global review is not persisted).
	//     This used to be the review_required signal in commit_chapter's return value and is now derived from the facts —
	//     the return value is only a mirror of the fact, and Route looks at the same fact directly in the store.
	if !p.Layered && s.LastCompleted > 0 {
		if due, reason := domain.ShouldReview(len(p.CompletedChapters)); due && !s.HasGlobalReview {
			task := fmt.Sprintf("对前 %d 章做全局审阅（save_review scope=global, chapter=%d）", s.LastCompleted, s.LastCompleted)
			if strings.EqualFold(s.Language, "vi") {
				task = fmt.Sprintf("Đánh giá toàn cục %d chương đầu (save_review scope=global, chapter=%d)", s.LastCompleted, s.LastCompleted)
			} else if strings.EqualFold(s.Language, "en") {
				task = fmt.Sprintf("Perform global review of first %d chapters (save_review scope=global, chapter=%d)", s.LastCompleted, s.LastCompleted)
			}
			return &Instruction{
				Agent:  "editor",
				Task:   task,
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
		task, reason := formatOutlineExhaustedTask(s.Language, len(p.CompletedChapters), p.TotalChapters, next)
		return &Instruction{
			Agent:  plannerForTier(s.PlanningTier),
			Task:   task,
			Reason: reason,
		}
	}

	// 13. Normal continuation
	task, reason := formatWriteTask(s.Language, next)
	return &Instruction{
		Agent:   "writer",
		Task:    task,
		Reason:  reason,
		Chapter: next,
	}
}

func refreshAgent(k AggregateKind) string {
	if k == AggregateArcReview || k == AggregateArcSummary || k == AggregateVolumeSummary || k == AggregateGlobalReview {
		return "editor"
	}
	return "editor"
}

func formatWriteTask(lang string, next int) (task, reason string) {
	switch strings.ToLower(strings.TrimSpace(lang)) {
	case "vi":
		return fmt.Sprintf("Viết chương %d", next), "Viết tiếp chương tiếp theo"
	case "en":
		return fmt.Sprintf("Write chapter %d", next), "Continue next chapter"
	default:
		return fmt.Sprintf("写第 %d 章", next), "续写下一章"
	}
}

func formatRewriteTask(lang string, ch int, polishing bool, queueLen int) (task, reason string) {
	switch strings.ToLower(strings.TrimSpace(lang)) {
	case "vi":
		verb := "Viết lại"
		if polishing {
			verb = "Gọt giũa"
		}
		return fmt.Sprintf("%s chương %d", verb, ch), fmt.Sprintf("Hàng đợi viết lại còn %d chương", queueLen)
	case "en":
		verb := "Rewrite"
		if polishing {
			verb = "Polish"
		}
		return fmt.Sprintf("%s chapter %d", verb, ch), fmt.Sprintf("%d chapters remaining in rewrite queue", queueLen)
	default:
		verb := "重写"
		if polishing {
			verb = "打磨"
		}
		return fmt.Sprintf("%s第 %d 章", verb, ch), fmt.Sprintf("PendingRewrites 队列剩余 %d 章", queueLen)
	}
}

func formatOutlineExhaustedTask(lang string, completed, total, next int) (task, reason string) {
	switch strings.ToLower(strings.TrimSpace(lang)) {
	case "vi":
		return fmt.Sprintf(
			"Dàn ý đã viết xong (hoàn thành %d chương, tổng %d chương): nếu truyện đã kết thúc, gọi save_foundation(type=complete_book); nếu cần viết tiếp, dùng revise_outline từ chương %d để tiếp tục kế hoạch",
			completed, total, next,
		), "Dàn ý đã hết, cần quyết định kết thúc hay viết tiếp"
	case "en":
		return fmt.Sprintf(
			"Outline completed (%d chapters done, out of %d chapters): if story is finished, call save_foundation(type=complete_book); if continuing, use revise_outline from chapter %d to extend the outline",
			completed, total, next,
		), "Outline exhausted, need to decide to conclude or continue"
	default:
		return fmt.Sprintf(
			"非分层大纲已写完（已完成 %d 章，共 %d 章）：若故事已收束，调用 save_foundation(type=complete_book)；若仍需继续，用 revise_outline 从第 %d 章续接后续计划",
			completed, total, next,
		), "非分层大纲已耗尽，需决定完结或续接"
	}
}

func formatFoundationMissingTask(lang string, missing []string) (task, reason string) {
	switch strings.ToLower(strings.TrimSpace(lang)) {
	case "vi":
		if len(missing) == 1 && missing[0] == "foundation_audit" {
			return "Thiết lập cơ bản đã đầy đủ: gọi lại novel_context để đọc toàn bộ tài liệu đã lưu và foundation_status.fingerprint, kiểm tra tính nhất quán ngữ nghĩa đa file rồi gọi audit_foundation; có vấn đề thì sửa trước rồi kiểm tra lại", "Thiết lập cơ bản chưa đủ, tiếp tục phân công cùng planner để hoàn thiện"
		}
		return fmt.Sprintf("Hoàn thiện các mục thiết lập cơ bản và thông tin tác phẩm còn thiếu: %s; với book dùng save_book, các thiết lập cơ bản khác dùng save_foundation lưu đĩa", strings.Join(missing, ", ")), "Thiết lập cơ bản chưa đủ, tiếp tục phân công cùng planner để hoàn thiện"
	case "en":
		if len(missing) == 1 && missing[0] == "foundation_audit" {
			return "Foundation settings are complete: call novel_context again to inspect all saved artifacts and foundation_status.fingerprint, verify semantic consistency across files then call audit_foundation; resolve any issues and re-check", "Foundation settings incomplete, assigning same planner to complete"
		}
		return fmt.Sprintf("Fill in missing foundation settings and story metadata: %s; use save_book for book, use save_foundation for other settings", strings.Join(missing, ", ")), "Foundation settings incomplete, assigning same planner to complete"
	default:
		if len(missing) == 1 && missing[0] == "foundation_audit" {
			return "基础设定已齐全：重新调用 novel_context 读取全部已落盘工件与 foundation_status.fingerprint，审查跨文件语义一致性后调用 audit_foundation；有问题先修正并重新审查", "基础设定缺项未齐，照缺项续派同一规划师"
		}
		return fmt.Sprintf("补齐基础设定与作品信息缺项：%s；book 使用 save_book，其余基础设定使用 save_foundation 落盘", strings.Join(missing, "、")), "基础设定缺项未齐，照缺项续派同一规划师"
	}
}

func formatAggregateRefreshTask(lang string, refresh *AggregateRefresh) (task, reason string) {
	switch refresh.Kind {
	case AggregateArcReview:
		switch strings.ToLower(strings.TrimSpace(lang)) {
		case "vi":
			return fmt.Sprintf("Duyệt quyển %d arc %d (chương %d-%d): gọi novel_context(chapter=%d), save_review dùng scope=arc, chapter=%d",
				refresh.Volume, refresh.Arc, refresh.StartChapter, refresh.EndChapter, refresh.EndChapter, refresh.EndChapter), "Thiếu đánh giá cấp arc"
		case "en":
			return fmt.Sprintf("Review volume %d arc %d (chapters %d-%d): call novel_context(chapter=%d), save_review with scope=arc, chapter=%d",
				refresh.Volume, refresh.Arc, refresh.StartChapter, refresh.EndChapter, refresh.EndChapter, refresh.EndChapter), "Missing arc-level review"
		default:
			return fmt.Sprintf("审阅第 %d 卷第 %d 弧（第 %d-%d 章）：调用 novel_context(chapter=%d)，save_review 使用 scope=arc、chapter=%d",
				refresh.Volume, refresh.Arc, refresh.StartChapter, refresh.EndChapter, refresh.EndChapter, refresh.EndChapter), "弧级审阅缺失"
		}
	case AggregateArcSummary:
		switch strings.ToLower(strings.TrimSpace(lang)) {
		case "vi":
			return fmt.Sprintf("Tạo tóm tắt quyển %d arc %d, snapshot nhân vật và quy tắc viết (save_arc_summary)", refresh.Volume, refresh.Arc), "Thiếu tóm tắt cấp arc"
		case "en":
			return fmt.Sprintf("Generate volume %d arc %d summary, character snapshot, and writing rules (save_arc_summary)", refresh.Volume, refresh.Arc), "Missing arc-level summary"
		default:
			return fmt.Sprintf("生成第 %d 卷第 %d 弧摘要、角色快照与写作规则（save_arc_summary）", refresh.Volume, refresh.Arc), "弧级摘要缺失"
		}
	case AggregateVolumeSummary:
		switch strings.ToLower(strings.TrimSpace(lang)) {
		case "vi":
			return fmt.Sprintf("Tạo tóm tắt quyển %d (save_volume_summary)", refresh.Volume), "Thiếu tóm tắt cấp quyển"
		case "en":
			return fmt.Sprintf("Generate volume %d summary (save_volume_summary)", refresh.Volume), "Missing volume summary"
		default:
			return fmt.Sprintf("生成第 %d 卷卷摘要（save_volume_summary）", refresh.Volume), "卷摘要缺失"
		}
	case AggregateGlobalReview:
		switch strings.ToLower(strings.TrimSpace(lang)) {
		case "vi":
			return fmt.Sprintf("Duyệt %d chương đầu: gọi novel_context(chapter=%d), save_review dùng scope=global, chapter=%d", refresh.EndChapter, refresh.EndChapter, refresh.EndChapter), "Thiếu đánh giá toàn cục"
		case "en":
			return fmt.Sprintf("Review first %d chapters: call novel_context(chapter=%d), save_review with scope=global, chapter=%d", refresh.EndChapter, refresh.EndChapter, refresh.EndChapter), "Missing global review"
		default:
			return fmt.Sprintf("审阅前 %d 章：调用 novel_context(chapter=%d)，save_review 使用 scope=global、chapter=%d", refresh.EndChapter, refresh.EndChapter, refresh.EndChapter), "全局审阅缺失"
		}
	}
	return "", ""
}
