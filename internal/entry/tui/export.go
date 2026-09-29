package tui

import (
	"context"
	"fmt"
	"github.com/CTKiet2006/kietnovel/internal/i18n"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/CTKiet2006/kietnovel/internal/host"
	"github.com/CTKiet2006/kietnovel/internal/host/exp"
)

// exportDoneMsg là kết quả cuối của lệnh /export.
//
// Không như /import chạy theo dòng sự kiện: xuất là IO local đồng bộ, không có tiến độ giữa chừng;
// chạy xong trong goroutine rồi bắn một lần tin này về.
type exportDoneMsg struct {
	result *exp.Result
	err    error
}

// startExport phân tích tham số rồi trả về tea.Cmd.
// Xuất thật chạy trong tea.Cmd (tránh chặn UI), xong thì gửi exportDoneMsg.
func startExport(rt *host.Host, args []string) (tea.Cmd, error) {
	opts, err := parseExportArgs(args)
	if err != nil {
		return nil, err
	}
	return func() tea.Msg {
		res, err := rt.Export(context.Background(), opts)
		return exportDoneMsg{result: res, err: err}
	}, nil
}

// parseExportArgs phân tích `/export [path] [from=N] [to=M] [--overwrite]`.
//
// Tham số vị trí: tối đa một, làm đường dẫn xuất; thiếu thì exp.Run tự quyết ({novelDir}/{BookMetadata.Title}.txt).
func parseExportArgs(args []string) (exp.Options, error) {
	var opts exp.Options
	for _, a := range args {
		if a == "--overwrite" {
			opts.Overwrite = true
			continue
		}
		if k, v, ok := strings.Cut(a, "="); ok {
			switch strings.ToLower(k) {
			case "from":
				n, err := strconv.Atoi(v)
				if err != nil || n < 0 {
					return exp.Options{}, fmt.Errorf(i18n.T("from phải là số nguyên không âm: %q"), v)
				}
				opts.From = n
			case "to":
				n, err := strconv.Atoi(v)
				if err != nil || n < 0 {
					return exp.Options{}, fmt.Errorf(i18n.T("to phải là số nguyên không âm: %q"), v)
				}
				opts.To = n
			default:
				return exp.Options{}, fmt.Errorf(i18n.T("Tham số không rõ %q (hỗ trợ: from / to)"), k)
			}
			continue
		}
		if strings.HasPrefix(a, "-") {
			return exp.Options{}, fmt.Errorf(i18n.T("Flag không rõ %q"), a)
		}
		if opts.OutPath != "" {
			return exp.Options{}, fmt.Errorf(i18n.T("Chỉ hỗ trợ một tham số đường dẫn: %q"), a)
		}
		opts.OutPath = a
	}
	return opts, nil
}

// formatExportSuccess vẽ Result thành Summary sự kiện.
func formatExportSuccess(res *exp.Result) string {
	var b strings.Builder
	fmt.Fprintf(&b, i18n.T("✓ Đã xuất %d chương / %s ra %s"), res.Chapters, humanBytes(res.Bytes), res.Path)
	if n := len(res.Skipped); n > 0 {
		fmt.Fprintf(&b, i18n.T(" (bỏ qua %d chương chưa xong: %s)"), n, briefIntList(res.Skipped, 5))
	}
	return b.String()
}

func humanBytes(n int) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	default:
		return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
	}
}

func briefIntList(xs []int, max int) string {
	if len(xs) == 0 {
		return ""
	}
	parts := make([]string, 0, len(xs))
	for i, x := range xs {
		if i >= max {
			parts = append(parts, "...")
			break
		}
		parts = append(parts, strconv.Itoa(x))
	}
	return strings.Join(parts, ",")
}
