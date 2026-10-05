package tools

import (
	"os"
	"strings"
	"testing"

	"github.com/CTKiet2006/kietnovel/internal/store"
)

func TestToolsLocalizationTriLanguage(t *testing.T) {
	for _, lang := range []string{"vi", "en", "zh"} {
		t.Run("lang_"+lang, func(t *testing.T) {
			dir, err := os.MkdirTemp("", "kietnovel-tool-i18n-*")
			if err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(dir)

			s := store.NewStore(dir)
			if err := s.BookLanguage.Save(lang); err != nil {
				t.Fatal(err)
			}

			draftTool := NewDraftChapterTool(s)
			planTool := NewPlanChapterTool(s)
			commitTool := NewCommitChapterTool(s, NewStyleStatsIndex(s))
			contextTool := NewContextTool(s, References{}, "default", NewStyleStatsIndex(s))

			switch lang {
			case "vi":
				if !strings.Contains(draftTool.Description(), "Ghi nội dung chương") {
					t.Fatalf("draftTool vi description mismatch: %s", draftTool.Description())
				}
				if draftTool.Label() != "Ghi chương" {
					t.Fatalf("draftTool vi label mismatch: %s", draftTool.Label())
				}
				if !strings.Contains(planTool.Description(), "Lưu ý đồ sáng tác") {
					t.Fatalf("planTool vi description mismatch: %s", planTool.Description())
				}
				if commitTool.Label() != "Nộp chương" {
					t.Fatalf("commitTool vi label mismatch: %s", commitTool.Label())
				}
				if contextTool.Label() != "Tải ngữ cảnh" {
					t.Fatalf("contextTool vi label mismatch: %s", contextTool.Label())
				}
			case "en":
				if !strings.Contains(draftTool.Description(), "Write chapter prose") {
					t.Fatalf("draftTool en description mismatch: %s", draftTool.Description())
				}
				if draftTool.Label() != "Draft chapter" {
					t.Fatalf("draftTool en label mismatch: %s", draftTool.Label())
				}
				if !strings.Contains(planTool.Description(), "Save chapter writing outline") {
					t.Fatalf("planTool en description mismatch: %s", planTool.Description())
				}
				if commitTool.Label() != "Commit chapter" {
					t.Fatalf("commitTool en label mismatch: %s", commitTool.Label())
				}
				if contextTool.Label() != "Load context" {
					t.Fatalf("contextTool en label mismatch: %s", contextTool.Label())
				}
			case "zh":
				if !strings.Contains(draftTool.Description(), "写入章节正文") {
					t.Fatalf("draftTool zh description mismatch: %s", draftTool.Description())
				}
				if draftTool.Label() != "写入章节" {
					t.Fatalf("draftTool zh label mismatch: %s", draftTool.Label())
				}
				if !strings.Contains(planTool.Description(), "保存章节写作构思") {
					t.Fatalf("planTool zh description mismatch: %s", planTool.Description())
				}
				if commitTool.Label() != "提交章节" {
					t.Fatalf("commitTool zh label mismatch: %s", commitTool.Label())
				}
				if contextTool.Label() != "加载上下文" {
					t.Fatalf("contextTool zh label mismatch: %s", contextTool.Label())
				}
			}
		})
	}
}
