package model

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// 事实核验任务只提交 canon 补丁、来源固定为任务章节、待核验事实全部处理且至少留一条事实。
func TestReviseCanonSubmissionCoversRequestedFacts(t *testing.T) {
	input := ReviseCanonInput{ChapterID: "chapter-1", FactIDs: []string{"fact-1"}, Reason: "正文被改过"}
	canon := func(id, source string) Patch {
		return Patch{Document: DocumentRef{Kind: DocumentCanon, ID: id}, Operation: PatchPut,
			Content: json.RawMessage(fmt.Sprintf(`{"id":%q,"kind":"event","subject_id":"hero","predicate":"event.done","old_value":true,"new_value":true,"source_chapter_id":%q}`, id, source))}
	}
	deleted := Patch{Document: DocumentRef{Kind: DocumentCanon, ID: "fact-1"}, Operation: PatchDelete}
	manuscript := Patch{Document: DocumentRef{Kind: DocumentManuscript, ID: "chapter-1"}, Operation: PatchPut, Content: json.RawMessage(`{}`)}
	for _, tc := range []struct {
		name    string
		patches []Patch
		wantErr string
	}{
		{"manuscript patches are rejected", []Patch{manuscript, canon("fact-1", "chapter-1")}, "only change canon facts"},
		{"pending fact must be handled", []Patch{canon("fact-2", "chapter-1")}, "pending verification"},
		{"chapter keeps at least one fact", []Patch{deleted}, "at least one canon fact"},
		{"source must be the task chapter", []Patch{canon("fact-1", "chapter-2")}, "sourced from chapter"},
		{"confirmation passes", []Patch{canon("fact-1", "chapter-1")}, ""},
		{"replacement passes", []Patch{deleted, canon("fact-2", "chapter-1")}, ""},
	} {
		projected := story{canon: make(map[string]CanonFact)}
		for _, patch := range tc.patches {
			if patch.Document.Kind == DocumentCanon && patch.Operation == PatchPut {
				if err := decodeInto(projected.canon, DocumentVersion{Document: patch.Document, Content: patch.Content}); err != nil {
					t.Fatal(err)
				}
			}
		}
		err := validateCanonRevision(input, projected, tc.patches)
		if tc.wantErr == "" && err != nil {
			t.Fatalf("%s: unexpected err %v", tc.name, err)
		}
		if tc.wantErr != "" && (!errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), tc.wantErr)) {
			t.Fatalf("%s: err = %v, want %q", tc.name, err, tc.wantErr)
		}
	}
}

// 章节任务只写它的目标章节（写章：目标计划节点的一章；重写：任务章节；受影响重写：
// 全部受影响章节），不得删除正文，正文满足任务携带要求的字数约束（S13）。
func TestChapterTasksWriteExactlyTheirTargets(t *testing.T) {
	chapter := func(id, plan, text string) Patch {
		content, _ := json.Marshal(ManuscriptChapter{ID: id, PlanNodeID: plan, Number: 1, Title: "章", Author: AuthorAI,
			Blocks: []ManuscriptBlock{{ID: "b", Text: text}}})
		return Patch{Document: DocumentRef{Kind: DocumentManuscript, ID: id}, Operation: PatchPut, Content: content}
	}
	task := func(kind OperationKind, input TaskInput) Operation {
		raw, _ := json.Marshal(input)
		return Operation{Kind: kind, Input: raw, Snapshot: ExecutionSnapshot{BaseRevision: 3}}
	}
	length := []Directive{{ID: "length", Scope: DirectiveScopeProject, Text: "每章十字左右", Status: DirectiveActive,
		Constraints: &DirectiveConstraints{TargetWords: 10}}}
	write := task(OperationWriteChapter, &WriteChapterInput{ChapterPlanID: "plan-1", ChapterNumber: 1, Directives: length})
	rewrite := task(OperationRewriteChapter, &RewriteChapterInput{ChapterID: "chapter-1", ChapterPlanID: "plan-1", ChapterNumber: 1, Findings: []string{"仓促"}})
	affected := task(OperationRewriteAffected, &RewriteAffectedInput{ChapterIDs: []string{"chapter-1", "chapter-2"}, BaseRevision: 3, ResolutionProposalID: "p", Reason: "改设定"})
	ten := strings.Repeat("好", 10)
	for _, c := range []struct {
		name    string
		task    Operation
		patches []Patch
		wantErr string
	}{
		{"write implements its plan", write, []Patch{chapter("chapter-1", "plan-1", ten)}, ""},
		{"write another plan", write, []Patch{chapter("chapter-1", "plan-2", ten)}, "does not implement the requested plan"},
		{"write nothing", write, nil, "exactly one new chapter"},
		{"write two chapters", write, []Patch{chapter("chapter-1", "plan-1", ten), chapter("chapter-2", "plan-1", ten)}, "exactly one new chapter"},
		{"write too short", write, []Patch{chapter("chapter-1", "plan-1", "太短")}, "requires 9-11"},
		{"rewrite its chapter", rewrite, []Patch{chapter("chapter-1", "plan-1", "新")}, ""},
		{"rewrite another chapter", rewrite, []Patch{chapter("chapter-2", "plan-1", "新")}, "exactly chapter chapter-1"},
		{"rewrite cannot delete", rewrite, []Patch{chapter("chapter-1", "plan-1", "新"),
			{Document: DocumentRef{Kind: DocumentManuscript, ID: "chapter-2"}, Operation: PatchDelete}}, "cannot delete chapter"},
		{"affected covers every chapter", affected, []Patch{chapter("chapter-1", "plan-1", "新"), chapter("chapter-2", "plan-2", "新")}, ""},
		{"affected cannot skip a chapter", affected, []Patch{chapter("chapter-1", "plan-1", "新")}, "exactly chapters"},
	} {
		t.Run(c.name, func(t *testing.T) {
			projected := story{chapters: make(map[string]ManuscriptChapter)}
			for _, patch := range c.patches {
				if patch.Operation == PatchPut {
					if err := decodeInto(projected.chapters, DocumentVersion{Document: patch.Document, Content: patch.Content}); err != nil {
						t.Fatal(err)
					}
				}
			}
			err := validateTaskSubmission(c.task, story{}, projected, c.patches)
			if c.wantErr == "" && err != nil {
				t.Fatalf("rejected: %v", err)
			}
			if c.wantErr != "" && (!errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), c.wantErr)) {
				t.Fatalf("err = %v, want %q", err, c.wantErr)
			}
		})
	}
	stale := task(OperationRewriteAffected, &RewriteAffectedInput{ChapterIDs: []string{"chapter-1"}, BaseRevision: 2, ResolutionProposalID: "p", Reason: "改设定"})
	if err := validateTaskSubmission(stale, story{}, story{}, nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("affected rewrite input must match its snapshot: %v", err)
	}
}
