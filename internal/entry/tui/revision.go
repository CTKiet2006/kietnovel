package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/voocel/ainovel-cli/internal/host"
	"github.com/voocel/ainovel-cli/internal/revision"
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
			return nil, false, fmt.Errorf("未知参数 %q（支持：--check）", arg)
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
		return "未检测到章节外部修改"
	}
	parts := make([]string, 0, len(result.Analyses))
	for i, analysis := range result.Analyses {
		if i >= len(result.Applied) {
			break
		}
		part := fmt.Sprintf("第%d章：%s", result.Applied[i], analysis.ChangeSummary)
		if analysis.StoryChanged {
			part += "（剧情事实已更新）"
		}
		if len(analysis.DownstreamIssues) > 0 {
			part += fmt.Sprintf("（发现%d项后续冲突）", len(analysis.DownstreamIssues))
		}
		parts = append(parts, part)
	}
	summary := fmt.Sprintf("已接纳章节修订：%v", result.Applied)
	if len(parts) > 0 {
		summary += "；" + strings.Join(parts, "；")
	}
	return summary
}
