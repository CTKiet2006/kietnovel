package bootstrap_test

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	projectdoc "github.com/voocel/ainovel-cli/internal/app/project"
	tasks "github.com/voocel/ainovel-cli/internal/app/task"
	"github.com/voocel/ainovel-cli/internal/domain/model"
)

// D66：内部文档 ID 曾经流进正文（ch-18、ch-20）。每类 Worker 编译出的完整提示词——协议、
// 工具 Schema、规则、上下文与任务——都只说故事语言，一个文档 ID 也不出现。
func TestCompiledPromptsCarryNoDocumentIDs(t *testing.T) {
	ctx := context.Background()
	authorityStore := openTestStore(t)
	service := newTestApp(authorityStore)
	now := testTime()
	rule := model.DocumentRef{Kind: model.DocumentCanon, ID: "fact-rule"}
	mentor := model.DocumentRef{Kind: model.DocumentEntity, ID: "ent-mentor"}
	if _, err := service.Projects.CreateProject(ctx, projectdoc.CreateProjectCommand{
		ProjectID: "book-ids", ChangeID: "create", UserID: "user-1", Reason: "创建作品", CreatedAt: now,
		Draft: projectdoc.ProjectDraft{
			Intent: model.Intent{Premise: "少年入山修行", Required: []string{"师徒情谊贯穿全书"}},
			Plan: []model.PlanNode{
				{ID: "vol-x", Kind: model.PlanVolume, Order: 1, Title: "入山", Summary: "拜入宗门"},
				{ID: "arc-x", Kind: model.PlanArc, ParentID: "vol-x", Order: 1, Title: "外门", Summary: "外门试炼"},
				{ID: "ch-x1", Kind: model.PlanChapter, ParentID: "arc-x", Order: 1, Title: "山门", Summary: "拜师"},
				{ID: "ch-x2", Kind: model.PlanChapter, ParentID: "arc-x", Order: 2, Title: "立誓", Summary: "立誓"},
				{ID: "ch-x3", Kind: model.PlanChapter, ParentID: "arc-x", Order: 3, Title: "下山", Summary: "下山"},
			},
			Entities: []model.Entity{
				{ID: "ent-hero", Kind: model.EntityCharacter, Name: "林凡"},
				{ID: mentor.ID, Kind: model.EntityCharacter, Name: "清虚子"},
			},
			Canon: []model.CanonFact{{ID: rule.ID, Kind: model.CanonWorldRule, SubjectID: mentor.ID, Predicate: "rule.no_killing", Value: json.RawMessage(`"门规禁杀"`)}},
			Ownership: []model.OwnershipRule{
				{Target: rule, Control: model.ControlLocked},
				{Target: mentor, Control: model.ControlGuided, Guidance: []string{"师父始终严厉"}},
			},
		},
	}); err != nil {
		t.Fatalf("create project: %v", err)
	}
	chapter := func(id, plan string, number int, title, text string) model.Patch {
		content, _ := json.Marshal(model.ManuscriptChapter{ID: id, PlanNodeID: plan, Number: number, Title: title, Author: model.AuthorAI,
			Blocks: []model.ManuscriptBlock{{ID: "b1", Text: text}}})
		return model.Patch{Document: model.DocumentRef{Kind: model.DocumentManuscript, ID: id}, Operation: model.PatchPut, Content: content}
	}
	fact := func(id string, kind model.CanonFactKind, predicate, value, source string) model.Patch {
		content, _ := json.Marshal(model.CanonFact{ID: id, Kind: kind, SubjectID: "ent-hero", Predicate: predicate,
			Value: json.RawMessage(`"` + value + `"`), SourceChapterID: source})
		return model.Patch{Document: model.DocumentRef{Kind: model.DocumentCanon, ID: id}, Operation: model.PatchPut, Content: content}
	}
	committed, err := service.changes.CommitUser(ctx, model.Proposal{
		ID: "write", Target: model.AuthorityTarget{Kind: model.AuthorityProject, ID: "book-ids"}, BaseRevision: 1,
		Author: model.Author{Kind: model.AuthorUser, ID: "user-1"}, Reason: "写两章",
		Patches: []model.Patch{
			chapter("ms-x1", "ch-x1", 1, "山门", "林凡跪在山门前。"), chapter("ms-x2", "ch-x2", 2, "立誓", "他在师父面前立誓。"),
			fact("fact-e1", model.CanonEvent, "event.kneel", "跪求入门", "ms-x1"),
			fact("fact-f1", model.CanonForeshadow, "foreshadow.jade", "玉佩来历不明", "ms-x1"),
			fact("fact-s2", model.CanonState, "state.status", "外门弟子", "ms-x2"),
		},
		ApprovalState: model.ApprovalPending, CreatedAt: now,
	}, now)
	if err != nil {
		t.Fatalf("commit chapters: %v", err)
	}
	revision := strconv.FormatInt(int64(committed.NewRevision), 10)
	basis := `{"documents":[{"ref":{"kind":"manuscript","id":"ms-x1"},"revision":` + revision + `}]}`
	inputs := map[model.OperationKind]string{
		model.OperationWriteChapter: `{"chapter_plan_id":"ch-x3","chapter_number":3,` +
			`"directives":[{"id":"d-1","scope":"plan_node:arc-x","text":"多写心理","status":"active"}]}`,
		model.OperationRewriteChapter:  `{"chapter_id":"ms-x2","chapter_plan_id":"ch-x2","chapter_number":2,"findings":["立誓太仓促"]}`,
		model.OperationRewriteAffected: `{"chapter_ids":["ms-x1"],"base_revision":` + revision + `,"resolution_proposal_id":"p-ms-x1","reason":"门规改变"}`,
		model.OperationReviewRange:     `{"chapter_ids":["ms-x1","ms-x2"],"basis":` + basis + `}`,
		model.OperationReviseCanon:     `{"chapter_id":"ms-x1","fact_ids":["fact-e1"],"reason":"正文改动后核验"}`,
		model.OperationRevisePlan:      `{"intent":"少年入山修行","existing_chapters":3,"requested_chapters":5}`,
	}
	ids := []string{"vol-x", "arc-x", "ch-x1", "ch-x2", "ch-x3", "ent-hero", "ent-mentor", "fact-rule", "fact-e1", "fact-f1", "fact-s2", "ms-x1", "ms-x2"}
	index := 0
	for kind, input := range inputs {
		index++
		operation, err := service.Tasks.StartOperation(ctx, tasks.StartOperationCommand{
			OperationID: "op-" + string(kind), ProjectID: "book-ids", RunID: ensureTestRun(t, ctx, authorityStore, "book-ids", now),
			Kind: kind, Input: json.RawMessage(input), CoreProtocolVersion: "core-v1",
			ApprovalPolicy: model.ApprovalManual, CreatedAt: now.Add(time.Duration(index) * time.Minute),
		})
		if err != nil {
			t.Fatalf("start %s: %v", kind, err)
		}
		text, _, err := service.Prompts.Prompt(ctx, operation.Snapshot.ConfigDigest)
		if err != nil {
			t.Fatalf("show %s prompt: %v", kind, err)
		}
		for _, id := range ids {
			if strings.Contains(text, id) {
				t.Errorf("%s prompt leaks document id %q", kind, id)
			}
		}
		if !strings.Contains(text, "林凡") {
			t.Errorf("%s prompt lost the story content:\n%s", kind, text)
		}
	}
}
