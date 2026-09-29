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
	return newCheckpointDeltaGuard(st, "writer", []string{"commit"}, writerBlockMsg, onBlock)
}

// writerBlockMsg works out which step the writer is stuck on, from the checkpoint steps seen in this run.
// Step names correspond to the values each tool persists: plan / draft / edit / consistency_check / commit.
func writerBlockMsg(seen map[string]struct{}) string {
	_, hasDraft := seen["draft"]
	_, hasEdit := seen["edit"]
	_, hasCheck := seen["consistency_check"]
	switch {
	case !hasDraft && !hasEdit:
		return "禁止结束：本轮尚未落盘任何正文。请按 plan_chapter → draft_chapter → check_consistency → commit_chapter 的顺序完成本章；正文只输出在聊天里等于丢失，必须通过工具落盘并提交。"
	case !hasCheck:
		return "禁止结束：正文已落盘但未收尾。请先调 check_consistency 核对一致性，再调 commit_chapter 提交本章。draft_chapter / edit_chapter 只是保存草稿，不算完成。"
	default:
		return "禁止结束：本章只差 commit_chapter 提交。请立即调用 commit_chapter；若它返回错误，先按错误信息处理（核对章节号、按提示补齐前置动作）再重试提交，不要在未提交的状态下结束。"
	}
}

// NewArchitectStopGuard requires the architect to persist at least one planning artifact in this run.
func NewArchitectStopGuard(st *store.Store, onBlock BlockHook) agentcore.StopGuard {
	return newCheckpointDeltaGuard(st, "architect",
		[]string{
			"book", "premise", "outline", "layered_outline", "characters", "world_rules",
			"foundation_audit", "expand_next_arc", "append_volume", "update_compass", "complete_book", "revise_outline", "resolve_outline_feedback",
		},
		staticBlockMsg("你必须调用 save_book、save_foundation、expand_next_arc、revise_outline、resolve_outline_feedback 或 audit_foundation 将产出落盘后才能结束。只输出 Markdown/JSON 文字等于丢失。"),
		onBlock,
	)
}

// NewEditorStopGuard requires the editor to persist an artifact matching the "task" before it may end.
//
// Task awareness: when dispatched to produce a summary, save_review alone (a review) does not count as done — the matching summary must be produced.
// Otherwise an editor "dispatched to write an arc summary but reviewed first" would satisfy the old lenient criterion,
// end early, and the arc summary would never land (together with dispatcher dedup going silent this once caused a
// mid-volume skeleton arc livelock, see outline-exhaustion-livelock). A terminal tool exit also consults the StopGuard
// (contract test TestContract_TerminalToolExitConsultsStopGuard), so hard stopping on save_review in build.go is safe:
// in a summary task, when the editor reviews first this guard vetoes that exit and nudges until the summary is persisted.
func NewEditorStopGuard(st *store.Store, task string, onBlock BlockHook) agentcore.StopGuard {
	switch {
	case strings.Contains(task, "save_volume_summary") || strings.Contains(task, "卷摘要"):
		return newCheckpointDeltaGuard(st, "editor", []string{"volume_summary"},
			staticBlockMsg("本次任务是生成卷摘要：你必须调用 save_volume_summary 落盘后才能结束，save_review 复核不算完成。"), onBlock)
	case strings.Contains(task, "save_arc_summary") || strings.Contains(task, "弧摘要"):
		return newCheckpointDeltaGuard(st, "editor", []string{"arc_summary"},
			staticBlockMsg("本次任务是生成弧摘要：你必须调用 save_arc_summary 落盘后才能结束，save_review 复核不算完成。"), onBlock)
	default:
		// Review or ad-hoc task: any review/summary landing is enough (keeping the existing lenient behavior).
		return newCheckpointDeltaGuard(st, "editor",
			[]string{"review", "arc_summary", "volume_summary"},
			staticBlockMsg("你必须调用 save_review / save_arc_summary / save_volume_summary 之一落盘结果后才能结束。"), onBlock)
	}
}
