package imp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"maps"
	"os"
	"slices"
	"strings"

	"github.com/CTKiet2006/kietnovel/internal/domain"
)

// analysisSchemaVersion is the per-chapter fact schema version, folded into the InputDigest.
const analysisSchemaVersion = 2

// ImportedCharacterFact / ImportedWorldFact are compact observations meant for whole-book synthesis; they are never written directly into official characters or world rules.
// They carry at least a chapter number, so the synthesis result has a stable provenance (RFC §9.1).
type ImportedCharacterFact struct {
	Chapter int    `json:"chapter"`
	Name    string `json:"name"`
	Note    string `json:"note,omitempty"`
}

type ImportedWorldFact struct {
	Chapter  int    `json:"chapter"`
	Category string `json:"category,omitempty"`
	Fact     string `json:"fact"`
}

// ImportedChapterFacts is the structured product of reverse-inferring a single chapter (RFC §9.1).
type ImportedChapterFacts struct {
	Chapter             int                        `json:"chapter"`
	Title               string                     `json:"title"`
	Summary             string                     `json:"summary"`
	KeyEvents           []string                   `json:"key_events"`
	CoreEvent           string                     `json:"core_event"`
	Hook                string                     `json:"hook,omitempty"`
	Scenes              []string                   `json:"scenes,omitempty"`
	Characters          []string                   `json:"characters,omitempty"`
	CharacterEvidence   []ImportedCharacterFact    `json:"character_evidence,omitempty"`
	WorldEvidence       []ImportedWorldFact        `json:"world_evidence,omitempty"`
	TimelineEvents      []domain.TimelineEvent     `json:"timeline_events,omitempty"`
	ForeshadowUpdates   []domain.ForeshadowUpdate  `json:"foreshadow_updates,omitempty"`
	RelationshipChanges []domain.RelationshipEntry `json:"relationship_changes,omitempty"`
	StateChanges        []domain.StateChange       `json:"state_changes,omitempty"`
	HookType            string                     `json:"hook_type"`
	DominantStrand      string                     `json:"dominant_strand"`
}

// AnalysisBatchResult is the structured return of one batch call, each element being one chapter's facts.
type AnalysisBatchResult struct {
	Chapters []ImportedChapterFacts `json:"chapters"`
}

// ChapterAnalysisPayload is the payload of a single-chapter analysis artifact; chapters of the same batch record the same BatchStart/BatchEnd.
type ChapterAnalysisPayload struct {
	BatchStart int                  `json:"batch_start"`
	BatchEnd   int                  `json:"batch_end"`
	Facts      ImportedChapterFacts `json:"facts"`
}

// AnalyzeBudget is the input/output budget pair for per-chapter analysis (RFC §9.2).
// Input approximates the context window in bytes; output approximates the completion cap with a conservative fact allowance per chapter.
type AnalyzeBudget struct {
	ContextBytes     int // input budget (body + ledger + overhead)
	MaxOutputTokens  int // visible output budget (the completion cap)
	PerChapterOutput int // conservative output reservation per chapter
	PromptOverhead   int // fixed input overhead of system/ledger (bytes)
}

func analysisPath(chapter int) string {
	return fmt.Sprintf("%s/%06d.json", dirAnalyses, chapter)
}

// analyzedChapters returns how many analysis artifacts form an unbroken run from chapter 1 whose InputDigest matches the current segmentation identity/version/body (RFC §9.6).
// A missing artifact, a parse failure or a digest mismatch truncates the count there, so an upstream change (re-segmentation, a different prompt/schema version) naturally invalidates the downstream analyses.
func analyzedChapters(w *Workspace, seg *Segmentation, normalized []byte, segIdentity, promptVersion string) int {
	n := 0
	for c := 1; c <= len(seg.Chapters); c++ {
		a, err := readArtifact[ChapterAnalysisPayload](w, analysisPath(c))
		if err != nil {
			break
		}
		if a.InputDigest != chapterInputDigest(segIdentity, promptVersion, seg, normalized, c-1) {
			break
		}
		n++
	}
	return n
}

// analyzedChaptersStrict shares the freshness semantics of analyzedChapters, but it surfaces corrupt or unreadable
// existing artifacts. State recovery uses the strict variant so that a real read error is never mistaken for "not analyzed yet" and then overwritten by a redo.
func analyzedChaptersStrict(w *Workspace, seg *Segmentation, normalized []byte, segIdentity, promptVersion string) (int, error) {
	n := 0
	for c := 1; c <= len(seg.Chapters); c++ {
		a, err := readArtifact[ChapterAnalysisPayload](w, analysisPath(c))
		if os.IsNotExist(err) {
			break
		}
		if err != nil {
			return n, fmt.Errorf("读取第 %d 章分析工件: %w", c, err)
		}
		if a.InputDigest != chapterInputDigest(segIdentity, promptVersion, seg, normalized, c-1) {
			break
		}
		n++
	}
	return n, nil
}

// discardAnalysesAfter deletes the per-chapter analysis artifacts whose chapter number is > keep, which is what makes "re-analyzing one chapter invalidates every analysis after it" hold (#4a).
// During normal forward analysis there are no artifacts after keep anyway, making it an idempotent no-op; it only clears the stale tail when re-analyzing mid-way (past the fresh prefix).
// A delete failure must propagate: this is the only enforcement point of that invariant, and swallowing the error lets the stale tail (whose per-chapter digests always match)
// be reused as if it were a fresh prefix, and synthesis would then consume a mishmash of old and new facts with no error raised at all.
func discardAnalysesAfter(w *Workspace, keep, total int) error {
	for c := keep + 1; c <= total; c++ {
		if err := os.Remove(w.path(analysisPath(c))); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("清理陈旧分析工件 %s：%w", analysisPath(c), err)
		}
	}
	return nil
}

// loadPriorFacts reads the already-persisted facts of chapters 1..count, for building the ledger.
func loadPriorFacts(w *Workspace, count int) []ImportedChapterFacts {
	var out []ImportedChapterFacts
	for c := 1; c <= count; c++ {
		a, err := readArtifact[ChapterAnalysisPayload](w, analysisPath(c))
		if err != nil {
			break
		}
		out = append(out, a.Payload.Facts)
	}
	return out
}

func loadPriorFactsStrict(w *Workspace, count int) ([]ImportedChapterFacts, error) {
	out := make([]ImportedChapterFacts, 0, count)
	for c := 1; c <= count; c++ {
		a, err := readArtifact[ChapterAnalysisPayload](w, analysisPath(c))
		if err != nil {
			return out, fmt.Errorf("读取第 %d 章分析事实: %w", c, err)
		}
		out = append(out, a.Payload.Facts)
	}
	return out, nil
}

// buildLedger derives compact continuity context from the analyzed chapters: character aliases + active foreshadow IDs + latest states.
func buildLedger(prior []ImportedChapterFacts) string {
	if len(prior) == 0 {
		return ""
	}
	names := map[string]bool{}
	active := map[string]string{} // foreshadow id -> desc
	var recent []string
	for _, f := range prior {
		for _, c := range f.Characters {
			names[c] = true
		}
		for _, fu := range f.ForeshadowUpdates {
			switch fu.Action {
			case "plant", "advance":
				if fu.Description != "" {
					active[fu.ID] = fu.Description
				} else if _, ok := active[fu.ID]; !ok {
					active[fu.ID] = ""
				}
			case "resolve":
				delete(active, fu.ID)
			}
		}
	}
	if len(prior) > 0 {
		last := prior[len(prior)-1]
		for _, sc := range last.StateChanges {
			recent = append(recent, fmt.Sprintf("%s.%s=%s", sc.Entity, sc.Field, sc.NewValue))
		}
	}
	var b strings.Builder
	if len(names) > 0 {
		b.WriteString("已知人物：")
		b.WriteString(strings.Join(slices.Sorted(maps.Keys(names)), "、"))
		b.WriteString("\n")
	}
	if len(active) > 0 {
		b.WriteString("活跃伏笔（复用 ID，勿新造）：\n")
		for _, id := range slices.Sorted(maps.Keys(active)) {
			fmt.Fprintf(&b, "- %s：%s\n", id, active[id])
		}
	}
	if len(recent) > 0 {
		b.WriteString("最近状态：")
		b.WriteString(strings.Join(recent, "；"))
		b.WriteString("\n")
	}
	return b.String()
}

// planBatch returns the end of a consecutive batch starting at chapter start, honouring the input/output budget pair ([start,end), chapter index 0-based).
// At least 1 chapter; a single chapter forms its own batch even when over budget, and the executor reports insufficient capacity on truncation (RFC §9.2).
func planBatch(chapters []ChapterSpan, start, ledgerBytes int, b AnalyzeBudget) int {
	end := start + 1
	if b.ContextBytes <= 0 || b.MaxOutputTokens <= 0 || b.PerChapterOutput <= 0 {
		return end // budget not configured: one chapter at a time
	}
	inAcc := ledgerBytes + b.PromptOverhead + chapterBytes(chapters, start)
	outAcc := b.PerChapterOutput
	for end < len(chapters) {
		cb := chapterBytes(chapters, end)
		if inAcc+cb > b.ContextBytes {
			break
		}
		if outAcc+b.PerChapterOutput > b.MaxOutputTokens {
			break
		}
		inAcc += cb
		outAcc += b.PerChapterOutput
		end++
	}
	return end
}

func chapterBytes(chapters []ChapterSpan, i int) int {
	return chapters[i].End - chapters[i].Start
}

// chapterInputDigest binds the analysis artifact identity per chapter: segmentation identity + prompt/schema version + chapter number + that chapter's body.
// Per-chapter rather than per-batch binding -- the batch split is an execution detail that shifts with model capability, and swapping models must not invalidate every analyzed chapter at once;
// binding segIdentity (the segmentation artifact's InputDigest) ensures that after re-segmentation every analysis naturally mismatches (RFC §9.1/§6.3).
func chapterInputDigest(segIdentity, promptVersion string, seg *Segmentation, normalized []byte, i int) string {
	var b strings.Builder
	b.WriteString("analyze\x00")
	b.WriteString(promptVersion)
	fmt.Fprintf(&b, "\x00v%d\x00", analysisSchemaVersion)
	b.WriteString(segIdentity)
	fmt.Fprintf(&b, "\x00ch%d\x00", seg.Chapters[i].Number)
	b.WriteString(seg.Content(normalized, i))
	return Digest([]byte(b.String()))
}

// validateBatch checks in two layers: batch-level contiguity with no gaps or duplicates, and per-chapter value ranges and references (RFC §9.4).
func validateBatch(r *AnalysisBatchResult, seg *Segmentation, start, end int) error {
	want := end - start
	if len(r.Chapters) != want {
		return fmt.Errorf("批次章节数 %d != 预期 %d", len(r.Chapters), want)
	}
	for i, f := range r.Chapters {
		want := seg.Chapters[start+i]
		if f.Chapter != want.Number {
			return fmt.Errorf("批次第 %d 项章号 %d != %d", i, f.Chapter, want.Number)
		}
		if strings.TrimSpace(f.Summary) == "" || strings.TrimSpace(f.CoreEvent) == "" {
			return fmt.Errorf("章 %d summary/core_event 不能为空", f.Chapter)
		}
		if !domain.ValidHookType(strings.ToLower(f.HookType)) {
			return fmt.Errorf("章 %d hook_type 非法：%q", f.Chapter, f.HookType)
		}
		if !domain.ValidDominantStrand(strings.ToLower(f.DominantStrand)) {
			return fmt.Errorf("章 %d dominant_strand 非法：%q", f.Chapter, f.DominantStrand)
		}
		for j, fu := range f.ForeshadowUpdates {
			if fu.Action == "plant" && strings.TrimSpace(fu.Description) == "" {
				return fmt.Errorf("章 %d foreshadow[%d] plant 需 description", f.Chapter, j)
			}
		}
		// The enum is validated lowercase, so it is persisted lowercase: commit_chapter does not re-validate enums, and a case variant would go straight into official state
		// (consumers such as HookHistory match the exact string and would treat a variant as an unknown type), so a value that passes validation is normalized.
		r.Chapters[i].HookType = strings.ToLower(f.HookType)
		r.Chapters[i].DominantStrand = strings.ToLower(f.DominantStrand)
	}
	return nil
}

// AnalyzeNext assembles one batch starting from the first missing analysis, persists it atomically, and returns how many chapters it covered.
// Truncation means "fail + shrink and regroup the batch" (the default, §9.5); once the batch has shrunk to a single chapter and is still truncated, insufficient capacity is reported explicitly.
func AnalyzeNext(ctx context.Context, m callModel, systemPrompt string, w *Workspace, normalized []byte, seg *Segmentation, segIdentity, promptVersion string, budget AnalyzeBudget, prof callProfile) (int, error) {
	total := len(seg.Chapters)
	start := analyzedChapters(w, seg, normalized, segIdentity, promptVersion)
	if start >= total {
		return 0, nil
	}
	ledger := buildLedger(loadPriorFacts(w, start))
	end := planBatch(seg.Chapters, start, len(ledger), budget)

	for {
		payload := buildAnalyzePayload(normalized, seg, ledger, start, end)
		res, err := callStructured[AnalysisBatchResult](ctx, m, analysisContract, systemPrompt, payload, budget.MaxOutputTokens, prof, func(r *AnalysisBatchResult) error {
			return validateBatch(r, seg, start, end)
		})
		if err != nil {
			var tr *errTruncated
			if errors.As(err, &tr) {
				// On truncation, first salvage the longest valid consecutive prefix starting at the batch's first chapter; the already committed part is not redone (§9.5).
				if salvaged := salvagePrefix(tr.Raw, seg, start); len(salvaged) > 0 {
					for i, f := range salvaged {
						ch := start + i + 1
						digest := chapterInputDigest(segIdentity, promptVersion, seg, normalized, start+i)
						art := ChapterAnalysisPayload{BatchStart: start + 1, BatchEnd: end, Facts: f}
						if werr := writeArtifact(w, analysisPath(ch), digest, art); werr != nil {
							return i, fmt.Errorf("落盘打捞章 %d：%w", ch, werr)
						}
					}
					w.writeFailure(FailureMeta{Stage: "analyze", Detail: fmt.Sprintf("批次 %d-%d 长度截断", start+1, end),
						StopReason: "length", PrefixSalvage: fmt.Sprintf("available:%d", len(salvaged))}, tr.Raw)
					prof.logger().Info("imp 分析截断，打捞连续前缀", "batch_start", start+1, "salvaged", len(salvaged))
					echoChapterFacts(prof, salvaged)
					return len(salvaged), nil
				}
				// No salvageable prefix: record it as unavailable and "fail + shrink and regroup the batch"; a single chapter still truncated reports insufficient capacity.
				w.writeFailure(FailureMeta{Stage: "analyze", Detail: fmt.Sprintf("批次 %d-%d 长度截断，无可打捞前缀", start+1, end),
					StopReason: "length", PrefixSalvage: "unavailable"}, tr.Raw)
				if end-start > 1 {
					prof.logger().Warn("imp 分析截断，缩小重组批", "batch", fmt.Sprintf("%d-%d", start+1, end), "prefix_salvage", "unavailable")
					end = start + (end-start)/2
					// A progress line with no Key: it both shows the user the batch-shrinking action and keeps the backoff lines of
					// two independent calls from being wrongly merged under the same Key (the Key contract only covers transient backoff within one call).
					prof.step(0, 0, "输出被长度截断且无可打捞前缀，缩小批次为第 %d-%d 章重试", start+1, end)
					continue
				}
				return 0, fmt.Errorf("章 %d 单章批次仍被长度截断，模型可见输出能力不足", start+1)
			}
			return 0, err
		}
		for i, f := range res.Chapters {
			ch := start + i + 1
			digest := chapterInputDigest(segIdentity, promptVersion, seg, normalized, start+i)
			payloadArt := ChapterAnalysisPayload{BatchStart: start + 1, BatchEnd: end, Facts: f}
			if err := writeArtifact(w, analysisPath(ch), digest, payloadArt); err != nil {
				return i, fmt.Errorf("落盘章 %d 分析：%w", ch, err)
			}
		}
		echoChapterFacts(prof, res.Chapters)
		return end - start, nil
	}
}

// echoChapterFacts echoes the model's core understanding of each chapter to the panel -- the user should see what the model understood,
// not just a mechanical batch count (§14.1).
func echoChapterFacts(prof callProfile, facts []ImportedChapterFacts) {
	for _, f := range facts {
		prof.step(0, 0, "第 %d 章〈%s〉：%s", f.Chapter, snippet(f.Title, 24), snippet(f.CoreEvent, 60))
	}
}

// buildAnalyzePayload assembles the batch input: the raw text of consecutive chapters + the ledger from before the batch.
func buildAnalyzePayload(normalized []byte, seg *Segmentation, ledger string, start, end int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "请分析第 %d-%d 章，返回 {\"chapters\":[每章一个事实对象]}，数组顺序与章号一致。\n\n", start+1, end)
	if ledger != "" {
		b.WriteString("## 连续性 ledger（参考）\n\n")
		b.WriteString(ledger)
		b.WriteString("\n")
	}
	for i := start; i < end; i++ {
		c := seg.Chapters[i]
		fmt.Fprintf(&b, "## 第 %d 章：%s\n\n", c.Number, c.Title)
		b.WriteString(seg.Content(normalized, i))
		b.WriteString("\n\n---\n\n")
	}
	return b.String()
}

// salvagePrefix parses the longest valid consecutive prefix out of a length-truncated batch response (RFC §9.5).
// It keeps only the objects that are consecutive from the batch's first chapter and pass per-chapter validation; it stops at the first incomplete/invalid/skipped-number object and does not interpret the bytes after that.
// A pure function, called first by AnalyzeNext on a capacity truncation so that already fully generated prefix chapters are not discarded.
func salvagePrefix(raw string, seg *Segmentation, start int) []ImportedChapterFacts {
	arr := extractChaptersArray(raw)
	if arr == "" {
		return nil
	}
	dec := json.NewDecoder(strings.NewReader(arr))
	if _, err := dec.Token(); err != nil { // consume the '['
		return nil
	}
	var out []ImportedChapterFacts
	for dec.More() {
		var f ImportedChapterFacts
		if err := dec.Decode(&f); err != nil {
			break // the first incomplete object, stop
		}
		idx := start + len(out)
		if idx >= len(seg.Chapters) || f.Chapter != seg.Chapters[idx].Number {
			break // chapter number skipped or out of range
		}
		one := AnalysisBatchResult{Chapters: []ImportedChapterFacts{f}}
		if err := validateBatch(&one, seg, idx, idx+1); err != nil {
			break
		}
		out = append(out, one.Chapters[0]) // validateBatch has already normalised the enums in place, so take the validated value
	}
	return out
}

// extractChaptersArray slices out the JSON array text following "chapters" (the tail may be truncated).
func extractChaptersArray(raw string) string {
	i := strings.Index(raw, "\"chapters\"")
	if i < 0 {
		return ""
	}
	j := strings.IndexByte(raw[i:], '[')
	if j < 0 {
		return ""
	}
	return raw[i+j:]
}
