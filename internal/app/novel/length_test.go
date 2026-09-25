package novel

import (
	"encoding/json"
	"strings"
	"testing"

	projectdoc "github.com/voocel/ainovel-cli/internal/app/project"
	"github.com/voocel/ainovel-cli/internal/domain/creation"
	"github.com/voocel/ainovel-cli/internal/domain/model"
)

func withCompass(project projectdoc.Snapshot, scaleMax, final int) projectdoc.Snapshot {
	project.Compass = &model.Compass{ScaleMax: scaleMax, Ending: "称帝", Final: final}
	return project
}

func extendTo(requested int, id string) stepWant {
	return stepWant{kind: model.OperationRevisePlan, id: id, check: func(t *testing.T, work creation.WorkItem) {
		if input := work.Input.(model.RevisePlanInput); input.RequestedChapters != requested || input.FixedChapters != 0 {
			t.Fatalf("extend input = %+v, want requested %d", input, requested)
		}
	}}
}

// 篇幅交给 AI（D63）：开放期按窗口扩展并以罗盘上限封顶；收官承诺后按全书章数完成；
// 过期的收官承诺（少于已规划章数）按开放期处理。规划任务 ID 带篇幅输入。
func TestAILengthRollsWithTheCompass(t *testing.T) {
	run := testRun(0, 2)
	first := []string{"chapter-i", "chapter-ii", "chapter-iii"}
	reviewed := Evidence{Verdicts: []StoredVerdict{storedTestVerdict("review-a", 1, model.ReviewPass, first, nil)}}
	written := novelFixture(t, 3, 3)

	assertStep(t, novelFixture(t, 0, 0), run, Evidence{}, stepWant{kind: model.OperationDevelopPlan, id: "run:book:1:plan:r3:f0",
		check: func(t *testing.T, work creation.WorkItem) {
			if input := work.Input.(model.DevelopPlanInput); input.RequestedChapters != 3 || input.FixedChapters != 0 ||
				!strings.Contains(input.Goal, "必须给出故事罗盘") {
				t.Fatalf("develop input = %+v", input)
			}
		}})
	for _, c := range []struct {
		name    string
		project projectdoc.Snapshot
		want    stepWant
	}{
		{"missing compass extends a full window", written, extendTo(6, "run:book:1:plan:extend:3:r6:f0")},
		{"open phase caps at scale_max", withCompass(written, 5, 0), extendTo(5, "run:book:1:plan:extend:3:r5:f0")},
		{"passed scale_max still asks for one more", withCompass(written, 3, 0), extendTo(4, "run:book:1:plan:extend:3:r4:f0")},
		{"stale final reopens the length", withCompass(written, 10, 2), extendTo(6, "run:book:1:plan:extend:3:r6:f0")},
		{"committed final extends up to it", withCompass(written, 10, 4), extendTo(4, "run:book:1:plan:extend:3:r4:f0")},
	} {
		t.Run(c.name, func(t *testing.T) { assertStep(t, c.project, run, reviewed, c.want) })
	}

	// 收官章所在窗口必须对收官要求下结论；满足后完成，章数等于收官承诺。
	finished := withCompass(written, 10, 3)
	finale := finaleRequirement(finished, 3).ID
	next, err := Policy{}.Next(finished, run, reviewed)
	if err != nil || next.Work == nil || next.Work.Kind != model.OperationReviewRange {
		t.Fatalf("finale review = %#v, %v", next, err)
	}
	requirementWant(t, *next.Work, map[string]bool{finale: true})
	closed := Evidence{Verdicts: []StoredVerdict{storedTestVerdict("review-a", 1, model.ReviewPass, first, nil, check(finale, model.CheckSatisfied))}}
	assertStep(t, finished, run, closed, stepWant{done: "全书 3 章完成并通过审阅"})

	// 续写：撤回收官承诺后按开放期扩窗，文案点明第 3 章是已写成的结局。
	assertStep(t, withCompass(written, 10, 0), run, closed, stepWant{kind: model.OperationRevisePlan, id: "run:book:1:plan:extend:3:r6:f0",
		check: func(t *testing.T, work creation.WorkItem) {
			if goal := work.Input.(model.RevisePlanInput).Goal; !strings.Contains(goal, "第 3 章已作为全书结局写成，这是续写") ||
				!strings.Contains(goal, "compass.ending") {
				t.Fatalf("continuation goal = %q", goal)
			}
		}})
	if goal := func() string {
		next, _ := Policy{}.Next(withCompass(written, 10, 0), run, reviewed)
		return next.Work.Input.(model.RevisePlanInput).Goal
	}(); strings.Contains(goal, "续写") {
		t.Fatalf("an unconcluded window must not be treated as continuation: %q", goal)
	}
}

// 收官要求（D63）只覆盖末章：文案是终局方向加未回收伏笔，ID 随伏笔集合变化——新增伏笔后
// 旧的"满足"不再算数，回收后回到原身份；终局与伏笔都没有时不生成。
func TestFinaleRequirementTracksOpenThreads(t *testing.T) {
	project := withCompass(novelFixture(t, 5, 5), 10, 5)
	base := finaleRequirement(project, 5)
	if !base.covers(5) || base.covers(4) || base.end != 5 || !strings.Contains(base.Text, "称帝") {
		t.Fatalf("finale = %+v", base)
	}
	thread := model.CanonFact{ID: "jade-seal", Kind: model.CanonForeshadow, SubjectID: "hero",
		Predicate: "foreshadow.jade_seal", Value: json.RawMessage(`"玉玺下落"`)}
	project.Canon = append(project.Canon, thread)
	open := finaleRequirement(project, 5)
	if open.ID == base.ID || !strings.Contains(open.Text, "jade-seal（foreshadow.jade_seal）") {
		t.Fatalf("open thread finale = %+v", open)
	}
	project.Canon[len(project.Canon)-1].Resolved = true
	if resolved := finaleRequirement(project, 5); resolved.ID != base.ID {
		t.Fatalf("resolved thread must restore the finale identity: %s vs %s", resolved.ID, base.ID)
	}
	project.Compass = nil
	list, _ := requirements(project, 5)
	for _, r := range list {
		if strings.HasPrefix(r.ID, "book:finale:") {
			t.Fatalf("finale without ending or threads must be skipped: %+v", r)
		}
	}
}

// 续跑与工作台的篇幅口径：没有运行时交给 AI，收官承诺不少于蓝图才生效。
func TestLengthOfReadsTheRunAndTheCompass(t *testing.T) {
	project := withCompass(novelFixture(t, 3, 0), 10, 5)
	if got := LengthOf(project, nil); got.Fixed != 0 || got.Final != 5 {
		t.Fatalf("no run length = %+v", got)
	}
	run := testRun(8, 2)
	if got := LengthOf(project, &run); got.Fixed != 8 || got.Final != 8 {
		t.Fatalf("fixed run length = %+v", got)
	}
	if got := LengthOf(withCompass(project, 10, 2), nil); got.Final != 0 {
		t.Fatalf("stale final must reopen: %+v", got)
	}
}

// AI 的收官承诺不得让用户要求落空（D63）：作用域落在全书之外时等待用户，而不是跳过后
// 完成；固定篇幅是用户自己划的边界，照旧跳过。
func TestAIFinaleCannotDropUserRequirements(t *testing.T) {
	project := withCompass(novelFixture(t, 3, 3), 10, 3)
	project.Directives = []model.Directive{{ID: "d-death", Scope: "chapter_range:5-6", Text: "第五六章主角死亡", Status: model.DirectiveActive}}
	finale := finaleRequirement(project, 3).ID
	first := []string{"chapter-i", "chapter-ii", "chapter-iii"}
	closed := Evidence{Verdicts: []StoredVerdict{storedTestVerdict("review-a", 1, model.ReviewPass, first, nil, check(finale, model.CheckSatisfied))}}
	assertStep(t, project, testRun(0, 2), closed, stepWant{wait: "要求「第五六章主角死亡」（作用域 chapter_range:5-6）因此落空"})
	assertStep(t, project, testRun(3, 2), closed, stepWant{done: "全书 3 章完成并通过审阅"})
}

// 首次规划尊重已知的收官承诺；过期的收官承诺在规划文案里点明必须修订。
func TestPlanningRespectsKnownAndStaleFinale(t *testing.T) {
	assertStep(t, withCompass(novelFixture(t, 0, 0), 10, 2), testRun(0, 2), Evidence{},
		stepWant{kind: model.OperationDevelopPlan, id: "run:book:1:plan:r2:f0"})
	stale := withCompass(novelFixture(t, 3, 3), 10, 2)
	reviewed := Evidence{Verdicts: []StoredVerdict{storedTestVerdict("review-a", 1, model.ReviewPass, []string{"chapter-i", "chapter-ii", "chapter-iii"}, nil)}}
	assertStep(t, stale, testRun(0, 2), reviewed, stepWant{kind: model.OperationRevisePlan, id: "run:book:1:plan:extend:3:r6:f0",
		check: func(t *testing.T, work creation.WorkItem) {
			if goal := work.Input.(model.RevisePlanInput).Goal; !strings.Contains(goal, "收官承诺 2 章已少于蓝图章数而失效") {
				t.Fatalf("stale finale goal = %q", goal)
			}
		}})
}
