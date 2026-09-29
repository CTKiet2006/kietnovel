package imp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/CTKiet2006/kietnovel/internal/domain"
)

// The closed set of story statuses (RFC §10.4).
const (
	storyOpen      = "open"
	storyClosed    = "closed"
	storyUncertain = "uncertain"
)

// synthesisSchemaVersion is folded into the RangeDigest / synthesis InputDigest; bump it when upgrading the synthesis contract to invalidate already-persisted artifacts.
// synthesizePromptVersion is folded into the synthesis InputDigest; bump it when changing the synthesis prompt, otherwise an old synthesis is still misjudged as valid.
const (
	synthesisSchemaVersion  = 3
	synthesizePromptVersion = "synthesize-v3"
	rangePromptVersion      = "range-v2" // 纳入 rangeInputDigest，改 Range prompt 时递增，否则旧区间摘要仍被误判有效
)

// ImportedArcRange / ImportedVolumeRange: synthesis returns only volume/arc ranges and never repeats every chapter (RFC §10.3).
type ImportedArcRange struct {
	Title        string `json:"title"`
	Goal         string `json:"goal"`
	StartChapter int    `json:"start_chapter"`
	EndChapter   int    `json:"end_chapter"`
}

type ImportedVolumeRange struct {
	Title string             `json:"title"`
	Theme string             `json:"theme"`
	Arcs  []ImportedArcRange `json:"arcs"`
}

// BookSynthesis is the final synthesis result: global facts + volume/arc ranges (RFC §10.3).
type BookSynthesis struct {
	Title        *string               `json:"title"`
	Synopsis     string                `json:"synopsis"`
	Premise      string                `json:"premise"`
	Characters   []domain.Character    `json:"characters"`
	WorldRules   []domain.WorldRule    `json:"world_rules"`
	Structure    []ImportedVolumeRange `json:"structure"`
	Compass      domain.StoryCompass   `json:"compass"`
	PlanningTier domain.PlanningTier   `json:"planning_tier"`
	StoryStatus  string                `json:"story_status"`
	StatusReason string                `json:"status_reason,omitempty"`
}

// RangeDigest is a consecutive range digest from the Map stage of a long book, with the output constrained to that single range (RFC §10.2).
type RangeDigest struct {
	StartChapter    int      `json:"start_chapter"`
	EndChapter      int      `json:"end_chapter"`
	Plot            string   `json:"plot"`
	Characters      []string `json:"characters,omitempty"`
	WorldFacts      []string `json:"world_facts,omitempty"`
	OpenedThreads   []string `json:"opened_threads,omitempty"`
	ResolvedThreads []string `json:"resolved_threads,omitempty"`
}

var validPlanningTiers = map[domain.PlanningTier]bool{
	domain.PlanningTierShort: true,
	domain.PlanningTierMid:   true,
	domain.PlanningTierLong:  true,
}

// planFactRanges splits the per-chapter facts into consecutive ranges by a byte budget; a short book that fits in one pass is synthesized directly as a single range (RFC §10.2).
func planFactRanges(facts []ImportedChapterFacts, budgetBytes int) [][2]int {
	if len(facts) == 0 {
		return nil
	}
	if budgetBytes <= 0 {
		return [][2]int{{0, len(facts)}}
	}
	var ranges [][2]int
	start, acc := 0, 0
	for i, f := range facts {
		size := len(compactFact(f))
		if i > start && acc+size > budgetBytes {
			ranges = append(ranges, [2]int{start, i})
			start, acc = i, 0
		}
		acc += size
	}
	ranges = append(ranges, [2]int{start, len(facts)})
	return ranges
}

// compactView is the compact view fed into synthesis: it keeps the fields needed for cross-chapter induction and contains no full text.
// character/world evidence is the observation extracted during per-chapter reverse inference specifically for whole-book synthesis, and it must be carried in --
// otherwise the synthesizer can only invent official characters and world rules out of the summaries, wasting evidence that was already extracted (RFC §9.1/§10).
type compactView struct {
	Chapter           int                     `json:"chapter"`
	Title             string                  `json:"title"`
	CoreEvent         string                  `json:"core_event"`
	Summary           string                  `json:"summary"`
	Characters        []string                `json:"characters,omitempty"`
	CharacterEvidence []ImportedCharacterFact `json:"character_evidence,omitempty"`
	WorldEvidence     []ImportedWorldFact     `json:"world_evidence,omitempty"`
}

func toCompact(f ImportedChapterFacts) compactView {
	return compactView{
		Chapter:           f.Chapter,
		Title:             f.Title,
		CoreEvent:         f.CoreEvent,
		Summary:           f.Summary,
		Characters:        f.Characters,
		CharacterEvidence: f.CharacterEvidence,
		WorldEvidence:     f.WorldEvidence,
	}
}

func compactFact(f ImportedChapterFacts) string {
	data, _ := json.Marshal(toCompact(f))
	return string(data)
}

func compactFacts(facts []ImportedChapterFacts) string {
	views := make([]compactView, len(facts))
	for i, f := range facts {
		views[i] = toCompact(f)
	}
	data, _ := json.Marshal(views)
	return string(data)
}

// Synthesize performs layered synthesis: a short book yields BookSynthesis directly, a long book yields RangeDigest first and then reduces them (RFC §10).
// bookPrompt describes the BookSynthesis contract and rangePrompt the RangeDigest contract -- the two stages have different output structures,
// so each must use its own system prompt; otherwise the model receives BookSynthesis instructions while being asked for a RangeDigest, and the instructions contradict each other.
func Synthesize(ctx context.Context, m callModel, bookPrompt, rangePrompt string, w *Workspace, facts []ImportedChapterFacts, budgetBytes, maxTokens int, prof callProfile) (*BookSynthesis, error) {
	ranges := planFactRanges(facts, budgetBytes)
	if len(ranges) <= 1 {
		return synthesizeBook(ctx, m, bookPrompt, compactFacts(facts), len(facts), maxTokens, prof)
	}
	digests := make([]RangeDigest, 0, len(ranges))
	for ri, r := range ranges {
		rangeFacts := facts[r[0]:r[1]]
		startCh, endCh := rangeFacts[0].Chapter, rangeFacts[len(rangeFacts)-1].Chapter
		want := rangeInputDigest(rangeFacts)
		rel := rangeDigestPath(startCh, endCh)
		// Persisted range digests whose InputDigest matches are reused directly, so a crash in any single range of a long book is never paid for twice (RFC §6/§10.2).
		if art, err := readArtifact[RangeDigest](w, rel); err == nil && art.InputDigest == want {
			digests = append(digests, art.Payload)
			continue
		}
		prof.step(ri+1, len(ranges), "区间摘要 %d/%d（第 %d-%d 章）...", ri+1, len(ranges), startCh, endCh)
		rd, err := callStructured[RangeDigest](ctx, m, rangeContract, rangePrompt, buildRangePayload(rangeFacts), maxTokens, prof, func(d *RangeDigest) error {
			return validateRangeDigest(d, startCh, endCh, "range digest")
		})
		if err != nil {
			return nil, fmt.Errorf("range %d-%d 综合：%w", startCh, endCh, err)
		}
		if err := writeArtifact(w, rel, want, rd); err != nil {
			return nil, fmt.Errorf("落盘 range digest：%w", err)
		}
		digests = append(digests, rd)
	}
	// Recursive Reduce: the total size of the range digests may still exceed the final synthesis input budget (this only defers #83 from "all chapters" to "all range digests").
	// Reducing layer by layer until they fit is what makes the scaling genuinely unbounded (RFC §10.2).
	digests, err := reduceToFit(ctx, m, rangePrompt, digests, budgetBytes, maxTokens, prof)
	if err != nil {
		return nil, err
	}
	data, _ := json.Marshal(digests)
	return synthesizeBook(ctx, m, bookPrompt, string(data), len(facts), maxTokens, prof)
}

// reduceToFit repeatedly groups and reduces consecutive range digests by budget until the serialized result fits the final BookSynthesis input budget.
// Each round strictly reduces the number of digests, so it must converge; a single digest is never split even when over budget (it is already the smallest semantic unit one layer down),
// and it is handed to the final call, where a resulting truncation is reported explicitly by callStructured instead of silently overflowing.
func reduceToFit(ctx context.Context, m callModel, rangePrompt string, digests []RangeDigest, budgetBytes, maxTokens int, prof callProfile) ([]RangeDigest, error) {
	round := 0
	for len(digests) > 1 {
		if budgetBytes <= 0 {
			return digests, nil
		}
		data, _ := json.Marshal(digests)
		if len(data) <= budgetBytes {
			return digests, nil
		}
		groups := groupDigestsByBudget(digests, budgetBytes)
		if len(groups) >= len(digests) {
			return digests, nil // 无法再合并（每组仅一个摘要）
		}
		round++
		merged := make([]RangeDigest, 0, len(groups))
		for gi, g := range groups {
			startCh, endCh := g[0].StartChapter, g[len(g)-1].EndChapter
			prof.step(gi+1, len(groups), "归并区间摘要（第 %d 轮 %d/%d，第 %d-%d 章）...",
				round, gi+1, len(groups), startCh, endCh)
			rd, err := callStructured[RangeDigest](ctx, m, rangeContract, rangePrompt, buildDigestReducePayload(g), maxTokens, prof, func(d *RangeDigest) error {
				return validateRangeDigest(d, startCh, endCh, "合并区间")
			})
			if err != nil {
				return nil, fmt.Errorf("合并区间 %d-%d：%w", startCh, endCh, err)
			}
			merged = append(merged, rd)
		}
		digests = merged
	}
	return digests, nil
}

func validateRangeDigest(d *RangeDigest, startChapter, endChapter int, label string) error {
	if strings.TrimSpace(d.Plot) == "" {
		return fmt.Errorf("%s plot 为空", label)
	}
	if d.StartChapter != startChapter || d.EndChapter != endChapter {
		return fmt.Errorf("%s 章范围 %d-%d 与请求 %d-%d 不符", label, d.StartChapter, d.EndChapter, startChapter, endChapter)
	}
	return nil
}

// groupDigestsByBudget splits consecutive range digests into consecutive groups by byte budget; a single digest forms its own group even when over budget.
func groupDigestsByBudget(digests []RangeDigest, budgetBytes int) [][]RangeDigest {
	var groups [][]RangeDigest
	var cur []RangeDigest
	acc := 0
	for _, d := range digests {
		b, _ := json.Marshal(d)
		if len(cur) > 0 && acc+len(b) > budgetBytes {
			groups = append(groups, cur)
			cur, acc = nil, 0
		}
		cur = append(cur, d)
		acc += len(b)
	}
	if len(cur) > 0 {
		groups = append(groups, cur)
	}
	return groups
}

// buildDigestReducePayload assembles the input for "merge several lower-layer range digests into one RangeDigest".
func buildDigestReducePayload(digests []RangeDigest) string {
	data, _ := json.Marshal(digests)
	return fmt.Sprintf("请把第 %d-%d 章的多个下层区间摘要合并为一个 RangeDigest（连续区间摘要）。下层摘要：\n%s",
		digests[0].StartChapter, digests[len(digests)-1].EndChapter, string(data))
}

// rangeDigestPath returns the relative path of a consecutive range digest artifact.
func rangeDigestPath(startChapter, endChapter int) string {
	return fmt.Sprintf("%s/%06d-%06d.json", dirRangeDigests, startChapter, endChapter)
}

// rangeInputDigest binds that consecutive range's compact facts to the Range prompt/schema version (RFC §6.3).
func rangeInputDigest(facts []ImportedChapterFacts) string {
	return Digest([]byte(fmt.Sprintf("range\x00%s\x00v%d\x00%s", rangePromptVersion, synthesisSchemaVersion, compactFacts(facts))))
}

func synthesizeBook(ctx context.Context, m callModel, systemPrompt, payload string, n, maxTokens int, prof callProfile) (*BookSynthesis, error) {
	prof.step(0, 0, "生成全书综合（作品信息/premise/characters/大纲结构）...")
	s, err := callStructured[BookSynthesis](ctx, m, synthesisContract, systemPrompt, buildBookPayload(payload, n), maxTokens, prof, func(s *BookSynthesis) error {
		return validateSynthesis(s, n)
	})
	if err != nil {
		return nil, err
	}
	// Echo the model's understanding of the whole book: this is the most important semantic output of the import and worth showing the user immediately.
	prof.step(0, 0, "模型概括全书：%s", snippet(s.Premise, 80))
	return &s, nil
}

func buildRangePayload(facts []ImportedChapterFacts) string {
	return fmt.Sprintf("请为第 %d-%d 章生成一个 RangeDigest（连续区间摘要）。逐章事实：\n%s",
		facts[0].Chapter, facts[len(facts)-1].Chapter, compactFacts(facts))
}

func buildBookPayload(inner string, n int) string {
	return fmt.Sprintf("以下是全书 %d 章的紧凑事实/区间摘要。请生成 BookSynthesis：title、synopsis、premise、characters、world_rules、卷弧范围 structure、compass、planning_tier、story_status。\n\n%s", n, inner)
}

// validateSynthesis checks the structural constraints of the synthesis result (value ranges / closed sets / spans) and does not re-judge literary quality.
func validateSynthesis(s *BookSynthesis, n int) error {
	if strings.TrimSpace(s.Synopsis) == "" {
		return fmt.Errorf("synopsis 为空")
	}
	if strings.TrimSpace(s.Premise) == "" {
		return fmt.Errorf("premise 为空")
	}
	if len(s.Characters) == 0 {
		return fmt.Errorf("characters 为空")
	}
	if !validPlanningTiers[s.PlanningTier] {
		return fmt.Errorf("planning_tier 非法：%q", s.PlanningTier)
	}
	switch s.StoryStatus {
	case storyOpen, storyClosed, storyUncertain:
	default:
		return fmt.Errorf("story_status 非法：%q", s.StoryStatus)
	}
	if strings.TrimSpace(s.Compass.EndingDirection) == "" {
		return fmt.Errorf("compass.ending_direction 为空")
	}
	return validateStructure(s.Structure, n)
}

// validateStructure checks that volume/arc ranges are consecutive, non-overlapping and cover 1..N completely (RFC §11 / invariant 5).
func validateStructure(structure []ImportedVolumeRange, n int) error {
	if len(structure) == 0 {
		return fmt.Errorf("structure 为空")
	}
	next := 1
	for vi, v := range structure {
		if len(v.Arcs) == 0 {
			return fmt.Errorf("卷[%d] %q 无弧", vi, v.Title)
		}
		for ai, a := range v.Arcs {
			if a.StartChapter != next {
				return fmt.Errorf("卷[%d]弧[%d] 起点 %d 应为 %d（须连续无缺口）", vi, ai, a.StartChapter, next)
			}
			if a.EndChapter < a.StartChapter {
				return fmt.Errorf("卷[%d]弧[%d] 范围倒置 %d..%d", vi, ai, a.StartChapter, a.EndChapter)
			}
			next = a.EndChapter + 1
		}
	}
	if next-1 != n {
		return fmt.Errorf("卷弧范围覆盖 %d 章，应为 %d 章", next-1, n)
	}
	return nil
}

// synthesisInputDigest binds the compact facts of the ordered per-chapter analysis set + the synthesis prompt/schema version (RFC §6.3 / invariant 6).
// The version is part of it, so changing the synthesis contract makes an old synthesis naturally stale and redone.
func synthesisInputDigest(facts []ImportedChapterFacts) string {
	var b strings.Builder
	b.WriteString("synthesize\x00")
	b.WriteString(synthesizePromptVersion)
	fmt.Fprintf(&b, "\x00v%d", synthesisSchemaVersion)
	for _, f := range facts {
		b.WriteByte(0)
		b.WriteString(compactFact(f))
	}
	return Digest([]byte(b.String()))
}

// Foundation is the set of official domain objects assembled from BookSynthesis + the per-chapter facts (fully validated before publishing, RFC §11).
type Foundation struct {
	Book         domain.BookMetadata
	PlanningTier domain.PlanningTier
	Premise      string
	Characters   []domain.Character
	WorldRules   []domain.WorldRule
	Volumes      []domain.VolumeOutline
	Compass      domain.StoryCompass
	Closed       bool
}

// AssembleFoundation assembles the official Foundation from the synthesis semantics + the per-chapter facts and validates it fully.
// closed is the closure fact settled by the story_status verdict; fallbackName supplies an inferred title for when the body text cannot confirm the book title.
func AssembleFoundation(s *BookSynthesis, facts []ImportedChapterFacts, closed bool, fallbackName string) (*Foundation, error) {
	n := len(facts)
	if err := validateSynthesis(s, n); err != nil {
		return nil, err
	}
	byChapter := make(map[int]ImportedChapterFacts, n)
	for _, f := range facts {
		byChapter[f.Chapter] = f
	}

	volumes := make([]domain.VolumeOutline, 0, len(s.Structure))
	for vi, v := range s.Structure {
		vol := domain.VolumeOutline{Index: vi + 1, Title: v.Title, Theme: v.Theme}
		for ai, a := range v.Arcs {
			arc := domain.ArcOutline{Index: ai + 1, Title: a.Title, Goal: a.Goal}
			for ch := a.StartChapter; ch <= a.EndChapter; ch++ {
				f, ok := byChapter[ch]
				if !ok {
					return nil, fmt.Errorf("弧范围引用不存在的章 %d", ch)
				}
				arc.Chapters = append(arc.Chapters, domain.OutlineEntry{
					Chapter: ch, Title: f.Title, CoreEvent: f.CoreEvent, Hook: f.Hook, Scenes: f.Scenes,
				})
			}
			vol.Arcs = append(vol.Arcs, arc)
		}
		volumes = append(volumes, vol)
	}
	if closed && len(volumes) > 0 {
		volumes[len(volumes)-1].Final = true
	}

	// After FlattenOutline the chapter count is N and the titles agree with the per-chapter facts (RFC §11.5).
	flat := domain.FlattenOutline(volumes)
	if len(flat) != n {
		return nil, fmt.Errorf("FlattenOutline 章数 %d != %d", len(flat), n)
	}
	for _, e := range flat {
		if e.Title != byChapter[e.Chapter].Title {
			return nil, fmt.Errorf("章 %d 标题与逐章事实不一致", e.Chapter)
		}
	}

	title := ""
	if s.Title != nil {
		title = strings.TrimSpace(*s.Title)
	}
	if title == "" {
		title = importedBookTitle(fallbackName)
	}
	return &Foundation{
		Book:         (domain.BookMetadata{Title: title, Synopsis: s.Synopsis}).Normalized(),
		PlanningTier: s.PlanningTier,
		Premise:      s.Premise,
		Characters:   s.Characters,
		WorldRules:   s.WorldRules,
		Volumes:      volumes,
		Compass:      s.Compass,
		Closed:       closed,
	}, nil
}

// importedBookTitle falls back to the source file name when the body text cannot confirm the book title, so the book info still has a definite title.
func importedBookTitle(fallbackName string) string {
	name := strings.TrimSuffix(fallbackName, ".txt")
	name = strings.TrimSuffix(name, ".md")
	if name == "" {
		name = "未命名导入"
	}
	return name
}
