package model

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"testing"
)

func TestValidateDocumentContentRejectsUnknownFields(t *testing.T) {
	content := json.RawMessage(`{"premise":"凡人修仙","surprise":"silent drift"}`)
	err := ValidateDocumentContent(DocumentRef{Kind: DocumentIntent, ID: "root"}, content)
	if err == nil {
		t.Fatal("unknown field accepted")
	}
}

func TestValidateDocumentContentRequiresStableID(t *testing.T) {
	content := json.RawMessage(`{"id":"arc-2","kind":"arc","parent_id":"volume-1","order":1,"title":"入门","summary":"进入宗门"}`)
	err := ValidateDocumentContent(DocumentRef{Kind: DocumentPlan, ID: "arc-1"}, content)
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("error = %v, want ErrInvalid", err)
	}
}

func TestManuscriptRejectsDuplicateBlockID(t *testing.T) {
	chapter := ManuscriptChapter{
		ID: "chapter-1", PlanNodeID: "chapter-plan-1", Number: 1, Title: "山门", Author: AuthorAI,
		Blocks: []ManuscriptBlock{{ID: "p-1", Text: "第一段"}, {ID: "p-1", Text: "第二段"}},
	}
	if err := chapter.Validate(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("error = %v, want ErrInvalid", err)
	}
}

func TestDocumentDependenciesIncludeStableParentAndPlan(t *testing.T) {
	planContent := json.RawMessage(`{
		"id":"chapter-plan-1",
		"kind":"chapter",
		"parent_id":"arc-1",
		"order":1,
		"title":"山门",
		"summary":"抵达山门",
		"depends_on":[{"kind":"canon","id":"hero-origin"}]
	}`)
	dependencies, err := DocumentDependencies(DocumentRef{Kind: DocumentPlan, ID: "chapter-plan-1"}, planContent)
	if err != nil {
		t.Fatalf("dependencies: %v", err)
	}
	keys := make([]string, len(dependencies))
	for i, dependency := range dependencies {
		keys[i] = dependency.Key()
	}
	if want := []string{"canon:hero-origin", "plan:arc-1"}; !slices.Equal(keys, want) {
		t.Fatalf("dependencies = %v, want %v", keys, want)
	}
}

// 蓝图不变量（§6.3 D63）：规划任务恰好产出请求的章节数；AI 定篇幅时罗盘必填，收官后
// 按收官章数封顶，未收官不得触及篇幅上限；罗盘只能由规划任务修改。
func TestValidateBlueprint(t *testing.T) {
	chapters := func(ids ...int) []PlanNode {
		var nodes []PlanNode
		for _, n := range ids {
			nodes = append(nodes, PlanNode{ID: fmt.Sprintf("chapter-%d", n), Kind: PlanChapter, ParentID: "arc-1", Order: n, Title: "章", Summary: "推进"})
		}
		return nodes
	}
	put := func(t *testing.T, ref DocumentRef, value any) Patch {
		t.Helper()
		content, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		return Patch{Document: ref, Operation: PatchPut, Content: content}
	}
	plans := func(t *testing.T, ids ...int) []Patch {
		var patches []Patch
		for _, node := range chapters(ids...) {
			patches = append(patches, put(t, DocumentRef{Kind: DocumentPlan, ID: node.ID}, node))
		}
		return patches
	}
	compass := func(t *testing.T, scaleMax, final int) Patch {
		return put(t, DocumentRef{Kind: DocumentCompass, ID: SingletonDocumentID}, Compass{ScaleMax: scaleMax, Ending: "称帝", Final: final})
	}
	extend := func(t *testing.T, fixed, existing, requested int) Operation {
		input, err := json.Marshal(RevisePlanInput{Intent: "逆袭", FixedChapters: fixed, ExistingChapters: existing, RequestedChapters: requested})
		if err != nil {
			t.Fatal(err)
		}
		return Operation{Kind: OperationRevisePlan, Input: input}
	}
	blueprint := func(compass *Compass, nodes ...PlanNode) story {
		s := story{plans: make(map[string]PlanNode), compass: compass}
		for _, node := range nodes {
			s.plans[node.ID] = node
		}
		return s
	}
	base := blueprint(&Compass{ScaleMax: 10, Ending: "称帝"}, chapters(1, 2, 3)...)
	chapterOp, _ := json.Marshal(WriteChapterInput{ChapterPlanID: "chapter-1", ChapterNumber: 1})
	for _, c := range []struct {
		name      string
		operation func(*testing.T) Operation
		base      story
		patches   func(*testing.T) []Patch
		ok        bool
	}{
		{"open phase adds exactly one window", func(t *testing.T) Operation { return extend(t, 0, 3, 6) }, base,
			func(t *testing.T) []Patch { return plans(t, 4, 5, 6) }, true},
		{"open phase cannot overshoot the request", func(t *testing.T) Operation { return extend(t, 0, 3, 6) }, base,
			func(t *testing.T) []Patch { return plans(t, 4, 5, 6, 7) }, false},
		{"reaching scale_max without a final is rejected", func(t *testing.T) Operation { return extend(t, 0, 3, 10) }, base,
			func(t *testing.T) []Patch { return plans(t, 4, 5, 6, 7, 8, 9, 10) }, false},
		{"raising scale_max unblocks the window", func(t *testing.T) Operation { return extend(t, 0, 3, 10) }, base,
			func(t *testing.T) []Patch { return append(plans(t, 4, 5, 6, 7, 8, 9, 10), compass(t, 20, 0)) }, true},
		{"final may cut the last window short", func(t *testing.T) Operation { return extend(t, 0, 3, 6) }, base,
			func(t *testing.T) []Patch { return append(plans(t, 4, 5), compass(t, 10, 5)) }, true},
		{"final may close on the existing chapters", func(t *testing.T) Operation { return extend(t, 0, 3, 6) }, base,
			func(t *testing.T) []Patch { return []Patch{compass(t, 10, 3)} }, true},
		{"final must be covered exactly", func(t *testing.T) Operation { return extend(t, 0, 3, 6) }, base,
			func(t *testing.T) []Patch { return append(plans(t, 4), compass(t, 10, 5)) }, false},
		{"final below the planned chapters is rejected", func(t *testing.T) Operation { return extend(t, 0, 3, 6) }, base,
			func(t *testing.T) []Patch { return []Patch{compass(t, 10, 2)} }, false},
		{"AI length requires a compass", func(t *testing.T) Operation { return extend(t, 0, 3, 6) }, blueprint(nil, chapters(1, 2, 3)...),
			func(t *testing.T) []Patch { return plans(t, 4, 5, 6) }, false},
		{"fixed length ignores the compass", func(t *testing.T) Operation { return extend(t, 6, 3, 6) }, blueprint(nil, chapters(1, 2, 3)...),
			func(t *testing.T) []Patch { return plans(t, 4, 5, 6) }, true},
		{"fixed length requires exact coverage", func(t *testing.T) Operation { return extend(t, 6, 3, 6) }, blueprint(nil, chapters(1, 2, 3)...),
			func(t *testing.T) []Patch { return plans(t, 4, 5) }, false},
		{"only planning tasks may change the compass", func(*testing.T) Operation { return Operation{Kind: OperationWriteChapter, Input: chapterOp} }, base,
			func(t *testing.T) []Patch { return []Patch{compass(t, 20, 0)} }, false},
		{"non-planning tasks cannot add chapter nodes", func(*testing.T) Operation { return Operation{Kind: OperationWriteChapter, Input: chapterOp} }, base,
			func(t *testing.T) []Patch { return plans(t, 4) }, false},
		{"non-planning tasks may edit existing nodes", func(*testing.T) Operation { return Operation{Kind: OperationWriteChapter, Input: chapterOp} }, base,
			func(t *testing.T) []Patch { return plans(t, 3) }, true},
		{"fixed length leaves the compass to the user", func(t *testing.T) Operation { return extend(t, 6, 3, 6) }, base,
			func(t *testing.T) []Patch { return append(plans(t, 4, 5, 6), compass(t, 6, 6)) }, false},
		{"deleting the compass keeps a node named root", func(t *testing.T) Operation { return extend(t, 0, 3, 6) },
			blueprint(&Compass{ScaleMax: 10, Ending: "称帝"}, append(chapters(1, 2), PlanNode{ID: "root", Kind: PlanChapter, ParentID: "arc-1", Order: 3, Title: "章", Summary: "推进"})...),
			func(t *testing.T) []Patch {
				return append(plans(t, 4, 5, 6), Patch{Document: DocumentRef{Kind: DocumentCompass, ID: SingletonDocumentID}, Operation: PatchDelete}, compass(t, 10, 0))
			}, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			patches := c.patches(t)
			projected := blueprint(c.base.compass, slices.Collect(maps.Values(c.base.plans))...)
			for _, patch := range patches {
				switch {
				case patch.Document.Kind == DocumentCompass && patch.Operation == PatchDelete:
					projected.compass = nil
				case patch.Document.Kind == DocumentCompass:
					projected.compass = new(Compass)
					if err := json.Unmarshal(patch.Content, projected.compass); err != nil {
						t.Fatal(err)
					}
				default:
					var node PlanNode
					if err := json.Unmarshal(patch.Content, &node); err != nil {
						t.Fatal(err)
					}
					projected.plans[node.ID] = node
				}
			}
			operation := c.operation(t)
			input, err := DecodeTaskInput(operation.Kind, operation.Input)
			if err != nil {
				t.Fatal(err)
			}
			err = validateBlueprint(input, c.base, projected, patches)
			if c.ok && err != nil {
				t.Fatalf("rejected: %v", err)
			}
			if !c.ok && !errors.Is(err, ErrInvalid) {
				t.Fatalf("error = %v, want ErrInvalid", err)
			}
		})
	}
}

// 规划请求：必须越过已有章数，固定篇幅时不超过全书章数；交给 AI 时不设上界（由罗盘约束）。
func TestPlanInputsValidateRequest(t *testing.T) {
	for _, c := range []struct {
		input TaskInput
		ok    bool
	}{
		{DevelopPlanInput{Intent: "逆袭", RequestedChapters: 3}, true},
		{DevelopPlanInput{Intent: "逆袭", FixedChapters: 2, RequestedChapters: 3}, false},
		{DevelopPlanInput{Intent: "逆袭"}, false},
		{RevisePlanInput{Intent: "逆袭", ExistingChapters: 3, RequestedChapters: 4}, true},
		{RevisePlanInput{Intent: "逆袭", ExistingChapters: 3, RequestedChapters: 3}, false},
		{RevisePlanInput{Intent: "逆袭", FixedChapters: 5, ExistingChapters: 3, RequestedChapters: 6}, false},
	} {
		if err := c.input.Validate(); (err == nil) != c.ok {
			t.Fatalf("%+v validate = %v, want ok %v", c.input, err, c.ok)
		}
	}
}

func TestCompassValidateAndNeverDependency(t *testing.T) {
	for _, c := range []struct {
		compass Compass
		ok      bool
	}{
		{Compass{ScaleMax: 60, Ending: "称帝"}, true},
		{Compass{ScaleMax: 60, Ending: "称帝", Final: 60}, true},
		{Compass{ScaleMax: 0, Ending: "称帝"}, false},
		{Compass{ScaleMax: 60}, false},
		{Compass{ScaleMax: 60, Ending: "称帝", Final: 61}, false},
		{Compass{ScaleMax: 60, Ending: "称帝", Final: -1}, false},
	} {
		if err := c.compass.Validate(); (err == nil) != c.ok {
			t.Fatalf("%+v validate = %v, want ok %v", c.compass, err, c.ok)
		}
	}
	node := PlanNode{ID: "chapter-1", Kind: PlanChapter, ParentID: "arc-1", Title: "章", Summary: "推进",
		DependsOn: []DocumentRef{{Kind: DocumentCompass, ID: SingletonDocumentID}}}
	if err := node.Validate(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("compass dependency error = %v, want ErrInvalid", err)
	}
}

func TestChapterPlansInOrderSortsByOrderThenID(t *testing.T) {
	plan := []PlanNode{
		{ID: "c", Kind: PlanChapter, Order: 2}, {ID: "arc", Kind: PlanArc},
		{ID: "b", Kind: PlanChapter, Order: 1}, {ID: "a", Kind: PlanChapter, Order: 2},
	}
	var ids []string
	for _, node := range ChapterPlansInOrder(plan) {
		ids = append(ids, node.ID)
	}
	if !slices.Equal(ids, []string{"b", "a", "c"}) {
		t.Fatalf("order = %v", ids)
	}
}
