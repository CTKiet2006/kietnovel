package bootstrap_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	projectdoc "github.com/voocel/ainovel-cli/internal/app/project"
	"github.com/voocel/ainovel-cli/internal/domain/model"
)

func TestLibraryUsesCurrentRunGoalAndRecentActivity(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	api := newTestApp(store)
	for _, id := range []string{"a", "z", "idle"} {
		project, err := api.Projects.CreateProject(ctx, projectdoc.CreateProjectCommand{ProjectID: id, ChangeID: "create-" + id, UserID: "user", Reason: "test", Draft: testProjectDraft(), CreatedAt: testTime()})
		if err != nil {
			t.Fatal(err)
		}
		// 固定篇幅的运行优先于罗盘；没有运行时全书章数取收官承诺（D63）。
		compass, _ := json.Marshal(model.Compass{ScaleMax: 600, Ending: "送完最后一封信", Final: 500})
		editCanonEvidence(t, api, project, "compass-"+id, model.Patch{
			Document: model.DocumentRef{Kind: model.DocumentCompass, ID: model.SingletonDocumentID}, Operation: model.PatchPut, Content: compass,
		})
	}
	a := ensureTestRun(t, ctx, store, "a", testTime())
	ensureTestRun(t, ctx, store, "z", testTime().Add(time.Minute))
	entries, err := api.Workbench.Library(ctx)
	if err != nil || len(entries) != 3 || entries[0].ID != "z" || entries[0].Target != 3 || entries[0].State != model.RunRunning || entries[2].ID != "idle" || entries[2].Target != 500 {
		t.Fatalf("library: %+v %v", entries, err)
	}
	if _, err := api.Runs.PauseCreationRun(ctx, a, testTime().Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	entries, err = api.Workbench.Library(ctx)
	if err != nil || entries[0].ID != "a" || entries[0].State != model.RunPaused {
		t.Fatalf("updated library: %+v %v", entries, err)
	}
}
