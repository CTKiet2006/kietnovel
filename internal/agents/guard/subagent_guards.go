package guard

import (
	"context"
	"log/slog"
	"strings"
	"sync/atomic"

	"github.com/CTKiet2006/kietnovel/internal/store"
	"github.com/voocel/agentcore"
)

// subagentMaxConsecutiveBlocks escalates to termination after N consecutive blocks, avoiding a livelock on weak models.
const subagentMaxConsecutiveBlocks = 3

// BlockHook is the StopGuard's audit callback: it is called synchronously on every block/escalation. The Host uses it to surface the
// block facts to the TUI event stream and to offscreen notifications — otherwise blocks only reach the log, and in the UI the user sees
// nothing but "stuttering + tokens speeding up", with no way to tell whether the system is self-healing or spinning (issue #75).
// The callback does not take part in guard decisions. reason values:
//   - "blocked"    a nudge message was injected and the model will carry on
//   - "escalated"  consecutive idling passed the limit; this run terminates and is handed back to the caller
//   - "hard_stop"  provider refusal (safety/content_filter); terminates immediately
type BlockHook func(agent, reason string, consecutive int32)

// hardStopReasons lists the provider-side refusal reasons that a nudge message cannot recover from. Injecting
// "you must commit" is useless for them and instead burns the tokens of a full LLM call every time,
// and once it does eventually escalate the Engine re-runs the entire Worker task on top of that, multiplying the waste
// (measured: when ch02 hit safety a single chapter write produced 3 re-dispatches and 17 LLM calls, with the hit rate
// falling from 50% to 2.8%).
//
// Note that StopReasonError / StopReasonAborted do not need to be listed: when agentcore
// loop.go receives either of these stop reasons it terminates the run outright and never calls StopGuard.
// Only the provider refusal semantics that actually reach StopGuard are listed here.
var hardStopReasons = map[agentcore.StopReason]struct{}{
	"safety":         {},
	"content_filter": {},
}

// newCheckpointDeltaGuard builds a StopGuard that
// rejects end_turn if no checkpoint for the given step appears after the baseline.
// The baseline is captured by the caller at factory time, so the per-run semantics are correct.
//
// blockMsg receives the set of checkpoint steps observed after the baseline and assembles
// the nudge message from them — a static message is misleading when "the required tool itself keeps erroring" (nagging the model to call a
// tool that is currently failing, see #75).
//
// The counting semantics are "progress resets": if anything new appears between two blocks —
// any new checkpoint (a re-draft, a re-check, and so on) counts as the model making progress and consecutive returns to zero;
// only consecutive idling with no artifact at all accumulates and escalates to termination.
func newCheckpointDeltaGuard(st *store.Store, agentName string, requiredSteps []string, blockMsg func(seen map[string]struct{}) string, onBlock BlockHook) agentcore.StopGuard {
	var baseline int64
	if cp := st.Checkpoints.LatestGlobal(); cp != nil {
		baseline = cp.Seq
	}
	need := make(map[string]struct{}, len(requiredSteps))
	for _, s := range requiredSteps {
		need[s] = struct{}{}
	}
	var consecutive atomic.Int32
	var lastBlockSeq atomic.Int64 // the latest checkpoint Seq observed at the previous block; -1 means it has never blocked yet
	lastBlockSeq.Store(-1)
	return func(_ context.Context, info agentcore.StopInfo) agentcore.StopDecision {
		// Unrecoverable error: escalate directly, without wasting a nudge.
		if _, hard := hardStopReasons[info.Message.StopReason]; hard {
			slog.Error("subagent stop_guard 检测到不可恢复停机，立即升级",
				"module", "agent.guard", "agent", agentName,
				"turn", info.TurnIndex, "stop_reason", info.Message.StopReason)
			if onBlock != nil {
				onBlock(agentName, "hard_stop", consecutive.Load())
			}
			return agentcore.StopDecision{Allow: false, Escalate: true}
		}
		// Scan the checkpoints after the baseline in reverse order, collecting the steps that appeared (shared by the pass decision and the progress message).
		// New checkpoints are at the tail, so break as soon as one is <= the baseline.
		all := st.Checkpoints.All()
		latestSeq := baseline
		seen := make(map[string]struct{})
		for i := len(all) - 1; i >= 0; i-- {
			cp := all[i]
			if cp.Seq <= baseline {
				break
			}
			if cp.Seq > latestSeq {
				latestSeq = cp.Seq
			}
			seen[cp.Step] = struct{}{}
		}
		for s := range need {
			if _, ok := seen[s]; ok {
				consecutive.Store(0)
				return agentcore.StopDecision{Allow: true}
			}
		}
		// A new artifact persisted since the last block = the model is making progress (for example it drafted again after being nudged and then tried to wrap up),
		// so the counter resets; escalation should only punish progress-free spinning, not scrap every block of a whole run at once.
		if prev := lastBlockSeq.Load(); prev >= 0 && latestSeq > prev {
			consecutive.Store(0)
		}
		lastBlockSeq.Store(latestSeq)
		n := consecutive.Add(1)
		if n > subagentMaxConsecutiveBlocks {
			slog.Error("subagent stop_guard 连续阻拦超限，升级为终止",
				"module", "agent.guard", "agent", agentName, "turn", info.TurnIndex, "consecutive", n)
			if onBlock != nil {
				onBlock(agentName, "escalated", n)
			}
			return agentcore.StopDecision{Allow: false, Escalate: true}
		}
		slog.Warn("subagent stop_guard 拦截 end_turn",
			"module", "agent.guard", "agent", agentName, "turn", info.TurnIndex, "consecutive", n)
		if onBlock != nil {
			onBlock(agentName, "blocked", n)
		}
		return agentcore.StopDecision{Allow: false, InjectMessage: blockMsg(seen)}
	}
}

// staticBlockMsg adapts fixed wording to the blockMsg signature (the architect's and editor's artifacts are persisted by a single tool,
// so there is no multi-step progress and a static nudge is enough).
func staticBlockMsg(msg string) func(map[string]struct{}) string {
	return func(map[string]struct{}) string { return msg }
}

// NewWriterStopGuard requires the writer to produce at least one successful commit_chapter in this run.
// The nudge message is assembled from persisted step progress: the writer is the only subagent with a multi-step tool chain,
// so a static "you must call commit_chapter" misleads when prerequisite steps are missing or commit itself errors.
func NewWriterStopGuard(st *store.Store, onBlock BlockHook) agentcore.StopGuard {
	lang := ""
	if st != nil && st.BookLanguage != nil {
		lang, _ = st.BookLanguage.Load()
	}
	return newCheckpointDeltaGuard(st, "writer", []string{"commit"}, writerBlockMsgFor(lang), onBlock)
}

func writerBlockMsg(seen map[string]struct{}) string {
	return writerBlockMsgFor("zh")(seen)
}

func writerBlockMsgFor(lang string) func(map[string]struct{}) string {
	return func(seen map[string]struct{}) string {
		_, hasDraft := seen["draft"]
		_, hasEdit := seen["edit"]
		_, hasCheck := seen["consistency_check"]
		switch strings.ToLower(strings.TrimSpace(lang)) {
		case "vi":
			switch {
			case !hasDraft && !hasEdit:
				return "Chưa được kết thúc: Lượt này chưa ghi nhận bất kỳ chính văn nào xuống đĩa. Vui lòng hoàn thành chương theo đúng thứ tự: plan_chapter → draft_chapter → check_consistency → commit_chapter; văn bản chỉ in ra trong chat sẽ bị mất, bắt buộc phải lưu đĩa và nộp qua công cụ."
			case !hasCheck:
				return "Chưa được kết thúc: Chính văn đã ghi đĩa nhưng chưa hoàn tất. Vui lòng gọi check_consistency để kiểm tra tính nhất quán trước, sau đó gọi commit_chapter để nộp chương này. draft_chapter / edit_chapter chỉ là lưu bản nháp, chưa tính là hoàn thành."
			default:
				return "Chưa được kết thúc: Chương này chỉ còn thiếu bước commit_chapter để nộp. Vui lòng gọi ngay commit_chapter; nếu công cụ báo lỗi, hãy xử lý theo thông báo lỗi (kiểm tra số chương, hoàn thành các bước điều kiện trước) rồi thử nộp lại, tuyệt đối không kết thúc khi chưa nộp thành công."
			}
		case "en":
			switch {
			case !hasDraft && !hasEdit:
				return "Cannot end turn: No chapter prose has been persisted in this turn. Complete the chapter following: plan_chapter → draft_chapter → check_consistency → commit_chapter; chat output will be lost, prose must be saved and committed via tools."
			case !hasCheck:
				return "Cannot end turn: Prose has been drafted but not finalized. Call check_consistency to verify consistency, then call commit_chapter to submit. draft_chapter / edit_chapter only saves a draft."
			default:
				return "Cannot end turn: Ready for commit_chapter. Call commit_chapter immediately; if it returns an error, resolve the error and retry submission. Do not end without committing."
			}
		default:
			switch {
			case !hasDraft && !hasEdit:
				return "禁止结束：本轮尚未落盘任何正文。请按 plan_chapter → draft_chapter → check_consistency → commit_chapter 的顺序完成本章；正文只输出在聊天里等于丢失，必须通过工具落盘并提交。"
			case !hasCheck:
				return "禁止结束：正文已落盘但未收尾。请先调 check_consistency 核对一致性，再调 commit_chapter 提交本章。draft_chapter / edit_chapter 只是保存草稿，不算完成。"
			default:
				return "禁止结束：本章只差 commit_chapter 提交。请立即调用 commit_chapter；若它返回错误，先按错误信息处理（核对章节号、按提示补齐前置动作）再重试提交，不要在未提交的状态下结束。"
			}
		}
	}
}

// NewArchitectStopGuard requires the architect to persist at least one planning artifact in this run.
func NewArchitectStopGuard(st *store.Store, onBlock BlockHook) agentcore.StopGuard {
	lang := ""
	if st != nil && st.BookLanguage != nil {
		lang, _ = st.BookLanguage.Load()
	}
	msg := "你必须调用 save_book、save_foundation、expand_next_arc、revise_outline、resolve_outline_feedback 或 audit_foundation 将产出落盘后才能结束。只输出 Markdown/JSON 文字等于丢失。"
	switch strings.ToLower(strings.TrimSpace(lang)) {
	case "vi":
		msg = "Bạn phải gọi save_book, save_foundation, expand_next_arc, revise_outline, resolve_outline_feedback hoặc audit_foundation để lưu kết quả xuống đĩa trước khi kết thúc. Chỉ in văn bản Markdown/JSON ra chat sẽ bị mất dữ liệu."
	case "en":
		msg = "You must call save_book, save_foundation, expand_next_arc, revise_outline, resolve_outline_feedback, or audit_foundation to persist artifacts before ending. Outputting Markdown/JSON text alone will be lost."
	}
	return newCheckpointDeltaGuard(st, "architect",
		[]string{
			"book", "premise", "outline", "layered_outline", "characters", "world_rules",
			"foundation_audit", "expand_next_arc", "append_volume", "update_compass", "complete_book", "revise_outline", "resolve_outline_feedback",
		},
		staticBlockMsg(msg),
		onBlock,
	)
}

// NewEditorStopGuard requires the editor to persist an artifact matching the "task" before it may end.
func NewEditorStopGuard(st *store.Store, task string, onBlock BlockHook) agentcore.StopGuard {
	lang := ""
	if st != nil && st.BookLanguage != nil {
		lang, _ = st.BookLanguage.Load()
	}
	lowerTask := strings.ToLower(task)
	isVolumeSummary := strings.Contains(task, "save_volume_summary") || strings.Contains(task, "卷摘要") || strings.Contains(task, "tóm tắt quyển") || strings.Contains(lowerTask, "volume summary")
	isArcSummary := strings.Contains(task, "save_arc_summary") || strings.Contains(task, "弧摘要") || strings.Contains(task, "tóm tắt arc") || strings.Contains(lowerTask, "arc summary")
	switch {
	case isVolumeSummary:
		msg := "本次任务是生成卷摘要：你必须调用 save_volume_summary 落盘后才能结束，save_review 复核不算完成。"
		if strings.EqualFold(lang, "vi") {
			msg = "Nhiệm vụ lần này là tạo tóm tắt quyển: bạn phải gọi save_volume_summary để lưu đĩa trước khi kết thúc, save_review không tính là hoàn thành nhiệm vụ này."
		} else if strings.EqualFold(lang, "en") {
			msg = "This task is to generate a volume summary: you must call save_volume_summary before ending; save_review does not count as completed."
		}
		return newCheckpointDeltaGuard(st, "editor", []string{"volume_summary"}, staticBlockMsg(msg), onBlock)
	case isArcSummary:
		msg := "本次任务是生成弧摘要：你必须调用 save_arc_summary 落盘后才能结束，save_review 复核不算完成。"
		if strings.EqualFold(lang, "vi") {
			msg = "Nhiệm vụ lần này là tạo tóm tắt arc: bạn phải gọi save_arc_summary để lưu đĩa trước khi kết thúc, save_review không tính là hoàn thành nhiệm vụ này."
		} else if strings.EqualFold(lang, "en") {
			msg = "This task is to generate an arc summary: you must call save_arc_summary before ending; save_review does not count as completed."
		}
		return newCheckpointDeltaGuard(st, "editor", []string{"arc_summary"}, staticBlockMsg(msg), onBlock)
	default:
		msg := "你必须调用 save_review / save_arc_summary / save_volume_summary 之一落盘结果后才能结束。"
		if strings.EqualFold(lang, "vi") {
			msg = "Bạn phải gọi một trong các công cụ save_review / save_arc_summary / save_volume_summary để lưu kết quả xuống đĩa trước khi kết thúc."
		} else if strings.EqualFold(lang, "en") {
			msg = "You must call one of save_review / save_arc_summary / save_volume_summary to persist results before ending."
		}
		return newCheckpointDeltaGuard(st, "editor",
			[]string{"review", "arc_summary", "volume_summary"},
			staticBlockMsg(msg), onBlock)
	}
}
