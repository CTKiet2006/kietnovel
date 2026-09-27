package novel

import (
	"slices"
	"strings"
	"testing"

	projectdoc "github.com/voocel/ainovel-cli/internal/app/project"
	"github.com/voocel/ainovel-cli/internal/domain/creation"
	"github.com/voocel/ainovel-cli/internal/domain/model"
)

var firstWindow = []string{"chapter-i", "chapter-ii", "chapter-iii"}

func blockingOn(chapterID, note string) model.ReviewFinding {
	return model.ReviewFinding{ChapterID: chapterID, Severity: model.FindingBlocking, Note: note}
}

// staleVerdict 用当时的真实窗口基线构造裁定：之后改动其中的章，它就成了上一轮裁定。
func staleVerdict(t *testing.T, project projectdoc.Snapshot, key string, at int, chapters []string, findings ...model.ReviewFinding) StoredVerdict {
	t.Helper()
	status := model.ReviewPass
	if slices.ContainsFunc(findings, func(f model.ReviewFinding) bool { return f.Severity == model.FindingBlocking }) {
		status = model.ReviewBlocked
	}
	stored := storedTestVerdict(key, at, status, chapters, append([]model.ReviewFinding{}, findings...))
	basis, err := ReviewBasis(project, chapters)
	if err != nil {
		t.Fatal(err)
	}
	stored.Verdict.Basis = basis
	return stored
}

// rewriteChapter 模拟一次重写：正文与来源事实一起前进到 revision，没有事实缺口。
func rewriteChapter(project *projectdoc.Snapshot, chapterID string, revision model.Revision) {
	editChapter(project, chapterID, revision)
	for _, fact := range project.Canon {
		if fact.SourceChapterID == chapterID {
			key := (model.DocumentRef{Kind: model.DocumentCanon, ID: fact.ID}).Key()
			entry := project.Index[key]
			entry.Revision = revision
			project.Index[key] = entry
		}
	}
	project.Revision = max(project.Revision, revision)
}

func recheckOf(revision model.Revision, chapters, reviewed []string, prior ...model.ReviewFinding) stepWant {
	input := model.ReviewRangeInput{ChapterIDs: chapters, Reviewed: reviewed, PriorFindings: prior}
	return stepWant{kind: model.OperationReviewRange, id: reviewOperationID("run:book:1", revision, input), check: func(t *testing.T, work creation.WorkItem) {
		got := work.Input.(model.ReviewRangeInput)
		if !slices.Equal(got.ChapterIDs, chapters) || !slices.Equal(got.Reviewed, reviewed) || !slices.Equal(got.PriorFindings, prior) {
			t.Fatalf("recheck input = %+v, want chapters %v reviewed %v prior %v", got, chapters, reviewed, prior)
		}
		if len(reviewed) > 0 && !strings.Contains(got.Goal, "只作上下文") || len(prior) > 0 && !strings.Contains(got.Goal, "prior_findings") {
			t.Fatalf("recheck goal = %q", got.Goal)
		}
	}}
}

func rewriteOf(chapterID string, findings ...string) stepWant {
	return stepWant{kind: model.OperationRewriteChapter, id: "run:book:1:rewrite:" + chapterID + ":r2", check: func(t *testing.T, work creation.WorkItem) {
		if got := work.Input.(model.RewriteChapterInput).Findings; !slices.Equal(got, findings) {
			t.Fatalf("rewrite findings = %q, want %q", got, findings)
		}
	}}
}

// 复审只对改动章下结论（D68）：上一轮已审且正文未变的章只作上下文，改动章带上一轮
// 的阻塞意见复核。
func TestRecheckFocusesOnChangedChapters(t *testing.T) {
	project := novelFixture(t, 5, 3)
	blocking := blockingOn("chapter-ii", "时间线冲突")
	prior := staleVerdict(t, project, "review-a", 1, firstWindow, blocking, model.ReviewFinding{ChapterID: "chapter-i", Severity: model.FindingNote, Note: "节奏偏慢"})
	rewriteChapter(&project, "chapter-ii", 3)
	evidence := Evidence{Prior: []StoredVerdict{prior}, Repairs: map[string]int{"chapter-ii": 1}}
	want := recheckOf(3, firstWindow, []string{"chapter-i", "chapter-iii"}, blocking)
	assertStep(t, project, testRun(5, 2), evidence, want)
	if want.id == reviewOperationID("run:book:1", 3, model.ReviewRangeInput{ChapterIDs: firstWindow}) {
		t.Fatal("recheck must not share the full review id")
	}
}

// 已知阻塞先修完（D68）：上一轮阻塞的章正文未变且有预算，先重写（与有效裁定同一槽位，
// 只带阻塞意见），全部改完再复审；预算已尽的章不重写，留在焦点由新裁定走等待。
func TestKnownBlockingIsRewrittenBeforeRecheck(t *testing.T) {
	run := testRun(5, 2)
	project := novelFixture(t, 5, 3)
	first, third := blockingOn("chapter-i", "人物前后矛盾"), blockingOn("chapter-iii", "左右手写反")
	note := model.ReviewFinding{ChapterID: "chapter-i", Severity: model.FindingNote, Note: "开头句式重复"}
	verdict := staleVerdict(t, project, "review-a", 1, firstWindow, first, note, third)

	assertStep(t, project, run, Evidence{Verdicts: []StoredVerdict{verdict}}, rewriteOf("chapter-i", first.Note))

	rewriteChapter(&project, "chapter-i", 3)
	prior := Evidence{Prior: []StoredVerdict{verdict}, Repairs: map[string]int{"chapter-i": 1}}
	assertStep(t, project, run, prior, rewriteOf("chapter-iii", third.Note))

	exhausted := prior
	exhausted.Repairs = map[string]int{"chapter-i": 1, "chapter-iii": 2}
	assertStep(t, project, run, exhausted, recheckOf(3, firstWindow, []string{"chapter-ii"}, first, third))

	rewriteChapter(&project, "chapter-iii", 4)
	prior.Repairs["chapter-iii"] = 1
	assertStep(t, project, run, prior, recheckOf(4, firstWindow, []string{"chapter-ii"}, first, third))
}

// 上一轮的阻塞不再成立时不重写：用户已接受的发现，或链接的要求已退役。
func TestSettledPriorBlockingIsNotRewritten(t *testing.T) {
	run := testRun(5, 2)
	project := novelFixture(t, 5, 3)
	first := blockingOn("chapter-i", "人物前后矛盾")
	third := blockingOn("chapter-iii", "左右手写反")
	retired := model.ReviewFinding{ChapterID: "chapter-iii", Severity: model.FindingBlocking, Note: "违反已退役的要求", Requirement: "directive:gone"}
	accepted := acceptedVerdict(staleVerdict(t, project, "review-a", 1, firstWindow, first, third), model.FindingID("review-a", 1))
	linked := staleVerdict(t, project, "review-a", 1, firstWindow, first, retired)
	rewriteChapter(&project, "chapter-i", 3)
	for name, verdict := range map[string]StoredVerdict{"accepted": accepted, "retired requirement": linked} {
		t.Run(name, func(t *testing.T) {
			evidence := Evidence{Prior: []StoredVerdict{verdict}, Repairs: map[string]int{"chapter-i": 1}}
			assertStep(t, project, run, evidence, recheckOf(3, firstWindow, []string{"chapter-ii", "chapter-iii"}, first))
		})
	}
}

// 衔接章在块外改过：块首章要重新看衔接，不算已审；要求变了只以 requirements 进入复审，
// 正文未变的章仍只作上下文。
func TestRecheckSeamAndRequirementChanges(t *testing.T) {
	project := novelFixture(t, 6, 6)
	second := []string{"chapter-iiii", "chapter-iiiii", "chapter-iiiiii"}
	prior := staleVerdict(t, project, "review-b", 1, second)
	rewriteChapter(&project, "chapter-iii", 3)
	passed := Evidence{Verdicts: []StoredVerdict{storedTestVerdict("review-a", 2, model.ReviewPass, firstWindow, nil)}, Prior: []StoredVerdict{prior}}
	assertStep(t, project, testRun(6, 2), passed, recheckOf(3, second, []string{"chapter-iiiii", "chapter-iiiiii"}))

	project = novelFixture(t, 5, 3)
	prior = staleVerdict(t, project, "review-a", 1, firstWindow)
	project.Directives = []model.Directive{{ID: "d-rain", Scope: "chapter_range:2-2", Text: "第二章下雨", Status: model.DirectiveActive}}
	next, err := Policy{}.Next(project, testRun(5, 2), Evidence{Prior: []StoredVerdict{prior}})
	if err != nil || next.Work == nil || next.Work.Kind != model.OperationReviewRange {
		t.Fatalf("requirement recheck = %#v, %v", next, err)
	}
	input := next.Work.Input.(model.ReviewRangeInput)
	if !slices.Equal(input.Reviewed, firstWindow) || !slices.ContainsFunc(input.Requirements, func(r model.Requirement) bool { return r.ID == "directive:d-rain" }) {
		t.Fatalf("requirement recheck input = %+v", input)
	}
}
