package model

import (
	"encoding/json"
	"testing"
)

func TestIsMilestone(t *testing.T) {
	volume, err := json.Marshal(PlanNode{ID: "volume-2", Kind: PlanVolume, Title: "远行", Summary: "进入新阶段"})
	if err != nil {
		t.Fatalf("marshal volume: %v", err)
	}
	chapter, err := json.Marshal(PlanNode{ID: "chapter-plan-2", Kind: PlanChapter, ParentID: "arc-1", Title: "第二章", Summary: "继续前行"})
	if err != nil {
		t.Fatalf("marshal chapter: %v", err)
	}
	if !IsMilestone(Proposal{Patches: []Patch{{
		Document: DocumentRef{Kind: DocumentPlan, ID: "volume-2"}, Operation: PatchPut, Content: volume,
	}}}) {
		t.Fatal("volume change was not classified as a milestone")
	}
	if IsMilestone(Proposal{Patches: []Patch{{
		Document: DocumentRef{Kind: DocumentPlan, ID: "chapter-plan-2"}, Operation: PatchPut, Content: chapter,
	}}}) {
		t.Fatal("chapter-only plan change was classified as a milestone")
	}
	if !IsMilestone(Proposal{Patches: []Patch{{
		Document: DocumentRef{Kind: DocumentCanon, ID: "hero-state"}, Operation: PatchDelete,
	}}}) {
		t.Fatal("canon change was not classified as a milestone")
	}

	// 例行推进：章节正文与随章 Canon Delta 不构成 milestone，否则中间档塌缩为 manual。
	chapterContent, err := json.Marshal(ManuscriptChapter{
		ID: "chapter-2", PlanNodeID: "chapter-plan-2", Number: 2, Title: "第二章", Author: AuthorAI,
		Blocks: []ManuscriptBlock{{ID: "p-1", Text: "旅程继续。"}},
	})
	if err != nil {
		t.Fatalf("marshal chapter content: %v", err)
	}
	routineDelta, err := json.Marshal(CanonFact{
		ID: "hero-position", Kind: CanonState, SubjectID: "hero", Predicate: "state.position",
		Value: json.RawMessage(`"官道"`), SourceChapterID: "chapter-2",
	})
	if err != nil {
		t.Fatalf("marshal routine delta: %v", err)
	}
	if IsMilestone(Proposal{Patches: []Patch{
		{Document: DocumentRef{Kind: DocumentManuscript, ID: "chapter-2"}, Operation: PatchPut, Content: chapterContent},
		{Document: DocumentRef{Kind: DocumentCanon, ID: "hero-position"}, Operation: PatchPut, Content: routineDelta},
	}}) {
		t.Fatal("routine chapter canon delta was classified as a milestone")
	}
	// 不随章的独立 Canon 修改仍是 milestone。
	standalone, err := json.Marshal(CanonFact{
		ID: "world-rule-1", Kind: CanonWorldRule, SubjectID: "world", Predicate: "rule.magic",
		Value: json.RawMessage(`"灵气复苏"`),
	})
	if err != nil {
		t.Fatalf("marshal standalone canon: %v", err)
	}
	if !IsMilestone(Proposal{Patches: []Patch{{
		Document: DocumentRef{Kind: DocumentCanon, ID: "world-rule-1"}, Operation: PatchPut, Content: standalone,
	}}}) {
		t.Fatal("standalone canon change was not classified as a milestone")
	}
}
