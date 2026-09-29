package imp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/CTKiet2006/kietnovel/internal/domain"
	"github.com/CTKiet2006/kietnovel/internal/logger"
	"github.com/CTKiet2006/kietnovel/internal/store"
	"github.com/voocel/agentcore"
)

// Each prompt/schema version is folded into its stage's InputDigest; bump it when upgrading a prompt contract so downstream artifacts are naturally invalidated.
const (
	segmentPromptVersion = "seg-v2" // v2: boundaries land only at real separators, titles are copied verbatim (paired with the title-echo validation)
	analyzePromptVersion = "analyze-v1"
	confirmMethodAuto    = "auto_authorized"
	confirmMethodUser    = "user_confirmed" // explicit human confirmation by pressing y after the TUI preview
)

// Prompts holds the system prompts of the semantic functions. Synthesis has two stages: Synthesize produces the whole-book BookSynthesis,
// Range produces a consecutive range RangeDigest for a long book; the two have different output structures and must each use their own prompt.
type Prompts struct {
	Segment    string
	Analyze    string
	Synthesize string
	Range      string
}

// RunBudgets holds the input/output budgets of the semantic functions. The first version uses conservative constants;
// in the future they should be derived from the current architect model's context window / completion cap so batches scale naturally with capability (RFC §9.2/§21).
type RunBudgets struct {
	MaxUnitBytes         int
	SegmentChunkBytes    int
	SegmentContextMargin int
	SegmentMaxTokens     int
	Analyze              AnalyzeBudget
	SynthesizeRangeBytes int
	SynthesizeMaxTokens  int
}

// DefaultRunBudgets returns the conservative default budgets, used as a fallback when model capability is unknown (probing failed).
func DefaultRunBudgets() RunBudgets {
	return RunBudgets{
		MaxUnitBytes:         8000,
		SegmentChunkBytes:    24000,
		SegmentContextMargin: 20,
		SegmentMaxTokens:     8192,
		Analyze:              AnalyzeBudget{ContextBytes: 24000, MaxOutputTokens: 8000, PerChapterOutput: 900, PromptOverhead: 2000},
		SynthesizeRangeBytes: 16000,
		SynthesizeMaxTokens:  8192,
	}
}

// ModelRuntime carries the model capability facts the imp semantic calls need, injected by the Host after boundary probing (RFC §13/§17).
// It lets the budget pair scale naturally with context/completion and lets thinking be sent according to capability; on an all-zero value it falls back to the
// conservative defaults, behaving exactly as before capability was wired in. Structured output does not send response_format based on provider capability (see the callProfile comment).
type ModelRuntime struct {
	ContextTokens   int                     // input context cap (tokens)
	MaxOutputTokens int                     // visible output cap for a single call (tokens)
	Thinking        agentcore.ThinkingLevel // already resolved by capability; ThinkingAuto("") means it is not sent explicitly
}

// profile derives this runtime's call capability options (thinking).
func (rt ModelRuntime) profile() callProfile {
	return callProfile{thinking: rt.Thinking}
}

// Caller is one semantic function's model tier: the model + that model's capability facts (RFC §13.1/§17).
// segment/analyze/synthesize each hold their own tier, and both the budgets and the call options are derived per tier,
// so a cheap tier's small window constrains only its own function and never holds back the other stages.
type Caller struct {
	Model   callModel
	Runtime ModelRuntime
}

// budgetsFromRuntime derives the semantic function budgets from the model's real context/completion caps (RFC §9.2/§21).
// Only this makes "switching to a stronger model automatically enlarges batches and reduces the call count" true; when capability is unknown it falls back to the conservative defaults.
func budgetsFromRuntime(rt ModelRuntime) RunBudgets {
	if rt.ContextTokens <= 0 || rt.MaxOutputTokens <= 0 {
		return DefaultRunBudgets()
	}
	const bytesPerToken = 3 // conservative CJK UTF-8 conversion: token->byte (underestimating capacity is the safer error)
	out := rt.MaxOutputTokens
	// Input budget: from the context window, subtract the visible output and a ~10% reasoning/system reserve, then convert to bytes.
	reserve := rt.ContextTokens / 10
	inTokens := rt.ContextTokens - out - reserve
	if inTokens < 2000 {
		inTokens = 2000
	}
	inBytes := inTokens * bytesPerToken
	return RunBudgets{
		MaxUnitBytes:         min(inBytes/2, 32000),
		SegmentChunkBytes:    inBytes,
		SegmentContextMargin: 20,
		SegmentMaxTokens:     out,
		Analyze: AnalyzeBudget{
			ContextBytes:     inBytes,
			MaxOutputTokens:  out,
			PerChapterOutput: 900,
			PromptOverhead:   2000,
		},
		SynthesizeRangeBytes: inBytes,
		SynthesizeMaxTokens:  out,
	}
}

// Confirmation is the segmentation confirmation artifact, bound to the current segmentation (RFC §8.4).
type Confirmation struct {
	Method   string `json:"method"`
	Chapters int    `json:"chapters"`
}

// StoryResolution is the user's verdict on an uncertain story status, bound to the current synthesis (RFC §10.4).
type StoryResolution struct {
	Choice string `json:"choice"` // open / closed
}

// Deps are the runner's narrow dependencies (RFC §17). Each of the three semantic functions declares its own model tier;
// the Host defaults them all to architect, and the config layer may point the more mechanical functions at a cheaper tier (RFC §13.1).
type Deps struct {
	Store         *store.Store
	CommitChapter ChapterCommitter
	Segment       Caller
	Analyze       Caller
	Synthesize    Caller // the range digest and the book synthesis share the same tier (one synthesis stage)
	Prompts       Prompts
	Budgets       RunBudgets
}

// budgetsFromDeps derives budgets from the capability of each semantic function's own tier (RFC §9.2/§13.1).
func budgetsFromDeps(d Deps) RunBudgets {
	seg := budgetsFromRuntime(d.Segment.Runtime)
	ana := budgetsFromRuntime(d.Analyze.Runtime)
	syn := budgetsFromRuntime(d.Synthesize.Runtime)
	return RunBudgets{
		MaxUnitBytes:         seg.MaxUnitBytes,
		SegmentChunkBytes:    seg.SegmentChunkBytes,
		SegmentContextMargin: seg.SegmentContextMargin,
		SegmentMaxTokens:     seg.SegmentMaxTokens,
		Analyze:              ana.Analyze,
		SynthesizeRangeBytes: syn.SynthesizeRangeBytes,
		SynthesizeMaxTokens:  syn.SynthesizeMaxTokens,
	}
}

// Run executes the full import pipeline: LoadState -> NextAction -> perform one action -> re-read the facts.
// It runs in its own goroutine; the returned event channel is closed by this function.
func Run(ctx context.Context, deps Deps, opts Options) (<-chan Event, error) {
	if deps.Store == nil || deps.CommitChapter == nil ||
		deps.Segment.Model == nil || deps.Analyze.Model == nil || deps.Synthesize.Model == nil {
		return nil, fmt.Errorf("deps 不完整")
	}
	if deps.Budgets == (RunBudgets{}) {
		deps.Budgets = budgetsFromDeps(deps)
	}
	// The import pipeline log lives in its own file: the complete transcript of one import (events, retries, full error chains) does not mingle with
	// the engine/TUI logs, so troubleshooting only has to look at this one file. A creation failure must be echoed -- the panel
	// points the user at logs/import.log, and a silent fallback would point at a file that does not exist (Debug-First).
	log, closeLog, logErr := logger.FileLogger(deps.Store.Dir(), "import.log")
	log.Info("imp 导入模型运行时",
		"segment_ctx", deps.Segment.Runtime.ContextTokens,
		"analyze_ctx", deps.Analyze.Runtime.ContextTokens,
		"synthesize_ctx", deps.Synthesize.Runtime.ContextTokens,
		"analyze_max_output", deps.Analyze.Runtime.MaxOutputTokens,
		"analyze_context_bytes", deps.Budgets.Analyze.ContextBytes)
	events := make(chan Event, 32)
	go func() {
		defer close(events)
		defer closeLog()
		r := &runner{deps: deps, opts: opts, events: events, ws: OpenWorkspace(deps.Store.Dir()), log: log}
		if logErr != nil {
			r.emit(StageIngesting, 0, 0, fmt.Sprintf("导入日志文件创建失败（%v），本次转录改走默认日志", logErr), nil)
		}
		r.run(ctx)
	}()
	return events, nil
}

type runner struct {
	deps   Deps
	opts   Options
	events chan Event
	ws     *Workspace
	act    Action       // the action currently being executed, so a failed artifact can be tagged with its stage
	log    *slog.Logger // import-specific log (logs/import.log); falls back to the default logger when nil
}

func (r *runner) emit(stage Stage, current, total int, msg string, err error) {
	r.send(Event{Time: time.Now(), Stage: stage, Current: current, Total: total, Message: msg, Err: err})
}

func (r *runner) send(ev Event) {
	r.logEvent(ev)
	// Terminal-state and stop-point events carry the only success/failure and action-required signals (losing a confirmation preview or a --story prompt leaves the user with no idea what to do),
	// so they must be delivered reliably; only intermediate progress events may be dropped when they pile up.
	if ev.Stage == StageError || ev.Stage == StageDone ||
		ev.Stage == StageAwaitingConfirmation || ev.Stage == StageAwaitingStoryStatus {
		r.events <- ev
		return
	}
	select {
	case r.events <- ev:
	default: // drop progress when the channel is full, never block the pipeline
	}
}

// logEvent transcribes every progress event into the import-specific log (<book root>/logs/import.log): the panel's retry line is overwritten in place and
// the panel vanishes on Esc, so the log is the only complete pipeline record available for after-the-fact troubleshooting (§14.1).
func (r *runner) logEvent(ev Event) {
	log := r.log
	if log == nil {
		log = slog.Default()
	}
	args := []any{"stage", string(ev.Stage)}
	if ev.Total > 0 {
		args = append(args, "progress", fmt.Sprintf("%d/%d", ev.Current, ev.Total))
	}
	if ev.Err != nil {
		args = append(args, "err", ev.Err)
	}
	level := slog.LevelInfo
	switch {
	case ev.Stage == StageError:
		level = slog.LevelError // the failure terminal state is the one most worth filtering out of the log, it must not land as INFO
	case ev.Level == "warn":
		level = slog.LevelWarn
	}
	log.Log(context.Background(), level, ev.Message, args...)
}

func (r *runner) fail(msg string, err error) {
	r.saveFailure(err)
	r.emit(StageError, 0, 0, msg, err)
}

// saveFailure uniformly persists a failure that carries a raw response into failures/ (the third landing point of RFC §14.2),
// a fallback shared by every semantic function such as segment/synthesize; the analysis truncation salvage path already writes finer metadata in place.
// Failures with no raw response (IO, cancellation, pre-validation) have no model output to save, so nothing is written.
func (r *runner) saveFailure(err error) {
	var se *errSemantic
	var tr *errTruncated
	switch {
	case errors.As(err, &se):
		r.ws.writeFailure(FailureMeta{Stage: string(r.act), Detail: err.Error()}, se.Raw)
	case errors.As(err, &tr):
		r.ws.writeFailure(FailureMeta{Stage: string(r.act), Detail: err.Error(), StopReason: "length"}, tr.Raw)
	}
}

// facts combines the workspace facts with the official publish reconciliation.
func (r *runner) facts() (Facts, error) {
	return CollectFacts(r.deps.Store, r.ws)
}

// profileFor derives a tier's call options and echoes request backoff / validation re-asks into that stage's event stream --
// a retry backoff can silently accumulate past 2 minutes, and without an echo the user would think it hung (§14.1).
// Key is given only to request backoff (with the deadline): it is transient state within one call and the UI updates a single line in place (the "attempt N" text changing).
// A validation re-ask is a cross-call semantic event -- segmentation calls chunk by chunk and each chunk re-asks independently, so sharing a Key would let a later chunk
// overwrite an earlier one and swallow the troubleshooting lead (in practice the panel showed a single row whose unit_id kept changing), so each gets its own line to preserve history.
func (r *runner) profileFor(c Caller, stage Stage) callProfile {
	prof := c.Runtime.profile()
	prof.log = r.log
	prof.notify = func(msg string, retryAt time.Time) {
		ev := Event{Time: time.Now(), Stage: stage, Message: msg, Level: "warn", RetryAt: retryAt}
		if !retryAt.IsZero() {
			ev.Key = "retry:" + string(stage)
		}
		r.send(ev)
	}
	prof.progress = func(current, total int, msg string) {
		r.send(Event{Time: time.Now(), Stage: stage, Current: current, Total: total, Message: msg})
	}
	return prof
}

// applyGuidance persists this run's explicit --guide guidance as a workspace semantic input (RFC §18.3).
// Guidance is one of the inputs to the segmentation InputDigest: a content change naturally mismatches the old segmentation and all of its downstream and forces a redo,
// so no manual invalidation rule is written. It is skipped while the workspace does not exist yet and written on the next loop iteration after ingest.
func (r *runner) applyGuidance() error {
	g := strings.TrimSpace(r.opts.Guidance)
	if g == "" || !r.ws.Active() {
		return nil
	}
	existing, err := r.ws.LoadGuidance()
	if err != nil {
		return fmt.Errorf("读取已有切分指导: %w", err)
	}
	if existing == g {
		return nil
	}
	// Once publishing has started, official artifacts cannot be overwritten (§12.2): re-segmenting at that point is bound to slam into publish's "refuse to overwrite" wall,
	// and before hitting that wall it would first re-pay the full chain of segmentation/analysis/synthesis model calls -- so the failure is moved forward to a zero-cost point.
	// book is the first write of publishing, so its existence means publishing has begun (import pre-validation guarantees the book started out empty).
	book, err := r.deps.Store.Book.Load()
	if err != nil {
		return fmt.Errorf("读取正式 book: %w", err)
	}
	if book != nil {
		return fmt.Errorf("正式 Foundation 已开始发布，--guide 重切会与已发布内容冲突而被拒绝覆盖，不再接受切分指导")
	}
	return r.ws.writeAtomic(fileGuidance, []byte(g))
}

// checkSourceIdentity blocks "a different source file is passed while a workspace is in progress": ingest only runs when there is no workspace,
// without a comparison, /import B.txt would silently resume from A's checkpoint, publish A to completion and not read a single byte of B (RFC §12.1/§18.2).
// Passing the same path repeatedly is a common habit (/import with the same path to resume), so compare by content digest rather than rejecting every repeated path.
func (r *runner) checkSourceIdentity() error {
	if r.opts.SourcePath == "" || !r.ws.Active() {
		return nil
	}
	m, err := r.ws.LoadManifest()
	if err != nil {
		return nil // an unreadable identity trio goes through the corruption diagnostic of ingest, do not report the error twice here
	}
	raw, err := os.ReadFile(r.opts.SourcePath)
	if err != nil {
		return fmt.Errorf("读取源文件 %s：%w", r.opts.SourcePath, err)
	}
	if Digest(raw) != m.RawSHA256 {
		return fmt.Errorf("已有 %q 的导入在进行中，本次源文件与其内容不同：请先完成或放弃旧导入（删除 meta/import/）再导入新书", m.SourceName)
	}
	return nil
}

func (r *runner) run(ctx context.Context) {
	if err := r.checkSourceIdentity(); err != nil {
		r.fail("校验源文件身份", err)
		return
	}
	var previous *Facts
	for {
		if ctx.Err() != nil {
			r.fail("用户取消", ctx.Err())
			return
		}
		if err := r.applyGuidance(); err != nil {
			r.fail("写入切分指导", err)
			return
		}
		facts, err := r.facts()
		if err != nil {
			r.fail("读取导入状态", err)
			return
		}
		if previous != nil && facts == *previous {
			r.fail("导入停滞", fmt.Errorf("动作执行后事实没有变化，下一动作仍为 %q", NextAction(facts)))
			return
		}
		snapshot := facts
		previous = &snapshot
		act := NextAction(facts)
		r.act = act
		err = nil
		switch act {
		case ActionIngest:
			err = r.ingest(ctx)
		case ActionSegment:
			err = r.segment(ctx)
		case ActionAwaitConfirmation:
			if !r.confirm() {
				return // interactive mode: wait for the user to confirm, stop here
			}
		case ActionAnalyze:
			err = r.analyze(ctx)
		case ActionSynthesize:
			err = r.synthesize(ctx)
		case ActionAwaitStoryResolution:
			if !r.resolveStoryStatus() {
				return // no explicit verdict: stop here and wait for --story=open|closed
			}
		case ActionPublish:
			err = r.publish(ctx)
		case ActionDone:
			r.emit(StageDone, 0, 0, "导入完成，等待验收后续写", nil)
			return
		default:
			err = fmt.Errorf("未知动作 %q", act)
		}
		if err != nil {
			r.fail("导入失败", err)
			return
		}
	}
}

func (r *runner) ingest(ctx context.Context) error {
	// Reaching ingest while the directory already exists means the identity trio (manifest/source/intent) is missing or corrupt:
	// createWorkspace refuses with "already exists (recover with a bare /import)", while rerunning bare in turn
	// fails because WorkspaceReady=false sends it back here demanding a source path -- the two messages contradict each other and the user is left with no way forward.
	if r.ws.Active() {
		return fmt.Errorf("meta/import/ 已存在但工作区身份不可用（manifest/source/intent 缺失或损坏），请人工确认后删除该目录再重新导入")
	}
	if err := checkImportPreconditions(r.deps.Store); err != nil {
		return err
	}
	if r.opts.SourcePath == "" {
		return fmt.Errorf("新导入需要源文件路径")
	}
	r.emit(StageIngesting, 0, 0, "读取、解码、归一化并快照源文件...", nil)
	_, m, err := Ingest(r.deps.Store.Dir(), r.opts.SourcePath, r.opts.intent())
	if err != nil {
		return err
	}
	r.emit(StageIngesting, 0, 0, fmt.Sprintf("源快照就绪：%s（编码 %s，%d 字节）", m.SourceName, m.Encoding, m.SizeBytes), nil)
	return nil
}

func (r *runner) segment(ctx context.Context) error {
	src, err := r.ws.LoadSource()
	if err != nil {
		return err
	}
	units := buildSourceUnits(src, r.deps.Budgets.MaxUnitBytes)
	guidance, err := r.ws.LoadGuidance()
	if err != nil {
		return fmt.Errorf("读取切分指导: %w", err)
	}
	r.emit(StageSegmenting, 0, 0, fmt.Sprintf("语义识别章节边界（%d 个坐标单元）...", len(units)), nil)
	digest := segmentInputDigest(Digest(src), guidance, segmentPromptVersion)
	// The chunk cache identity additionally binds MaxUnitBytes: the unit table is uniquely determined by (normalized source, MaxUnitBytes), so switching model
	// tier and changing MaxUnitBytes reshapes the virtual splits of over-long lines -- the ID sequence (L1.1...) and the chunk endpoints are reproducible but the byte
	// ranges have changed, so matching on endpoint IDs alone would reuse misaligned old boundaries (either a deterministic anchor mismatch failure or a silent mis-segmentation).
	chunkIdentity := fmt.Sprintf("%s\x00units:%d", digest, r.deps.Budgets.MaxUnitBytes)
	seg, err := Segment(ctx, r.deps.Segment.Model, r.deps.Prompts.Segment, src, units, guidance,
		r.deps.Budgets.SegmentChunkBytes, r.deps.Budgets.SegmentContextMargin, r.deps.Budgets.SegmentMaxTokens,
		r.profileFor(r.deps.Segment, StageSegmenting), r.ws, chunkIdentity)
	if err != nil {
		return err
	}
	if err := writeArtifact(r.ws, fileSegmentation, digest, *seg); err != nil {
		return err
	}
	// The final segmentation is on disk and the chunk-level cache has served its purpose; a cleanup failure does not harm correctness (the digest still agrees) but must leave a trace.
	if cerr := r.ws.clearDir(dirSegmentChunks); cerr != nil {
		r.emit(StageSegmenting, 0, 0, fmt.Sprintf("块级缓存清理失败（不影响切分结果）：%v", cerr), nil)
	}
	r.emit(StageSegmenting, len(seg.Chapters), len(seg.Chapters),
		fmt.Sprintf("切分完成：%d 章、%d 个附属区域", len(seg.Chapters), len(seg.Matter)), nil)
	return nil
}

// confirm handles segmentation confirmation. --yes accepts automatically and writes the confirmation artifact; otherwise it shows the preview and stops.
func (r *runner) confirm() bool {
	seg, err := readArtifact[Segmentation](r.ws, fileSegmentation)
	if err != nil {
		r.fail("读取切分结果", err)
		return false
	}
	in, err := r.ws.LoadIntent()
	if err != nil {
		r.fail("读取导入意图", err)
		return false
	}
	accept := r.opts.AcceptSegmentation
	auto := r.opts.AutoConfirm || (in != nil && in.AutoConfirm)
	// A segmentation where semantic tolerance kicked in (Notes non-empty: empty-chapter absorption / leading fallback / overlap dedup) is not blindly waved through by --yes:
	// the structure was deterministically rewritten, so it must be reviewed by a human -- otherwise the tolerance notes go unseen under --yes, which amounts to a silent rewrite.
	// Pressing y after the TUI preview goes through AcceptSegmentation (an explicit verdict made after viewing the preview) and is exempt from this.
	blockedByNotes := auto && !accept && len(seg.Payload.Notes) > 0
	if blockedByNotes {
		auto = false
	}
	if !auto && !accept {
		msg := buildConfirmPreview(&seg.Payload)
		if blockedByNotes {
			msg += "  ! 存在切分容错说明，--yes 未自动放行，请人工核对\n"
		}
		r.emit(StageAwaitingConfirmation, len(seg.Payload.Chapters), len(seg.Payload.Chapters), msg, nil)
		return false
	}
	raw, err := r.ws.readBytes(fileSegmentation)
	if err != nil {
		r.fail("读取切分工件", err)
		return false
	}
	method, doneMsg := confirmMethodAuto, "已自动接受切分（--yes）"
	if accept {
		method, doneMsg = confirmMethodUser, "已确认切分（人工核对）"
	}
	conf := Confirmation{Method: method, Chapters: len(seg.Payload.Chapters)}
	if err := writeArtifact(r.ws, fileConfirmation, Digest(raw), conf); err != nil {
		r.fail("写确认工件", err)
		return false
	}
	r.emit(StageAwaitingConfirmation, len(seg.Payload.Chapters), len(seg.Payload.Chapters), doneMsg, nil)
	return true
}

// buildConfirmPreview assembles the segmentation confirmation preview: chapter count, ancillary regions, all chapter titles and uncertain markers (RFC §8.4).
// Everything is listed in full so the panel viewport can scroll through it; no truncation cap is set.
func buildConfirmPreview(seg *Segmentation) string {
	var b strings.Builder
	fmt.Fprintf(&b, "已切分 %d 章", len(seg.Chapters))
	if len(seg.Matter) > 0 {
		fmt.Fprintf(&b, "、%d 个附属区域", len(seg.Matter))
	}
	if len(seg.Uncertain) > 0 {
		fmt.Fprintf(&b, "（%d 章存疑）", len(seg.Uncertain))
	}
	b.WriteString("，请核对：\n")
	uncertain := make(map[int]bool, len(seg.Uncertain))
	for _, n := range seg.Uncertain {
		uncertain[n] = true
	}
	for _, c := range seg.Chapters {
		fmt.Fprintf(&b, "  第%d章 %s", c.Number, c.Title)
		if uncertain[c.Number] {
			b.WriteString("  [存疑]")
		}
		b.WriteByte('\n')
	}
	for _, mt := range seg.Matter {
		fmt.Fprintf(&b, "  [%s] %s\n", mt.Kind, mt.Title)
	}
	// Tolerance notes from segmentation (such as a placeholder title with an empty body being merged into the previous segment) must surface at the human stop point, otherwise the absorption becomes a silent rewrite.
	for _, n := range seg.Notes {
		fmt.Fprintf(&b, "  ! %s\n", n)
	}
	// The action hints (y to confirm / --guide to re-segment / Esc) are rendered uniformly by the TUI pause block; only facts are kept here, to avoid two copies of the wording drifting apart.
	return b.String()
}

func (r *runner) analyze(ctx context.Context) error {
	src, err := r.ws.LoadSource()
	if err != nil {
		return err
	}
	segArt, err := readArtifact[Segmentation](r.ws, fileSegmentation)
	if err != nil {
		return err
	}
	seg := &segArt.Payload
	total := len(seg.Chapters)
	// A per-chapter digest binds only that chapter's body, with no batch context and no earlier ledger. If chapter K needs re-analysis because it is missing or mismatched,
	// the old artifacts after it whose digests happen to match would be reused carrying an already invalidated ledger. Before starting analysis, clear the tail that runs past the fresh prefix,
	// forcing "re-analyzing one chapter invalidates every analysis after it", after which forward analysis no longer produces a stale tail (RFC §9.6 / #4a).
	if err := discardAnalysesAfter(r.ws, analyzedChapters(r.ws, seg, src, segArt.InputDigest, analyzePromptVersion), total); err != nil {
		return err
	}
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		start := analyzedChapters(r.ws, seg, src, segArt.InputDigest, analyzePromptVersion)
		if start >= total {
			break
		}
		r.emit(StageAnalyzing, start, total, fmt.Sprintf("分析第 %d 章起的连续批次...", start+1), nil)
		done, err := AnalyzeNext(ctx, r.deps.Analyze.Model, r.deps.Prompts.Analyze, r.ws, src, seg, segArt.InputDigest, analyzePromptVersion, r.deps.Budgets.Analyze, r.profileFor(r.deps.Analyze, StageAnalyzing))
		if err != nil {
			return err
		}
		if done == 0 {
			break
		}
	}
	r.emit(StageAnalyzing, total, total, "逐章事实提取完成", nil)
	return nil
}

func (r *runner) synthesize(ctx context.Context) error {
	segArt, err := readArtifact[Segmentation](r.ws, fileSegmentation)
	if err != nil {
		return err
	}
	total := len(segArt.Payload.Chapters)
	facts := loadPriorFacts(r.ws, total)
	if len(facts) != total {
		return fmt.Errorf("逐章分析不完整：%d/%d", len(facts), total)
	}
	r.emit(StageSynthesizing, 0, total, "分层归纳全书语义...", nil)
	syn, err := Synthesize(ctx, r.deps.Synthesize.Model, r.deps.Prompts.Synthesize, r.deps.Prompts.Range, r.ws, facts,
		r.deps.Budgets.SynthesizeRangeBytes, r.deps.Budgets.SynthesizeMaxTokens, r.profileFor(r.deps.Synthesize, StageSynthesizing))
	if err != nil {
		return err
	}
	if err := writeArtifact(r.ws, fileSynthesis, synthesisInputDigest(facts), *syn); err != nil {
		return err
	}
	r.emit(StageSynthesizing, total, total, fmt.Sprintf("综合完成：%d 卷、故事状态 %s", len(syn.Structure), syn.StoryStatus), nil)
	return nil
}

func (r *runner) publish(ctx context.Context) error {
	synArt, err := readArtifact[BookSynthesis](r.ws, fileSynthesis)
	if err != nil {
		return err
	}
	segArt, err := readArtifact[Segmentation](r.ws, fileSegmentation)
	if err != nil {
		return err
	}
	seg := &segArt.Payload
	src, err := r.ws.LoadSource()
	if err != nil {
		return err
	}
	total := len(seg.Chapters)
	facts := loadPriorFacts(r.ws, total)
	if len(facts) != total {
		return fmt.Errorf("发布前分析不完整：%d/%d", len(facts), total)
	}
	closed, err := r.resolveStory(&synArt.Payload)
	if err != nil {
		return err
	}
	manifest, err := r.ws.LoadManifest()
	if err != nil {
		return err
	}
	f, err := AssembleFoundation(&synArt.Payload, facts, closed, manifest.SourceName)
	if err != nil {
		return err
	}
	r.emit(StageValidating, 0, total, "Foundation 组装校验通过", nil)

	r.emit(StagePublishing, 0, total, "发布正式 Foundation...", nil)
	if err := publishFoundation(r.deps.Store, f); err != nil {
		return err
	}
	// The import-complete Hold must be persisted before any chapter commit: if a crash happens between "commit the last chapter" and "set the Hold",
	// then after the restart isPublished=true -> the import is judged complete yet the Hold was never set, and the Engine mistakes the imported book for an ordinary one to continue writing.
	// It is placed after publishFoundation (which has initialized RunMeta) and before the chapter commits, closing that window completely; when publishing is rerun it idempotently
	// re-sets it (--continue sets no Hold and leaves that to the automatic relay, RFC §12.4).
	if err := r.setCompletionHold(); err != nil {
		return fmt.Errorf("建立导入完成 Hold：%w", err)
	}
	for i, c := range seg.Chapters {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		r.emit(StagePublishing, c.Number, total, fmt.Sprintf("发布第 %d/%d 章：%s", c.Number, total, c.Title), nil)
		if err := publishChapter(ctx, r.deps.Store, r.deps.CommitChapter, c.Number, seg.Content(src, i), facts[i]); err != nil {
			return err
		}
	}
	return nil
}

// storyChoice returns the effective verdict for an uncertain status: first the persisted verdict bound to the current synthesis, then this run's opts, then the original intent.
// A persisted verdict must be validated for an InputDigest consistent with the current synthesis -- after re-synthesis the old verdict is void, and silently
// applying the old open/closed to the new result would mean the user is never asked again (RFC §10.4). An explicit --story (intent) is a standing user instruction across syntheses and may be kept.
func (r *runner) storyChoice() (string, error) {
	if raw, err := r.ws.readBytes(fileSynthesis); err == nil {
		if art, aerr := readArtifact[StoryResolution](r.ws, fileStoryResolve); aerr == nil && art.InputDigest == Digest(raw) {
			return art.Payload.Choice, nil
		} else if aerr != nil && !os.IsNotExist(aerr) {
			return "", fmt.Errorf("读取故事状态裁定: %w", aerr)
		}
	} else {
		return "", fmt.Errorf("读取综合工件: %w", err)
	}
	if r.opts.StoryResolution != "" {
		return r.opts.StoryResolution, nil
	}
	in, err := r.ws.LoadIntent()
	if err != nil {
		return "", fmt.Errorf("读取导入意图: %w", err)
	}
	return in.StoryResolution, nil
}

// resolveStoryStatus persists story-resolution.json when the status is uncertain and an explicit verdict already exists (bound to the current synthesis),
// so that the downstream NextAction lets it through naturally; with no verdict it shows a waiting state and stops.
func (r *runner) resolveStoryStatus() bool {
	choice, err := r.storyChoice()
	if err != nil {
		r.fail("读取故事状态裁定", err)
		return false
	}
	if choice != storyOpen && choice != storyClosed {
		r.emit(StageAwaitingStoryStatus, 0, 0, "综合判定故事状态为 uncertain，请用 --story=open|closed 明确后重试", nil)
		return false
	}
	raw, err := r.ws.readBytes(fileSynthesis)
	if err != nil {
		r.fail("读取综合结果", err)
		return false
	}
	if err := writeArtifact(r.ws, fileStoryResolve, Digest(raw), StoryResolution{Choice: choice}); err != nil {
		r.fail("落盘故事状态裁定", err)
		return false
	}
	return true
}

// resolveStory decides the story closure status from the synthesis result and the user's explicit verdict (RFC §10.4).
func (r *runner) resolveStory(syn *BookSynthesis) (bool, error) {
	switch syn.StoryStatus {
	case storyClosed:
		return true, nil
	case storyOpen:
		return false, nil
	case storyUncertain:
		choice, err := r.storyChoice()
		if err != nil {
			return false, err
		}
		switch choice {
		case storyClosed:
			return true, nil
		case storyOpen:
			return false, nil
		default:
			return false, fmt.Errorf("故事状态 uncertain，需 --story=open|closed")
		}
	default:
		return false, fmt.Errorf("未知 story_status：%q", syn.StoryStatus)
	}
}

// setCompletionHold sets an import-complete Hold; only --continue skips it (RFC §12.4).
// The error must propagate -- the Hold is the only safeguard against mistakenly continuing to write after an import, and a silent failure means the safeguard is gone.
func (r *runner) setCompletionHold() error {
	in, err := r.ws.LoadIntent()
	if err != nil {
		return fmt.Errorf("读取导入意图: %w", err)
	}
	if r.opts.ContinueAfter || (in != nil && in.ContinueAfterImport) {
		return nil
	}
	return r.deps.Store.RunMeta.SetAdvanceHold(domain.AdvanceHold{
		After:  domain.AdvanceHoldAtBoundary,
		Reason: "外部小说导入完成，等待验收后续写",
	})
}
