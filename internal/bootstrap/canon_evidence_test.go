package bootstrap_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"

	novelapp "github.com/voocel/ainovel-cli/internal/app/novel"
	projectdoc "github.com/voocel/ainovel-cli/internal/app/project"
	"github.com/voocel/ainovel-cli/internal/domain/change"
	"github.com/voocel/ainovel-cli/internal/domain/derive"
	"github.com/voocel/ainovel-cli/internal/domain/model"
	"github.com/voocel/ainovel-cli/internal/domain/narrative"
)

func editCanonEvidence(t *testing.T, api *testApp, project projectdoc.Snapshot, id string, patches ...model.Patch) projectdoc.Snapshot {
	t.Helper()
	at := testTime().Add(time.Duration(project.Revision) * time.Hour)
	_, err := api.changes.CommitUser(context.Background(), model.Proposal{
		ID: id, Target: model.AuthorityTarget{Kind: model.AuthorityProject, ID: project.ID}, BaseRevision: project.Revision,
		Author: model.Author{Kind: model.AuthorUser, ID: "user-1"}, Reason: "核对事实",
		Patches: patches, ApprovalState: model.ApprovalPending, CreatedAt: at,
	}, at)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := api.Projects.Project(context.Background(), project.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	return updated
}

func canonEvidencePatch(t *testing.T, fact model.CanonFact) model.Patch {
	t.Helper()
	raw, err := json.Marshal(fact)
	if err != nil {
		t.Fatal(err)
	}
	return model.Patch{Document: model.DocumentRef{Kind: model.DocumentCanon, ID: fact.ID}, Operation: model.PatchPut, Content: raw}
}

// D62/D69：窗口审阅的证据只钉正文、本章计划、Intent 与要求作用域。事实只进审阅上下文：状态原地
// 更新到后续章节、用户修订事实都不废掉旧窗口；窗口正文或衔接章正文变化才失效。
func TestWindowReviewEvidencePinsManuscriptsNotCanon(t *testing.T) {
	ctx := context.Background()
	executor := &scriptedQuickExecutor{now: testTime()}
	api := newQuickTestApp(t, executor)
	command := novelapp.QuickWriteCommand{ProjectID: "canon-evidence", UserID: "user-1", Premise: "邮差送信", Chapters: 3, WorkerID: "worker", LeaseDuration: time.Minute, CreatedAt: testTime()}
	if _, err := api.Novels.QuickWrite(ctx, command); err != nil {
		t.Fatal(err)
	}
	project, err := api.Projects.Project(ctx, command.ProjectID, 0)
	if err != nil {
		t.Fatal(err)
	}
	firstWindow, laterWindow := []string{"chapter-chapter-plan-1"}, []string{"chapter-chapter-plan-2", "chapter-chapter-plan-3"}
	firstBasis, err := novelapp.ReviewBasis(project, firstWindow)
	if err != nil {
		t.Fatal(err)
	}
	laterBasis, err := novelapp.ReviewBasis(project, laterWindow)
	if err != nil {
		t.Fatal(err)
	}
	input, _ := json.Marshal(model.ReviewRangeInput{ChapterIDs: firstWindow, Basis: firstBasis})
	view, err := derive.BuildModelView(derive.ProjectContent{ID: project.ID, Revision: project.Revision, Plan: project.Plan, Entities: project.Entities, Canon: project.Canon, Manuscript: project.Manuscript}, model.OperationReviewRange, input)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(view.Context.Facts, func(fact narrative.FactView) bool {
		return fact.Chapter == 1 && fact.Predicate == "event.chapter_outcome"
	}) {
		t.Fatalf("review context must still show the window's own facts: %+v", view.Context.Facts)
	}
	for _, document := range append(slices.Clone(firstBasis.Documents), laterBasis.Documents...) {
		if document.Ref.Kind == model.DocumentCanon {
			t.Fatalf("review basis pins canon %s", document.Ref.Key())
		}
	}
	assertValid := func(project projectdoc.Snapshot, basis model.EvidenceBasis, valid bool) {
		t.Helper()
		target := model.AuthorityTarget{Kind: model.AuthorityProject, ID: project.ID}
		got, err := api.Evidence.Valid(ctx, target, project.Revision, basis)
		if err != nil || got != valid {
			t.Fatalf("valid = %v, err = %v, want valid %v", got, err, valid)
		}
		// The public reader translates the commit engine's mismatch into invalidity.
		err = api.changes.VerifyBasis(ctx, target, basis, project.Revision)
		if valid && err != nil || !valid && !errors.Is(err, change.ErrBasisMismatch) {
			t.Fatalf("VerifyBasis = %v, want valid %v", err, valid)
		}
	}
	// 用户修订第一章的事实，以及状态从第一章原地更新到第三章（D61），都不废掉窗口裁定。
	first := project.Canon[0]
	first.PreviousValue, first.Value = first.Value, json.RawMessage(`"第一章事实已改变"`)
	project = editCanonEvidence(t, api, project, "changed-fact", canonEvidencePatch(t, first))
	state := model.CanonFact{ID: "hero-state", Kind: model.CanonState, SubjectID: "hero", Predicate: "state.location", Value: json.RawMessage(`"第一站"`), SourceChapterID: firstWindow[0]}
	project = editCanonEvidence(t, api, project, "state-first", canonEvidencePatch(t, state))
	state.PreviousValue, state.Value, state.SourceChapterID = state.Value, json.RawMessage(`"第三站"`), laterWindow[1]
	project = editCanonEvidence(t, api, project, "state-moved", canonEvidencePatch(t, state))
	assertValid(project, firstBasis, true)
	assertValid(project, laterBasis, true)
	// 改第一章正文：第一章的窗口失效，以它为衔接章的后一窗口也失效。
	chapter := project.Manuscript[0]
	chapter.Blocks[0].Text = "用户修订了第一章"
	raw, _ := json.Marshal(chapter)
	project = editCanonEvidence(t, api, project, "edit-first", model.Patch{Document: model.DocumentRef{Kind: model.DocumentManuscript, ID: chapter.ID}, Operation: model.PatchPut, Content: raw})
	assertValid(project, firstBasis, false)
	assertValid(project, laterBasis, false)
}

// 只改事实不触发重审（D62 已知边界）：账本与正文的一致性归事实核验（D41），
// 完成的书续跑直接确认完成，不再调用模型。
func TestQuickWriteKeepsReviewAfterCanonOnlyChange(t *testing.T) {
	ctx := context.Background()
	executor := &scriptedQuickExecutor{now: testTime()}
	api := newQuickTestApp(t, executor)
	command := novelapp.QuickWriteCommand{ProjectID: "canon-rereview", UserID: "user-1", Premise: "邮差送信", Chapters: 1, WorkerID: "worker", LeaseDuration: time.Minute, CreatedAt: testTime()}
	if _, err := api.Novels.QuickWrite(ctx, command); err != nil {
		t.Fatal(err)
	}
	project, err := api.Projects.Project(ctx, command.ProjectID, 0)
	if err != nil {
		t.Fatal(err)
	}
	fact := project.Canon[0]
	fact.PreviousValue, fact.Value = fact.Value, json.RawMessage(`"主角其实已经死亡"`)
	project = editCanonEvidence(t, api, project, "correct-fact", canonEvidencePatch(t, fact))
	verdicts, err := api.Reviews.ListVerdicts(ctx, project)
	if err != nil || len(verdicts) != 1 {
		t.Fatalf("window verdict must stay valid: %v, %v", verdicts, err)
	}
	before := executor.calls
	result, err := api.Novels.QuickWrite(ctx, command)
	if err != nil || result.RunState != model.RunCompleted || executor.calls != before {
		t.Fatalf("result = %+v, calls %d -> %d, err %v", result, before, executor.calls, err)
	}
}

// D69：实体与卷弧只是审阅上下文。给角色追加别名、修订卷弧摘要都不废掉窗口裁定，完成的
// 书续跑直接确认完成、不再调用模型；改章节自己的计划节点（章的位置）才失效。
func TestQuickWriteKeepsReviewAfterEntityAndArcChange(t *testing.T) {
	ctx := context.Background()
	executor := &scriptedQuickExecutor{now: testTime()}
	api := newQuickTestApp(t, executor)
	command := novelapp.QuickWriteCommand{ProjectID: "context-rereview", UserID: "user-1", Premise: "邮差送信", Chapters: 2, WorkerID: "worker", LeaseDuration: time.Minute, CreatedAt: testTime()}
	if _, err := api.Novels.QuickWrite(ctx, command); err != nil {
		t.Fatal(err)
	}
	project, err := api.Projects.Project(ctx, command.ProjectID, 0)
	if err != nil {
		t.Fatal(err)
	}
	reviewed, err := api.Reviews.ListVerdicts(ctx, project)
	if err != nil || len(reviewed) == 0 {
		t.Fatalf("completed book must have verdicts: %v, %v", reviewed, err)
	}
	put := func(kind model.DocumentKind, id string, value any) model.Patch {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		return model.Patch{Document: model.DocumentRef{Kind: kind, ID: id}, Operation: model.PatchPut, Content: raw}
	}
	hero := project.Entities[slices.IndexFunc(project.Entities, func(entity model.Entity) bool { return entity.ID == "hero" })]
	hero.Aliases = append(hero.Aliases, "送信人")
	patches := []model.Patch{put(model.DocumentEntity, hero.ID, hero)}
	var chapterPlan model.PlanNode
	for _, node := range project.Plan {
		if node.Kind == model.PlanChapter {
			chapterPlan = node
			continue
		}
		node.Summary += "（按已发生的故事修订）"
		patches = append(patches, put(model.DocumentPlan, node.ID, node))
	}
	project = editCanonEvidence(t, api, project, "context-edit", patches...)
	verdicts, err := api.Reviews.ListVerdicts(ctx, project)
	if err != nil || len(verdicts) != len(reviewed) {
		t.Fatalf("entity and arc edits must keep verdicts: %d -> %d, %v", len(reviewed), len(verdicts), err)
	}
	before := executor.calls
	result, err := api.Novels.QuickWrite(ctx, command)
	if err != nil || result.RunState != model.RunCompleted || executor.calls != before {
		t.Fatalf("result = %+v, calls %d -> %d, err %v", result, before, executor.calls, err)
	}

	chapter := project.Manuscript[slices.IndexFunc(project.Manuscript, func(chapter model.ManuscriptChapter) bool { return chapter.PlanNodeID == chapterPlan.ID })]
	untouched := slices.DeleteFunc(slices.Clone(verdicts), func(stored novelapp.StoredVerdict) bool {
		return slices.Contains(stored.Verdict.ChapterIDs, chapter.ID)
	})
	chapterPlan.Summary += "（改了这一章的计划）"
	project = editCanonEvidence(t, api, project, "chapter-plan-edit", put(model.DocumentPlan, chapterPlan.ID, chapterPlan))
	if verdicts, err = api.Reviews.ListVerdicts(ctx, project); err != nil || len(verdicts) != len(untouched) || len(untouched) == len(reviewed) {
		t.Fatalf("chapter plan edit must invalidate exactly the windows holding it: %d -> %d (want %d), %v", len(reviewed), len(verdicts), len(untouched), err)
	}
}

func TestCanonSourceHistorySurvivesStateMovingToLaterChapter(t *testing.T) {
	ctx := context.Background()
	executor := &scriptedQuickExecutor{now: testTime()}
	api := newQuickTestApp(t, executor)
	command := novelapp.QuickWriteCommand{ProjectID: "canon-history", UserID: "user-1", Premise: "邮差送信", Chapters: 2, WorkerID: "worker", LeaseDuration: time.Minute, CreatedAt: testTime()}
	if _, err := api.Novels.QuickWrite(ctx, command); err != nil {
		t.Fatal(err)
	}
	project, err := api.Projects.Project(ctx, command.ProjectID, 0)
	if err != nil {
		t.Fatal(err)
	}
	first := project.Manuscript[0]
	state := model.CanonFact{ID: "hero-state", Kind: model.CanonState, SubjectID: "hero", Predicate: "state.location", Value: json.RawMessage(`"第一站"`), SourceChapterID: first.ID}
	project = editCanonEvidence(t, api, project, "state-first", canonEvidencePatch(t, state))
	state.PreviousValue, state.Value = state.Value, json.RawMessage(`"第二站"`)
	state.SourceChapterID = project.Manuscript[1].ID
	project = editCanonEvidence(t, api, project, "state-second", canonEvidencePatch(t, state), model.Patch{Document: model.DocumentRef{Kind: model.DocumentCanon, ID: first.ID + "-outcome"}, Operation: model.PatchDelete})
	if gaps := novelapp.CanonGaps(project); len(gaps) != 0 {
		t.Fatalf("state moved but chapter is already recorded: %+v", gaps)
	}
	first.Blocks[0].Text = "用户修订了第一站的故事"
	raw, _ := json.Marshal(first)
	project = editCanonEvidence(t, api, project, "edit-source", model.Patch{Document: model.DocumentRef{Kind: model.DocumentManuscript, ID: first.ID}, Operation: model.PatchPut, Content: raw})
	if gaps := novelapp.CanonGaps(project); len(gaps) != 1 || !gaps[0].Unrecorded || gaps[0].ChapterID != first.ID {
		t.Fatalf("edited old source must be checked again: %+v", gaps)
	}
}
