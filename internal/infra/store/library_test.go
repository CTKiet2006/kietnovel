package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/voocel/ainovel-cli/internal/domain/model"
)

func TestLibrarySummaryCountsLatestLiveChapters(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	target := model.AuthorityTarget{Kind: model.AuthorityProject, ID: "long-book"}
	intent := json.RawMessage(`{"premise":"雨夜来信"}`)
	compass := json.RawMessage(`{"scale_max":600,"ending":"送完最后一封信","final":500}`)
	compassRef := model.DocumentRef{Kind: model.DocumentCompass, ID: model.SingletonDocumentID}
	patches := []model.Patch{
		{Document: model.DocumentRef{Kind: model.DocumentIntent, ID: "root"}, Operation: model.PatchPut, Content: intent},
		{Document: compassRef, Operation: model.PatchPut, Content: compass},
		// 已规划章数只数 chapter 节点：卷弧不算。
		{Document: model.DocumentRef{Kind: model.DocumentPlan, ID: "arc-1"}, Operation: model.PatchPut, Content: json.RawMessage(`{"id":"arc-1","kind":"arc","parent_id":"v","order":1,"title":"弧","summary":"s"}`)},
		{Document: model.DocumentRef{Kind: model.DocumentPlan, ID: "c-1"}, Operation: model.PatchPut, Content: json.RawMessage(`{"id":"c-1","kind":"chapter","parent_id":"arc-1","order":1,"title":"章","summary":"s"}`)},
		{Document: model.DocumentRef{Kind: model.DocumentPlan, ID: "c-2"}, Operation: model.PatchPut, Content: json.RawMessage(`{"id":"c-2","kind":"chapter","parent_id":"arc-1","order":2,"title":"章","summary":"s"}`)},
	}
	body, _ := json.Marshal(map[string]string{"text": strings.Repeat("雨夜来信", 500)})
	for i := 1; i <= 500; i++ {
		patches = append(patches, model.Patch{Document: model.DocumentRef{Kind: model.DocumentManuscript, ID: fmt.Sprint(i)}, Operation: model.PatchPut, Content: body})
	}
	if _, err := commitTestProposal(ctx, s, testChange("initial", target, 0, patches...)); err != nil {
		t.Fatal(err)
	}
	revisions := []model.Patch{{Document: model.DocumentRef{Kind: model.DocumentManuscript, ID: "1"}, Operation: model.PatchPut, Content: body}, {Document: model.DocumentRef{Kind: model.DocumentManuscript, ID: "2"}, Operation: model.PatchDelete}}
	if _, err := commitTestProposal(ctx, s, testChange("revision", target, 1, revisions...)); err != nil {
		t.Fatal(err)
	}
	summaries, err := s.ListProjectSummaries(ctx)
	if err != nil || len(summaries) != 1 || summaries[0].Written != 499 || summaries[0].Planned != 2 ||
		string(summaries[0].Intent) != string(intent) || string(summaries[0].Compass) != string(compass) {
		t.Fatalf("summary = %+v, %v", summaries, err)
	}
	// 罗盘删除后摘要里没有罗盘，而不是读到删除前的旧版本。
	if _, err := commitTestProposal(ctx, s, testChange("drop-compass", target, 2, model.Patch{Document: compassRef, Operation: model.PatchDelete})); err != nil {
		t.Fatal(err)
	}
	if summaries, err = s.ListProjectSummaries(ctx); err != nil || summaries[0].Compass != nil {
		t.Fatalf("summary after compass delete = %+v, %v", summaries, err)
	}
}
