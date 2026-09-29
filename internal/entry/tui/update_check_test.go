package tui

import (
	"strings"
	"testing"

	buildversion "github.com/CTKiet2006/kietnovel/internal/version"
	"github.com/charmbracelet/lipgloss"
)

func TestUpdateNotesPreviewSanitizesAndTruncates(t *testing.T) {
	notes := "\x1b[31m## 重要更新\x1b[0m\x00\n" + strings.Repeat("后续内容", 40)
	got := updateNotesPreview(notes)
	if got != "重要更新" {
		t.Fatalf("Tóm tắt chưa làm sạch Markdown/ANSI/ký tự điều khiển đúng: %q", got)
	}

	got = updateNotesPreview(strings.Repeat("更新", 100))
	if lipgloss.Width(got) > updateNotesPreviewWidth {
		t.Fatalf("Rộng tóm tắt vượt giới hạn: width=%d text=%q", lipgloss.Width(got), got)
	}
}

func TestFormatUpdateNoticeIncludesSafePreview(t *testing.T) {
	got := formatUpdateNotice(&buildversion.CheckResult{
		Latest: "v1.2.4",
		Notes:  "## 修复启动问题",
	})
	for _, want := range []string{"v1.2.4", "修复启动问题", buildversion.AppName + " update"} {
		if !strings.Contains(got, want) {
			t.Fatalf("Thông báo cập nhật %q thiếu %q", got, want)
		}
	}
}
