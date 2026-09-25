package novel

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	projectdoc "github.com/voocel/ainovel-cli/internal/app/project"
	"github.com/voocel/ainovel-cli/internal/domain/creation"
	"github.com/voocel/ainovel-cli/internal/domain/model"
)

// novelFixture 构造带索引的小说快照：plans 个章节计划、written 个正文，索引 revision 统一为 2。
func novelFixture(t *testing.T, plans, written int) projectdoc.Snapshot {
	t.Helper()
	project := projectdoc.Snapshot{
		ID: "book", Revision: 2, Intent: model.Intent{Premise: "故事"}, Index: projectdoc.DocumentIndex{},
		Plan: []model.PlanNode{
			{ID: "volume-1", Kind: model.PlanVolume, Order: 1, Title: "卷一", Summary: "开端"},
			{ID: "arc-1", Kind: model.PlanArc, ParentID: "volume-1", Order: 1, Title: "弧一", Summary: "启程"},
		},
	}
	for number := 1; number <= plans; number++ {
		project.Plan = append(project.Plan, model.PlanNode{
			ID: "chapter-plan-" + strings.Repeat("i", number), Kind: model.PlanChapter, ParentID: "arc-1",
			Order: number, Title: "第" + strings.Repeat("一", number) + "章", Summary: "推进",
		})
	}
	for number := 1; number <= written; number++ {
		plan := project.Plan[1+number]
		chapterID := "chapter-" + strings.Repeat("i", number)
		project.Manuscript = append(project.Manuscript, model.ManuscriptChapter{
			ID: chapterID, PlanNodeID: plan.ID, Number: number, Title: plan.Title,
			Author: model.AuthorAI, Blocks: []model.ManuscriptBlock{{ID: "b", Text: "正文"}},
		})
		// 每章随章入账一条事实：没有来源事实的章视为未入账（§4.5）。
		project.Canon = append(project.Canon, model.CanonFact{
			ID: chapterID + "-outcome", Kind: model.CanonEvent, SubjectID: "hero", Predicate: "event.chapter_outcome",
			Value: json.RawMessage(`"推进"`), SourceChapterID: chapterID,
		})
	}
	index := func(ref model.DocumentRef, value any) {
		content, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("encode %s: %v", ref.Key(), err)
		}
		if err := project.Index.Add(model.DocumentVersion{Document: ref, Revision: 2, Content: content}); err != nil {
			t.Fatalf("index %s: %v", ref.Key(), err)
		}
	}
	index(model.DocumentRef{Kind: model.DocumentIntent, ID: "root"}, project.Intent)
	for _, node := range project.Plan {
		index(model.DocumentRef{Kind: model.DocumentPlan, ID: node.ID}, node)
	}
	for _, chapter := range project.Manuscript {
		index(model.DocumentRef{Kind: model.DocumentManuscript, ID: chapter.ID}, chapter)
	}
	project.Entities = []model.Entity{{ID: "hero", Kind: model.EntityCharacter, Name: "主角"}}
	index(model.DocumentRef{Kind: model.DocumentEntity, ID: "hero"}, project.Entities[0])
	for _, fact := range project.Canon {
		index(model.DocumentRef{Kind: model.DocumentCanon, ID: fact.ID}, fact)
	}
	return project
}

// editChapter 模拟用户改动正文：该章最后变化 revision 前进，事实不动。
func editChapter(project *projectdoc.Snapshot, chapterID string, revision model.Revision) {
	key := (model.DocumentRef{Kind: model.DocumentManuscript, ID: chapterID}).Key()
	entry := project.Index[key]
	entry.Revision = revision
	project.Index[key] = entry
}

// dropFacts 移除某章的全部来源事实：该章未入账。
func dropFacts(project *projectdoc.Snapshot, chapterID string) {
	kept := project.Canon[:0]
	for _, fact := range project.Canon {
		if fact.SourceChapterID == chapterID {
			delete(project.Index, (model.DocumentRef{Kind: model.DocumentCanon, ID: fact.ID}).Key())
			continue
		}
		kept = append(kept, fact)
	}
	project.Canon = kept
}

func TestCanonGapsFollowRevisions(t *testing.T) {
	project := novelFixture(t, 3, 3)
	if gaps := CanonGaps(project); len(gaps) != 0 {
		t.Fatalf("intact project has gaps: %#v", gaps)
	}
	editChapter(&project, "chapter-iii", 3)
	dropFacts(&project, "chapter-i")
	gaps := CanonGaps(project)
	if len(gaps) != 2 || !gaps[0].Unrecorded || gaps[0].ChapterID != "chapter-i" || gaps[0].Revision != 2 ||
		gaps[1].Unrecorded || gaps[1].ChapterID != "chapter-iii" || gaps[1].Revision != 3 ||
		len(gaps[1].Pending) != 1 || gaps[1].Pending[0] != "chapter-iii-outcome" {
		t.Fatalf("gaps = %#v", gaps)
	}
}

// storedTestVerdict 构造一份有效裁定：key 区分不同审阅，at 决定新旧（越大越新）。
func storedTestVerdict(key string, at int, status string, chapters []string, findings []model.ReviewFinding, checks ...model.RequirementCheck) StoredVerdict {
	verdict := model.ReviewVerdict{
		Status: status, Revision: 2, ChapterIDs: chapters, ReviewKey: "review", Findings: findings, Checks: checks,
		Basis: model.EvidenceBasis{Documents: []model.DocumentBasis{{Ref: model.DocumentRef{Kind: model.DocumentIntent, ID: "root"}, Revision: 1}}},
	}
	if findings == nil {
		verdict.Findings = []model.ReviewFinding{}
	}
	return StoredVerdict{Verdict: verdict, Key: key, CreatedAt: time.Date(2026, 9, 8, at, 0, 0, 0, time.UTC)}
}

// acceptedVerdict 给裁定附上用户裁决（D43）：推导器只看生效裁定。
func acceptedVerdict(verdict StoredVerdict, findings ...string) StoredVerdict {
	verdict.Accepted = make(map[string]struct{}, len(findings))
	for _, id := range findings {
		verdict.Accepted[id] = struct{}{}
	}
	return verdict
}

func testRun(target, budget int) model.CreationRun {
	return model.CreationRun{
		ID: "run:book:1", ProjectID: "book",
		Goal:     model.NovelGoal{Premise: "故事", TargetChapters: target}.Goal(),
		Strategy: model.CreationRunStrategy{PlanWindowChapters: 3, ReviewCadence: model.ReviewPerPlanWindow, AutoRepairBudget: budget},
	}
}

func TestNovelDeriverNextFollowsNovelRules(t *testing.T) {
	run := testRun(5, 1)
	first := []string{"chapter-i", "chapter-ii", "chapter-iii"}
	last := []string{"chapter-iiii", "chapter-iiiii"}
	blocking := []model.ReviewFinding{{ChapterID: "chapter-ii", Severity: model.FindingBlocking, Note: "第二章崩了"}}
	lastBlocking := []model.ReviewFinding{{ChapterID: "chapter-iiii", Severity: model.FindingBlocking, Note: "第四章崩了"}}
	note := []model.ReviewFinding{{ChapterID: "chapter-iii", Severity: model.FindingNote, Note: "动机要更明确"}}
	firstPass := storedTestVerdict("review-a", 1, model.ReviewPass, first, nil)
	cases := []struct {
		name     string
		plans    int
		written  int
		evidence Evidence
		edit     func(project *projectdoc.Snapshot)
		wantKind model.OperationKind
		wantID   string
		wantWait string
		wantDone string
		wantFail string
		check    func(t *testing.T, work creation.WorkItem)
	}{
		{name: "empty plan develops", wantKind: model.OperationDevelopPlan, wantID: "run:book:1:plan:r3:f5"},
		{name: "unwritten window writes next chapter", plans: 3, written: 1,
			wantKind: model.OperationWriteChapter, wantID: "run:book:1:chapter:chapter-plan-ii"},
		{name: "written window reviews before extending", plans: 3, written: 3,
			wantKind: model.OperationReviewRange, wantID: reviewOperationID(run.ID, first, 2, nil),
			check: func(t *testing.T, work creation.WorkItem) {
				input := work.Input.(model.ReviewRangeInput)
				if len(input.ChapterIDs) != 3 || len(input.Requirements) != 0 || len(input.Basis.Documents) == 0 {
					t.Fatalf("window review input = %#v", input)
				}
			}},
		// 蓝图一次铺满也按窗口节奏审（D62）：写第 4 章前先审完前 3 章。
		{name: "full blueprint reviews each window before writing on", plans: 5, written: 3,
			wantKind: model.OperationReviewRange, wantID: reviewOperationID(run.ID, first, 2, nil)},
		{name: "reviewed window writes on", plans: 5, written: 3,
			evidence: Evidence{Verdicts: []StoredVerdict{firstPass}},
			wantKind: model.OperationWriteChapter, wantID: "run:book:1:chapter:chapter-plan-iiii"},
		{name: "reviewed window extends with notes", plans: 3, written: 3,
			evidence: Evidence{Verdicts: []StoredVerdict{storedTestVerdict("review-a", 1, model.ReviewPass, first, note)}},
			wantKind: model.OperationRevisePlan, wantID: "run:book:1:plan:extend:3:r5:f5",
			check: func(t *testing.T, work creation.WorkItem) {
				input := work.Input.(model.RevisePlanInput)
				if input.ExistingChapters != 3 || input.RequestedChapters != 5 || len(input.ReviewNotes) != 1 {
					t.Fatalf("extend input = %#v", input)
				}
			}},
		{name: "blocked window rewrites within budget", plans: 3, written: 3,
			evidence: Evidence{Verdicts: []StoredVerdict{storedTestVerdict("review-a", 1, model.ReviewBlocked, first, blocking)}},
			wantKind: model.OperationRewriteChapter, wantID: "run:book:1:rewrite:chapter-ii:r2"},
		{name: "exhausted budget waits", plans: 3, written: 3,
			evidence: Evidence{Verdicts: []StoredVerdict{storedTestVerdict("review-a", 1, model.ReviewBlocked, first, blocking)}, Repairs: map[string]int{"chapter-ii": 1}},
			wantWait: "第 2 章《第一一章》的自动修订预算（每章 1 次）已用尽"},
		// 预算按章计（D63）：别的章用过的重写次数不挤占本章。
		{name: "budget is per chapter", plans: 5, written: 5,
			evidence: Evidence{Verdicts: []StoredVerdict{firstPass, storedTestVerdict("review-b", 2, model.ReviewBlocked, last, lastBlocking)}, Repairs: map[string]int{"chapter-ii": 1}},
			wantKind: model.OperationRewriteChapter, wantID: "run:book:1:rewrite:chapter-iiii:r2"},
		// 完成时只审尚未覆盖的窗口，不再累计重审全书。
		{name: "complete manuscript reviews the uncovered window", plans: 5, written: 5,
			evidence: Evidence{Verdicts: []StoredVerdict{firstPass}},
			wantKind: model.OperationReviewRange, wantID: reviewOperationID(run.ID, last, 2, nil),
			check: func(t *testing.T, work creation.WorkItem) {
				if input := work.Input.(model.ReviewRangeInput); len(input.ChapterIDs) != 2 || input.ChapterIDs[0] != "chapter-iiii" {
					t.Fatalf("final window input = %#v", input)
				}
			}},
		{name: "every chapter covered completes", plans: 5, written: 5,
			evidence: Evidence{Verdicts: []StoredVerdict{firstPass, storedTestVerdict("review-b", 2, model.ReviewPass, last, nil)}},
			wantDone: "全书 5 章完成并通过审阅"},
		{name: "later blocked window rewrites its chapter", plans: 5, written: 5,
			evidence: Evidence{Verdicts: []StoredVerdict{firstPass, storedTestVerdict("review-b", 2, model.ReviewBlocked, last, lastBlocking)}},
			wantKind: model.OperationRewriteChapter, wantID: "run:book:1:rewrite:chapter-iiii:r2"},
		// 同一窗口审过多次时以最新的有效裁定为准。
		{name: "newest verdict of a window wins", plans: 3, written: 3,
			evidence: Evidence{Verdicts: []StoredVerdict{
				storedTestVerdict("review-a", 1, model.ReviewBlocked, first, blocking), storedTestVerdict("review-b", 2, model.ReviewPass, first, nil),
			}},
			wantKind: model.OperationRevisePlan, wantID: "run:book:1:plan:extend:3:r5:f5"},
		{name: "accepted blocking finding passes the window", plans: 3, written: 3,
			evidence: Evidence{Verdicts: []StoredVerdict{acceptedVerdict(storedTestVerdict("review-a", 1, model.ReviewBlocked, first, blocking), "review-a/0")}},
			wantKind: model.OperationRevisePlan, wantID: "run:book:1:plan:extend:3:r5:f5"},
		{name: "accepted final finding completes", plans: 5, written: 5,
			evidence: Evidence{Verdicts: []StoredVerdict{firstPass, acceptedVerdict(storedTestVerdict("review-b", 2, model.ReviewBlocked, last, lastBlocking), "review-b/0")}},
			wantDone: "全书 5 章完成并通过审阅"},
		{name: "edited chapter verifies its facts before anything else", plans: 3, written: 3,
			evidence: Evidence{Verdicts: []StoredVerdict{storedTestVerdict("review-a", 1, model.ReviewBlocked, first, blocking)}},
			edit:     func(project *projectdoc.Snapshot) { editChapter(project, "chapter-ii", 3) },
			wantKind: model.OperationReviseCanon, wantID: "run:book:1:canon:chapter-ii:r3",
			check: func(t *testing.T, work creation.WorkItem) {
				input := work.Input.(model.ReviseCanonInput)
				if input.ChapterID != "chapter-ii" || len(input.FactIDs) != 1 || input.FactIDs[0] != "chapter-ii-outcome" {
					t.Fatalf("verify input = %#v", input)
				}
			}},
		{name: "unrecorded chapter is booked before writing on", plans: 3, written: 2,
			edit:     func(project *projectdoc.Snapshot) { dropFacts(project, "chapter-i") },
			wantKind: model.OperationReviseCanon, wantID: "run:book:1:canon:chapter-i:r2",
			check: func(t *testing.T, work creation.WorkItem) {
				if input := work.Input.(model.ReviseCanonInput); len(input.FactIDs) != 0 || !strings.Contains(input.Reason, "没有入账") {
					t.Fatalf("booking input = %#v", input)
				}
			}},
		{name: "oversized plan waits", plans: 6, wantWait: "蓝图包含 6 个有效章节节点"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			project := novelFixture(t, tc.plans, tc.written)
			if tc.edit != nil {
				tc.edit(&project)
			}
			assertStep(t, project, run, tc.evidence, stepWant{kind: tc.wantKind, id: tc.wantID, wait: tc.wantWait, done: tc.wantDone, fail: tc.wantFail, check: tc.check})
		})
	}
}

type stepWant struct {
	kind             model.OperationKind
	id               string
	wait, done, fail string
	check            func(t *testing.T, work creation.WorkItem)
}

func assertStep(t *testing.T, project projectdoc.Snapshot, run model.CreationRun, evidence Evidence, want stepWant) {
	t.Helper()
	next, err := Policy{}.Next(project, run, evidence)
	if err != nil {
		t.Fatalf("next: %v", err)
	}
	switch {
	case want.wait != "":
		if next.Work != nil || !strings.Contains(next.Wait, want.wait) {
			t.Fatalf("step = %#v, want wait %q", next, want.wait)
		}
	case want.done != "":
		if next.Work != nil || next.Done != want.done {
			t.Fatalf("step = %#v, want done %q", next, want.done)
		}
	case want.fail != "":
		if next.Work != nil || next.Fail != want.fail {
			t.Fatalf("step = %#v, want fail %q", next, want.fail)
		}
	default:
		if next.Work == nil || next.Work.Kind != want.kind || next.Work.ID != want.id {
			t.Fatalf("step = %#v, want %s %s", next, want.kind, want.id)
		}
		if err := next.Work.Input.Validate(); err != nil {
			t.Fatalf("work input: %v", err)
		}
		if want.check != nil {
			want.check(t, *next.Work)
		}
	}
}
