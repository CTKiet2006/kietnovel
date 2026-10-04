package tools

import (
	"fmt"
	"slices"

	"github.com/CTKiet2006/kietnovel/internal/domain"
	"github.com/CTKiet2006/kietnovel/internal/rules"
)

type contextBuildState struct {
	chapter         int
	profile         domain.ContextProfile
	progress        *domain.Progress
	runMeta         *domain.RunMeta
	outline         []domain.OutlineEntry
	currentEntry    *domain.OutlineEntry
	chapterPlan     *domain.ChapterPlan
	storyThreads    []domain.RecallItem
	foreshadow      []domain.ForeshadowEntry
	relationships   []domain.RelationshipEntry
	allStateChanges []domain.StateChange
	styleRules      *domain.WritingStyleRules
	cast            []domain.CastEntry
}

type chapterContextEnvelope struct {
	Working    map[string]any
	Episodic   map[string]any
	References map[string]any
	Selected   map[string]any
}

type architectContextEnvelope struct {
	Planning   map[string]any
	Foundation map[string]any
	References map[string]any
}

// planningVolumeOutline is the Architect's read-only structural projection. The volume/arc skeleton is kept globally,
// while only the current arc or the explicitly focused arc carries chapter details, so the detail does not grow linearly with the planning scale.
type planningVolumeOutline struct {
	Index int                  `json:"index"`
	Title string               `json:"title"`
	Theme string               `json:"theme"`
	Final bool                 `json:"final,omitempty"`
	Arcs  []planningArcOutline `json:"arcs"`
}

type planningArcOutline struct {
	Index             int                   `json:"index"`
	Title             string                `json:"title"`
	Goal              string                `json:"goal"`
	Status            string                `json:"status"`
	StartChapter      int                   `json:"start_chapter,omitempty"`
	EndChapter        int                   `json:"end_chapter,omitempty"`
	ChapterCount      int                   `json:"chapter_count,omitempty"`
	EstimatedChapters int                   `json:"estimated_chapters,omitempty"`
	Chapters          []domain.OutlineEntry `json:"chapters,omitempty"`
	ChaptersOmitted   bool                  `json:"chapters_omitted,omitempty"`
}

func newChapterContextEnvelope() chapterContextEnvelope {
	return chapterContextEnvelope{
		Working:    make(map[string]any),
		Episodic:   make(map[string]any),
		References: make(map[string]any),
		Selected:   make(map[string]any),
	}
}

func newArchitectContextEnvelope() architectContextEnvelope {
	return architectContextEnvelope{
		Planning:   make(map[string]any),
		Foundation: make(map[string]any),
		References: make(map[string]any),
	}
}

func (e chapterContextEnvelope) apply(result map[string]any) {
	// The chapter path applies the preparation-stage content and then the build-stage content, so an existing section is merged.
	mergeEnvelopeSection(result, "working_memory", e.Working)
	mergeEnvelopeSection(result, "episodic_memory", e.Episodic)
	mergeEnvelopeSection(result, "reference_pack", e.References)
	if len(e.Selected) > 0 {
		mergeEnvelopeSection(result, "selected_memory", e.Selected)
	}
}

// mergeEnvelopeSection merges section into the existing container at result[key]; when the container does not exist it mounts it directly.
func mergeEnvelopeSection(result map[string]any, key string, section map[string]any) {
	if existing, ok := result[key].(map[string]any); ok {
		for k, v := range section {
			existing[k] = v
		}
		return
	}
	result[key] = section
}

func (e architectContextEnvelope) apply(result map[string]any) {
	result["planning_memory"] = e.Planning
	result["foundation_memory"] = e.Foundation
	result["reference_pack"] = e.References
}

// buildProgressStatus returns a progress summary when the Architect passes no chapter.
// The Writer/Editor chapter paths do not need this information, and including it would only disturb the writing.
func (t *ContextTool) buildProgressStatus(result map[string]any, reads *contextReads) {
	progress, err := t.store.Progress.Load()
	if err != nil {
		reads.require("progress_status", err)
		return
	}
	if progress == nil {
		return
	}
	status := map[string]any{
		"phase":              string(progress.Phase),
		"flow":               string(progress.Flow),
		"completed_chapters": len(progress.CompletedChapters),
		"next_chapter":       progress.NextChapter(),
		"total_word_count":   progress.TotalWordCount,
	}
	if progress.InProgressChapter > 0 {
		status["in_progress_chapter"] = progress.InProgressChapter
	}
	if len(progress.PendingRewrites) > 0 {
		status["pending_rewrites"] = progress.PendingRewrites
		status["rewrite_reason"] = progress.RewriteReason
	}
	if progress.Layered {
		status["layered"] = true
		status["dynamic_planning"] = true
		outline, outlineErr := t.store.Outline.LoadOutline()
		if outlineErr != nil {
			reads.require("progress_status.outline", outlineErr)
		} else {
			status["outlined_chapters"] = len(outline)
		}
		status["current_volume"] = progress.CurrentVolume
		status["current_arc"] = progress.CurrentArc
	} else {
		status["total_chapters"] = progress.TotalChapters
	}
	if progress.Phase == domain.PhaseComplete {
		status["finished"] = true
	}
	result["progress_status"] = status
}

// buildUserRules injects the merged Bundle into working_memory.user_rules (the canonical path).
//
// Single point of injection: whichever of the writer / editor / architect paths calls novel_context
// gets the same preferences at working_memory.user_rules. The architect path originally had no working_memory,
// so this function creates one on demand (holding only user_rules); on the chapter > 0 path working_memory already exists and is embedded directly.
//
// It injects even when the Bundle is empty, keeping the field stable so the LLM never sees user_rules=null and takes an abnormal branch.
//
// Injection policy: only structured + preferences are shown to the LLM -- those two are the preferences that actually have to be followed while writing.
// sources / conflicts are diagnostic information (for troubleshooting the user's conflicts) and do not go to the LLM; the CLI startup diagnostic panel shows them on demand.
func (t *ContextTool) buildUserRules(result map[string]any, reads *contextReads) *rules.Snapshot {
	snap, err := t.store.UserRules.Load()
	if err != nil {
		reads.require("user_rules", err)
	}
	if snap == nil {
		// While the snapshot is not yet initialised the code's built-in defaults are used, guaranteeing that the mechanical floor (word count / banned words / fatigue words) always exists.
		lang := ""
		if t.store != nil && t.store.BookLanguage != nil {
			lang, _ = t.store.BookLanguage.Load()
		}
		def := rules.BuildSnapshot([]rules.Candidate{rules.SystemDefaultsForLanguage(lang)})
		snap = &def
	}
	working, ok := result["working_memory"].(map[string]any)
	if !ok {
		working = map[string]any{}
		result["working_memory"] = working
	}
	working["user_rules"] = snap.Payload()
	return snap
}

func (t *ContextTool) buildRuleViolations(result map[string]any, chapter int, snap *rules.Snapshot, reads *contextReads) {
	record, err := t.store.ChapterRecords.Load(chapter)
	if err != nil {
		reads.require("chapter_record", err)
		return
	}
	if record == nil {
		return
	}
	violations := rules.Lint(record.Content)
	violations = append(violations, rules.Check(record.Content, snap.Structured)...)
	if len(violations) > 0 {
		result["rule_violations"] = violations
	}
}

func (t *ContextTool) buildSimulationProfile(result map[string]any, sectionKey string, reads *contextReads) {
	profile, err := t.store.Simulation.Load()
	if err != nil {
		reads.warn("simulation_profile", err)
		return
	}
	compact := domain.CompactSimulationProfile(profile)
	if compact == nil {
		return
	}
	section, ok := result[sectionKey].(map[string]any)
	if !ok {
		section = map[string]any{}
		result[sectionKey] = section
	}
	section["simulation_profile"] = compact
}

func (t *ContextTool) buildBaseContext(result map[string]any, reads *contextReads) {
	if book, err := t.store.Book.Load(); err == nil && book != nil {
		result["book"] = book
	} else {
		reads.require("book", err)
	}
	if premise, err := t.store.Outline.LoadPremise(); err == nil && premise != "" {
		result["premise"] = premise
		if sections := parsePremiseSections(premise); len(sections) > 0 {
			result["premise_sections"] = sections
		}
		tier := domain.PlanningTier("")
		if meta, err := t.store.RunMeta.Load(); err == nil && meta != nil {
			tier = meta.PlanningTier
		} else {
			reads.require("run_meta", err)
		}
		result["premise_structure"] = premiseStructure(premise, tier)
	} else {
		reads.require("premise", err)
	}
	if rules, err := t.store.World.LoadWorldRules(); err == nil && len(rules) > 0 {
		result["world_rules"] = rules
	} else {
		reads.require("world_rules", err)
	}
}

func (t *ContextTool) prepareChapterContext(chapter int, envelope *chapterContextEnvelope, reads *contextReads) contextBuildState {
	state := contextBuildState{
		chapter: chapter,
		profile: domain.NewContextProfile(0),
	}

	progress, err := t.store.Progress.Load()
	reads.require("progress", err)
	runMeta, err := t.store.RunMeta.Load()
	reads.require("run_meta", err)
	state.progress = progress
	state.runMeta = runMeta
	if progress != nil && len(progress.CompletedChapters) > 0 {
		state.cast, err = t.store.BuildCast(progress.CompletedChapters)
		reads.require("supporting_cast", err)
	}

	if runMeta != nil && runMeta.PlanningTier != "" {
		envelope.Episodic["planning_tier"] = runMeta.PlanningTier
	}
	if progress != nil && progress.TotalChapters > 0 {
		state.profile = domain.NewContextProfile(progress.TotalChapters)
	}
	if progress == nil || !progress.Layered {
		state.profile.Layered = false
	}

	outline, outlineErr := t.store.Outline.LoadOutline()
	reads.require("outline", outlineErr)
	state.outline = outline
	currentEntry := findOutlineEntry(outline, chapter)
	if currentEntry != nil {
		envelope.Working["current_chapter_outline"] = currentEntry
	}
	state.currentEntry = currentEntry

	chapterPlan, chapterPlanErr := t.store.Drafts.LoadChapterPlan(chapter)
	if chapterPlanErr == nil && chapterPlan != nil {
		envelope.Working["chapter_plan"] = chapterPlan
		if len(chapterPlan.Contract.RequiredBeats) > 0 ||
			len(chapterPlan.Contract.ForbiddenMoves) > 0 ||
			len(chapterPlan.Contract.ContinuityChecks) > 0 ||
			len(chapterPlan.Contract.EvaluationFocus) > 0 ||
			chapterPlan.Contract.EmotionTarget != "" ||
			len(chapterPlan.Contract.PayoffPoints) > 0 ||
			chapterPlan.Contract.HookGoal != "" {
			envelope.Working["chapter_contract"] = chapterPlan.Contract
		}
	} else {
		reads.require("chapter_plan", chapterPlanErr)
	}
	state.chapterPlan = chapterPlan

	// Whether this chapter is currently being rewritten: it decides whether novel_context adds "rewrite-specific" facts.
	isRewrite := progress != nil && slices.Contains(progress.PendingRewrites, chapter)

	// Expose the fact of whether the draft already exists, so a re-dispatched writer can decide on its own whether to skip the rewrite or overwrite.
	// Only exists + word_count are exposed, never the body text (the writer pulls the body on demand with read_chapter).
	if _, draftWords, draftErr := t.store.Drafts.LoadChapterContent(chapter); draftErr == nil && draftWords > 0 {
		envelope.Working["chapter_draft"] = map[string]any{
			"exists":     true,
			"word_count": draftWords,
		}
	} else if draftErr != nil {
		reads.require("chapter_draft", draftErr)
	}

	// On a rewrite, hand the writer "why it is changing + what to change": the reason comes from the rework queue, the specific criticism from this chapter's review
	// (selectReviewLessons only recalls chapter-1..chapter-3, which happens to miss this chapter itself, and the writer has no tool to read a review).
	// The body text is not injected here -- the convention "pull the body on demand with read_chapter" stays intact.
	if isRewrite {
		brief := map[string]any{"reason": progress.RewriteReason}
		if reviews, reviewErr := t.store.World.LoadReviewsAffectingChapter(chapter); reviewErr == nil {
			var sources []map[string]any
			for _, review := range reviews {
				item := map[string]any{
					"review_chapter": review.Chapter,
					"scope":          review.Scope,
					"summary":        review.Summary,
				}
				var issues []domain.ConsistencyIssue
				for _, issue := range review.Issues {
					// A new review is dispatched precisely through the problem-to-chapter mapping; an old review without a mapping keeps
					// all of its problems, so that historical rework reasons do not vanish after an upgrade.
					if len(issue.Chapters) == 0 || (issue.RequiresChange && slices.Contains(issue.Chapters, chapter)) {
						issues = append(issues, issue)
					}
				}
				if len(issues) > 0 {
					item["issues"] = issues
				}
				if review.Scope == "chapter" && len(review.ContractMisses) > 0 {
					item["contract_misses"] = review.ContractMisses
				}
				sources = append(sources, item)
			}
			if len(sources) > 0 {
				brief["reviews"] = sources
				// A single source keeps the old field, so existing context consumers do not lose information on upgrade.
				if len(sources) == 1 {
					brief["review_summary"] = sources[0]["summary"]
					if issues, ok := sources[0]["issues"]; ok {
						brief["issues"] = issues
					}
					if misses, ok := sources[0]["contract_misses"]; ok {
						brief["contract_misses"] = misses
					}
				}
			}
		} else {
			reads.require("rewrite_review", reviewErr)
		}
		envelope.Working["rewrite_brief"] = brief
	}

	foreshadow, foreshadowErr := t.store.World.LoadActiveForeshadow()
	reads.require("foreshadow_ledger", foreshadowErr)
	state.foreshadow = foreshadow

	relationships, relErr := t.store.World.LoadRelationships()
	reads.require("relationship_state", relErr)
	if len(relationships) > 0 {
		envelope.Episodic["relationship_state"] = relationships
	}
	state.relationships = relationships

	allStateChanges, scErr := t.store.World.LoadStateChanges()
	reads.require("recent_state_changes", scErr)
	state.allStateChanges = allStateChanges
	if len(allStateChanges) > 0 {
		start := max(chapter-2, 1)
		var recent []domain.StateChange
		for _, c := range allStateChanges {
			if c.Chapter >= start && c.Chapter < chapter {
				recent = append(recent, c)
			}
		}
		if len(recent) > 0 {
			envelope.Episodic["recent_state_changes"] = recent
		}
	}

	styleRules, styleErr := t.store.World.LoadStyleRules()
	reads.require("style_rules", styleErr)
	state.styleRules = styleRules
	state.storyThreads = t.selectStoryThreads(state)
	if len(state.storyThreads) > 0 && len(state.storyThreads) < storyThreadRecallMinSelected {
		state.storyThreads = nil
	}

	return state
}

func (t *ContextTool) buildChapterContext(result map[string]any, state contextBuildState, reads *contextReads) {
	envelope := newChapterContextEnvelope()
	result["memory_policy"] = domain.NewChapterMemoryPolicy(state.progress, state.profile, state.currentEntry != nil)

	if state.profile.Layered {
		t.loadLayeredCharacters(envelope.Episodic, state.chapter, reads)
	} else {
		t.loadFilteredCharacters(envelope.Episodic, state.chapter, reads)
	}

	t.buildChapterEpisodicMemory(&envelope, state, reads)
	t.buildChapterWorkingMemory(&envelope, state, reads)
	t.buildChapterReferencePack(&envelope, state, reads)
	t.buildChapterSelectedMemory(&envelope, state, reads)
	t.buildStyleStats(&envelope, state, reads)
	envelope.apply(result)
}

// buildStyleStats computes whole-book style statistics over all completed chapters and injects episodic_memory.style_stats.
// The in-arc review window is naturally blind to "a sentence tic repeated dozens of times per chapter, isomorphic chapter endings, cross-chapter repetition"; only
// whole-book statistics can expose it -- the statistics belong to the code (deterministic) and the judgement to the LLM (the editor scores on the aesthetic dimension
// from the numbers, and the writer avoids it accordingly). When there are too few chapters stylestat returns nil and nothing is injected.
func (t *ContextTool) buildStyleStats(envelope *chapterContextEnvelope, state contextBuildState, reads *contextReads) {
	if state.progress == nil || len(state.progress.CompletedChapters) == 0 {
		return
	}

	var titles []string
	if outline, err := t.store.Outline.LoadOutline(); err == nil {
		for _, entry := range outline {
			titles = append(titles, entry.Title)
		}
	} else {
		reads.warn("style_stats.outline", err)
	}

	stats, err := t.styleStats.Snapshot(
		state.progress.CompletedChapters,
		titles,
		t.styleStopwords(state.cast, reads),
	)
	if err != nil {
		reads.warn("style_stats", err)
		return
	}
	if stats == nil {
		return
	}
	envelope.Episodic["style_stats"] = stats
}

// styleStopwords collects character names and aliases for phrase mining to filter -- names of characters who appear are naturally frequent and are not a style problem.
func (t *ContextTool) styleStopwords(cast []domain.CastEntry, reads *contextReads) []string {
	var words []string
	if chars, err := t.store.Characters.Load(); err == nil {
		for _, c := range chars {
			words = append(words, c.Name)
			words = append(words, c.Aliases...)
		}
	} else {
		reads.warn("style_stats.characters", err)
	}
	for _, entry := range domain.RecentCast(cast, 50) {
		words = append(words, entry.Name)
	}
	return words
}

func (t *ContextTool) buildChapterWorkingMemory(envelope *chapterContextEnvelope, state contextBuildState, reads *contextReads) {
	t.buildOutlineWindow(envelope.Working, state, reads)
	if next := findOutlineEntry(state.outline, state.chapter+1); next != nil {
		envelope.Working["next_chapter_outline"] = next
	}

	if state.profile.Layered {
		t.loadLayeredSummaries(envelope.Working, state.chapter, state.profile.SummaryWindow, reads)
		// Finale discipline: injected when this chapter belongs to an already declared finale volume, so the writer does not open a new hook near the end of the finale section
		//（a finale volume completes the book automatically once written, so foreshadowing newly planted at that point would never get a chance to be resolved）.
		if volumes, err := t.store.Outline.LoadLayeredOutline(); err == nil {
			if fv := domain.FinaleVolume(volumes); fv > 0 {
				if b, boundaryErr := t.store.Outline.CheckArcBoundary(state.chapter); boundaryErr == nil && b != nil && b.Volume == fv {
					envelope.Working["finale"] = "本卷为全书收官卷：不再新开长线或埋新伏笔，优先回收既有伏笔、收拢关系线，按大纲把故事推向终局。"
				} else {
					reads.require("arc_boundary", boundaryErr)
				}
			}
		} else {
			reads.require("layered_outline", err)
		}
	} else {
		if summaries, err := t.store.Summaries.LoadRecentSummaries(state.chapter, state.profile.SummaryWindow); err == nil && len(summaries) > 0 {
			envelope.Working["recent_summaries"] = summaries
		} else {
			reads.require("recent_summaries", err)
		}
	}

	if timeline, err := t.store.World.LoadRecentTimeline(state.chapter, state.profile.TimelineWindow); err == nil && len(timeline) > 0 {
		envelope.Working["timeline"] = timeline
	} else {
		reads.require("timeline", err)
	}

	if state.progress != nil {
		checkpoint := map[string]any{
			"in_progress_chapter": state.progress.InProgressChapter,
		}
		if len(state.progress.StrandHistory) > 0 {
			checkpoint["strand_history"] = state.progress.StrandHistory
		}
		if len(state.progress.HookHistory) > 0 {
			checkpoint["hook_history"] = state.progress.HookHistory
		}
		envelope.Working["checkpoint"] = checkpoint
	}

	if state.chapter > 1 {
		if prevText, err := t.store.Drafts.LoadChapterText(state.chapter - 1); err == nil && prevText != "" {
			runes := []rune(prevText)
			if len(runes) > 800 {
				runes = runes[len(runes)-800:]
			}
			envelope.Working["previous_tail"] = string(runes)
		} else {
			reads.require("previous_chapter", err)
		}
	}
}

// buildOutlineWindow keeps for the Writer/Editor only the outline directly relevant to the current task, instead of injecting
// the complete flat outline that grows along with the whole book. Layered mode uses the current arc; non-layered mode uses the most recent review cycle.
func (t *ContextTool) buildOutlineWindow(working map[string]any, state contextBuildState, reads *contextReads) {
	outline := state.outline
	if len(outline) == 0 {
		return
	}

	start := max(1, state.chapter-domain.ReviewInterval+1)
	end := min(state.chapter, len(outline))
	if state.profile.Layered {
		boundary, err := t.store.Outline.CheckArcBoundary(state.chapter)
		if err != nil {
			reads.require("outline_window.arc_boundary", err)
			return
		}
		if boundary == nil {
			return
		}
		start = boundary.StartChapter
		end = min(boundary.EndChapter, len(outline))
	}
	if start <= end {
		working["outline_window"] = outline[start-1 : end]
	}
}

func findOutlineEntry(outline []domain.OutlineEntry, chapter int) *domain.OutlineEntry {
	for i := range outline {
		if outline[i].Chapter == chapter {
			return &outline[i]
		}
	}
	return nil
}

func (t *ContextTool) buildChapterSelectedMemory(envelope *chapterContextEnvelope, state contextBuildState, reads *contextReads) {
	if len(state.storyThreads) > 0 {
		envelope.Selected["story_threads"] = state.storyThreads
	}
	if lessons := t.selectReviewLessons(state.chapter, reads); len(lessons) > 0 {
		envelope.Selected["review_lessons"] = lessons
	}
}

func (t *ContextTool) buildChapterEpisodicMemory(envelope *chapterContextEnvelope, state contextBuildState, reads *contextReads) {
	if len(state.foreshadow) > 0 && len(state.storyThreads) == 0 {
		envelope.Episodic["foreshadow_ledger"] = state.foreshadow
	}

	// Recall the most recently active secondary characters, so the Writer keeps the tone/positioning consistent when reintroducing an older character.
	// Not every entry is recalled (that would bloat in a long book); only the N most recently active ones, in descending LastSeenChapter order
	if recentCast := domain.RecentCast(state.cast, 15); len(recentCast) > 0 {
		simplified := make([]map[string]any, 0, len(recentCast))
		for _, e := range recentCast {
			item := map[string]any{
				"name":             e.Name,
				"first_seen":       e.FirstSeenChapter,
				"last_seen":        e.LastSeenChapter,
				"appearance_count": e.AppearanceCount,
			}
			if e.BriefRole != "" {
				item["brief_role"] = e.BriefRole
			}
			simplified = append(simplified, item)
		}
		envelope.Episodic["recent_cast"] = simplified
	}

	if state.progress != nil && state.progress.TotalChapters > 30 && state.currentEntry != nil {
		if related := t.buildRelatedChapters(
			state.chapter,
			state.currentEntry,
			state.foreshadow,
			state.relationships,
			state.allStateChanges,
			reads,
		); len(related) > 0 {
			envelope.Episodic["related_chapters"] = related
		}
	}

	if state.profile.Layered && state.progress != nil {
		pos := map[string]any{
			"volume": state.progress.CurrentVolume,
			"arc":    state.progress.CurrentArc,
		}
		if volumes, err := t.store.Outline.LoadLayeredOutline(); err == nil {
			globalCh := 1
			for _, v := range volumes {
				if v.Index == state.progress.CurrentVolume {
					pos["volume_title"] = v.Title
					pos["volume_theme"] = v.Theme
				}
				for _, arc := range v.Arcs {
					if v.Index == state.progress.CurrentVolume && arc.Index == state.progress.CurrentArc {
						pos["arc_title"] = arc.Title
						pos["arc_goal"] = arc.Goal
						if n := len(arc.Chapters); n > 0 {
							pos["arc_total_chapters"] = n
							pos["arc_chapter_index"] = state.chapter - globalCh + 1
						}
					}
					globalCh += len(arc.Chapters)
				}
			}
		} else {
			reads.require("layered_outline", err)
		}
		envelope.Episodic["position"] = pos
	}
}

func (t *ContextTool) buildChapterReferencePack(envelope *chapterContextEnvelope, state contextBuildState, reads *contextReads) {
	authorStyle, err := t.store.World.LoadAuthorRevisionStyle()
	reads.warn("author_revision_style", err)
	if authorStyle != nil && (len(authorStyle.Prose) > 0 || len(authorStyle.Dialogue) > 0 || len(authorStyle.Taboos) > 0) {
		envelope.References["author_revision_style"] = authorStyle
	}

	if state.styleRules != nil {
		envelope.References["style_rules"] = state.styleRules
	} else {
		var maxCompleted int
		if state.progress != nil {
			maxCompleted = maxCompletedChapter(state.progress.CompletedChapters)
		}
		anchors, err := t.store.Drafts.ExtractStyleAnchors(3, maxCompleted)
		reads.warn("style_anchors", err)
		if len(anchors) > 0 {
			envelope.References["style_anchors"] = anchors
		}

		if state.currentEntry != nil {
			var voiceSamples []map[string]any
			chars, err := t.store.Characters.Load()
			reads.warn("voice_samples.characters", err)
			for _, c := range chars {
				if c.Tier == "secondary" || c.Tier == "decorative" {
					continue
				}
				samples, err := t.store.Drafts.ExtractDialogue(c.Name, c.Aliases, 3, maxCompleted)
				reads.warn("voice_samples."+c.Name, err)
				if len(samples) > 0 {
					voiceSamples = append(voiceSamples, map[string]any{
						"character": c.Name,
						"samples":   samples,
					})
				}
				if len(voiceSamples) >= 5 {
					break
				}
			}
			if len(voiceSamples) > 0 {
				envelope.References["voice_samples"] = voiceSamples
			}
		}
	}

	envelope.References["references"] = t.writerReferences(state.chapter)
}

func (t *ContextTool) buildArchitectContext(result map[string]any, reads *contextReads, volume, arc int) {
	envelope := newArchitectContextEnvelope()
	result["memory_policy"] = domain.NewArchitectMemoryPolicy()
	t.buildArchitectPlanning(&envelope, reads, volume, arc)
	t.buildArchitectFoundation(&envelope, reads)
	t.buildArchitectReferences(&envelope, reads)
	envelope.apply(result)
}

func (t *ContextTool) buildArchitectPlanning(envelope *architectContextEnvelope, reads *contextReads, requestedVolume, requestedArc int) {
	runMeta, err := t.store.RunMeta.Load()
	reads.require("run_meta", err)
	if runMeta != nil && runMeta.PlanningTier != "" {
		envelope.Planning["planning_tier"] = runMeta.PlanningTier
	}
	progress, progressErr := t.store.Progress.Load()
	reads.require("progress_for_planning", progressErr)

	layered, err := t.store.Outline.LoadLayeredOutline()
	reads.require("layered_outline", err)
	if err != nil {
		return
	}
	if len(layered) > 0 {
		latestCompleted := 0
		if progress != nil {
			latestCompleted = progress.LatestCompleted()
		}
		if requestedVolume > 0 {
			_, ok := findPlanningArc(layered, requestedVolume, requestedArc)
			if !ok {
				reads.fail(fmt.Errorf("planning scope v%da%d not found", requestedVolume, requestedArc))
				return
			}
		}
		detailVolume, detailArc := planningDetailScope(layered, progress, requestedVolume, requestedArc)
		outline, detailIncluded := projectLayeredOutlineForPlanning(
			layered,
			latestCompleted,
			detailVolume,
			detailArc,
		)
		envelope.Planning["layered_outline"] = outline
		if detailIncluded {
			envelope.Planning["outline_detail"] = map[string]int{"volume": detailVolume, "arc": detailArc}
		}
		var skeletonArcs []map[string]any
		for _, v := range layered {
			for _, a := range v.Arcs {
				if !a.IsExpanded() {
					skeletonArcs = append(skeletonArcs, map[string]any{
						"volume":             v.Index,
						"arc":                a.Index,
						"title":              a.Title,
						"goal":               a.Goal,
						"estimated_chapters": a.EstimatedChapters,
					})
				}
			}
		}
		if len(skeletonArcs) > 0 {
			envelope.Planning["skeleton_arcs"] = skeletonArcs
		}
	} else {
		if requestedVolume > 0 {
			reads.fail(fmt.Errorf("planning scope requires a layered outline"))
			return
		}
		if outline, err := t.store.Outline.LoadOutline(); err == nil && len(outline) > 0 {
			envelope.Planning["outline"] = outline
		} else {
			reads.require("outline", err)
		}
	}

	var compass *domain.StoryCompass
	if c, err := t.store.Outline.LoadCompass(); err == nil && c != nil {
		compass = c
		envelope.Planning["compass"] = compass
	} else {
		reads.require("compass", err)
	}
	if volSummaries, err := t.store.Summaries.LoadAllVolumeSummaries(); err == nil && len(volSummaries) > 0 {
		envelope.Planning["volume_summaries"] = volSummaries
	} else {
		reads.require("volume_summaries", err)
	}
	// The volume summary carries the completed volumes, and the current volume's arc summaries carry the most recent actual plot. When expanding an arc, both it and
	// the skeleton goal are handed to the Architect together, letting the model decide on its own whether to keep or revise the unwritten plan.
	if progressErr == nil && progress != nil && progress.CurrentVolume > 0 {
		if arcSummaries, err := t.store.Summaries.LoadArcSummaries(progress.CurrentVolume); err == nil && len(arcSummaries) > 0 {
			envelope.Planning["arc_summaries"] = arcSummaries
		} else {
			reads.require("arc_summaries", err)
		}
	} else {
		reads.require("progress_for_arc_summaries", progressErr)
	}

	// completion_signals brings together the key facts for "should the whole book end",
	// so the architect sees the whole picture at a glance when ruling on complete_book / append_volume.
	// Scattered across progress / compass / foreshadow / layered_outline, they are easy to miss when the LLM does the arithmetic in its head.
	envelope.Planning["completion_signals"] = t.completionSignals(layered, compass, reads)
}

// planningDetailScope selects the single outline arc that carries full chapters this round. An explicit request wins;
// by default the current progress arc is used, and when the state is not yet established the first expanded arc is selected.
func planningDetailScope(volumes []domain.VolumeOutline, progress *domain.Progress, requestedVolume, requestedArc int) (int, int) {
	if requestedVolume > 0 {
		return requestedVolume, requestedArc
	}
	if progress != nil {
		if arc, ok := findPlanningArc(volumes, progress.CurrentVolume, progress.CurrentArc); ok && arc.IsExpanded() {
			return progress.CurrentVolume, progress.CurrentArc
		}
	}
	for _, volume := range volumes {
		for _, arc := range volume.Arcs {
			if arc.IsExpanded() {
				return volume.Index, arc.Index
			}
		}
	}
	return 0, 0
}

func findPlanningArc(volumes []domain.VolumeOutline, volumeIndex, arcIndex int) (*domain.ArcOutline, bool) {
	for vi := range volumes {
		if volumes[vi].Index != volumeIndex {
			continue
		}
		for ai := range volumes[vi].Arcs {
			if volumes[vi].Arcs[ai].Index == arcIndex {
				return &volumes[vi].Arcs[ai], true
			}
		}
	}
	return nil, false
}

func projectLayeredOutlineForPlanning(volumes []domain.VolumeOutline, latestCompleted, detailVolume, detailArc int) ([]planningVolumeOutline, bool) {
	projected := make([]planningVolumeOutline, 0, len(volumes))
	chapter := 1
	detailIncluded := false
	for _, volume := range volumes {
		pv := planningVolumeOutline{
			Index: volume.Index, Title: volume.Title, Theme: volume.Theme, Final: volume.Final,
			Arcs: make([]planningArcOutline, 0, len(volume.Arcs)),
		}
		for _, arc := range volume.Arcs {
			pa := planningArcOutline{
				Index: arc.Index, Title: arc.Title, Goal: arc.Goal,
				EstimatedChapters: arc.EstimatedChapters,
			}
			if len(arc.Chapters) == 0 {
				pa.Status = "skeleton"
				pv.Arcs = append(pv.Arcs, pa)
				continue
			}
			pa.StartChapter = chapter
			pa.EndChapter = chapter + len(arc.Chapters) - 1
			pa.ChapterCount = len(arc.Chapters)
			if pa.EndChapter <= latestCompleted {
				pa.Status = "completed"
			} else {
				pa.Status = "expanded"
			}
			if volume.Index == detailVolume && arc.Index == detailArc {
				pa.Chapters = arc.Chapters
				detailIncluded = true
			} else {
				pa.ChaptersOmitted = true
			}
			chapter = pa.EndChapter + 1
			pv.Arcs = append(pv.Arcs, pa)
		}
		projected = append(projected, pv)
	}
	return projected, detailIncluded
}

func (t *ContextTool) completionSignals(layered []domain.VolumeOutline, compass *domain.StoryCompass, reads *contextReads) map[string]any {
	signals := map[string]any{}
	if progress, err := t.store.Progress.Load(); progress != nil {
		signals["completed_chapters"] = len(progress.CompletedChapters)
		signals["total_word_count"] = progress.TotalWordCount
		signals["phase"] = string(progress.Phase)
	} else {
		reads.require("completion_signals.progress", err)
	}
	if len(layered) > 0 {
		signals["planned_chapters"] = len(domain.FlattenOutline(layered))
		signals["volumes_total"] = len(layered)
		if fv := domain.FinaleVolume(layered); fv > 0 {
			signals["final_volume"] = fv
		}
	}
	if compass != nil {
		if compass.EstimatedScale != "" {
			signals["compass_estimated_scale"] = compass.EstimatedScale
		}
		signals["open_threads_count"] = len(compass.OpenThreads)
	}
	if active, err := t.store.World.LoadActiveForeshadow(); err == nil {
		signals["active_foreshadow_count"] = len(active)
	} else {
		reads.require("completion_signals.foreshadow", err)
	}
	return signals
}

func (t *ContextTool) buildArchitectFoundation(envelope *architectContextEnvelope, reads *contextReads) {
	if book, err := t.store.Book.Load(); err == nil && book != nil {
		envelope.Foundation["book"] = book
	} else {
		reads.require("book", err)
	}
	if premise, err := t.store.Outline.LoadPremise(); err == nil && premise != "" {
		envelope.Foundation["premise"] = premise
		if sections := parsePremiseSections(premise); len(sections) > 0 {
			envelope.Foundation["premise_sections"] = sections
		}
		tier := domain.PlanningTier("")
		if meta, err := t.store.RunMeta.Load(); err == nil && meta != nil {
			tier = meta.PlanningTier
		} else {
			reads.require("run_meta", err)
		}
		envelope.Foundation["premise_structure"] = premiseStructure(premise, tier)
	} else {
		reads.require("premise", err)
	}

	if chars, err := t.store.Characters.Load(); err == nil && chars != nil {
		envelope.Foundation["characters"] = chars
	} else {
		reads.require("characters", err)
	}

	if snapshots, err := t.store.Characters.LoadLatestSnapshots(); err == nil && len(snapshots) > 0 {
		envelope.Foundation["character_snapshots"] = snapshots
	} else {
		reads.require("character_snapshots", err)
	}
	if rules, err := t.store.World.LoadWorldRules(); err == nil && len(rules) > 0 {
		envelope.Foundation["world_rules"] = rules
	} else {
		reads.require("world_rules", err)
	}
	if foreshadow, err := t.store.World.LoadActiveForeshadow(); err == nil && len(foreshadow) > 0 {
		envelope.Foundation["foreshadow_ledger"] = foreshadow
	} else {
		reads.require("foreshadow_ledger", err)
	}
	if status, err := t.foundationStatus(); err == nil {
		envelope.Foundation["foundation_status"] = status
	} else {
		reads.require("foundation_status", err)
	}
	// Writer feedback pool: outline deviations / suggestions persisted by commit_chapter, which must be consulted when planning the next arc / volume;
	// cleared automatically once expand_next_arc / append_volume / update_compass succeeds (already consumed).
	if fbs, err := t.store.Outline.LoadPendingOutlineFeedback(); err == nil && len(fbs) > 0 {
		envelope.Foundation["writer_feedback"] = fbs
	} else {
		reads.require("writer_feedback", err)
	}
}

func (t *ContextTool) buildArchitectReferences(envelope *architectContextEnvelope, reads *contextReads) {
	if styleRules, err := t.store.World.LoadStyleRules(); err == nil && styleRules != nil {
		envelope.References["style_rules"] = styleRules
	} else {
		reads.warn("style_rules", err)
	}

	envelope.References["references"] = t.architectReferences()
}
