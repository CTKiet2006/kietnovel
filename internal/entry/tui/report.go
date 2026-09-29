package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/voocel/ainovel-cli/internal/diag"
)

type reportState struct {
	reqID      int
	report     *diag.Report
	exportPath string // Đường dẫn file chẩn đoán đã khử nhạy cảm, vẽ ở đầu báo cáo để dán vào issue
	exportErr  error
	loading    bool
	renderW    int
	startedAt  time.Time
	finishedAt time.Time
	viewport   viewport.Model
}

func newReportState(width, height int, reqID int, startedAt time.Time) *reportState {
	boxW, boxH := reportModalSize(width, height)
	contentW := paddedModalContentWidth(boxW)
	vp := viewport.New(contentW, boxH-4) // border 2 + padding 2
	state := &reportState{
		reqID:     reqID,
		loading:   true,
		startedAt: startedAt,
		viewport:  vp,
	}
	state.setContent(contentW)
	return state
}

func (s *reportState) load(report diag.Report, contentW int, exportPath string, exportErr error, finishedAt time.Time) {
	s.loading = false
	s.report = &report
	s.exportPath = exportPath
	s.exportErr = exportErr
	s.finishedAt = finishedAt
	s.setContent(contentW)
}

func (s *reportState) setContent(contentW int) {
	s.renderW = contentW
	switch {
	case s.loading:
		s.viewport.SetContent(renderReportLoadingText(contentW, s.startedAt))
	case s.report != nil:
		s.viewport.SetContent(renderReportText(*s.report, contentW, s.exportPath, s.exportErr, s.startedAt, s.finishedAt))
	default:
		s.viewport.SetContent("Báo cáo chẩn đoán không khả dụng")
	}
}

func reportModalSize(termW, termH int) (int, int) {
	w := termW * 80 / 100
	if w > 100 {
		w = 100
	}
	if w < 60 {
		w = termW - 4
	}
	h := termH * 85 / 100
	if h < 20 {
		h = termH - 2
	}
	return w, h
}

func renderReportText(report diag.Report, width int, exportPath string, exportErr error, startedAt, finishedAt time.Time) string {
	var b strings.Builder
	st := report.Stats

	// Tổng quan
	titleStyle := lipgloss.NewStyle().Foreground(colorAccent).Bold(true)
	dimStyle := lipgloss.NewStyle().Foreground(colorDim)
	mutedStyle := lipgloss.NewStyle().Foreground(colorMuted)

	// Chẩn đoán khử nhạy cảm đã xuất → hướng người dùng dán issue
	if exportPath != "" {
		exportStyle := lipgloss.NewStyle().Foreground(colorAccent2)
		b.WriteString(exportStyle.Render("Đã xuất chẩn đoán khử nhạy cảm (dán được vào GitHub issue)"))
		b.WriteString("\n")
		b.WriteString(dimStyle.Render(wrapText(exportPath, width)))
		b.WriteString("\n\n")
	} else if exportErr != nil {
		b.WriteString(lipgloss.NewStyle().Foreground(colorError).Render("Xuất chẩn đoán khử nhạy cảm thất bại: " + exportErr.Error()))
		b.WriteString("\n\n")
	}

	b.WriteString(titleStyle.Render("Tổng quan"))
	b.WriteString("\n\n")
	b.WriteString(dimStyle.Render("Bắt đầu "))
	b.WriteString(formatReportTime(startedAt))
	if !finishedAt.IsZero() {
		b.WriteString(dimStyle.Render("  Xong "))
		b.WriteString(formatReportTime(finishedAt))
	}
	b.WriteString("\n\n")

	// Dòng 1: chương + số chữ
	b.WriteString(mutedStyle.Render("Chương "))
	b.WriteString(fmt.Sprintf("%d/%d", st.CompletedChapters, st.TotalChapters))
	b.WriteString(mutedStyle.Render("  Số chữ "))
	b.WriteString(fmt.Sprintf("%d", st.TotalWords))
	if st.AvgWordsPerCh > 0 {
		b.WriteString(dimStyle.Render(fmt.Sprintf(" (%d/ch)", st.AvgWordsPerCh)))
	}
	b.WriteString(mutedStyle.Render("  Giai đoạn "))
	b.WriteString(st.Phase)
	if st.Flow != "" && st.Flow != "writing" {
		b.WriteString(mutedStyle.Render("/"))
		b.WriteString(st.Flow)
	}
	b.WriteString("\n")

	// Dòng 2: duyệt + viết lại + điểm TB
	b.WriteString(mutedStyle.Render("Duyệt "))
	b.WriteString(fmt.Sprintf("%d lần", st.ReviewCount))
	if st.RewriteCount > 0 {
		b.WriteString(mutedStyle.Render("  Viết lại "))
		b.WriteString(fmt.Sprintf("%d lần", st.RewriteCount))
	}
	if st.AvgReviewScore > 0 {
		b.WriteString(mutedStyle.Render("  Điểm TB "))
		b.WriteString(fmt.Sprintf("%.1f", st.AvgReviewScore))
	}
	b.WriteString("\n")

	// Dòng 3: nút thắt + kế hoạch
	if st.ForeshadowOpen > 0 || st.ForeshadowStale > 0 {
		b.WriteString(mutedStyle.Render("Nút thắt "))
		b.WriteString(fmt.Sprintf("Mở %d", st.ForeshadowOpen))
		if st.ForeshadowStale > 0 {
			b.WriteString(lipgloss.NewStyle().Foreground(colorReview).Render(fmt.Sprintf(" kẹt %d", st.ForeshadowStale)))
		}
		b.WriteString("\n")
	}
	if st.PlanningTier != "" {
		b.WriteString(mutedStyle.Render("Kế hoạch "))
		b.WriteString(st.PlanningTier)
		b.WriteString("\n")
	}

	// Phát hiện
	b.WriteString("\n")
	findings := report.Findings
	if len(findings) == 0 {
		b.WriteString(lipgloss.NewStyle().Foreground(colorSuccess).Render("Không phát hiện vấn đề"))
		b.WriteString("\n")
		return b.String()
	}

	criticals, warnings, infos := countSeverities(findings)
	b.WriteString(titleStyle.Render("Phát hiện"))
	b.WriteString(" ")
	b.WriteString(dimStyle.Render(formatSeverityCounts(criticals, warnings, infos)))
	b.WriteString("\n")

	for _, f := range findings {
		b.WriteString("\n")
		renderFinding(&b, f, width)
	}

	if len(report.Actions) > 0 {
		b.WriteString("\n")
		b.WriteString(titleStyle.Render("Việc có thể làm"))
		b.WriteString(" ")
		b.WriteString(dimStyle.Render(fmt.Sprintf("(%d)", len(report.Actions))))
		b.WriteString("\n")
		actionStyle := lipgloss.NewStyle().Foreground(colorSuccess)
		for _, a := range report.Actions {
			b.WriteString("\n")
			b.WriteString(actionStyle.Render("[" + string(a.Kind) + "]"))
			b.WriteString(" ")
			b.WriteString(a.Summary)
			b.WriteString("\n")
			if a.Message != "" {
				b.WriteString("  ")
				b.WriteString(mutedStyle.Render(wrapText(a.Message, width-4)))
				b.WriteString("\n")
			}
		}
	}

	return b.String()
}

func renderReportLoadingText(width int, startedAt time.Time) string {
	titleStyle := lipgloss.NewStyle().Foreground(colorAccent).Bold(true)
	bodyStyle := lipgloss.NewStyle().Foreground(colorMuted)
	hintStyle := lipgloss.NewStyle().Foreground(colorDim)

	var b strings.Builder
	b.WriteString(titleStyle.Render("Đang tạo báo cáo chẩn đoán"))
	b.WriteString("\n\n")
	b.WriteString(hintStyle.Render("Giờ bắt đầu " + formatReportTime(startedAt)))
	b.WriteString("\n\n")
	b.WriteString(bodyStyle.Render(wrapText("Đang đọc output truyện hiện tại để phân tích luồng, chất lượng, kế hoạch và vấn đề ngữ cảnh. Truyện lớn có thể mất vài giây.", width)))
	b.WriteString("\n\n")
	b.WriteString(hintStyle.Render("Esc đóng panel trước cũng được, phân tích nền xong lần mở sau sẽ tạo lại."))
	return b.String()
}

func formatReportTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Format("2006-01-02 15:04:05")
}

func renderFinding(b *strings.Builder, f diag.Finding, width int) {
	var sevStyle lipgloss.Style
	var marker string
	switch f.Severity {
	case diag.SevCritical:
		sevStyle = lipgloss.NewStyle().Foreground(colorError).Bold(true)
		marker = "critical"
	case diag.SevWarning:
		sevStyle = lipgloss.NewStyle().Foreground(colorReview)
		marker = "warning"
	default:
		sevStyle = lipgloss.NewStyle().Foreground(colorDim)
		marker = "info"
	}

	evidenceStyle := lipgloss.NewStyle().Foreground(colorDim)
	suggestionStyle := lipgloss.NewStyle().Foreground(colorAccent2)

	b.WriteString(sevStyle.Render(fmt.Sprintf("[%s]", marker)))
	b.WriteString(" ")
	b.WriteString(f.Title)
	if f.Confidence != "" || f.AutoLevel != "" {
		tagStyle := lipgloss.NewStyle().Foreground(colorDim)
		tags := ""
		if f.Confidence != "" {
			tags += string(f.Confidence)
		}
		if f.AutoLevel != "" && f.AutoLevel != diag.AutoNone {
			if tags != "" {
				tags += "/"
			}
			tags += string(f.AutoLevel)
		}
		if tags != "" {
			b.WriteString(" ")
			b.WriteString(tagStyle.Render("[" + tags + "]"))
		}
	}
	b.WriteString("\n")

	if f.Evidence != "" {
		b.WriteString("  ")
		b.WriteString(evidenceStyle.Render(wrapText(f.Evidence, width-4)))
		b.WriteString("\n")
	}
	if f.Suggestion != "" {
		b.WriteString("  ")
		b.WriteString(suggestionStyle.Render("-> " + wrapText(f.Suggestion, width-7)))
		b.WriteString("\n")
	}
}

func countSeverities(findings []diag.Finding) (c, w, i int) {
	for _, f := range findings {
		switch f.Severity {
		case diag.SevCritical:
			c++
		case diag.SevWarning:
			w++
		case diag.SevInfo:
			i++
		}
	}
	return
}

func formatSeverityCounts(c, w, i int) string {
	parts := make([]string, 0, 3)
	if c > 0 {
		parts = append(parts, fmt.Sprintf("%d critical", c))
	}
	if w > 0 {
		parts = append(parts, fmt.Sprintf("%d warning", w))
	}
	if i > 0 {
		parts = append(parts, fmt.Sprintf("%d info", i))
	}
	if len(parts) == 0 {
		return ""
	}
	return "(" + strings.Join(parts, " / ") + ")"
}

// wrapText xuống dòng đơn giản cho chữ dài.
func wrapText(s string, maxWidth int) string {
	if maxWidth <= 0 || lipgloss.Width(s) <= maxWidth {
		return s
	}
	var b strings.Builder
	lineW := 0
	for _, r := range s {
		// Chỗ xuống dòng sẵn có phải reset độ rộng dòng: '\n' rộng 0, không reset sẽ cộng dồn độ rộng
		// nhiều dòng thành quá khổ, từ dòng bị ngắt đầu tiên mỗi dòng sau đều bị chèn ngắt giả + thụt (vỡ cả khối).
		if r == '\n' {
			b.WriteRune(r)
			lineW = 0
			continue
		}
		w := lipgloss.Width(string(r))
		if lineW+w > maxWidth && lineW > 0 {
			b.WriteRune('\n')
			b.WriteString("  ") // indent continuation
			lineW = 2
		}
		b.WriteRune(r)
		lineW += w
	}
	return b.String()
}

func renderReportModal(width, height int, state *reportState) string {
	if state == nil {
		return ""
	}

	boxW, boxH := reportModalSize(width, height)

	contentW := paddedModalContentWidth(boxW)

	// Nếu kích thước viewport đổi thì cập nhật
	if state.viewport.Width != contentW {
		state.viewport.Width = contentW
		state.viewport.Height = boxH - 4
	}
	if state.viewport.Height != boxH-4 {
		state.viewport.Height = boxH - 4
	}
	if state.renderW != contentW {
		state.setContent(contentW)
	}

	modal := renderPaddedModalFrame(
		boxW,
		boxH,
		"Báo cáo chẩn đoán",
		"  ↑↓ cuộn · Esc đóng",
		strings.Split(state.viewport.View(), "\n"),
	)
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, modal)
}

func (m Model) handleReportKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.report == nil {
		return m, nil
	}
	switch msg.Type {
	case tea.KeyEsc:
		m.report = nil
		return m, m.textarea.Focus()
	case tea.KeyUp:
		m.report.viewport.ScrollUp(1)
		return m, nil
	case tea.KeyDown:
		m.report.viewport.ScrollDown(1)
		return m, nil
	case tea.KeyPgUp:
		m.report.viewport.HalfPageUp()
		return m, nil
	case tea.KeyPgDown:
		m.report.viewport.HalfPageDown()
		return m, nil
	default:
		return m, nil
	}
}
