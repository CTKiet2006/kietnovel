package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/CTKiet2006/kietnovel/internal/chapterfacts"
	"github.com/CTKiet2006/kietnovel/internal/domain"
	"github.com/CTKiet2006/kietnovel/internal/errs"
	"github.com/CTKiet2006/kietnovel/internal/revision"
	"github.com/CTKiet2006/kietnovel/internal/rules"
	"github.com/CTKiet2006/kietnovel/internal/store"
	"github.com/voocel/agentcore/schema"
)

// CommitChapterTool commits a chapter: load the body -> save the final version -> generate the summary -> update the state -> update the progress.
type CommitChapterTool struct {
	store      *store.Store
	styleStats *StyleStatsIndex
}

// NewCommitChapterTool creates the commit tool. styleStats must be shared with novel_context,
// so that the same statistics index is refreshed after a new commit, a rewrite or a recovery.
func NewCommitChapterTool(store *store.Store, styleStats *StyleStatsIndex) *CommitChapterTool {
	if styleStats == nil {
		panic("tools: NewCommitChapterTool requires StyleStatsIndex")
	}
	return &CommitChapterTool{store: store, styleStats: styleStats}
}

func (t *CommitChapterTool) chapterStyleDelta(chapter int) (domain.StyleDelta, error) {
	record, err := t.store.ChapterRecords.Load(chapter)
	if err != nil || record == nil {
		return domain.StyleDelta{}, err
	}
	return record.StyleDelta, nil
}

// commitOutput embeds extra fields on top of domain.CommitResult, keeping the domain package free of a dependency on rules.
// Since embedded fields are promoted by the JSON marshaler, the serialized result is equivalent to a flat structure.
type commitOutput struct {
	domain.CommitResult
	RuleViolations []rules.Violation `json:"rule_violations,omitempty"`
}

// commitArgs is the normalized structured payload of the commit saga. On the first execution it is written, together with the body snapshot, into the
// PendingCommit; a crash recovery always replays this frozen intent and ignores the parameters and draft produced by a new Worker.
type commitArgs struct {
	Chapter int `json:"chapter"`
	domain.ChapterFacts
}

func (t *CommitChapterTool) Name() string { return "commit_chapter" }
func (t *CommitChapterTool) Description() string {
	return "提交章节终稿。加载草稿正文保存为终稿，更新时间线、伏笔、关系、角色状态和进度。" +
		"返回结构化事实：next_chapter / review_required / arc_end / volume_end / needs_expansion / book_complete / flow 等"
}
func (t *CommitChapterTool) Label() string { return "提交章节" }

// A writing tool (a recoverable saga crossing domains: full payload -> final version/state -> progress -> checkpoint); concurrency is forbidden.
func (t *CommitChapterTool) ReadOnly(_ json.RawMessage) bool        { return false }
func (t *CommitChapterTool) ConcurrencySafe(_ json.RawMessage) bool { return false }
func (t *CommitChapterTool) StrictSchema() bool                     { return true }

func (t *CommitChapterTool) Schema() map[string]any {
	props := []schema.Prop{schema.Property("chapter", schema.Int("章节号")).Required()}
	props = append(props, chapterfacts.Properties(true)...)
	return schema.Object(props...)
}

func (t *CommitChapterTool) Execute(_ context.Context, args json.RawMessage) (json.RawMessage, error) {
	var requested commitArgs
	if err := json.Unmarshal(args, &requested); err != nil {
		return nil, fmt.Errorf("invalid args: %w: %w", errs.ErrToolArgs, err)
	}
	if requested.Chapter <= 0 {
		return nil, fmt.Errorf("chapter must be > 0: %w", errs.ErrToolArgs)
	}
	existingPending, err := t.store.Signals.LoadPendingCommit()
	if err != nil {
		return nil, fmt.Errorf("load pending commit: %w: %w", errs.ErrStoreRead, err)
	}
	if existingPending != nil && existingPending.Chapter != requested.Chapter {
		return nil, fmt.Errorf("存在未恢复的章节提交：第 %d 章（阶段 %s），请先恢复或重新提交该章: %w", existingPending.Chapter, existingPending.Stage, errs.ErrToolConflict)
	}
	if existingPending != nil {
		switch existingPending.Stage {
		case domain.CommitStageStarted, domain.CommitStageStateApplied, domain.CommitStageProgressMarked, domain.CommitStageSignalSaved:
		default:
			return nil, fmt.Errorf("pending commit 阶段非法: %q: %w", existingPending.Stage, errs.ErrToolConflict)
		}
	}

	a := requested
	if existingPending != nil && existingPending.Stage != domain.CommitStageProgressMarked && existingPending.Stage != domain.CommitStageSignalSaved {
		if len(existingPending.Payload) == 0 {
			return nil, fmt.Errorf("第 %d 章存在旧版未完成提交，但缺少可重放 payload；拒绝使用新生成参数覆盖，请从最近 checkpoint 恢复或人工核对 meta/pending_commit.json: %w",
				existingPending.Chapter, errs.ErrToolConflict)
		}
		if err := json.Unmarshal(existingPending.Payload, &a); err != nil {
			return nil, fmt.Errorf("decode pending commit payload: %w: %w", errs.ErrStoreRead, err)
		}
		if a.Chapter != existingPending.Chapter {
			return nil, fmt.Errorf("pending commit payload 章节不一致：记录=%d payload=%d: %w", existingPending.Chapter, a.Chapter, errs.ErrToolConflict)
		}
	}

	progress, err := t.store.Progress.Load()
	if err != nil {
		return nil, fmt.Errorf("load progress: %w: %w", errs.ErrStoreRead, err)
	}
	if progress == nil {
		return nil, fmt.Errorf("progress 未初始化: %w", errs.ErrToolPrecondition)
	}
	completed := slices.Contains(progress.CompletedChapters, a.Chapter)
	if existingPending != nil && (existingPending.Stage == domain.CommitStageProgressMarked || existingPending.Stage == domain.CommitStageSignalSaved) {
		if !completed {
			return nil, fmt.Errorf("pending commit 已到 %s，但 progress 未标记第 %d 章完成: %w", existingPending.Stage, a.Chapter, errs.ErrToolConflict)
		}
		return t.finishPendingCommit(*existingPending, progress)
	}
	if existingPending == nil || existingPending.Stage == domain.CommitStageStarted {
		if err := t.validateCommitArgs(a); err != nil {
			// When a frozen payload is replayed over and over, every retry hits the same error and the model cannot
			// get out of it with new parameters, because the payload replay above overwrites whatever it just passed. Keeping this
			// payload immutable only manufactures a deadlock, so for every stage=Started (that is, the step where no body text
			// has been written yet) the freeze is explicitly released, so the model can correct it and resubmit. The body text and the chapter record are unaffected.
			//
			// Restricted to stage=Started: a commit whose progress is already marked / whose signal is already saved takes the
			// finishPendingCommit path, where the payload must stay immutable.
			//
			// ErrToolArgs / ErrToolPrecondition are the classes the model can get past by changing parameters;
			// ErrToolConflict (the queue/state class) says the environment changed rather than the parameters being wrong,
			// and unlocking there would let the model bypass the state machine checks, so it is not allowed.
			if existingPending != nil && existingPending.Stage == domain.CommitStageStarted &&
				(errors.Is(err, errs.ErrToolArgs) || errors.Is(err, errs.ErrToolPrecondition)) {
				if clearErr := t.store.Signals.ClearPendingCommit(); clearErr != nil {
					return nil, fmt.Errorf("提交校验失败（%v），且清理冻结提交失败: %w: %w", err, errs.ErrStoreWrite, clearErr)
				}
				if existingPending.Rewrite {
					return nil, fmt.Errorf("旧版遗留的返工提交未通过校验，已解除冻结；请修正后重新提交: %w", err)
				}
				return nil, fmt.Errorf("未完成的提交未通过校验，已解除冻结；请修正参数后重新提交: %w", err)
			}
			return nil, err
		}
	}

	if existingPending != nil && existingPending.Rewrite {
		if !completed {
			return nil, fmt.Errorf("返工提交要求第 %d 章已存在终稿: %w", a.Chapter, errs.ErrToolConflict)
		}
		return t.executeRewriteCommit(a, progress, *existingPending, true)
	}
	if existingPending == nil && completed {
		if slices.Contains(progress.PendingRewrites, a.Chapter) {
			content, err := t.validateRewriteDraft(a.Chapter, a.Title, progress)
			if err != nil {
				return nil, err
			}
			payload, err := json.Marshal(a)
			if err != nil {
				return nil, fmt.Errorf("marshal rewrite payload: %w", err)
			}
			now := time.Now().Format(time.RFC3339)
			mode := "rewrite"
			if progress.Flow == domain.FlowPolishing {
				mode = "polish"
			}
			pending := domain.PendingCommit{Chapter: a.Chapter, Stage: domain.CommitStageStarted,
				Rewrite: true, RewriteMode: mode, Payload: payload, DraftContent: content,
				Summary: a.Summary, HookType: a.HookType,
				DominantStrand: a.DominantStrand, StartedAt: now, UpdatedAt: now}
			if err := t.store.Signals.SavePendingCommit(pending); err != nil {
				return nil, fmt.Errorf("save rewrite pending commit: %w: %w", errs.ErrStoreWrite, err)
			}
			return t.executeRewriteCommit(a, progress, pending, false)
		}
		return t.buildSkipResult(a.Chapter, progress)
	}

	// A new commit must pass the current phase / rework queue checks; an existing ordinary PendingCommit is the recovery protocol,
	// which allows it to carry on wrapping up across the interruption window where "Progress already landed / Phase already completed".
	if existingPending == nil {
		if err := t.store.Progress.ValidateChapterWork(a.Chapter); err != nil {
			// A queue conflict is left as it is (it already carries the ErrToolConflict classification); other IO errors are classed as Precondition.
			if errors.Is(err, errs.ErrToolConflict) {
				return nil, err
			}
			return nil, fmt.Errorf("章节当前不允许提交: %w: %w", errs.ErrToolPrecondition, err)
		}
		if progress.Flow != domain.FlowRewriting && progress.Flow != domain.FlowPolishing {
			expected := progress.NextChapter()
			if a.Chapter != expected {
				return nil, fmt.Errorf("正常续写只能提交下一章 %d，收到第 %d 章: %w", expected, a.Chapter, errs.ErrToolConflict)
			}
		}
	}

	// Out-of-range block in layered mode: it must come before any write, otherwise an out-of-range commit would corrupt the chapter file, the summary and
	// Progress alike. boundary is reused by step 6b below to compute the arc/volume signal.
	var boundary *store.ArcBoundary
	if progress.Layered {
		b, bErr := t.store.Outline.CheckArcBoundary(a.Chapter)
		if bErr != nil {
			return nil, fmt.Errorf("弧边界检测失败 chapter=%d: %w: %w", a.Chapter, errs.ErrStoreRead, bErr)
		}
		if b == nil {
			return nil, fmt.Errorf(
				"第 %d 章不在分层大纲范围内：写作必须先 expand_next_arc 扩展弧或 append_volume 追加卷；若全书已完结请调 save_foundation type=complete_book: %w",
				a.Chapter, errs.ErrToolPrecondition)
		}
		boundary = b
	}

	// 1. Freeze the chapter body. A first commit reads it from the draft and persists it together with the PendingCommit; on recovery
	// only that snapshot is used, which avoids a new Worker overwriting the draft before the retry and ending up with "old facts + new body".
	var content string
	if existingPending != nil {
		content = existingPending.DraftContent
		if content == "" {
			return nil, fmt.Errorf("第 %d 章未完成提交缺少 draft_content，无法证明恢复正文与原提交一致: %w",
				a.Chapter, errs.ErrToolConflict)
		}
	} else {
		var loadErr error
		content, _, loadErr = t.store.Drafts.LoadChapterContent(a.Chapter)
		if loadErr != nil {
			return nil, fmt.Errorf("load chapter content: %w: %w", errs.ErrStoreRead, loadErr)
		}
	}
	if content == "" {
		return nil, fmt.Errorf("no content found for chapter %d: %w", a.Chapter, errs.ErrToolPrecondition)
	}
	wordCount := domain.WordCount(content)

	var pending domain.PendingCommit
	if existingPending != nil {
		pending = *existingPending
	} else {
		payload, err := json.Marshal(a)
		if err != nil {
			return nil, fmt.Errorf("marshal commit payload: %w", err)
		}
		now := time.Now().Format(time.RFC3339)
		pending = domain.PendingCommit{
			Chapter: a.Chapter, Stage: domain.CommitStageStarted, Payload: payload, DraftContent: content,
			Summary: a.Summary, HookType: a.HookType, DominantStrand: a.DominantStrand,
			StartedAt: now, UpdatedAt: now,
		}
		if err := t.store.Signals.SavePendingCommit(pending); err != nil {
			return nil, fmt.Errorf("save pending commit: %w: %w", errs.ErrStoreWrite, err)
		}
	}

	// StageStarted may mean no artifact has been written yet, or a crash partway through the state delta; every operation of the full payload
	// must be idempotent, so it is replayed as a whole. StageStateApplied instead goes straight to Progress.
	if pending.Stage == domain.CommitStageStarted {
		// 2. Save the final version
		if err := t.store.Drafts.SaveFinalChapter(a.Chapter, content); err != nil {
			return nil, fmt.Errorf("save final chapter: %w: %w", errs.ErrStoreWrite, err)
		}
		style, err := t.chapterStyleDelta(a.Chapter)
		if err != nil {
			return nil, fmt.Errorf("load chapter style: %w: %w", errs.ErrStoreRead, err)
		}
		if _, err := t.store.ChapterRecords.Accept(a.Chapter, domain.ChapterOriginGenerated, content, a.ChapterFacts, style); err != nil {
			return nil, fmt.Errorf("save chapter record: %w: %w", errs.ErrStoreWrite, err)
		}

		// 3. Save the summary
		summary := domain.ChapterSummary{
			Chapter: a.Chapter, Title: a.Title, Summary: a.Summary, Characters: a.Characters, KeyEvents: a.KeyEvents,
		}
		if err := t.store.Summaries.SaveSummary(summary); err != nil {
			return nil, fmt.Errorf("save summary: %w: %w", errs.ErrStoreWrite, err)
		}

		// 4. Update the state delta
		if len(a.TimelineEvents) > 0 {
			for i := range a.TimelineEvents {
				a.TimelineEvents[i].Chapter = a.Chapter
			}
			if err := t.store.World.AppendTimelineEvents(a.TimelineEvents); err != nil {
				return nil, fmt.Errorf("append timeline: %w: %w", errs.ErrStoreWrite, err)
			}
		}
		if len(a.ForeshadowUpdates) > 0 {
			if err := t.store.World.UpdateForeshadow(a.Chapter, a.ForeshadowUpdates); err != nil {
				return nil, fmt.Errorf("update foreshadow: %w: %w", errs.ErrStoreWrite, err)
			}
		}
		if len(a.RelationshipChanges) > 0 {
			for i := range a.RelationshipChanges {
				a.RelationshipChanges[i].Chapter = a.Chapter
			}
			if err := t.store.World.UpdateRelationships(a.RelationshipChanges); err != nil {
				return nil, fmt.Errorf("update relationships: %w: %w", errs.ErrStoreWrite, err)
			}
		}
		if len(a.StateChanges) > 0 {
			for i := range a.StateChanges {
				a.StateChanges[i].Chapter = a.Chapter
			}
			if err := t.store.World.AppendStateChanges(a.StateChanges); err != nil {
				return nil, fmt.Errorf("append state changes: %w: %w", errs.ErrStoreWrite, err)
			}
		}

		pending.Stage = domain.CommitStageStateApplied
		pending.UpdatedAt = time.Now().Format(time.RFC3339)
		if err := t.store.Signals.SavePendingCommit(pending); err != nil {
			return nil, fmt.Errorf("update pending commit stage: %w: %w", errs.ErrStoreWrite, err)
		}
	}

	// 5. Update the progress
	if !completed {
		if err := t.store.Progress.MarkChapterComplete(a.Chapter, wordCount, a.HookType, a.DominantStrand); err != nil {
			return nil, fmt.Errorf("mark chapter complete: %w: %w", errs.ErrStoreWrite, err)
		}
	}

	// 6. Decide whether a review is needed
	progress, err = t.store.Progress.Load()
	if err != nil {
		return nil, fmt.Errorf("load progress: %w: %w", errs.ErrStoreRead, err)
	}
	completedCount := 0
	if progress != nil {
		completedCount = len(progress.CompletedChapters)
	}

	// 6b. Arc/volume signal in long-form mode: boundary was already validated at the entry and is guaranteed non-nil when Layered
	var arcEnd, volumeEnd, needsExpansion, needsNewVolume bool
	var vol, arc, nextVol, nextArc int
	if progress != nil && progress.Layered && boundary != nil {
		arcEnd = boundary.IsArcEnd
		volumeEnd = boundary.IsVolumeEnd
		vol = boundary.Volume
		arc = boundary.Arc
		needsExpansion = boundary.NeedsExpansion
		needsNewVolume = boundary.NeedsNewVolume
		nextVol = boundary.NextVolume
		nextArc = boundary.NextArc
		if err := t.store.Progress.UpdateVolumeArc(vol, arc); err != nil {
			return nil, fmt.Errorf("update volume/arc: %w: %w", errs.ErrStoreWrite, err)
		}
	}

	var reviewRequired bool
	var reviewReason string
	if progress != nil && progress.Layered {
		reviewRequired, reviewReason = domain.ShouldArcReview(arcEnd, volumeEnd, vol, arc)
	} else {
		reviewRequired, reviewReason = domain.ShouldReview(completedCount)
	}

	// 7. Build the structured signal
	result := domain.CommitResult{
		Chapter:        a.Chapter,
		Committed:      true,
		WordCount:      wordCount,
		NextChapter:    a.Chapter + 1,
		ReviewRequired: reviewRequired,
		ReviewReason:   reviewReason,
		HookType:       a.HookType,
		DominantStrand: a.DominantStrand,
		Feedback:       a.Feedback,
		// (feedback is also persisted into the feedback pool, see persistFeedback below -- the return value is only a mirror,
		// what the architect consumes via novel_context is the store fact)
		ArcEnd:         arcEnd,
		VolumeEnd:      volumeEnd,
		Volume:         vol,
		Arc:            arc,
		NeedsExpansion: needsExpansion,
		NeedsNewVolume: needsNewVolume,
		NextVolume:     nextVol,
		NextArc:        nextArc,
	}

	// 8. Completion determination: the last chapter in non-layered mode / the last chapter of the final volume in layered mode -> MarkComplete
	bookComplete, err := t.applyCompletion(&result, progress)
	if err != nil {
		return nil, err
	}
	if bookComplete {
		result.BookComplete = true
	}
	latestProgress, err := t.store.Progress.Load()
	if err != nil {
		return nil, fmt.Errorf("load progress after completion: %w: %w", errs.ErrStoreRead, err)
	}
	if latestProgress != nil {
		result.Flow = string(latestProgress.Flow)
	}

	// 8.5 The feedback pool is a persistent fact for later planning, consumed by the Architect at the next structural operation.
	if a.Feedback != nil && (strings.TrimSpace(a.Feedback.Deviation) != "" || strings.TrimSpace(a.Feedback.Suggestion) != "") {
		if err := t.store.Outline.AppendOutlineFeedback(store.ChapterFeedback{
			Chapter: a.Chapter, Deviation: a.Feedback.Deviation, Suggestion: a.Feedback.Suggestion,
		}); err != nil {
			return nil, fmt.Errorf("persist outline feedback: %w: %w", errs.ErrStoreWrite, err)
		}
	}

	// The mechanical rules are part of the output and must be fixed before ProgressMarked, so that recovery returns exactly the same output.
	violations := t.checkRules(content)
	output, err := json.Marshal(commitOutput{CommitResult: result, RuleViolations: violations})
	if err != nil {
		return nil, fmt.Errorf("marshal commit output: %w", err)
	}

	pending.Stage = domain.CommitStageProgressMarked
	pending.Result = &result
	pending.Output = output
	pending.UpdatedAt = time.Now().Format(time.RFC3339)
	if err := t.store.Signals.SavePendingCommit(pending); err != nil {
		return nil, fmt.Errorf("update pending commit result: %w: %w", errs.ErrStoreWrite, err)
	}

	// 9. Append the checkpoint. This must come before clearing pending_commit, so that a pending_commit visible after a restart
	// pending_commit can always drive a rerun to fill in the missing checkpoint.
	if err := t.appendCommitCheckpoint(a.Chapter); err != nil {
		return nil, fmt.Errorf("checkpoint commit: %w: %w", errs.ErrStoreWrite, err)
	}
	pending.Stage = domain.CommitStageSignalSaved
	pending.UpdatedAt = time.Now().Format(time.RFC3339)
	if err := t.store.Signals.SavePendingCommit(pending); err != nil {
		return nil, fmt.Errorf("update pending commit checkpoint stage: %w: %w", errs.ErrStoreWrite, err)
	}

	// 10. Clear the intermediate progress state
	if err := t.store.Progress.ClearInProgress(); err != nil {
		return nil, fmt.Errorf("clear in-progress: %w: %w", errs.ErrStoreWrite, err)
	}
	if err := t.store.Signals.ClearPendingCommit(); err != nil {
		return nil, fmt.Errorf("clear pending commit: %w: %w", errs.ErrStoreWrite, err)
	}

	t.refreshStyleStats(a.Chapter, content)
	return output, nil
}

// finishPendingCommit wraps up the ProgressMarked/SignalSaved interruption window. The checkpoint append is idempotent by
// digest; the recovery record is deleted only after both the checkpoint and the intermediate-state cleanup have succeeded.
func (t *CommitChapterTool) finishPendingCommit(pending domain.PendingCommit, progress *domain.Progress) (json.RawMessage, error) {
	if pending.Stage == domain.CommitStageProgressMarked {
		if err := t.appendCommitCheckpoint(pending.Chapter); err != nil {
			return nil, fmt.Errorf("checkpoint commit: %w: %w", errs.ErrStoreWrite, err)
		}
		pending.Stage = domain.CommitStageSignalSaved
		pending.UpdatedAt = time.Now().Format(time.RFC3339)
		if err := t.store.Signals.SavePendingCommit(pending); err != nil {
			return nil, fmt.Errorf("update pending commit checkpoint stage: %w: %w", errs.ErrStoreWrite, err)
		}
	}
	if err := t.store.Progress.ClearInProgress(); err != nil {
		return nil, fmt.Errorf("clear in-progress: %w: %w", errs.ErrStoreWrite, err)
	}
	if err := t.store.Signals.ClearPendingCommit(); err != nil {
		return nil, fmt.Errorf("clear pending commit: %w: %w", errs.ErrStoreWrite, err)
	}
	t.refreshStyleStats(pending.Chapter, pending.DraftContent)
	if len(pending.Output) > 0 {
		return append(json.RawMessage(nil), pending.Output...), nil
	}
	if pending.Result != nil {
		return json.Marshal(pending.Result)
	}
	return t.buildSkipResult(pending.Chapter, progress)
}

func (t *CommitChapterTool) validateRewriteDraft(chapter int, title string, progress *domain.Progress) (string, error) {
	content, _, err := t.store.Drafts.LoadChapterContent(chapter)
	if err != nil {
		return "", fmt.Errorf("rewrite: load chapter content: %w: %w", errs.ErrStoreRead, err)
	}
	if content == "" {
		return "", fmt.Errorf("no content found for chapter %d: %w", chapter, errs.ErrToolPrecondition)
	}
	changed, err := t.rewriteChanged(chapter, content, title)
	if err != nil {
		return "", err
	}
	if changed {
		return content, nil
	}
	mode := "重写"
	if progress != nil && progress.Flow == domain.FlowPolishing {
		mode = "打磨"
	}
	return "", fmt.Errorf("第 %d 章正文和标题均未发生变化，未检测到%s改动: %w",
		chapter, mode, errs.ErrToolPrecondition)
}

func (t *CommitChapterTool) rewriteChanged(chapter int, content, title string) (bool, error) {
	existingFinal, err := t.store.Drafts.LoadChapterText(chapter)
	if err != nil {
		return false, fmt.Errorf("rewrite: load final chapter: %w: %w", errs.ErrStoreRead, err)
	}
	if existingFinal != content {
		return true, nil
	}
	summary, err := t.store.Summaries.LoadSummary(chapter)
	if err != nil {
		return false, fmt.Errorf("rewrite: load chapter summary: %w: %w", errs.ErrStoreRead, err)
	}
	return summary == nil || strings.TrimSpace(summary.Title) != strings.TrimSpace(title), nil
}

func (t *CommitChapterTool) appendCommitCheckpoint(chapter int) error {
	_, err := t.store.Checkpoints.AppendArtifacts(
		domain.ChapterScope(chapter), "commit",
		fmt.Sprintf("chapters/%02d.md", chapter),
		fmt.Sprintf("summaries/%02d.json", chapter),
		store.ChapterRecordPath(chapter),
	)
	return err
}

// checkRules runs mechanical checks on the chapter body: the built-in product floor Lint (a mechanism residue, always executed)
// plus the user rules Check (reading this book's snapshot `structured`; when the snapshot is missing it falls back to the built-in defaults, guaranteeing that the mechanical floor is always present).
func (t *CommitChapterTool) checkRules(text string) []rules.Violation {
	violations := rules.Lint(text)
	structured := rules.SystemDefaults().Structured
	if snap, err := t.store.UserRules.Load(); err == nil && snap != nil {
		structured = snap.Structured
	}
	return append(violations, rules.Check(text, structured)...)
}

// executeRewriteCommit handles the commit of a polished/rewritten chapter: overwrite the final version and the summary, update the word count, drain the queue.
// It skips every world state append (timeline / foreshadow / relationship / state_changes) and the arc boundary detection,
// because those were already applied when the chapter was originally committed.
func (t *CommitChapterTool) executeRewriteCommit(a commitArgs, progress *domain.Progress, pending domain.PendingCommit, recovering bool) (json.RawMessage, error) {
	chapter := a.Chapter
	// 1. Use only the rework body frozen at the first commit; crash recovery must not adopt a draft that was overwritten afterwards.
	content := pending.DraftContent
	if content == "" {
		return nil, fmt.Errorf("第 %d 章返工提交缺少 draft_content，无法安全恢复: %w", chapter, errs.ErrToolConflict)
	}
	wordCount := domain.WordCount(content)

	// 2. At least one of the body or the title changed; polishing a title must not require faking a body change.
	if !recovering {
		changed, err := t.rewriteChanged(chapter, content, a.Title)
		if err != nil {
			return nil, err
		}
		if !changed {
			mode := "重写"
			if progress != nil && progress.Flow == domain.FlowPolishing {
				mode = "打磨"
			}
			return nil, fmt.Errorf("第 %d 章正文和标题均未发生变化，未检测到%s改动: %w",
				chapter, mode, errs.ErrToolPrecondition)
		}
	}

	if pending.Stage == domain.CommitStageStarted {
		// 3. Build the complete candidate record set first and validate by replay. The old implementation overwrote the records and then rebuilt the projection,
		// so as soon as the fact chain did not close it left the failed payload on disk and every later retry read a broken baseline.
		existing, err := t.store.ChapterRecords.Load(chapter)
		if err != nil {
			return nil, fmt.Errorf("rewrite: load chapter record: %w: %w", errs.ErrStoreRead, err)
		}
		var existingUpdates []domain.ForeshadowUpdate
		var style domain.StyleDelta
		if existing != nil {
			existingUpdates = existing.Facts.ForeshadowUpdates
			style = existing.StyleDelta
		}
		recovered, err := t.restoreRewritePlants(chapter, existingUpdates, &a.ChapterFacts)
		if err != nil {
			return nil, err
		}
		candidate, err := t.store.ChapterRecords.Prepare(
			chapter, domain.ChapterOriginGenerated, content, a.ChapterFacts, style,
		)
		if err != nil {
			return nil, fmt.Errorf("rewrite: prepare chapter record: %w: %w", errs.ErrStoreRead, err)
		}
		chapters := slices.Clone(progress.CompletedChapters)
		slices.Sort(chapters)
		records := make([]domain.ChapterRecord, 0, len(chapters))
		for _, completedChapter := range chapters {
			if completedChapter == chapter {
				records = append(records, *candidate)
				continue
			}
			record, err := t.store.ChapterRecords.Load(completedChapter)
			if err != nil {
				return nil, fmt.Errorf("rewrite: load chapter record %d: %w: %w", completedChapter, errs.ErrStoreRead, err)
			}
			if record == nil {
				return nil, fmt.Errorf("rewrite: 第 %d 章缺少接纳记录: %w", completedChapter, errs.ErrToolConflict)
			}
			records = append(records, *record)
		}
		if err := revision.ValidateRecords(records); err != nil {
			if clearErr := t.store.Signals.ClearPendingCommit(); clearErr != nil {
				return nil, fmt.Errorf("rewrite: 章节事实链校验失败（%v），且清理冻结提交失败: %w: %w", err, errs.ErrStoreWrite, clearErr)
			}
			return nil, fmt.Errorf("rewrite: 章节事实链校验失败，已解除冻结且未写入返工结果: %w: %w", errs.ErrToolPrecondition, err)
		}

		// 4. Only after validation passes are the authoritative records and the final version overwritten; the same frozen payload can be replayed safely.
		if err := t.store.Drafts.SaveFinalChapter(chapter, content); err != nil {
			return nil, fmt.Errorf("rewrite: save final chapter: %w: %w", errs.ErrStoreWrite, err)
		}
		if err := t.store.ChapterRecords.Save(*candidate); err != nil {
			return nil, fmt.Errorf("rewrite: save chapter record: %w: %w", errs.ErrStoreWrite, err)
		}
		if len(recovered) > 0 {
			slog.Warn("已从伏笔账本恢复旧版本丢失的种植事实", "module", "commit", "chapter", chapter, "foreshadows", recovered)
		}
		if err := revision.NewProjector(t.store).Apply(records); err != nil {
			return nil, fmt.Errorf("rewrite: rebuild chapter projections: %w: %w", errs.ErrStoreWrite, err)
		}
		if err := t.store.Summaries.SaveSummary(domain.ChapterSummary{
			Chapter: chapter, Title: a.Title, Summary: a.Summary, Characters: a.Characters, KeyEvents: a.KeyEvents,
		}); err != nil {
			return nil, fmt.Errorf("rewrite: save summary: %w: %w", errs.ErrStoreWrite, err)
		}
		pending.Stage = domain.CommitStageStateApplied
		pending.UpdatedAt = time.Now().Format(time.RFC3339)
		if err := t.store.Signals.SavePendingCommit(pending); err != nil {
			return nil, fmt.Errorf("rewrite: update pending state stage: %w: %w", errs.ErrStoreWrite, err)
		}
	}

	// 5. Update the word count (MarkChapterComplete is idempotent for an already completed chapter: it replaces the word count, and slice.Contains prevents double enqueueing)
	if progress.Phase != domain.PhaseComplete {
		if err := t.store.Progress.MarkChapterComplete(chapter, wordCount, a.HookType, a.DominantStrand); err != nil {
			return nil, fmt.Errorf("rewrite: update word count: %w: %w", errs.ErrStoreWrite, err)
		}

		// 6. Drain the pending queue; when the queue is empty CompleteRewrite automatically switches flow back to writing
		if err := t.store.Progress.CompleteRewrite(chapter); err != nil {
			return nil, fmt.Errorf("rewrite: complete rewrite: %w: %w", errs.ErrStoreWrite, err)
		}
	}

	// 7. Read the Progress snapshot after the drain and return it as the fact
	mode := pending.RewriteMode
	if mode == "" {
		mode = "rewrite"
	}
	latest, err := t.store.Progress.Load()
	if err != nil {
		return nil, fmt.Errorf("rewrite: load progress after drain: %w: %w", errs.ErrStoreRead, err)
	}
	remaining := []int{}
	nextChapter := chapter + 1
	flow := string(domain.FlowWriting)
	if latest != nil {
		remaining = append(remaining, latest.PendingRewrites...)
		nextChapter = latest.NextChapter()
		flow = string(latest.Flow)
	}
	drained := len(remaining) == 0

	// Completion is determined only after the queue is empty: a rework commit does not go through the main path's applyCompletion, so completion can only be triggered here.
	//   - Layered + forward writing: the overall layeredComplete determination (structurally written if a finale volume is declared / the quality-level check if not).
	//   - Layered + reopen rework (ReopenedFromComplete): rework only modifies existing chapters and never adds or removes structure, so structural completeness
	//     alone re-completes the book -- if a mere disturbance of some thread by the rework left it stuck in writing, the end of the final volume would fall into an out-of-range continuation livelock.
	//   - Non-layered: reaching TotalChapters completes the book (rework neither adds nor removes chapters; it was already full).
	bookComplete := false
	if drained && latest != nil {
		reComplete := false
		switch {
		case latest.Layered && latest.ReopenedFromComplete:
			reComplete, err = layeredStructurallyComplete(t.store, latest)
		case latest.Layered:
			reComplete, err = layeredComplete(t.store, latest)
		default:
			reComplete = latest.TotalChapters > 0 && len(latest.CompletedChapters) >= latest.TotalChapters
		}
		if err != nil {
			return nil, fmt.Errorf("rewrite: evaluate completion: %w: %w", errs.ErrStoreRead, err)
		}
		if reComplete {
			if err := t.store.Progress.MarkComplete(); err != nil {
				return nil, fmt.Errorf("rewrite: mark complete: %w: %w", errs.ErrStoreWrite, err)
			}
			bookComplete = true
			p, err := t.store.Progress.Load()
			if err != nil {
				return nil, fmt.Errorf("rewrite: reload completed progress: %w: %w", errs.ErrStoreRead, err)
			}
			if p != nil {
				flow = string(p.Flow)
			}
		}
	}

	// Same as the main path: rewrite/polish also returns the mechanical check results based on the current body text.
	violations := t.checkRules(content)
	output, err := json.Marshal(map[string]any{
		"chapter": chapter, "rewritten": true, "mode": mode, "word_count": wordCount,
		"remaining_queue": remaining, "queue_drained": drained, "next_chapter": nextChapter,
		"flow": flow, "book_complete": bookComplete, "rule_violations": violations,
	})
	if err != nil {
		return nil, fmt.Errorf("rewrite: marshal output: %w", err)
	}
	pending.Stage = domain.CommitStageProgressMarked
	pending.Output = output
	pending.UpdatedAt = time.Now().Format(time.RFC3339)
	if err := t.store.Signals.SavePendingCommit(pending); err != nil {
		return nil, fmt.Errorf("rewrite: update pending progress stage: %w: %w", errs.ErrStoreWrite, err)
	}

	// 8. After the checkpoint, mark signal_saved, and finally clear the PendingCommit.
	if err := t.appendCommitCheckpoint(chapter); err != nil {
		return nil, fmt.Errorf("rewrite: checkpoint commit: %w: %w", errs.ErrStoreWrite, err)
	}
	pending.Stage = domain.CommitStageSignalSaved
	pending.UpdatedAt = time.Now().Format(time.RFC3339)
	if err := t.store.Signals.SavePendingCommit(pending); err != nil {
		return nil, fmt.Errorf("rewrite: update pending checkpoint stage: %w: %w", errs.ErrStoreWrite, err)
	}
	if err := t.store.Progress.ClearInProgress(); err != nil {
		return nil, fmt.Errorf("rewrite: clear in-progress: %w: %w", errs.ErrStoreWrite, err)
	}
	if err := t.store.Signals.ClearPendingCommit(); err != nil {
		return nil, fmt.Errorf("rewrite: clear pending commit: %w: %w", errs.ErrStoreWrite, err)
	}

	t.refreshStyleStats(chapter, content)
	return output, nil
}

// restoreRewritePlants repairs only the single corruption shape older versions could produce: the foreshadowing ledger still records this chapter's
// plant, while this chapter's accepted record has been overwritten by the failed rework. The ledger supplies the full id, description and planting chapter,
// so the restore can be deterministic; any other inconsistency still reports an explicit error rather than guessing plot facts.
func (t *CommitChapterTool) restoreRewritePlants(chapter int, existing []domain.ForeshadowUpdate, facts *domain.ChapterFacts) ([]string, error) {
	planted := make(map[string]struct{}, len(existing)+len(facts.ForeshadowUpdates))
	for _, update := range existing {
		if update.Action == "plant" {
			planted[update.ID] = struct{}{}
		}
	}
	for _, update := range facts.ForeshadowUpdates {
		if update.Action == "plant" {
			planted[update.ID] = struct{}{}
		}
	}

	ledger, err := t.store.World.LoadForeshadowLedger()
	if err != nil {
		return nil, fmt.Errorf("rewrite: load foreshadow ledger for recovery: %w: %w", errs.ErrStoreRead, err)
	}
	var restored []domain.ForeshadowUpdate
	var ids []string
	for _, entry := range ledger {
		if entry.PlantedAt != chapter {
			continue
		}
		if _, ok := planted[entry.ID]; ok {
			continue
		}
		if strings.TrimSpace(entry.ID) == "" || strings.TrimSpace(entry.Description) == "" {
			return nil, fmt.Errorf("rewrite: 第 %d 章伏笔账本缺少可恢复的 id 或 description: %w", chapter, errs.ErrToolConflict)
		}
		planted[entry.ID] = struct{}{}
		restored = append(restored, domain.ForeshadowUpdate{
			ID: entry.ID, Action: "plant", Description: entry.Description,
		})
		ids = append(ids, entry.ID)
	}
	if len(restored) > 0 {
		facts.ForeshadowUpdates = append(restored, facts.ForeshadowUpdates...)
	}
	return ids, nil
}

func (t *CommitChapterTool) refreshStyleStats(chapter int, content string) {
	if content == "" {
		var err error
		content, err = t.store.Drafts.LoadChapterText(chapter)
		if err != nil {
			slog.Error("风格统计索引更新失败", "module", "tools", "chapter", chapter, "err", err)
			return
		}
		if content == "" {
			slog.Error("风格统计索引更新失败", "module", "tools", "chapter", chapter, "err", errors.New("终稿不存在"))
			return
		}
	}
	t.styleStats.ChapterCommitted(chapter, content)
}

// buildSkipResult builds a fact return aligned with a normal commit for a "repeat submission of an already completed chapter".
// The coordinator makes its follow-up decisions on this (dispatching writer/editor/architect) instead of hallucinating because it received a prose hint.
func (t *CommitChapterTool) buildSkipResult(chapter int, progress *domain.Progress) (json.RawMessage, error) {
	_, wordCount, err := t.store.Drafts.LoadChapterContent(chapter)
	if err != nil {
		return nil, fmt.Errorf("load completed chapter: %w: %w", errs.ErrStoreRead, err)
	}

	result := domain.CommitResult{
		Chapter:     chapter,
		Committed:   true,
		WordCount:   wordCount,
		NextChapter: chapter + 1,
	}

	if progress != nil && progress.Layered {
		boundary, err := t.store.Outline.CheckArcBoundary(chapter)
		if err != nil {
			return nil, fmt.Errorf("check completed chapter boundary: %w: %w", errs.ErrStoreRead, err)
		}
		if boundary != nil {
			result.ArcEnd = boundary.IsArcEnd
			result.VolumeEnd = boundary.IsVolumeEnd
			result.Volume = boundary.Volume
			result.Arc = boundary.Arc
			result.NeedsExpansion = boundary.NeedsExpansion
			result.NeedsNewVolume = boundary.NeedsNewVolume
			result.NextVolume = boundary.NextVolume
			result.NextArc = boundary.NextArc
		}
		result.ReviewRequired, result.ReviewReason = domain.ShouldArcReview(result.ArcEnd, result.VolumeEnd, result.Volume, result.Arc)
	} else if progress != nil {
		result.ReviewRequired, result.ReviewReason = domain.ShouldReview(len(progress.CompletedChapters))
	}

	if progress != nil {
		if progress.Phase == domain.PhaseComplete {
			result.BookComplete = true
		}
		result.Flow = string(progress.Flow)
	}

	return json.Marshal(result)
}

// applyCompletion decides whether this commit completes the whole book, and if so calls MarkComplete and returns true.
//   - Non-layered: finishing the agreed total chapter count completes the book.
//   - Layered: an explicit architect save_foundation type=complete_book is the main path; this adds a second
//     deterministic backstop (see layeredComplete) -- stopping the model from reaching the end with neither append_volume nor
//     complete_book, which produces the livelock "the writer runs on into out-of-range chapters -> the out-of-range guard blocks it -> repeated retries"
//     (the root cause of the 《凡骨》ch204..347 case).
func (t *CommitChapterTool) applyCompletion(result *domain.CommitResult, progress *domain.Progress) (bool, error) {
	if progress == nil {
		return false, nil
	}
	if progress.Phase == domain.PhaseComplete {
		return true, nil
	}
	if progress.Layered {
		complete, err := layeredComplete(t.store, progress)
		if err != nil {
			return false, fmt.Errorf("evaluate layered completion: %w: %w", errs.ErrStoreRead, err)
		}
		if complete {
			if err := t.store.Progress.MarkComplete(); err != nil {
				return false, fmt.Errorf("mark book complete: %w: %w", errs.ErrStoreWrite, err)
			}
			return true, nil
		}
		return false, nil
	}
	if progress.TotalChapters > 0 && result.NextChapter > progress.TotalChapters {
		if err := t.store.Progress.MarkComplete(); err != nil {
			return false, fmt.Errorf("mark book complete: %w: %w", errs.ErrStoreWrite, err)
		}
		return true, nil
	}
	return false, nil
}

// ── Layered completion determination (package level: shared by the two trigger points commit_chapter and save_volume_summary) ──
//
// The completion check always happens in the tool where the last fact lands:
//   - Finale not declared: the last chapter's commit (the quality-level layeredBookComplete)
//   - Finale declared: the last piece of the forward main path is the end-of-volume wrap-up trio (review -> arc summary -> volume summary),
//     so the trigger point is save_volume_summary; after a rework drain, once the trio is complete the commit triggers it.

// layeredStructurallyComplete determines whether a layered long book is "structurally written": rework queue empty + no skeleton arc left to expand
// + all expanded chapters written. This is a deterministic terminal fact containing no semantic judgement such as foreshadowing / long threads -- it serves as the safety net
// against a "terminal-state livelock" (re-completing the book once the rework has drained).
func layeredStructurallyComplete(st *store.Store, progress *domain.Progress) (bool, error) {
	// 1. The rework queue must be empty
	if len(progress.PendingRewrites) > 0 {
		return false, nil
	}
	volumes, err := st.Outline.LoadLayeredOutline()
	if err != nil {
		return false, fmt.Errorf("load layered outline: %w", err)
	}
	if len(volumes) == 0 {
		return false, nil
	}
	// 2. No skeleton arc may be left to expand (there is still planned content to write)
	for i := range volumes {
		for j := range volumes[i].Arcs {
			if !volumes[i].Arcs[j].IsExpanded() {
				return false, nil
			}
		}
	}
	// 3. All expanded chapters must be fully written
	expanded := len(domain.FlattenOutline(volumes))
	return expanded > 0 && len(progress.CompletedChapters) >= expanded, nil
}

// finaleWrapped reports whether a finale volume has the complete end-of-volume wrap-up trio (arc review / arc summary / volume summary).
// Finale completion does not require foreshadowing / long threads to reach zero, but it must wait for the last arc to pass the editorial quality gate -- the ending is the most important part of the book,
// so completion must not jump ahead of the editor review (which may enqueue a rework) and the summary being persisted.
func finaleWrapped(st *store.Store, progress *domain.Progress) (bool, error) {
	last := progress.LatestCompleted()
	if last <= 0 {
		return false, nil
	}
	b, err := st.Outline.CheckArcBoundary(last)
	if err != nil {
		return false, fmt.Errorf("check finale boundary: %w", err)
	}
	if b == nil || !b.IsArcEnd {
		return false, nil
	}
	hasReview, err := st.World.HasArcReview(last)
	if err != nil {
		return false, fmt.Errorf("load finale review: %w", err)
	}
	hasArcSummary, err := st.Summaries.HasArcSummary(b.Volume, b.Arc)
	if err != nil {
		return false, fmt.Errorf("load finale arc summary: %w", err)
	}
	hasVolumeSummary, err := st.Summaries.HasVolumeSummary(b.Volume)
	if err != nil {
		return false, fmt.Errorf("load finale volume summary: %w", err)
	}
	return hasReview && hasArcSummary && hasVolumeSummary, nil
}

// layeredComplete is the overall completion determination for layered forward writing:
//   - A declared finale volume (the last volume of layered_outline carries final) -> structurally written + the end-of-volume wrap-up trio complete
//     means complete, with no further requirement that foreshadowing / long threads reach zero. A finale volume targets thread closure for the whole volume (the architect already distributed the long threads /
//     foreshadowing among the arcs at planning time), so an individual omission is an editorial quality issue and should not hold the whole book outside the terminal state -- otherwise
//     a book whose estimated_scale is overestimated could never legitimately finish (the other side of the root cause of the 140-chapter stop guard circuit-breaker case).
//   - Not declared -> the quality-level layeredBookComplete, preventing the model from wrapping up prematurely where the outline
//     is exhausted when it neither declares a finale nor completes the book.
func layeredComplete(st *store.Store, progress *domain.Progress) (bool, error) {
	volumes, err := st.Outline.LoadLayeredOutline()
	if err != nil {
		return false, fmt.Errorf("load layered outline: %w", err)
	}
	if domain.FinaleVolume(volumes) > 0 {
		structurallyComplete, err := layeredStructurallyComplete(st, progress)
		if err != nil || !structurallyComplete {
			return structurallyComplete, err
		}
		return finaleWrapped(st, progress)
	}
	return layeredBookComplete(st, progress)
}

// ReconcileLayeredCompletion fills in the completion state of a layered book from the current persisted facts.
// The normal path of save_volume_summary and the Engine's crash recovery share this entry point, which prevents the automatic completion trigger from being lost forever
// when the volume summary has already landed but Progress has not yet had the chance to MarkComplete.
func ReconcileLayeredCompletion(st *store.Store) (bool, error) {
	progress, err := st.Progress.Load()
	if err != nil {
		return false, fmt.Errorf("load progress: %w", err)
	}
	if progress == nil || !progress.Layered {
		return false, nil
	}
	if progress.Phase == domain.PhaseComplete {
		return true, nil
	}
	if progress.Phase != domain.PhaseWriting {
		return false, nil
	}
	complete, err := layeredComplete(st, progress)
	if err != nil || !complete {
		return complete, err
	}
	if err := st.Progress.MarkComplete(); err != nil {
		return false, fmt.Errorf("mark complete: %w", err)
	}
	return true, nil
}

// layeredBookComplete judges with objective facts whether a layered long book is truly finished, against the quantifiable items in the completion
// checklist of architect-long.md plus the structural facts. On top of structural completeness it further requires foreshadowing to reach zero and long threads to be resolved -- if either is unmet it
// yields to the architect to keep doing expand_next_arc / append_volume, and it never wraps up while the story is unfinished. With no compass it conservatively
// judges the book unfinished. This is the "quality-level" completion determination used when no finale volume is declared, and it is stricter than layeredStructurallyComplete.
func layeredBookComplete(st *store.Store, progress *domain.Progress) (bool, error) {
	structurallyComplete, err := layeredStructurallyComplete(st, progress)
	if err != nil || !structurallyComplete {
		return structurallyComplete, err
	}
	// 4. Active foreshadowing must reach zero (the promises have been kept)
	active, err := st.World.LoadActiveForeshadow()
	if err != nil {
		return false, fmt.Errorf("load active foreshadow: %w", err)
	}
	if len(active) > 0 {
		return false, nil
	}
	// 5. The compass' active long threads must be resolved (no compass / unresolved threads both go back to the architect's judgement)
	compass, err := st.Outline.LoadCompass()
	if err != nil {
		return false, fmt.Errorf("load compass: %w", err)
	}
	if compass == nil || len(compass.OpenThreads) > 0 {
		return false, nil
	}
	return true, nil
}
