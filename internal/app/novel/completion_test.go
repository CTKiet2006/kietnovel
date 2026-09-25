package novel

import (
	"strings"
	"testing"

	projectdoc "github.com/voocel/ainovel-cli/internal/app/project"
	"github.com/voocel/ainovel-cli/internal/domain/model"
)

func TestCompletionContractRejectsExtraPlanChapters(t *testing.T) {
	project := projectdoc.Snapshot{
		Plan: []model.PlanNode{
			{ID: "chapter-1", Kind: model.PlanChapter, Order: 1, Title: "第一章", Summary: "开端"},
			{ID: "chapter-2", Kind: model.PlanChapter, Order: 2, Title: "第二章", Summary: "推进"},
		},
		Manuscript: []model.ManuscriptChapter{
			{ID: "manuscript-1", PlanNodeID: "chapter-1", Number: 1, Title: "第一章", Blocks: []model.ManuscriptBlock{{ID: "block-1", Text: "正文"}}},
			{ID: "manuscript-2", PlanNodeID: "chapter-2", Number: 2, Title: "第二章", Blocks: []model.ManuscriptBlock{{ID: "block-2", Text: "正文"}}},
		},
	}
	if unmet := completionUnmet(project, Length{Fixed: 1, Final: 1}); !strings.Contains(unmet, "蓝图有 2 章") {
		t.Fatalf("completion mismatch = %q", unmet)
	}
	// 篇幅交给 AI 且尚未收官（D63）：内容再齐也不能完成。
	if unmet := completionUnmet(project, Length{Compass: &model.Compass{ScaleMax: 10, Ending: "收束"}}); unmet != "全书尚未收官" {
		t.Fatalf("open-phase completion = %q", unmet)
	}
	if unmet := completionUnmet(project, lengthOf(projectdoc.Snapshot{Plan: project.Plan, Manuscript: project.Manuscript,
		Compass: &model.Compass{ScaleMax: 10, Ending: "收束", Final: 2}}, 0)); unmet != "" {
		t.Fatalf("committed finale completion = %q", unmet)
	}
}
