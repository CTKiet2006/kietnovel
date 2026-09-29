package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/CTKiet2006/kietnovel/internal/host"
	"github.com/CTKiet2006/kietnovel/internal/revision"
)

type revisionDoneMsg struct {
	checkOnly bool
	chapters  []int
	result    *revision.Result
	err       error
}

func startRevisionSync(rt *host.Host, args []string) (tea.Cmd, bool, error) {
	checkOnly := false
	for _, arg := range args {
		switch arg {
		case "--check":
			checkOnly = true
		default:
			return nil, false, fmt.Errorf("Tham số không rõ %q (hỗ trợ: --check)", arg)
		}
	}
	return func() tea.Msg {
		if checkOnly {
			chapters, err := rt.CheckChapterRevisions()
			return revisionDoneMsg{checkOnly: true, chapters: chapters, err: err}
		}
		result, err := rt.SyncChapterRevisions(context.Background())
		return revisionDoneMsg{result: result, err: err}
	}, checkOnly, nil
}

func formatRevisionResult(result *revision.Result) string {
	if result == nil || len(result.Applied) == 0 {
		return "Không phát hiện chương nào bị sửa từ bên ngoài"
	}
	parts := make([]string, 0, len(result.Analyses))
	for i, analysis := range result.Analyses {
		if i >= len(result.Applied) {
			break
		}
		part := fmt.Sprintf("Chương %d: %s", result.Applied[i], analysis.ChangeSummary)
		if analysis.StoryChanged {
			part += " (sự kiện cốt truyện đã cập nhật)"
		}
		if len(analysis.DownstreamIssues) > 0 {
			part += fmt.Sprintf(" (phát hiện %d xung đột tiếp theo)", len(analysis.DownstreamIssues))
		}
		parts = append(parts, part)
	}
	summary := fmt.Sprintf("Đã nhận bản sửa chương: %v", result.Applied)
	if len(parts) > 0 {
		summary += "; " + strings.Join(parts, "; ")
	}
	return summary
}
