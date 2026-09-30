package host

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	buildversion "github.com/CTKiet2006/kietnovel/internal/version"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/voocel/agentcore"
	"github.com/voocel/agentcore/subagent"

	"github.com/CTKiet2006/kietnovel/internal/arbiter"
	"github.com/CTKiet2006/kietnovel/internal/domain"
	"github.com/CTKiet2006/kietnovel/internal/errs"
	"github.com/CTKiet2006/kietnovel/internal/flow"
	"github.com/CTKiet2006/kietnovel/internal/notify"
	storepkg "github.com/CTKiet2006/kietnovel/internal/store"
	"github.com/CTKiet2006/kietnovel/internal/tools"
)

// engine is the deterministic execution engine: read facts -> Route -> pre-checks -> run the Worker directly ->
// check progress -> loop; semantic scenarios consult the Arbiter on demand. It executes decisions and takes no
// part in literary judgement (docs/engine-rfc.md). Single goroutine, serial, and control state only changes at loop boundaries.
type engine struct {
	store   *storepkg.Store
	workers *subagent.Runner

	arbiterModel    agentcore.ChatModel
	failurePrompt   string
	planStartPrompt string // system prompt for the start verdict: when the verdict has never completed, the engine runs the verdict on the spot from StartPrompt
	style           string // style name, passed to DecidePlanStart during the on-the-spot verdict
	// reconsult sends an expired steer back through the host's full verdict path (persistence / audit / applying
	// every action), asynchronously - the engine only discards expired dispatches and never runs a partial re-verdict itself.
	reconsult func(text string)

	observer  *observer
	budget    *BudgetSentinel
	gate      *ChapterAdvanceGate
	refresh   func() // refresh RestorePack before every writer dispatch
	emitEvent func(Event)
	notify    func(kind, level, title, body string)
	onPause   func(summary string) // engine-initiated pause (deadlock / failed verdict abort): goes through the host unified pause semantics (lifecycle=paused)
	onDone    func()               // run ended (for any reason); the host decides the final state from the store facts

	mu      sync.Mutex
	wg      sync.WaitGroup
	cancel  context.CancelFunc
	running bool
	// aborting đánh dấu vòng lặp hiện tại bị dừng chủ ý (người dùng tạm dừng, vào
	// đồng sáng tác, chạm trần ngân sách) chứ không phải hỏng. Xem abort().
	aborting bool
	pending  []controlOp       // control-state actions to intervene, committed at the boundary
	next     *flow.Instruction // the instruction to execute first on the next round (plan_start / arbiter dispatch)
	// deferGateForNext lives and dies with the next op: a hold+dispatch must first run the paired
	// editor/writer so it establishes the rewrite queue, and only then can the Gate judge rewrites_drained.
	deferGateForNext bool

	// deadlock tracking: if Route still produces the same command key after a round, the count accumulates.
	// A Router command is a projection of the task's post-condition; real progress makes the next command change.
	lastKey string
	repeats int
	// failure retry: the same command key is retried only once, then the Arbiter is consulted.
	failedKey string
	// keeps the most recent Worker error for the same command, so the deadlock verdict sees the real cause.
	lastWorkerErrorKey string
	lastWorkerError    error
}

// deadlockConsultAt / deadlockAbortAt: once repeats reaches the former the Arbiter is consulted, once it reaches the latter it hard-fuses.
// A deterministic Engine must give a clear upper bound for a no-progress loop (RFC §5).
const (
	deadlockConsultAt = 3
	deadlockAbortAt   = 5
)

// controlOp is the action in a steer verdict that modifies control state (a boundary commit; RFC §3).
// text/facts keep the original consultation context: when a dispatch fails reconciliation it is re-queried with the new facts.
type controlOp struct {
	hold     *arbiter.AdvanceHoldOp
	reopen   *arbiter.ReopenOp
	dispatch *arbiter.DispatchOp
	text     string
	facts    arbiter.InterventionFacts
}

// start starts the engine loop; a no-op if it is already running (returns false).
func (e *engine) start(initial *flow.Instruction) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.running {
		return false
	}
	ctx, cancel := context.WithCancel(context.Background())
	ctx = agentcore.WithToolProgress(ctx, e.observer.workerProgress)
	e.cancel = cancel
	e.running = true
	// Mỗi vòng lặp bắt đầu chưa bị dừng: cờ của vòng trước không được mang sang,
	// không thì lỗi thật ở vòng sau sẽ bị quy cho là do dừng.
	e.aborting = false
	// An empty initial does not overwrite e.next - a steer arriving during the stop may already have queued a
	// verdict dispatch (e.g. an editor rewrite) through applyControlOp, and start(nil) wiping it would let
	// Route dispatch a writer to continue, the opposite of the user's intent.
	if initial != nil {
		e.next = initial
		e.deferGateForNext = false
	}
	e.lastKey, e.repeats, e.failedKey = "", 0, ""
	e.lastWorkerErrorKey, e.lastWorkerError = "", nil
	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		e.run(ctx)
	}()
	return true
}

// abort cancels the current loop (pause semantics; the checkpoint makes it lossless).
//
// Sets aborting first, so any tool call interrupted by this cancel is reported as
// "stopped" rather than as a failure. Canceling the context halfway through a model
// call is normal (user pause, entering co-create, budget cap); showing it as a red
// error makes a normal operation look like a fault. The flag is cleared when the
// next run starts, so a real error in a later run still shows up.
func (e *engine) abort() {
	e.mu.Lock()
	e.aborting = true
	cancel := e.cancel
	e.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// isAborting reports whether the current loop was stopped by an explicit abort,
// as opposed to a genuine failure.
func (e *engine) isAborting() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.aborting
}

// wait waits for the current Engine goroutine to exit completely. Host.Close calls cancel first and then this,
// so the event channel and process exit only after the write helpers and runEnded have finished.
func (e *engine) wait() {
	e.wg.Wait()
}

func (e *engine) isRunning() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.running
}

// enqueue puts the steer's control-state actions on the boundary queue (while the engine runs); false means it is not
// running and the caller should execute them itself immediately.
func (e *engine) enqueue(op controlOp) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.running {
		return false
	}
	e.pending = append(e.pending, op)
	return true
}

func (e *engine) run(ctx context.Context) {
	defer func() {
		e.mu.Lock()
		e.running = false
		e.cancel = nil
		leftover := e.pending
		e.pending = nil
		e.mu.Unlock()
		// exit race: when enqueue races with the exit, the leftover steer actions must not be dropped silently -
		// hold/reopen are idempotent fact writes, so they are replayed with an independent ctx; a dispatch has no
		// engine to hand it to, so the PendingSteer is persisted again (the host may already have cleared it as
		// "enqueued"), and the next Resume/Continue replays the whole steer.
		for _, op := range leftover {
			if op.dispatch != nil {
				if op.text != "" {
					if err := e.store.RunMeta.SetPendingSteer(op.text); err != nil {
						slog.Warn("Không lưu lại được can thiệp còn dư", "module", "engine", "err", err)
					}
				}
				e.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Level: "warn",
					Summary: "Engine đã dừng, phần phát việc theo định đoạn chưa chạy; can thiệp đã được giữ, sẽ tự định đoạn lại khi viết tiếp"})
				op.dispatch = nil
			}
			if op.hold != nil || op.reopen != nil {
				if err := e.applyControlOp(context.Background(), op); err != nil {
					e.emitEvent(Event{Time: time.Now(), Category: "ERROR", Level: "error",
						Summary: "Khi Engine thoát, không bổ sung lại được can thiệp: " + err.Error()})
				}
			}
		}
		e.onDone()
	}()

	for {
		if ctx.Err() != nil {
			return
		}
		// hold+dispatch must first let the paired dispatch establish the rewrite fact; in every other case the Gate
		// is checked uniformly before dispatching, so a boundary hold or an unpermitted review cannot run one extra Worker.
		deferGate := e.applyPendingOps(ctx) || e.nextDefersGate()
		if !deferGate {
			if e.gate.HandleBoundary() {
				return
			}
		}

		inst := e.takeNext()
		if inst == nil {
			state, err := flow.LoadState(e.store)
			if err != nil {
				e.pauseWithNotify(notify.KindWorkerFailure, "路由事实读取失败，已暂停: "+err.Error())
				return
			}
			// the volume summary may already be on disk while the process has not had the chance to MarkComplete yet. When all
			// aggregated artefacts are complete, finish the completion decision from the facts first and only then hand over to the Router, so the closing volume is not mistakenly dispatched to continue a volume.
			if state.AggregateRefresh == nil && state.Progress != nil && state.Progress.Layered &&
				state.Progress.Phase == domain.PhaseWriting && state.ArcBoundary != nil &&
				state.ArcBoundary.IsVolumeEnd && state.HasArcReview && state.HasArcSummary && state.HasVolumeSummary {
				complete, reconcileErr := tools.ReconcileLayeredCompletion(e.store)
				if reconcileErr != nil {
					e.pauseWithNotify(notify.KindWorkerFailure, "完结状态恢复失败，已暂停: "+reconcileErr.Error())
					return
				}
				if complete {
					continue
				}
			}
			inst = flow.Route(state)
		}
		if inst == nil {
			var err error
			inst, err = e.planStartFallback(ctx)
			if err != nil {
				e.pauseWithNotify(notify.KindPlanStart, "规划恢复事实读取失败，已暂停: "+err.Error())
				return
			}
		}
		if inst == nil {
			// semantic scenario or terminal state: book finished -> deterministic wrap-up; anything else (leftover Steering etc.)
			// -> a natural stop, waiting for the user to Continue / steer.
			return
		}
		replaced, err := e.precheck(inst)
		if err != nil {
			e.pauseWithNotify(notify.KindWorkerFailure, "派单前置校验失败，已暂停: "+err.Error())
			return
		}
		if replaced != nil {
			inst = replaced
		}
		allowed, gateErr := e.gate.Allow(inst)
		if gateErr != nil {
			e.pauseWithNotify(notify.KindAdvanceGate, "章节推进控制错误，已暂停: "+gateErr.Error())
			return
		}
		if !allowed {
			return
		}
		if stop := e.trackDeadlock(ctx, &inst); stop {
			return
		}
		if inst == nil {
			continue // a deadlock verdict requires the route to be recomputed
		}

		err = e.runWorker(ctx, inst)
		if ctx.Err() != nil {
			return
		}
		e.rememberWorkerError(inst, err)
		if err != nil {
			// trackDeadlock pre-records this attempt before dispatching. Errors that never entered a valid Worker
			// semantic execution must not be counted as "no progress on the same task".
			e.discardNonSemanticDeadlockAttempt(inst, err)
			if stop := e.handleWorkerError(ctx, inst, err); stop {
				return
			}
		}

		// policy boundary: the budget stop-loss takes priority over an acceptance / progress pause.
		if e.budget.HandleBoundary() {
			return
		}
		if e.gate.HandleBoundary() {
			return
		}
	}
}

func (e *engine) takeNext() *flow.Instruction {
	e.mu.Lock()
	defer e.mu.Unlock()
	inst := e.next
	e.next = nil
	e.deferGateForNext = false
	return inst
}

func (e *engine) nextDefersGate() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.next != nil && e.deferGateForNext
}

// planStartFallback covers the two windows where planning facts are missing and Route cannot derive the architect:
//  1. the verdict is on disk but the first save_foundation has not happened yet -> continue from the fixed
//     PlanStartRecord without re-verdicts (RFC §6); once the first foundation is on disk the tier is in place and the
//     completion branch takes over.
//  2. the verdict never completed (model failure at startup) but the input fact StartPrompt is there -> run a verdict on the spot.
//     This is a retry of the first verdict and does not violate "resuming does not depend on re-verdict" - that discipline targets verdicts that already exist. A failed re-verdict goes to an explicit pause: a failed startup must not stop silently.
func (e *engine) planStartFallback(ctx context.Context) (*flow.Instruction, error) {
	progress, err := e.store.Progress.Load()
	if err != nil {
		return nil, fmt.Errorf("load progress: %w", err)
	}
	if progress == nil {
		return nil, nil
	}
	if progress.Phase == domain.PhaseWriting || progress.Phase == domain.PhaseComplete {
		return nil, nil
	}
	meta, err := e.store.RunMeta.Load()
	if err != nil {
		return nil, fmt.Errorf("load run meta: %w", err)
	}
	if meta == nil || meta.PlanningTier != "" {
		return nil, nil
	}
	missing, err := e.store.FoundationMissing()
	if err != nil {
		return nil, fmt.Errorf("load foundation state: %w", err)
	}
	if len(missing) == 0 {
		return nil, nil
	}
	if meta.PlanStart != nil {
		return &flow.Instruction{
			Agent:  meta.PlanStart.Planner,
			Task:   meta.PlanStart.PlannerTask,
			Reason: "按已固化的启动裁定开始规划",
		}, nil
	}
	if meta.StartPrompt == "" {
		return nil, nil
	}
	return e.retryPlanStart(ctx, meta.StartPrompt), nil
}

// retryPlanStart makes the missing startup verdict and fixes it in (the verdict writes its facts before acting, same structure as StartPrepared).
func (e *engine) retryPlanStart(ctx context.Context, prompt string) *flow.Instruction {
	start := time.Now()
	decision, derr := runObservedDecision(e.observer, "启动补裁", func() (arbiter.PlanStartDecision, error) {
		return arbiter.DecidePlanStart(ctx, e.arbiterModel, e.planStartPrompt, prompt, e.style)
	})
	rec := storepkg.DecisionRecord{Kind: "plan_start", Decider: "arbiter", Input: prompt,
		Reason: decision.Reason, DurationMs: time.Since(start).Milliseconds()}
	if derr == nil {
		if data, err := json.Marshal(decision); err == nil {
			rec.Decision = data
		}
	} else {
		rec.Error = derr.Error()
	}
	rec, recErr := e.store.Decisions.Append(rec)
	if recErr != nil {
		slog.Warn("启动补裁审计落盘失败", "module", "engine", "err", recErr)
	}
	if derr != nil {
		e.pauseWithNotify(notify.KindPlanStart, "启动裁定失败,已暂停(请检查模型/网络配置后继续): "+derr.Error())
		return nil
	}
	if err := e.store.RunMeta.SetPlanStart(domain.PlanStartRecord{
		RawPrompt: prompt, Planner: decision.Planner, PlannerTask: decision.Task, DecisionID: rec.ID,
	}); err != nil {
		e.pauseWithNotify(notify.KindPlanStart, "Không ghi được định đoạn khởi động, đã tạm dừng: "+err.Error())
		return nil
	}
	e.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Level: "info",
		Summary: fmt.Sprintf("Đã bù xong định đoạn khởi động (kiến trúc sư: %s — %s)", decision.Planner, decision.Reason)})
	return &flow.Instruction{Agent: decision.Planner, Task: decision.Task, Reason: decision.Reason}
}

// precheck is the deterministic embodiment of the old ToolGate: an illegal dispatch is rewritten directly, with no teaching text.
func (e *engine) precheck(inst *flow.Instruction) (*flow.Instruction, error) {
	progress, err := e.store.Progress.Load()
	if err != nil {
		return nil, fmt.Errorf("load progress: %w", err)
	}
	if progress != nil && progress.Phase == domain.PhaseComplete {
		// the only legal exit during the completion phase is reopen (a steer action); any dispatch is dropped outright.
		slog.Warn("完本期派发被丢弃", "module", "engine", "agent", inst.Agent)
		return &flow.Instruction{}, nil // blanked: the next round sees Route return nil and stops on its own
	}
	if inst.Agent == "writer" {
		if progress == nil || progress.Phase != domain.PhaseWriting {
			phase := "<nil>"
			if progress != nil {
				phase = string(progress.Phase)
			}
			return nil, fmt.Errorf("writer 仅能在 writing 阶段派发（当前 phase=%s）: %w", phase, errInvalidWriteTarget)
		}
		ch, err := writerTargetChapter(e.store)
		if err != nil {
			return nil, err
		}
		if ch > 0 {
			if err := tools.EnsureChapterExpanded(e.store, ch); err != nil {
				if !errors.Is(err, errs.ErrToolPrecondition) {
					return nil, err
				}
				// the target chapter is not laid out -> deterministically reassign architect_long to lay it out (the old gate's
				// teaching text was for the LLM; the Engine simply does the right thing).
				return &flow.Instruction{
					Agent:  "architect_long",
					Task:   fmt.Sprintf("下一弧为骨架(%s)。调用 expand_next_arc 展开下一弧；若当前卷已写完，改用 save_foundation(type=append_volume) 追加并展开下一卷。", err),
					Reason: "写作目标章未展开,先展开再续写",
				}, nil
			}
		}
		e.refresh()
	}
	return nil, nil
}

// writerTargetChapter derives the chapter a writer dispatch will actually write next (the head of the rewrite queue, otherwise the next chapter).
func writerTargetChapter(st *storepkg.Store) (int, error) {
	progress, err := st.Progress.Load()
	if err != nil {
		return 0, fmt.Errorf("load progress: %w", err)
	}
	if progress == nil {
		return 0, fmt.Errorf("progress 未初始化")
	}
	if len(progress.PendingRewrites) > 0 {
		return progress.PendingRewrites[0], nil
	}
	return progress.NextChapter(), nil
}

// trackDeadlock maintains the deadlock count: the same Agent+Task appearing in a row means the previous
// round did not satisfy the routing post-condition. Intermediate checkpoints inside the Worker
// (plan / draft / edit etc.) are only for recovery and observation and must not reset the Engine-level count (issue #84).
// The Arbiter is consulted once repeats reaches the threshold, and a hard upper bound fuses directly.
// Returning stop=true means this round should end the loop; inst may be rewritten (reroute) or set to nil (recomputed) by the Arbiter.
func (e *engine) trackDeadlock(ctx context.Context, inst **flow.Instruction) (stop bool) {
	in := *inst
	if in == nil || in.Agent == "" {
		*inst = nil
		return false
	}
	key := instructionKey(in)
	if key == e.lastKey {
		e.repeats++
	} else {
		e.lastKey, e.repeats = key, 1
	}
	if e.repeats < deadlockConsultAt {
		return false
	}
	if e.repeats >= deadlockAbortAt {
		e.pauseStuck(notify.KindDeadlock, in, fmt.Sprintf("僵局熔断: 指令连续 %d 次无进展(%s),已暂停等待人工介入", e.repeats, in.Agent))
		return true
	}
	// Arbiter deadlock consultation (repeats ∈ [consultAt, abortAt)). A retry verdict does not zero the count.
	facts := e.failureFacts("deadlock", in, e.workerErrorFor(in))
	decision, err := runObservedDecision(e.observer, "僵局裁定", func() (arbiter.FailureDecision, error) {
		return arbiter.DecideFailure(ctx, e.arbiterModel, e.failurePrompt, facts)
	})
	e.recordFailureDecision("deadlock", in, facts, decision, err)
	if err != nil {
		e.pauseWithNotify(notify.KindDeadlock, "僵局裁定失败,已暂停等待人工介入: "+err.Error())
		return true
	}
	switch decision.Action {
	case "retry":
		return false
	case "reroute":
		*inst = &flow.Instruction{Agent: decision.Dispatch.Agent, Task: decision.Dispatch.Task, Reason: decision.Reason}
		return false
	default: // abort
		e.pauseStuck(notify.KindDeadlock, in, "僵局裁定: "+decision.Reason)
		return true
	}
}

// runWorker runs one subagent directly: DISPATCH events + progress relay + result parsing.
func (e *engine) runWorker(ctx context.Context, inst *flow.Instruction) error {
	e.observer.dispatchStart(inst.Agent, inst.Task, inst.Reason)
	// the Writer task is pre-marked in progress (same as the old Dispatcher: the UI outline immediately reflects "▸ in progress").
	if inst.Agent == "writer" && inst.Chapter > 0 {
		if err := e.store.Progress.ValidateChapterWork(inst.Chapter); err != nil {
			runErr := fmt.Errorf("%w: %w", errInvalidWriteTarget, err)
			e.observer.dispatchFinish(inst.Agent, runErr)
			return runErr
		}
		if err := e.store.Progress.StartChapter(inst.Chapter); err != nil {
			runErr := fmt.Errorf("%w: 预标第 %d 章进行中失败: %w", errInvalidWriteTarget, inst.Chapter, err)
			e.observer.dispatchFinish(inst.Agent, runErr)
			return runErr
		}
	}

	// Worker progress is relayed to the observer through ctx ToolProgress.
	runCtx := agentcore.WithToolProgress(ctx, func(p agentcore.ProgressPayload) {
		e.observer.workerProgress(p)
	})
	_, err := e.workers.Run(runCtx, inst.Agent, inst.Task)
	if err == nil {
		// success clears the failure tracking: the next failure of the same key enjoys the "retry once" allowance again.
		e.failedKey = ""
	}
	e.observer.dispatchFinish(inst.Agent, err)
	return err
}

// handleWorkerError retries the same command once, then hands the error type and the current facts to the Arbiter.
// The Engine does not hardcode which execution errors are "necessarily unrecoverable"; the semantic reassignment
// is the model's decision, and the Store boundary keeps blocking illegal writes.
func (e *engine) handleWorkerError(ctx context.Context, inst *flow.Instruction, werr error) (stop bool) {
	msg := werr.Error()

	key := instructionKey(inst)
	if e.failedKey != key {
		// first failure: retry the original command once (the next round's Route recomputes it, and fact-driven recomputation is naturally idempotent).
		e.failedKey = key
		return false
	}
	e.failedKey = ""
	facts := e.failureFacts("worker_failure", inst, werr)
	decision, err := runObservedDecision(e.observer, "失败裁定", func() (arbiter.FailureDecision, error) {
		return arbiter.DecideFailure(ctx, e.arbiterModel, e.failurePrompt, facts)
	})
	e.recordFailureDecision("worker_failure", inst, facts, decision, err)
	if err != nil {
		e.pauseWithNotify(notify.KindWorkerFailure, "失败裁定不可用,已暂停等待人工介入: "+msg+contentFilterAdvice(werr))
		return true
	}
	switch decision.Action {
	case "retry":
		return false
	case "reroute":
		e.mu.Lock()
		e.next = &flow.Instruction{Agent: decision.Dispatch.Agent, Task: decision.Dispatch.Task, Reason: decision.Reason}
		e.deferGateForNext = false
		e.mu.Unlock()
		return false
	default: // abort
		e.pauseStuck(notify.KindWorkerFailure, inst, "失败裁定: "+decision.Reason+contentFilterAdvice(werr))
		return true
	}
}

// pauseStuck pauses when the engine gives up on a command: a chapter awaiting rewrite leaves the queue before the
// stop. It is only used for the exits where the engine has already decided the command cannot go through
// (deadlock fuse, a deadlock / failure verdict abort); an unavailable verdict and other infrastructure failures still go through pauseWithNotify - those are external problems and must not cost a rewrite of a whole chapter.
func (e *engine) pauseStuck(kind string, inst *flow.Instruction, body string) {
	if e.dropStuckRewrite(inst) {
		body += fmt.Sprintf("；第 %d 章已移出返工队列(保留上一版终稿),继续创作将从后续章节推进", inst.Chapter)
	}
	e.pauseWithNotify(kind, body)
}

// dropStuckRewrite removes a stuck rewrite chapter from the queue. PendingRewrites is persisted fact; if the
// engine gives up on this command without dequeuing it, a restart would immediately replay the same dead
// command and lock the whole book forever (issue #110). true means it really was dequeued.
func (e *engine) dropStuckRewrite(inst *flow.Instruction) bool {
	if inst == nil || inst.Agent != "writer" || inst.Chapter <= 0 {
		return false
	}
	progress, err := e.store.Progress.Load()
	if err != nil || progress == nil || !slices.Contains(progress.PendingRewrites, inst.Chapter) {
		return false
	}
	if err := e.store.Progress.CompleteRewrite(inst.Chapter); err != nil {
		slog.Warn("卡死返工章出队失败", "module", "engine", "chapter", inst.Chapter, "err", err)
		return false
	}
	return true
}

// discardNonSemanticDeadlockAttempt undoes the semantic attempt that trackDeadlock pre-recorded for
// this dispatch. Only stable error types where the model call did not execute to completion are excluded;
// content_filter stays on the existing self-healing path, while real no-progress from max_turns, stop_guard, cancellation and the like is still counted.
func (e *engine) discardNonSemanticDeadlockAttempt(inst *flow.Instruction, werr error) {
	if inst == nil || !isNonSemanticWorkerFailure(werr) {
		return
	}
	key := instructionKey(inst)
	if e.lastKey != key || e.repeats <= 0 {
		return
	}
	e.repeats--
	if e.repeats == 0 {
		e.lastKey = ""
	}
}

// isNonSemanticWorkerFailure recognises only errors meaning "this model execution produced no judgeable semantics".
// It prefers the agentcore error-chain contract; when the provider flattens the chain it reuses the log classification.
func isNonSemanticWorkerFailure(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, agentcore.ErrContextOverflow) || errors.Is(err, agentcore.ErrStreamPartial) {
		return true
	}
	providerErr := agentcore.ClassifyProvider(err)
	classified := errors.Is(providerErr, agentcore.ErrProviderStreamIdle) ||
		errors.Is(providerErr, agentcore.ErrProviderQuota) ||
		errors.Is(providerErr, agentcore.ErrProviderRateLimit) ||
		errors.Is(providerErr, agentcore.ErrProviderTimeout) ||
		errors.Is(providerErr, agentcore.ErrProviderAuth) ||
		errors.Is(providerErr, agentcore.ErrProviderNetwork) ||
		errors.Is(providerErr, agentcore.ErrProviderOverloaded)
	return classified || errorKind(err, err.Error()) == "overloaded"
}

func instructionKey(inst *flow.Instruction) string {
	if inst == nil {
		return ""
	}
	return inst.Agent + "\x00" + inst.Task
}

func (e *engine) rememberWorkerError(inst *flow.Instruction, workerErr error) {
	if workerErr == nil || inst == nil {
		e.lastWorkerErrorKey, e.lastWorkerError = "", nil
		return
	}
	e.lastWorkerErrorKey, e.lastWorkerError = instructionKey(inst), workerErr
}

func (e *engine) workerErrorFor(inst *flow.Instruction) error {
	if e.lastWorkerErrorKey != instructionKey(inst) {
		return nil
	}
	return e.lastWorkerError
}

// contentFilterAdvice attaches an actionable exit to a pause caused by a content-moderation block.
// Moderation is a provider black box, so pre-checking and evading are both infeasible and the only thing
// left is to hand the decision to the user; the block itself is not fused early - re-dispatching with a
// different context has a real self-healing rate for it (measured on ch21-24), so it goes through "free retry -> arbitration" before pausing.
func contentFilterAdvice(werr error) string {
	if !errors.Is(werr, agentcore.ErrProviderContentFilter) {
		return ""
	}
	return "。这是服务商内容审核拦截(非本地错误),可选: /model 切到无审核层的服务商后输入「继续」;或修改本章草稿(drafts/)措辞后再继续;原样重试大概率仍被拦"
}

// errInvalidWriteTarget marks an illegal write target rejected by runWorker's pre-check, so the error chain and
// the Arbiter facts keep a stable meaning; whether to retry or reassign is still decided by the unified failure flow.
var errInvalidWriteTarget = errors.New("非法写作目标")

func (e *engine) failureFacts(kind string, inst *flow.Instruction, workerErr error) arbiter.FailureFacts {
	f := arbiter.FailureFacts{Kind: kind, Agent: inst.Agent, Task: inst.Task, Repeats: e.repeats}
	if workerErr != nil {
		f.Error = workerErr.Error()
		f.ErrorKind = errorKind(workerErr, f.Error)
		if f.ErrorKind == "" {
			f.ErrorKind = "unknown"
		}
	}
	missing, err := e.store.FoundationMissing()
	if err != nil {
		f.FactWarnings = append(f.FactWarnings, "基础设定状态读取失败: "+err.Error())
	} else {
		f.FoundationGap = missing
	}
	p, err := e.store.Progress.Load()
	if err != nil {
		f.FactWarnings = append(f.FactWarnings, "创作进度读取失败: "+err.Error())
	}
	if p != nil {
		f.Phase = string(p.Phase)
		f.NextChapter = p.NextChapter()
		f.PendingQueue = p.PendingRewrites
	}
	return f
}

func (e *engine) recordFailureDecision(kind string, inst *flow.Instruction, facts arbiter.FailureFacts, d arbiter.FailureDecision, derr error) {
	rec := storepkg.DecisionRecord{Kind: kind, Decider: "arbiter", Input: inst.Agent + ": " + inst.Task, Reason: d.Reason}
	if data, err := json.Marshal(facts); err == nil {
		rec.Facts = data
	}
	if derr == nil {
		if data, err := json.Marshal(d); err == nil {
			rec.Decision = data
		}
	} else {
		rec.Error = derr.Error()
	}
	if _, err := e.store.Decisions.Append(rec); err != nil {
		slog.Warn("裁定审计落盘失败", "module", "engine", "kind", kind, "err", err)
	}
}

// applyPendingOps commits the steer's control-state actions at a loop boundary; the loop is drained here - a
// synchronous re-consult (reconsult) appends new actions while they are being applied, and those must be
// consumed within this boundary, otherwise an extra worker would be dispatched in between (a steer must take
// effect before any subsequent writing). It reports whether a hold+dispatch paired dispatch must be executed first; in that case the caller defers the Gate check.
func (e *engine) applyPendingOps(ctx context.Context) (deferGate bool) {
	for {
		e.mu.Lock()
		ops := e.pending
		e.pending = nil
		e.mu.Unlock()
		if len(ops) == 0 {
			return deferGate
		}
		for _, op := range ops {
			pairedHoldDispatch := op.hold != nil && !op.hold.Cancel && op.dispatch != nil
			err := e.applyControlOp(ctx, op)
			if err != nil {
				// action persistence failed: the host already cleared PendingSteer as "enqueued", so
				// the whole steer is stored again here and re-verdict / retried on resume or continue (the actions are idempotent and the re-consult uses the new facts).
				if op.text != "" {
					if serr := e.store.RunMeta.SetPendingSteer(op.text); serr != nil {
						slog.Warn("Không lưu lại được can thiệp", "module", "engine", "err", serr)
					}
				}
				e.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Level: "warn",
					Summary: "Thực thi hành động can thiệp thất bại, đã giữ lại; sẽ tự thử lại khi khôi phục/tiếp tục"})
			} else if pairedHoldDispatch && e.nextDefersGate() {
				// only when both the hold and the paired dispatch land successfully may this Gate check be bypassed.
				// Bypassing it when the hold write fails, or when the dispatch is dropped because the facts went
				// stale, would let an unprotected Worker move forward.
				deferGate = true
			}
		}
	}
}

// applyControlOp executes one control-state action (hold writes RunMeta directly, reopen calls the tool kernel, dispatch reconciles first).
// When the engine is not running the host calls this directly on the steer path; it returns the first
// persistence failure (the caller decides from it whether to keep PendingSteer for a replay on resume).
func (e *engine) applyControlOp(ctx context.Context, op controlOp) error {
	var firstErr error
	fail := func(err error) {
		if firstErr == nil {
			firstErr = err
		}
	}
	if op.dispatch != nil {
		// Expect must be verified before the hold and the other paired actions are written. Otherwise an old
		// hold would remain after the dispatch went stale and would conflict with the hold re-verdicted from the new facts, ending in a pause that misses the modification.
		fresh, err := arbiter.CollectInterventionFacts(e.store)
		if err != nil {
			return fmt.Errorf("Làm mới dữ kiện can thiệp thất bại: %w", err)
		}
		if fresh.Phase != op.facts.Phase || fresh.Flow != op.facts.Flow ||
			fresh.QueueHead() != op.facts.QueueHead() {
			e.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Level: "warn",
				Summary: "Phát việc theo định đoạn đã lỗi thời (dữ kiện đã tiến), sẽ định đoạn lại theo dữ kiện mới nhất"})
			e.recordStale(op)
			if op.text != "" && e.reconsult != nil {
				// synchronous re-consult: a steer must take effect before any subsequent writing - going
				// asynchronous would let the engine dispatch another worker before the new verdict lands. New actions are drained at this boundary by applyPendingOps.
				e.reconsult(op.text)
			}
			return nil
		}
	}
	if op.hold != nil {
		if op.hold.Cancel {
			meta, err := e.store.RunMeta.Load()
			if err != nil {
				e.emitEvent(Event{Time: time.Now(), Category: "ERROR", Summary: "Đọc trạng thái tạm dừng một lần thất bại: " + err.Error(), Level: "error"})
				return err
			}
			if meta != nil && meta.AdvanceHold != nil {
				if err := e.store.RunMeta.ClearAdvanceHold(*meta.AdvanceHold); err != nil {
					e.emitEvent(Event{Time: time.Now(), Category: "ERROR", Summary: "Hủy tạm dừng một lần thất bại: " + err.Error(), Level: "error"})
					return err
				}
			}
			e.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Summary: "Đã hủy tạm dừng một lần", Level: "info"})
		} else {
			hold := domain.AdvanceHold{After: op.hold.After, TargetChapter: op.hold.TargetChapter, Reason: op.hold.Reason}
			if err := e.store.RunMeta.SetAdvanceHold(hold); err != nil {
				e.emitEvent(Event{Time: time.Now(), Category: "ERROR", Summary: "Đặt tạm dừng một lần thất bại: " + err.Error(), Level: "error"})
				return err // the paired dispatch must not run while the hold is not persisted
			}
			e.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Summary: "Đã đặt tạm dừng một lần: " + op.hold.Reason, Level: "info"})
		}
	}
	if op.reopen != nil {
		if err := tools.ReopenBook(e.store, op.reopen.Chapters, op.reopen.Reason); err != nil {
			e.emitEvent(Event{Time: time.Now(), Category: "ERROR", Summary: "Mở lại để viết lại thất bại: " + err.Error(), Level: "error"})
			fail(err)
		} else {
			e.emitEvent(Event{Time: time.Now(), Category: "SYSTEM",
				Summary: fmt.Sprintf("Đã mở lại toàn bộ để viết lại: chương %v đã vào hàng đợi", op.reopen.Chapters), Level: "info"})
		}
	}
	if op.dispatch != nil {
		// Expect has already been verified before any paired state write. CheckpointSeq is only kept for the
		// audit and takes no part in reconciliation: by the time a steer arrives the worker is usually still running, so the seq has almost certainly advanced.
		e.mu.Lock()
		// known window (best-effort boundary, see clarification ③ in engine-arbiter.md): the dispatch lives in
		// memory from here on, so a hard kill before the worker starts (kill -9, defer not executed) loses
		// this dispatch intent - a normal exit / Abort is covered by the defer in run, which stores PendingSteer back.
		e.next = &flow.Instruction{Agent: op.dispatch.Agent, Task: interventionDispatchTask(op.dispatch.Task, op.text), Reason: "Định đoạn can thiệp người dùng"}
		e.deferGateForNext = op.hold != nil && !op.hold.Cancel
		e.mu.Unlock()
	}
	return firstErr
}

// interventionDispatchTask keeps the user's original steer so the Arbiter cannot accidentally widen the
// modification target while paraphrasing the task. Downstream code may read broader context, but only the original text is authorised as the action source.
func interventionDispatchTask(task, original string) string {
	task = strings.TrimSpace(task)
	if strings.TrimSpace(original) == "" {
		return task
	}
	return task + "\n\n用户原始干预（本次修改授权的唯一来源；上下文只用于理解，不得扩大目标或范围）：\n" + original
}

func (e *engine) recordStale(op controlOp) {
	rec := storepkg.DecisionRecord{Kind: "decision_stale", Decider: "engine", Input: op.text}
	if data, err := json.Marshal(op.facts); err == nil {
		rec.Facts = data
	}
	if _, err := e.store.Decisions.Append(rec); err != nil {
		slog.Warn("stale 记录失败", "module", "engine", "err", err)
	}
}

// pauseWithNotify is the engine's self-pause (deadlock fuse / failure verdict abort): an off-screen notification plus the
// host's unified pause semantics (onPause -> abortWithEvent: lifecycle=paused + an on-screen event + cancel ctx).
func (e *engine) pauseWithNotify(kind, body string) {
	e.notify(kind, "warn", buildversion.AppName+": 引擎暂停", body)
	if e.onPause != nil {
		e.onPause(body)
		return
	}
	e.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Summary: body, Level: "warn"})
	e.abort()
}

// completionSummary is the deterministic wrap-up report for finishing a book, spending no LLM call.
func completionSummary(progress domain.Progress, book domain.BookMetadata) string {
	var b strings.Builder
	fmt.Fprintf(&b, "《%s》创作完成: 共 %d 章 %d 字", book.Title, len(progress.CompletedChapters), progress.TotalWordCount)
	return b.String()
}
