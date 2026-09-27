package workbench

import (
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
