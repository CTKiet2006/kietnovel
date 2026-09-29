package imp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/CTKiet2006/kietnovel/internal/domain"
	"github.com/voocel/agentcore"
)

func factsN(n int) []ImportedChapterFacts {
	out := make([]ImportedChapterFacts, n)
	for i := 0; i < n; i++ {
		out[i] = ImportedChapterFacts{
			Chapter: i + 1, Title: "第" + itoa(i+1) + "章", CoreEvent: "事件", Summary: "摘要",
			HookType: "mystery", DominantStrand: "quest",
		}
	}
	return out
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func TestValidateStructure(t *testing.T) {
	ok := []ImportedVolumeRange{{Title: "卷一", Arcs: []ImportedArcRange{{StartChapter: 1, EndChapter: 3}}}}
	if err := validateStructure(ok, 3); err != nil {
		t.Fatalf("合法结构应通过：%v", err)
	}
	gap := []ImportedVolumeRange{{Arcs: []ImportedArcRange{{StartChapter: 1, EndChapter: 2}, {StartChapter: 4, EndChapter: 5}}}}
	if err := validateStructure(gap, 5); err == nil {
		t.Fatal("缺口应拒绝")
	}
	short := []ImportedVolumeRange{{Arcs: []ImportedArcRange{{StartChapter: 1, EndChapter: 2}}}}
	if err := validateStructure(short, 3); err == nil {
		t.Fatal("未覆盖 N 应拒绝")
	}
}

func TestAssembleFoundationHappyClosed(t *testing.T) {
	facts := factsN(3)
	s := &BookSynthesis{
		Synopsis:     "无剧透简介",
		Premise:      "# 故事前提\n\n前提",
		Characters:   []domain.Character{{Name: "甲"}},
		PlanningTier: domain.PlanningTierShort,
		StoryStatus:  storyClosed,
		Compass:      domain.StoryCompass{EndingDirection: "收束"},
		Structure:    []ImportedVolumeRange{{Title: "卷一", Arcs: []ImportedArcRange{{Title: "弧一", StartChapter: 1, EndChapter: 3}}}},
	}
	f, err := AssembleFoundation(s, facts, true, "book.txt")
	if err != nil {
		t.Fatalf("组装应成功：%v", err)
	}
	if len(domain.FlattenOutline(f.Volumes)) != 3 {
		t.Fatal("展开章数应为 3")
	}
	if !f.Volumes[len(f.Volumes)-1].Final {
		t.Fatal("closed 时末卷应 Final")
	}
	if f.Book.Title != "book" || f.Book.Synopsis != "无剧透简介" {
		t.Fatalf("作品信息组装错误: %+v", f.Book)
	}
}

func TestAssembleFoundationTitleMismatch(t *testing.T) {
	facts := factsN(2)
	facts[1].Title = "" // would breaking title consistency fail the FlattenOutline validation? the title is empty but the structure is taken from facts, so it is consistent.
	// Create a real inconsistency that structural validation cannot cover: a wrong chapter count.
	s := &BookSynthesis{
		Synopsis: "无剧透简介", Premise: "# 故事前提", Characters: []domain.Character{{Name: "甲"}},
		PlanningTier: domain.PlanningTierShort, StoryStatus: storyOpen,
		Compass:   domain.StoryCompass{EndingDirection: "x"},
		Structure: []ImportedVolumeRange{{Arcs: []ImportedArcRange{{StartChapter: 1, EndChapter: 1}}}},
	}
	if _, err := AssembleFoundation(s, facts, false, "b.txt"); err == nil {
		t.Fatal("结构只覆盖 1 章而事实 2 章应拒绝")
	}
}

func TestImportedBookTitle(t *testing.T) {
	if got := importedBookTitle("我的小说.txt"); got != "我的小说" {
		t.Fatalf("应从文件名推断书名：%q", got)
	}
}

func TestPlanFactRangesSplits(t *testing.T) {
	facts := factsN(20)
	one := len(compactFact(facts[0]))
	ranges := planFactRanges(facts, one*3) // about 3 chapters per range
	if len(ranges) < 2 {
		t.Fatalf("应分多区间，得 %d", len(ranges))
	}
	if ranges[0][0] != 0 || ranges[len(ranges)-1][1] != 20 {
		t.Fatal("区间未完整覆盖")
	}
}

// TestToCompactCarriesEvidence guards #6: the character/world evidence from per-chapter reverse inference must reach the synthesis compact view,
// otherwise the synthesizer can only invent official characters and world rules out of the summaries.
func TestToCompactCarriesEvidence(t *testing.T) {
	f := ImportedChapterFacts{
		Chapter: 1, Title: "第一章", CoreEvent: "e", Summary: "s",
		CharacterEvidence: []ImportedCharacterFact{{Chapter: 1, Name: "甲", Note: "沉稳"}},
		WorldEvidence:     []ImportedWorldFact{{Chapter: 1, Category: "magic", Fact: "灵气充盈"}},
	}
	cv := toCompact(f)
	if len(cv.CharacterEvidence) != 1 || cv.CharacterEvidence[0].Name != "甲" {
		t.Fatalf("character evidence 未带入紧凑视图：%+v", cv.CharacterEvidence)
	}
	if len(cv.WorldEvidence) != 1 || cv.WorldEvidence[0].Fact != "灵气充盈" {
		t.Fatalf("world evidence 未带入紧凑视图：%+v", cv.WorldEvidence)
	}
}

// TestSynthesizeRejectsRangeMismatch guards #4: the start/end chapters of a range digest in a long book's Map stage must match the request,
// otherwise reduction would treat a misaligned range as this range's own digest.
func TestSynthesizeRejectsRangeMismatch(t *testing.T) {
	err := validateRangeDigest(&RangeDigest{StartChapter: 1, EndChapter: 5, Plot: "错位区间"}, 1, 2, "range digest")
	if err == nil {
		t.Fatal("区间起止章与请求不符应拒绝")
	}
	if !strings.Contains(err.Error(), "章范围") {
		t.Fatalf("错误应指出区间范围不符，得：%v", err)
	}
}

// TestGroupDigestsByBudget guards #3 reduction grouping: consecutive range digests are split into consecutive groups by byte budget, and a single over-budget digest still forms its own group.
func TestGroupDigestsByBudget(t *testing.T) {
	ds := []RangeDigest{
		{StartChapter: 1, EndChapter: 5, Plot: strings.Repeat("x", 200)},
		{StartChapter: 6, EndChapter: 10, Plot: strings.Repeat("y", 200)},
		{StartChapter: 11, EndChapter: 15, Plot: strings.Repeat("z", 200)},
		{StartChapter: 16, EndChapter: 20, Plot: strings.Repeat("w", 200)},
	}
	per := len(mustJSON(t, ds[0]))
	groups := groupDigestsByBudget(ds, per*2+10) // about 2 per group
	if len(groups) != 2 || len(groups[0]) != 2 || len(groups[1]) != 2 {
		t.Fatalf("应分 2 组各 2 个，得 %v", groups)
	}
	if groups[0][0].StartChapter != 1 || groups[1][1].EndChapter != 20 {
		t.Fatal("分组未保持连续覆盖")
	}
}

// TestReduceToFitMergesUntilBudget guards #3: when the range digests exceed the budget they are merged layer by layer until they fit,
// rather than flowing unbounded into the final synthesis call.
func TestReduceToFitMergesUntilBudget(t *testing.T) {
	ds := []RangeDigest{
		{StartChapter: 1, EndChapter: 5, Plot: strings.Repeat("x", 200)},
		{StartChapter: 6, EndChapter: 10, Plot: strings.Repeat("y", 200)},
		{StartChapter: 11, EndChapter: 15, Plot: strings.Repeat("z", 200)},
		{StartChapter: 16, EndChapter: 20, Plot: strings.Repeat("w", 200)},
	}
	budget := len(mustJSON(t, ds[0]))*2 + 10
	// Each group reduces to one small digest: chapters 1-10, chapters 11-20.
	m := &mockModel{responses: []string{
		rangeDigestJSON(1, 10, "合并一"),
		rangeDigestJSON(11, 20, "合并二"),
	}}
	out, err := reduceToFit(context.Background(), m, "range", ds, budget, 4096, callProfile{})
	if err != nil {
		t.Fatalf("reduceToFit: %v", err)
	}
	if len(out) != 2 || out[0].StartChapter != 1 || out[0].EndChapter != 10 || out[1].StartChapter != 11 || out[1].EndChapter != 20 {
		t.Fatalf("应归并为 2 个连续区间摘要，得 %+v", out)
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestSynthesizeDirectWithMock(t *testing.T) {
	facts := factsN(3)
	resp := synthesisFixtureJSON(3, storyOpen)
	m := &mockModel{responses: []string{resp}}
	s, err := Synthesize(context.Background(), m, "sys", "range-sys", &Workspace{dir: t.TempDir()}, facts, 0, 4096, callProfile{})
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	if s.StoryStatus != storyOpen || len(s.Structure) != 1 {
		t.Fatalf("综合结果不符：%+v", s)
	}
	if _, err := AssembleFoundation(s, facts, false, "b.txt"); err != nil {
		t.Fatalf("组装应成功：%v", err)
	}
	_ = agentcore.StopReasonStop
}
