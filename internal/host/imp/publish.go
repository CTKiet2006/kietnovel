package imp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/CTKiet2006/kietnovel/internal/domain"
	"github.com/CTKiet2006/kietnovel/internal/store"
)

// ChapterCommitter is the minimal interface required to publish a chapter, satisfied by tools.CommitChapterTool.
// It reuses that PendingCommit saga, its checkpoints and its completed-chapter idempotency check rather than duplicating a second commit path (RFC §12.3).
type ChapterCommitter interface {
	Execute(ctx context.Context, args json.RawMessage) (json.RawMessage, error)
}

// publishFoundation publishes the Foundation in official dependency order, matching the Architect's long-form write order (RFC §12.2).
// Publishing the same content again is idempotent (the Store overwrites with identical content + checkpoint dedup).
func publishFoundation(st *store.Store, f *Foundation) error {
	// Pre-publish conflict reconciliation: an official artifact that already exists with different content is refused, not overwritten (§12.2 / invariant 6).
	// Identical content proceeds idempotently (the Store overwrites with identical content + checkpoint dedup).
	if err := checkFoundationConflicts(st, f); err != nil {
		return err
	}
	if err := st.Book.Save(f.Book); err != nil {
		return fmt.Errorf("book：%w", err)
	}
	if _, err := st.Checkpoints.AppendArtifact(domain.GlobalScope(), "book", "meta/book.json"); err != nil {
		return fmt.Errorf("checkpoint book：%w", err)
	}
	if err := st.RunMeta.SetPlanningTier(f.PlanningTier); err != nil {
		return fmt.Errorf("planning tier：%w", err)
	}
	// premise
	if err := st.Outline.SavePremise(f.Premise); err != nil {
		return fmt.Errorf("premise：%w", err)
	}
	if err := st.Progress.UpdatePhase(domain.PhasePremise); err != nil {
		return fmt.Errorf("phase premise：%w", err)
	}
	if _, err := st.Checkpoints.AppendArtifact(domain.GlobalScope(), "premise", "premise.md"); err != nil {
		return fmt.Errorf("checkpoint premise：%w", err)
	}
	// characters
	if err := st.Characters.Save(f.Characters); err != nil {
		return fmt.Errorf("characters：%w", err)
	}
	if _, err := st.Checkpoints.AppendArtifact(domain.GlobalScope(), "characters", "characters.json"); err != nil {
		return fmt.Errorf("checkpoint characters：%w", err)
	}
	// world rules
	if err := st.World.SaveWorldRules(f.WorldRules); err != nil {
		return fmt.Errorf("world_rules：%w", err)
	}
	if _, err := st.Checkpoints.AppendArtifact(domain.GlobalScope(), "world_rules", "world_rules.json"); err != nil {
		return fmt.Errorf("checkpoint world_rules：%w", err)
	}
	// The layered outline is the single source; the Store rebuilds the flat outline in sync.
	if err := st.Outline.SaveLayeredOutline(f.Volumes); err != nil {
		return fmt.Errorf("layered outline：%w", err)
	}
	// Outline-stage progress is what the engine recomputes routing from (chapter capacity / layering / current volume arc); a failed write leaves an inconsistent
	// published state, which must be surfaced rather than swallowed (RFC §12.2).
	if err := st.Progress.UpdatePhase(domain.PhaseOutline); err != nil {
		return fmt.Errorf("phase outline：%w", err)
	}
	if err := st.Progress.SetTotalChapters(domain.EstimatedChapterCapacity(f.Volumes)); err != nil {
		return fmt.Errorf("total chapters：%w", err)
	}
	if err := st.Progress.SetLayered(true); err != nil {
		return fmt.Errorf("set layered：%w", err)
	}
	if len(f.Volumes) > 0 && len(f.Volumes[0].Arcs) > 0 {
		if err := st.Progress.UpdateVolumeArc(f.Volumes[0].Index, f.Volumes[0].Arcs[0].Index); err != nil {
			return fmt.Errorf("volume arc：%w", err)
		}
	}
	if _, err := st.Checkpoints.AppendArtifact(domain.GlobalScope(), "layered_outline", "layered_outline.json"); err != nil {
		return fmt.Errorf("checkpoint layered outline：%w", err)
	}
	// compass
	if err := st.Outline.SaveCompass(f.Compass); err != nil {
		return fmt.Errorf("compass：%w", err)
	}
	if _, err := st.Checkpoints.AppendArtifact(domain.GlobalScope(), "compass", "meta/compass.json"); err != nil {
		return fmt.Errorf("checkpoint compass：%w", err)
	}
	// Every official write of the imported Foundation has succeeded, so it may explicitly enter writing.
	// It must not reuse the normal writing flow's FoundationMissing: import allows world_rules to be empty,
	// and treating a legal empty value as missing would strand progress at outline forever, after which StartChapter is rejected by the stage gate.
	p, err := st.Progress.Load()
	if err != nil {
		return fmt.Errorf("load progress：%w", err)
	}
	if p == nil {
		return fmt.Errorf("load progress：progress 未初始化")
	}
	if p.Phase != domain.PhaseWriting && p.Phase != domain.PhaseComplete {
		if err := st.Progress.UpdatePhase(domain.PhaseWriting); err != nil {
			return fmt.Errorf("phase writing：%w", err)
		}
	}
	return nil
}

// checkFoundationConflicts checks the pending Foundation against the existing official artifacts:
// an empty existing set counts as a first publish; identical counts as idempotent; differing content is a conflict and is not overwritten (RFC §12.2 / invariant 6).
// compass and the flat outline are derived from the layered outline, so a consistent layered outline implies consistent derivations; the derived artifacts are therefore not checked separately.
// A read error must not be swallowed as "file not found": the store loaders return (zero value, nil) for a missing file, so any non-nil is a real error
// (corrupt / permission / invalid JSON); treating it as empty and continuing would overwrite an official artifact that cannot be read (RFC §12.2).
func checkFoundationConflicts(st *store.Store, f *Foundation) error {
	wantBook := f.Book.Normalized()
	book, err := st.Book.Load()
	if err != nil {
		return fmt.Errorf("读取正式 book：%w", err)
	}
	if book != nil && !jsonEqual(book, wantBook) {
		return fmt.Errorf("正式 book 与导入综合冲突（已存在不同版本），拒绝覆盖")
	}
	cur, err := st.Outline.LoadPremise()
	if err != nil {
		return fmt.Errorf("读取正式 premise：%w", err)
	}
	if cur != "" && cur != f.Premise {
		return fmt.Errorf("正式 premise 与导入综合冲突（已存在不同版本），拒绝覆盖")
	}
	chars, err := st.Characters.Load()
	if err != nil {
		return fmt.Errorf("读取正式 characters：%w", err)
	}
	if len(chars) > 0 && !jsonEqual(chars, f.Characters) {
		return fmt.Errorf("正式 characters 与导入综合冲突（已存在不同版本），拒绝覆盖")
	}
	rules, err := st.World.LoadWorldRules()
	if err != nil {
		return fmt.Errorf("读取正式 world_rules：%w", err)
	}
	if len(rules) > 0 && !jsonEqual(rules, f.WorldRules) {
		return fmt.Errorf("正式 world_rules 与导入综合冲突（已存在不同版本），拒绝覆盖")
	}
	layered, err := st.Outline.LoadLayeredOutline()
	if err != nil {
		return fmt.Errorf("读取正式 layered_outline：%w", err)
	}
	if len(layered) > 0 && !jsonEqual(layered, f.Volumes) {
		return fmt.Errorf("正式 layered_outline 与导入综合冲突（已存在不同版本），拒绝覆盖")
	}
	return nil
}

// jsonEqual compares whether two values are equivalent by normalized JSON bytes.
func jsonEqual(a, b any) bool {
	ab, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	return bytes.Equal(ab, bb)
}

// publishChapter reuses commit_chapter to publish a single chapter; already completed chapters are skipped by its idempotency check (RFC §12.3).
func publishChapter(ctx context.Context, st *store.Store, commit ChapterCommitter, chapter int, content string, f ImportedChapterFacts) error {
	completed, err := st.Progress.IsChapterCompleted(chapter)
	if err != nil {
		return fmt.Errorf("load progress ch%d：%w", chapter, err)
	}
	if completed {
		// A crash can land between MarkChapterComplete and ClearPendingCommit, leaving a pending_commit residue
		// that points at this chapter. Skipping directly would bypass the cleanup branch the commit tool prepared for exactly this window (append the
		// checkpoint + clear the residue), and the next chapter's Execute would then refuse with "an unrecovered chapter commit exists", so every import
		// rerun would die at the same spot and require a manual deletion of meta/pending_commit.json to unlock. On hitting residue it still goes through the tool's idempotent path to finish the cleanup.
		pending, err := st.Signals.LoadPendingCommit()
		if err != nil {
			return fmt.Errorf("load pending commit ch%d：%w", chapter, err)
		}
		if pending != nil && pending.Chapter == chapter {
			raw, err := json.Marshal(commitArgs(chapter, f))
			if err != nil {
				return fmt.Errorf("marshal commit ch%d：%w", chapter, err)
			}
			if _, err := commit.Execute(ctx, raw); err != nil {
				return fmt.Errorf("commit ch%d：%w", chapter, err)
			}
		}
		return nil
	}
	if err := st.Drafts.SaveDraft(chapter, content); err != nil {
		return fmt.Errorf("save draft ch%d：%w", chapter, err)
	}
	if err := st.Progress.StartChapter(chapter); err != nil {
		return fmt.Errorf("start ch%d：%w", chapter, err)
	}
	raw, err := json.Marshal(commitArgs(chapter, f))
	if err != nil {
		return fmt.Errorf("marshal commit ch%d：%w", chapter, err)
	}
	if _, err := commit.Execute(ctx, raw); err != nil {
		return fmt.Errorf("commit ch%d：%w", chapter, err)
	}
	return nil
}

// commitArgs maps the per-chapter facts onto commit_chapter arguments.
func commitArgs(chapter int, f ImportedChapterFacts) map[string]any {
	keyEvents := f.KeyEvents
	if len(keyEvents) == 0 {
		keyEvents = []string{f.CoreEvent} // core_event 已校验非空
	}
	args := map[string]any{
		"chapter":         chapter,
		"title":           f.Title,
		"summary":         f.Summary,
		"characters":      f.Characters,
		"key_events":      keyEvents,
		"hook_type":       f.HookType,
		"dominant_strand": f.DominantStrand,
	}
	if len(f.TimelineEvents) > 0 {
		args["timeline_events"] = f.TimelineEvents
	}
	if len(f.ForeshadowUpdates) > 0 {
		args["foreshadow_updates"] = f.ForeshadowUpdates
	}
	if len(f.RelationshipChanges) > 0 {
		args["relationship_changes"] = f.RelationshipChanges
	}
	if len(f.StateChanges) > 0 {
		args["state_changes"] = f.StateChanges
	}
	return args
}

// isPublished reports whether the official state already reflects a complete import: the Foundation is on disk and the completed chapters reached the expected count.
// It reconciles only the artifacts the import actually produces -- book, premise, the flat outline covering every chapter, completed chapters -- and does not reuse
// FoundationMissing(): the latter is the normal writing flow's "writable" gate and would misjudge a legally empty world_rules
// as incomplete, which would make publish reconciliation never converge (RFC §12.3).
func isPublished(st *store.Store, expected int) (bool, error) {
	if expected == 0 {
		return false, nil
	}
	book, err := st.Book.Load()
	if err != nil {
		return false, fmt.Errorf("读取正式 book: %w", err)
	}
	if book == nil {
		return false, nil
	}
	p, err := st.Outline.LoadPremise()
	if err != nil {
		return false, fmt.Errorf("读取正式 premise: %w", err)
	}
	if p == "" {
		return false, nil
	}
	o, err := st.Outline.LoadOutline()
	if err != nil {
		return false, fmt.Errorf("读取正式 outline: %w", err)
	}
	if len(o) < expected {
		return false, nil
	}
	prog, err := st.Progress.Load()
	if err != nil {
		return false, fmt.Errorf("读取正式 progress: %w", err)
	}
	return prog != nil && len(prog.CompletedChapters) >= expected, nil
}
