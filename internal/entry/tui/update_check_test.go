package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	buildversion "github.com/voocel/ainovel-cli/internal/version"
)

func TestUpdateNotesPreviewSanitizesAndTruncates(t *testing.T) {
	notes := "\x1b[31m## 重要更新\x1b[0m\x00\n" + strings.Repeat("后续内容", 40)
	got := updateNotesPreview(notes)
	if got != "重要更新" {
		t.Fatalf("摘要未正确清理 Markdown/ANSI/控制字符: %q", got)
	}

	got = updateNotesPreview(strings.Repeat("更新", 100))
	if lipgloss.Width(got) > updateNotesPreviewWidth {
		t.Fatalf("摘要宽度超限: width=%d text=%q", lipgloss.Width(got), got)
	}
}

func TestFormatUpdateNoticeIncludesSafePreview(t *testing.T) {
	got := formatUpdateNotice(&buildversion.CheckResult{
		Latest: "v1.2.4",
		Notes:  "## 修复启动问题",
	})
	for _, want := range []string{"v1.2.4", "修复启动问题", "ainovel-cli update"} {
		if !strings.Contains(got, want) {
			t.Fatalf("更新提示 %q 缺少 %q", got, want)
		}
	}
}
