package workbench

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	projectdoc "github.com/voocel/ainovel-cli/internal/app/project"
	"github.com/voocel/ainovel-cli/internal/domain/model"
)

// 进行中环节要点名正在处理的章：审阅与重写的都是已入稿章节，不能笼统成种类文案。
func TestOperationPhaseNamesChaptersInFlight(t *testing.T) {
	project := projectdoc.Snapshot{Manuscript: []model.ManuscriptChapter{
		{ID: "c1", PlanNodeID: "p1", Number: 1}, {ID: "c2", PlanNodeID: "p2", Number: 2}, {ID: "c3", PlanNodeID: "p3", Number: 3},
	}}
	const basis = `"basis":{"documents":[{"ref":{"kind":"manuscript","id":"c1"},"revision":3}]}`
	for _, tc := range []struct {
		kind               model.OperationKind
		input, phase, plan string
	}{
		{model.OperationReviewRange, `{"chapter_ids":["c1","c2","c3"],` + basis + `}`, "正在审阅第 1–3 章", ""},
		{model.OperationReviewRange, `{"chapter_ids":["c2"],` + basis + `}`, "正在审阅第 2 章", ""},
		{model.OperationReviewRange, `{"chapter_ids":["c1","c2","c3"],"reviewed":["c1","c3"],` + basis + `}`, "正在复审第 1–3 章", ""},
		{model.OperationRewriteChapter, `{"chapter_id":"c1","chapter_plan_id":"p1","chapter_number":1,"findings":["称呼混用"]}`, "正在按意见重写第 1 章", "p1"},
	} {
		if phase, plan := operationPhase(model.Operation{Kind: tc.kind, Input: []byte(tc.input)}, project); phase != tc.phase || plan != tc.plan {
			t.Errorf("%s: phase=%q plan=%q, want %q %q", tc.kind, phase, plan, tc.phase, tc.plan)
		}
	}
}

// 待确认方案用故事语言呈现并投进大纲：规模摘要、按卷弧章成树的大纲、人物名、设定句，
// 全程不露文档 ID；未过期的方案在大纲里标为 Proposed，过期的不投。
func TestPendingProposalReadsAsStoryAndPreviewsInOutline(t *testing.T) {
	put := func(kind model.DocumentKind, id string, value any) model.Patch {
		content, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		return model.Patch{Document: model.DocumentRef{Kind: kind, ID: id}, Operation: model.PatchPut, Content: content}
	}
	volume := model.PlanNode{ID: "plan-volume-1", Kind: model.PlanVolume, Order: 1, Title: "雨城", Summary: "旧案重启"}
	arc := model.PlanNode{ID: "plan-arc-1", Kind: model.PlanArc, ParentID: volume.ID, Order: 1, Title: "来信", Summary: "无名信"}
	chapter := model.PlanNode{ID: "plan-chapter-1", Kind: model.PlanChapter, ParentID: arc.ID, Order: 1, Title: "来信", Summary: "陈渡收到信"}
	project := projectdoc.Snapshot{
		Plan:       []model.PlanNode{volume, arc, chapter},
		Entities:   []model.Entity{{ID: "entity-hero", Kind: model.EntityCharacter, Name: "陈渡"}},
		Manuscript: []model.ManuscriptChapter{{ID: "chapter-1", PlanNodeID: chapter.ID, Number: 1, Title: "来信"}},
	}
	revised := chapter
	revised.Summary = "陈渡收到信，认出笔迹"
	newArc := model.PlanNode{ID: "plan-arc-2", Kind: model.PlanArc, ParentID: volume.ID, Order: 2, Title: "对峙", Summary: "门后的人"}
	proposal := model.Proposal{Patches: []model.Patch{
		put(model.DocumentPlan, revised.ID, revised),
		put(model.DocumentPlan, newArc.ID, newArc),
		put(model.DocumentPlan, "plan-chapter-2", model.PlanNode{ID: "plan-chapter-2", Kind: model.PlanChapter, ParentID: newArc.ID, Order: 2, Title: "门后", Summary: "声音"}),
		put(model.DocumentPlan, "plan-chapter-3", model.PlanNode{ID: "plan-chapter-3", Kind: model.PlanChapter, ParentID: newArc.ID, Order: 3, Title: "旧友", Summary: "相认"}),
		put(model.DocumentEntity, "entity-su", model.Entity{ID: "entity-su", Kind: model.EntityCharacter, Name: "苏晚", Aliases: []string{"晚晚"}}),
		put(model.DocumentCanon, "fact-su", model.CanonFact{ID: "fact-su", SubjectID: "entity-su", Predicate: "身份", Value: json.RawMessage(`"陈渡的旧友"`)}),
		put(model.DocumentCompass, model.SingletonDocumentID, model.Compass{ScaleMax: 60, Ending: "真相大白"}),
		put(model.DocumentOverlay, model.SingletonDocumentID, map[string]string{}),
	}}

	view, err := proposalView(project, proposal)
	if err != nil {
		t.Fatal(err)
	}
	wantSummary := []string{"新增 1 个故事弧 · 2 章", "修订大纲 1 处", "新增人物地点 1 个", "设定 1 条", "其他变更 1 项"}
	if !slices.Equal(view.Summary, wantSummary) {
		t.Fatalf("summary = %q, want %q", view.Summary, wantSummary)
	}
	if len(view.Outline) != 1 || len(view.Outline[0].Arcs) != 2 || view.Outline[0].Arcs[0].Chapters[0].Summary != revised.Summary ||
		len(view.Outline[0].Arcs[1].Chapters) != 2 || view.Outline[0].Arcs[1].Chapters[1].Chapter != 3 {
		t.Fatalf("outline tree = %+v", view.Outline)
	}
	if len(view.Entities) != 1 || view.Entities[0].Name != "苏晚" || view.Facts[0] != "「苏晚」身份：陈渡的旧友" ||
		view.Compass == nil || view.Compass.ScaleMax != 60 || !slices.Equal(view.Other, []string{"书级创作规则"}) {
		t.Fatalf("view = %+v", view)
	}
	encoded, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"plan-", "entity-", "fact-", "overlay"} {
		if strings.Contains(string(encoded), id) {
			t.Fatalf("view leaks document id %q: %s", id, encoded)
		}
	}

	decision := &PendingDecision{Proposal: proposal, HasProposal: true}
	plan, proposed, err := proposedPlan(project.Plan, decision)
	if err != nil {
		t.Fatal(err)
	}
	outline := buildOutline(plan, proposed, project.Manuscript, nil, "")
	var got []string
	for _, entry := range outline {
		got = append(got, fmt.Sprintf("%s#%d:%s:%t:%s", entry.Node.Title, entry.Number, entry.State, entry.Proposed, entry.Detail))
	}
	want := []string{
		"雨城#0::false:", "来信#0::false:", "来信#1:confirmed:true:",
		"对峙#0::true:", "门后#2:planned:true:方案待你确认", "旧友#3:planned:true:方案待你确认",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("outline = %q\nwant %q", got, want)
	}
	decision.Stale = true
	if plan, proposed, err = proposedPlan(project.Plan, decision); err != nil || len(plan) != 3 || len(proposed) != 0 {
		t.Fatalf("stale proposal must not project: plan=%d proposed=%d err=%v", len(plan), len(proposed), err)
	}
}
