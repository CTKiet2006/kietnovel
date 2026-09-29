package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	"github.com/charmbracelet/lipgloss"
	"github.com/voocel/ainovel-cli/internal/host"
)

// renderTopBar vẽ thanh trạng thái trên đỉnh.
// Trái: provider/model, giữa: tên sách, phải: viên trạng thái.
func renderTopBar(snap host.UISnapshot, width int, spinnerFrame, version string) string {
	bookTitle := snap.BookTitle
	if bookTitle == "" {
		bookTitle = "Chưa đặt tên"
	}

	var infoParts []string
	if version != "" {
		infoParts = append(infoParts, "ainovel-cli "+version)
	}
	if snap.Provider != "" {
		infoParts = append(infoParts, snap.Provider)
	}
	if snap.ModelName != "" {
		if w := formatContextWindow(snap.ModelContextWindow); w != "" {
			infoParts = append(infoParts, snap.ModelName+"("+w+")")
		} else {
			infoParts = append(infoParts, snap.ModelName)
		}
	}
	if snap.Style != "" && snap.Style != "default" {
		infoParts = append(infoParts, snap.Style)
	}
	leftText := strings.Join(infoParts, " · ")

	label := snap.StatusLabel
	if label == "" {
		label = "READY"
	}
	color, ok := statusColors[label]
	if !ok {
		color = colorDim
	}
	disp, ok := statusDisplay[label]
	if !ok {
		disp = struct {
			icon  string
			label string
		}{"○", strings.ToLower(label)}
	}
	icon := disp.icon
	if snap.IsRunning && spinnerFrame != "" {
		icon = spinnerFrame
	}
	var status string
	if icon != "" {
		status = statusIconStyle.Foreground(color).Render(icon) + " " + statusLabelStyle.Render(disp.label)
	} else {
		status = statusLabelStyle.Render(disp.label)
	}

	innerW := max(12, width-2)
	titleText := truncate(bookTitle, max(8, innerW/3))
	centerW := max(16, lipgloss.Width(titleText)+6)
	if centerW > innerW-24 {
		centerW = max(8, innerW-24)
	}
	sideTotal := innerW - centerW
	if sideTotal < 0 {
		sideTotal = 0
		centerW = innerW
	}
	leftW := sideTotal / 2
	rightW := innerW - centerW - leftW

	leftCell := lipgloss.NewStyle().
		Width(leftW).
		AlignHorizontal(lipgloss.Left).
		Foreground(colorDim).
		Render(truncate(leftText, leftW))
	centerCell := lipgloss.NewStyle().
		Width(centerW).
		AlignHorizontal(lipgloss.Center).
		Bold(true).
		Foreground(bodyTextColor).
		Render(titleText)
	rightCell := lipgloss.NewStyle().
		Width(rightW).
		AlignHorizontal(lipgloss.Right).
		Render(status)

	content := leftCell + centerCell + rightCell
	return topBarStyle.Width(width).
		Border(baseBorder, false, false, true, false).
		BorderForeground(colorDim).
		Render(content)
}

// renderStatePanel bọc nội dung cột trạng thái (đã ở stateVP) vào hộp có viền phải.
// Đối xứng với renderDetailPanel: nội dung do renderStateContent sinh rồi đưa vào viewport, ở đây chỉ lo khung.
// MaxHeight kẹp cao, chống cửa sổ co lại tràn cao hơn cột phải (xem khế ước chiều cao trong panels_test.go).
func renderStatePanel(vp viewport.Model, width, height int, focused bool) string {
	borderColor := colorDim
	if focused {
		borderColor = colorAccent
	}
	style := lipgloss.NewStyle().
		Width(width).
		Height(height).
		MaxHeight(height).
		Border(baseBorder, false, true, false, false).
		BorderForeground(borderColor).
		Padding(1, 1, 0, 1)
	return style.Render(vp.View())
}

// renderDetailPanel vẽ panel chi tiết cuộn được bên phải.
func renderDetailPanel(vp viewport.Model, width, height int, focused bool) string {
	borderColor := colorDim
	if focused {
		borderColor = colorAccent
	}
	style := lipgloss.NewStyle().
		Width(width).
		Height(height).
		MaxHeight(height).
		Border(baseBorder, false, false, false, true).
		BorderForeground(borderColor).
		Padding(0, 1)

	return style.Render(vp.View())
}

// renderWelcome vẽ màn hình đầu trạng thái mới.
func renderWelcome(width, height int, errMsg string, mode startupMode, importHint, updateHint string) string {
	// Tiêu đề gọn
	title := lipgloss.NewStyle().
		Foreground(colorAccent).
		Bold(true).
		Render("A I N O V E L")

	// Phụ đề
	subtitle := lipgloss.NewStyle().
		Foreground(colorMuted).
		Italic(true).
		Render("Engine sáng tác tiểu thuyết bằng AI")

	// Đường phân cách
	divW := 44
	if divW > width-8 {
		divW = width - 8
	}
	divider := lipgloss.NewStyle().Foreground(colorDim).
		Render(strings.Repeat("~", divW))

	// Điểm nổi bật
	features := []struct{ icon, label, desc string }{
		{">>", "Đa model phối hợp", "Architect dàn ý / Writer viết / Editor duyệt"},
		{"::", "Tự nối khi đứt đoạn", "Sập hay ngắt giữa chừng vẫn viết tiếp từ chỗ cũ"},
		{"<>", "Can thiệp trực tiếp", "Đang viết vẫn chỉnh hướng truyện được"},
		{"##", "Truyện dài phân tầng", "Viết trường thiên theo tầng tập - cung - chương"},
	}
	iconStyle := lipgloss.NewStyle().Foreground(colorAccent2).Bold(true)
	featLabelStyle := lipgloss.NewStyle().Foreground(bodyTextColor)
	descStyle := lipgloss.NewStyle().Foreground(colorDim)
	var featLines []string
	for _, f := range features {
		line := iconStyle.Render(f.icon) + " " +
			featLabelStyle.Render(f.label) + "  " +
			descStyle.Render(f.desc)
		featLines = append(featLines, line)
	}
	feats := strings.Join(featLines, "\n")

	// Gợi ý nhập
	prompt := lipgloss.NewStyle().Foreground(bodyTextColor).Render("Nhập ý tưởng truyện của bạn ở dưới để bắt đầu")

	modeLine := lipgloss.NewStyle().
		Foreground(colorMuted).
		Render("Chế độ hiện tại: " + mode.label() + " · " + mode.subtitle())

	// Ví dụ
	examples := []string{
		"Viết truyện đô thị huyền nghi 12 chương, chính là nữ pháp y",
		"Sáng tác trường thiên tiên hiệp, chính từ phàm nhân tu tới phi thăng",
		"Viết truyện ngắn khoa học viễn tưởng về AI thức tỉnh và nghịch lý đạo đức",
	}
	exStyle := lipgloss.NewStyle().Foreground(colorAccent)
	dotStyle := lipgloss.NewStyle().Foreground(colorDim)
	var exLines []string
	for _, ex := range examples {
		exLines = append(exLines, dotStyle.Render("  . ")+exStyle.Render(ex))
	}
	exBlock := strings.Join(exLines, "\n")

	// Ráp lại
	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(title)
	b.WriteString("\n")
	b.WriteString(subtitle)
	b.WriteString("\n\n")
	b.WriteString(divider)
	b.WriteString("\n\n")
	b.WriteString(feats)
	b.WriteString("\n\n")
	b.WriteString(divider)
	b.WriteString("\n\n")
	b.WriteString(modeLine)
	b.WriteString("\n\n")
	b.WriteString(prompt)
	b.WriteString("\n\n")
	b.WriteString(exBlock)
	b.WriteString("\n\n")
	if importHint != "" {
		// Sách này kẹt giữa chừng lúc nhập: gợi ý lối khôi phục cho nổi, thay gợi ý nhập thường.
		b.WriteString(lipgloss.NewStyle().Foreground(colorAccent2).Bold(true).
			Render("! " + importHint))
	} else {
		b.WriteString(lipgloss.NewStyle().Foreground(colorDim).
			Render("Có dàn ý/thiết lập sẵn? /start <đường dẫn file> để tạo truyện mới · Có bản thảo cũ? /import <đường dẫn file> để nhập vào viết tiếp"))
	}
	if updateHint != "" {
		// Kiểm tra bản mới lúc khởi động trúng bản mới: thêm một dòng cùng kiểu nhấn như importHint.
		b.WriteString("\n")
		b.WriteString(lipgloss.NewStyle().Foreground(colorAccent2).Bold(true).
			Render("! " + updateHint))
	}
	b.WriteString("\n\n")
	b.WriteString(lipgloss.NewStyle().Foreground(colorDim).Italic(true).
		Render("Tab đổi chế độ · Bắt đầu nhanh thì Enter là viết · Đồng sáng tác thì Enter để trò chuyện"))

	if errMsg != "" {
		b.WriteString("\n\n")
		b.WriteString(lipgloss.NewStyle().Foreground(colorError).Bold(true).Render("! " + errMsg))
	}

	return lipgloss.NewStyle().
		Width(width).
		Height(height).
		AlignHorizontal(lipgloss.Center).
		AlignVertical(lipgloss.Center).
		Render(b.String())
}
