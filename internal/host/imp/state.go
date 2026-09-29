package imp

import (
	"fmt"
	"os"

	"github.com/CTKiet2006/kietnovel/internal/store"
)

// Action is the deterministic next step that NextAction derives from the workspace facts.
// The persisted state stores no drift-prone stage enum; the next action is derived from artifacts alone (RFC §6.2).
type Action string

const (
	ActionIngest               Action = "ingest"
	ActionSegment              Action = "segment"
	ActionAwaitConfirmation    Action = "await_confirmation"
	ActionAnalyze              Action = "analyze"
	ActionSynthesize           Action = "synthesize"
	ActionAwaitStoryResolution Action = "await_story_resolution"
	ActionPublish              Action = "publish"
	ActionDone                 Action = "done"
)

// Facts is the minimal fact snapshot read from the workspace, just enough to decide the next action.
// It separates the pure decision (NextAction) from IO (LoadState): NextAction is constant for the same Facts (RFC §20.1).
type Facts struct {
	WorkspaceReady   bool // the manifest + intent + source trio is complete
	Segmented        bool
	Confirmed        bool
	ExpectedChapters int // total number of chapters the segmentation confirmed (filled from stage two on)
	AnalyzedChapters int // number of analyses that are consecutive from chapter 1 and whose InputDigest matches (filled from stage three on)
	Synthesized      bool
	StoryUncertain   bool
	StoryResolved    bool
	Published        bool // the official artifact matches synthesis exactly (filled from stage five on)
}

// NextAction walks a fixed linear pipeline and returns the first missing or unsatisfied action. A pure function, with no IO.
func NextAction(f Facts) Action {
	switch {
	case f.Published:
		// Publishing is terminal: the official store is fully reconciled and the workspace is only an audit
		// archive. Upstream artifacts going stale from a prompt version / guidance upgrade no longer demand
		// rework -- otherwise a version upgrade would retroactively judge an already published book half-done, and the Engine's cross-restart gate would lock it out forever.
		return ActionDone
	case !f.WorkspaceReady:
		return ActionIngest
	case !f.Segmented:
		return ActionSegment
	case !f.Confirmed:
		return ActionAwaitConfirmation
	case f.AnalyzedChapters < f.ExpectedChapters:
		return ActionAnalyze
	case !f.Synthesized:
		return ActionSynthesize
	case f.StoryUncertain && !f.StoryResolved:
		return ActionAwaitStoryResolution
	default:
		return ActionPublish
	}
}

// artifactFresh reports whether the artifact exists and its InputDigest equals the `want` a rebuild would produce right now;
// missing, unparsable, or schema/digest mismatched all count as stale (needs rework).
func artifactFresh[T any](w *Workspace, rel, want string) (bool, error) {
	a, err := readArtifact[T](w, rel)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return a.InputDigest == want, nil
}

// LoadState reads the current fact snapshot from the workspace (workspace only, not the official Store).
// Linear short-circuit: every step verifies the artifact's InputDigest against the digest rebuildable from the current upstream, and any
// mismatch counts that step as not done, leaves downstream facts false, and hands the redo to NextAction from that point on -- this is what makes "changing the segmentation / prompt version / source" naturally invalidate downstream work (RFC §6.2/§6.3 / invariant 1).
// Published is filled in by the caller from the official publish reconciliation (always via CollectFacts).
func LoadState(w *Workspace) (Facts, error) {
	var f Facts
	if !w.Active() {
		return f, nil
	}
	if !(w.has(fileManifest) && w.has(fileIntent) && w.has(fileSource)) {
		return f, nil
	}
	src, err := w.LoadSource()
	if err != nil {
		return f, fmt.Errorf("读取导入源快照: %w", err)
	}
	f.WorkspaceReady = true
	guidance, err := w.LoadGuidance()
	if err != nil {
		return f, fmt.Errorf("读取切分指导: %w", err)
	}

	// segmentation: bound to the normalized source + user guidance + segmentation prompt version. A guidance change (--guide re-recognition) naturally invalidates the old segmentation.
	segArt, err := readArtifact[Segmentation](w, fileSegmentation)
	if os.IsNotExist(err) {
		return f, nil
	}
	if err != nil {
		return f, fmt.Errorf("读取切分工件: %w", err)
	}
	if segArt.InputDigest != segmentInputDigest(Digest(src), guidance, segmentPromptVersion) {
		return f, nil
	}
	f.Segmented = true
	seg := &segArt.Payload
	f.ExpectedChapters = len(seg.Chapters)

	// confirmation: bound to the raw bytes of the segmentation artifact.
	segRaw, err := w.readBytes(fileSegmentation)
	if err != nil {
		return f, fmt.Errorf("读取切分工件原文: %w", err)
	}
	confirmed, err := artifactFresh[Confirmation](w, fileConfirmation, Digest(segRaw))
	if err != nil {
		return f, fmt.Errorf("读取切分确认: %w", err)
	}
	if !confirmed {
		return f, nil
	}
	f.Confirmed = true

	// Per-chapter analysis: the length of the run of consecutive chapters whose per-chapter InputDigest matches the segmentation identity/version/body.
	f.AnalyzedChapters, err = analyzedChaptersStrict(w, seg, src, segArt.InputDigest, analyzePromptVersion)
	if err != nil {
		return f, err
	}
	if f.AnalyzedChapters < f.ExpectedChapters {
		return f, nil
	}

	// synthesis: bound to the ordered per-chapter facts.
	facts, err := loadPriorFactsStrict(w, f.ExpectedChapters)
	if err != nil {
		return f, err
	}
	synArt, err := readArtifact[BookSynthesis](w, fileSynthesis)
	if os.IsNotExist(err) {
		return f, nil
	}
	if err != nil {
		return f, fmt.Errorf("读取全书综合工件: %w", err)
	}
	if synArt.InputDigest != synthesisInputDigest(facts) {
		return f, nil
	}
	f.Synthesized = true
	f.StoryUncertain = synArt.Payload.StoryStatus == storyUncertain

	// story resolution: when uncertain, bound to the raw bytes of the synthesis artifact, or preselected by intent.
	synRaw, err := w.readBytes(fileSynthesis)
	if err != nil {
		return f, fmt.Errorf("读取全书综合工件原文: %w", err)
	}
	resolved, err := artifactFresh[StoryResolution](w, fileStoryResolve, Digest(synRaw))
	if err != nil {
		return f, fmt.Errorf("读取故事状态裁定: %w", err)
	}
	if resolved {
		f.StoryResolved = true
	} else if in, iErr := w.LoadIntent(); iErr != nil {
		return f, fmt.Errorf("读取导入意图: %w", iErr)
	} else if in.StoryResolution != "" {
		f.StoryResolved = true
	}
	return f, nil
}

// CollectFacts combines the workspace facts with the official publish reconciliation; it is the single entry point for the facts used by
// ResumeStatus/ResumeSummary/runner. The expected chapter count for publish reconciliation prefers a fresh segmentation; when the segmentation is stale
// from a prompt version / guidance upgrade it falls back to the chapter count confirmed at the time inside the artifact -- the official chapters of a
// published book were written from exactly that segmentation, so recomputing the digest with the current version would reconcile against nothing at all.
func CollectFacts(st *store.Store, w *Workspace) (Facts, error) {
	f, err := LoadState(w)
	if err != nil {
		return f, err
	}
	expected := f.ExpectedChapters
	if expected == 0 {
		if segArt, err := readArtifact[Segmentation](w, fileSegmentation); err == nil {
			expected = len(segArt.Payload.Chapters)
		}
	}
	f.Published, err = isPublished(st, expected)
	return f, err
}

// ResumeStatus reports whether an active import workspace exists and whether it is thoroughly complete (including the official publish reconciliation).
// It feeds the Engine's cross-restart gate (RFC §12.5): while active && !done, the normal writing flow must not consume a half-published state.
func ResumeStatus(st *store.Store) (active, done bool, err error) {
	w := OpenWorkspace(st.Dir())
	if !w.Active() {
		return false, false, nil
	}
	f, err := CollectFacts(st, w)
	if err != nil {
		return true, false, err
	}
	return true, NextAction(f) == ActionDone, nil
}

// ResumeSummary builds a one-line notice for an unfinished import (RFC §18.2); with no unfinished import it returns an empty string.
// The host shows it proactively at startup / in the welcome screen, so the user does not discover that this book is stuck mid-import only after the gate rejects their writing.
func ResumeSummary(st *store.Store) string {
	w := OpenWorkspace(st.Dir())
	if !w.Active() {
		return ""
	}
	f, err := CollectFacts(st, w)
	if err != nil {
		return "发现导入状态读取异常：" + err.Error() + "；请运行 /import 查看并修复"
	}
	var state string
	switch NextAction(f) {
	case ActionDone:
		return ""
	case ActionIngest, ActionSegment:
		state = "尚未完成切分"
	case ActionAwaitConfirmation:
		state = fmt.Sprintf("已切分 %d 章，等待核对确认", f.ExpectedChapters)
	case ActionAnalyze:
		state = fmt.Sprintf("已分析 %d/%d 章", f.AnalyzedChapters, f.ExpectedChapters)
	case ActionSynthesize:
		state = "逐章分析完成，待全书综合"
	case ActionAwaitStoryResolution:
		state = "待明确故事状态（--story=open|closed）"
	case ActionPublish:
		state = "综合完成，待发布正式状态"
	}
	return "发现未完成的导入（" + state + "），输入 /import 从断点恢复"
}

// checkImportPreconditions validates the preconditions for a new import (RFC §12.1):
// there must be no existing book info, no completed chapters and no in-flight PendingCommit. The merge semantics of an existing novel with new external text are unclear, so the first version rejects it outright.
func checkImportPreconditions(st *store.Store) error {
	book, err := st.Book.Load()
	if err != nil {
		return fmt.Errorf("读取作品信息：%w", err)
	}
	if book != nil {
		return fmt.Errorf("已有作品《%s》，拒绝把外部小说并入非空书籍", book.Title)
	}
	prog, err := st.Progress.Load()
	if err != nil {
		return fmt.Errorf("读取进度：%w", err)
	}
	if prog != nil && len(prog.CompletedChapters) > 0 {
		return fmt.Errorf("已有 %d 个完成章节，拒绝把外部小说并入非空书籍", len(prog.CompletedChapters))
	}
	pending, err := st.Signals.LoadPendingCommit()
	if err != nil {
		return fmt.Errorf("读取在途提交：%w", err)
	}
	if pending != nil {
		return fmt.Errorf("存在在途章节提交，请先完成或清理后再导入")
	}
	return nil
}
