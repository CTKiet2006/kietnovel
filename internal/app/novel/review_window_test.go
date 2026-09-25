package novel

import (
	"slices"
	"strings"
	"testing"

	projectdoc "github.com/voocel/ainovel-cli/internal/app/project"
	"github.com/voocel/ainovel-cli/internal/domain/creation"
	"github.com/voocel/ainovel-cli/internal/domain/model"
)

// requirementFixture 在小说快照上加意图条目与各类作用域的要求。
func requirementFixture(t *testing.T, plans, written int) projectdoc.Snapshot {
	t.Helper()
	project := novelFixture(t, plans, written)
	project.Intent.Required = []string{"主角登场"}
	project.Intent.Forbidden = []string{"感情线"}
	project.Intent.EndingDirection = "称帝"
	project.Directives = []model.Directive{
		{ID: "d-arc", Scope: "plan_node:arc-1", Text: "弧一压住节奏", Status: model.DirectiveActive},
		{ID: "d-empty", Scope: "plan_node:arc-9", Text: "弧九换视角", Status: model.DirectiveActive},
		{ID: "d-late", Scope: "from_chapter:9", Text: "第九章起换地图", Status: model.DirectiveActive},
		{ID: "d-range", Scope: "chapter_range:2-3", Text: "第二三章下雨", Status: model.DirectiveActive},
	}
	return project
}

func check(id, status string) model.RequirementCheck {
	return model.RequirementCheck{ID: id, Status: status}
}

func requirementEnds(list []requirement, _ []model.Directive) map[string]int {
	ends := make(map[string]int, len(list))
	for _, r := range list {
		ends[r.ID] = r.end
	}
	return ends
}

// 作用域末章：章号作用域立即确定；plan_node 作用域要等蓝图覆盖全书；全书内没有章节的跳过。
// 全书章数未知（D63）：全书与起始章作用域末章未定、照样投递，章号区间自带末章，没有收官要求。
func TestRequirementScopesAndEnds(t *testing.T) {
	assertEnds := func(t *testing.T, got, want map[string]int) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatalf("ends = %v, want %v", got, want)
		}
		for id, end := range want {
			if value, ok := got[id]; !ok || value != end {
				t.Fatalf("%s end = %d (present %v), want %d; all = %v", id, value, ok, end, got)
			}
		}
	}
	project := requirementFixture(t, 3, 3)
	assertEnds(t, requirementEnds(requirements(project, 5)), map[string]int{
		"intent:required:0": 5, "intent:forbidden:0": 5, finaleRequirement(project, 5).ID: 5,
		"directive:d-arc": 0, "directive:d-empty": 0, "directive:d-range": 3,
	})
	assertEnds(t, requirementEnds(requirements(project, 0)), map[string]int{
		"intent:required:0": 0, "intent:forbidden:0": 0,
		"directive:d-arc": 0, "directive:d-empty": 0, "directive:d-late": 0, "directive:d-range": 3,
	})
	// 作用域落在全书之外的用户要求单列出来，由推导器按篇幅来源处理。
	if _, dropped := requirements(project, 5); len(dropped) != 1 || dropped[0].ID != "d-late" {
		t.Fatalf("dropped = %+v, want d-late", dropped)
	}
	complete := requirementEnds(requirements(requirementFixture(t, 5, 3), 5))
	if complete["directive:d-arc"] != 5 {
		t.Fatalf("complete blueprint must settle the plan-scoped end: %v", complete)
	}
	if _, ok := complete["directive:d-empty"]; ok {
		t.Fatalf("plan-scoped requirement without chapters must be skipped once the blueprint is complete: %v", complete)
	}
}

func requirementWant(t *testing.T, work creation.WorkItem, settle map[string]bool) {
	t.Helper()
	input := work.Input.(model.ReviewRangeInput)
	got := make(map[string]bool, len(input.Requirements))
	for _, r := range input.Requirements {
		got[r.ID] = r.Settle
	}
	if len(got) != len(settle) {
		t.Fatalf("requirements = %+v, want settle %v", input.Requirements, settle)
	}
	for id, want := range settle {
		if value, ok := got[id]; !ok || value != want {
			t.Fatalf("requirement %s settle = %v (present %v), want %v; all = %+v", id, value, ok, want, input.Requirements)
		}
	}
}

// Settle 规则：禁止项每窗必判；其余要求由作用域末章所在窗口兑现，已在别处满足的不强迫末窗重判。
func TestReviewWindowSettlesDueRequirements(t *testing.T) {
	run := testRun(5, 2)
	first := []string{"chapter-i", "chapter-ii", "chapter-iii"}
	last := []string{"chapter-iiii", "chapter-iiiii"}
	firstChecks := func(required string) []model.RequirementCheck {
		return []model.RequirementCheck{
			check("intent:required:0", required), check("intent:forbidden:0", model.CheckSatisfied),
			check("directive:d-arc", model.CheckSatisfied), check("directive:d-range", model.CheckSatisfied),
		}
	}
	project := requirementFixture(t, 5, 5)
	finale := finaleRequirement(project, 5).ID

	t.Run("first window delivers what it can see", func(t *testing.T) {
		next, err := Policy{}.Next(project, run, Evidence{})
		if err != nil || next.Work == nil {
			t.Fatalf("next = %#v, %v", next, err)
		}
		requirementWant(t, *next.Work, map[string]bool{
			"intent:required:0": false, "intent:forbidden:0": true, "directive:d-arc": false, "directive:d-range": true,
		})
	})
	t.Run("final window settles what is still open", func(t *testing.T) {
		evidence := Evidence{Verdicts: []StoredVerdict{storedTestVerdict("review-a", 1, model.ReviewPass, first, nil, firstChecks(model.CheckPending)...)}}
		next, err := Policy{}.Next(project, run, evidence)
		if err != nil || next.Work == nil || next.Work.Input.(model.ReviewRangeInput).ChapterIDs[0] != "chapter-iiii" {
			t.Fatalf("next = %#v, %v", next, err)
		}
		requirementWant(t, *next.Work, map[string]bool{
			"intent:required:0": true, "intent:forbidden:0": true, finale: true, "directive:d-arc": false,
		})
	})
	t.Run("already satisfied elsewhere is not forced on the final window", func(t *testing.T) {
		evidence := Evidence{Verdicts: []StoredVerdict{storedTestVerdict("review-a", 1, model.ReviewPass, first, nil, firstChecks(model.CheckSatisfied)...)}}
		next, err := Policy{}.Next(project, run, evidence)
		if err != nil || next.Work == nil {
			t.Fatalf("next = %#v, %v", next, err)
		}
		requirementWant(t, *next.Work, map[string]bool{
			"intent:required:0": false, "intent:forbidden:0": true, finale: true, "directive:d-arc": false,
		})
	})

	lastChecks := []model.RequirementCheck{
		check("intent:required:0", model.CheckPending), check("intent:forbidden:0", model.CheckSatisfied),
		check(finale, model.CheckSatisfied), check("directive:d-arc", model.CheckSatisfied),
	}
	t.Run("covered but unmet requirement re-reviews its closing window", func(t *testing.T) {
		evidence := Evidence{Verdicts: []StoredVerdict{
			storedTestVerdict("review-a", 1, model.ReviewPass, first, nil, firstChecks(model.CheckPending)...),
			storedTestVerdict("review-b", 2, model.ReviewPass, last, nil, lastChecks...),
		}}
		next, err := Policy{}.Next(project, run, evidence)
		if err != nil || next.Work == nil || next.Work.Kind != model.OperationReviewRange {
			t.Fatalf("next = %#v, %v", next, err)
		}
		input := next.Work.Input.(model.ReviewRangeInput)
		if !slices.Equal(input.ChapterIDs, last) {
			t.Fatalf("settle review window = %v, want %v", input.ChapterIDs, last)
		}
		requirementWant(t, *next.Work, map[string]bool{
			"intent:required:0": true, "intent:forbidden:0": true, finale: false, "directive:d-arc": false,
		})
		// ID 标识完整输入：必须下结论的再审与先前的窗口审阅不撞 ID。
		earlier := slices.Clone(input.Requirements)
		for i := range earlier {
			earlier[i].Settle = earlier[i].ID == "intent:forbidden:0"
		}
		if next.Work.ID == reviewOperationID(run.ID, last, 2, earlier) {
			t.Fatal("settle review must not reuse the earlier review id")
		}
	})
	// 复核反例：同窗后来又审出 pending，更早的 satisfied 仍是有效证据（正文未变），不再二次派发末窗。
	t.Run("satisfied evidence is monotonic", func(t *testing.T) {
		evidence := Evidence{Verdicts: []StoredVerdict{
			storedTestVerdict("review-a", 1, model.ReviewPass, first, nil, firstChecks(model.CheckSatisfied)...),
			storedTestVerdict("review-c", 3, model.ReviewPass, first, nil, firstChecks(model.CheckPending)...),
			storedTestVerdict("review-b", 2, model.ReviewPass, last, nil, lastChecks...),
		}}
		assertStep(t, project, run, evidence, stepWant{done: "全书 5 章完成并通过审阅"})
	})
}

// 扩窗携带仍待兑现的要求：已兑现的、禁止项、收官要求与作用域已收尾的都不带。
func TestExtendCarriesPendingRequirements(t *testing.T) {
	project := requirementFixture(t, 3, 3)
	first := []string{"chapter-i", "chapter-ii", "chapter-iii"}
	evidence := Evidence{Verdicts: []StoredVerdict{storedTestVerdict("review-a", 1, model.ReviewPass, first, nil,
		check("intent:required:0", model.CheckPending), check("intent:forbidden:0", model.CheckSatisfied),
		check("directive:d-arc", model.CheckSatisfied), check("directive:d-range", model.CheckSatisfied),
	)}}
	assertStep(t, project, testRun(5, 1), evidence, stepWant{
		kind: model.OperationRevisePlan, id: "run:book:1:plan:extend:3:r5:f5",
		check: func(t *testing.T, work creation.WorkItem) {
			got := work.Input.(model.RevisePlanInput).PendingRequirements
			if !slices.Equal(got, []string{"主角登场", "弧九换视角（作用域 plan_node:arc-9）"}) {
				t.Fatalf("pending requirements = %v", got)
			}
		},
	})
}

// 审阅基线钉住窗口正文与衔接章，不钉 Canon（D62）：事实原地更新不会废掉旧窗口。
func TestReviewBasisPinsSeamButNotCanon(t *testing.T) {
	project := novelFixture(t, 5, 5)
	keys := func(chapters []string) []string {
		basis, err := ReviewBasis(project, chapters)
		if err != nil {
			t.Fatal(err)
		}
		var refs []string
		for _, document := range basis.Documents {
			refs = append(refs, document.Ref.Key())
			if document.Ref.Kind == model.DocumentCanon {
				t.Fatalf("review basis pins canon %s", document.Ref.Key())
			}
		}
		return refs
	}
	if refs := keys([]string{"chapter-iiii", "chapter-iiiii"}); !slices.Contains(refs, "manuscript:chapter-iii") {
		t.Fatalf("later window must pin its seam chapter: %v", refs)
	}
	manuscripts := 0
	for _, key := range keys([]string{"chapter-i", "chapter-ii", "chapter-iii"}) {
		if strings.HasPrefix(key, "manuscript:") {
			manuscripts++
		}
	}
	if manuscripts != 3 {
		t.Fatalf("first window has no seam, pinned %d manuscripts", manuscripts)
	}
}
