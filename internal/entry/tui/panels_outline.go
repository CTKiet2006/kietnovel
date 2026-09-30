package tui

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/CTKiet2006/kietnovel/internal/host"
	"github.com/CTKiet2006/kietnovel/internal/i18n"
	"github.com/charmbracelet/lipgloss"
)

// outlineGridThreshold là ngưỡng số chương để dàn ý chuyển nhiều cột.
// Bậc short tối đa 25 chương, dưới 20 cột đơn vừa một màn hình mà vẫn giữ được nhãn "đang làm";
// truyện dài chế độ layered cuộn bung ra thì n tự vượt 20, mượt mà chuyển nhiều cột.
const outlineGridThreshold = 20

// scaleRangeRe bóc khoang so kiem tu estimated_scale.
var scaleRangeRe = regexp.MustCompile(`\d+(?:\s*[-–~]\s*\d+)?`)

// compassScaleRange bóc phan so cua estimated_scale.
//
// Protocol moi yeu cau chi tra ve khoang so thuan ("4-6") de lop voice ghep don vi
// theo ngon ngu dang chon. Nhung sach da tao truoc do luu kem chu vi du
// "预计 4-6 卷", va LLM van co the tu them tu. Boc so giup ca hai dang hien thi
// giong nhau, ma khong phai do ten don vi cua mo hinh co khop hay khong.
func compassScaleRange(s string) string {
	return scaleRangeRe.FindString(strings.TrimSpace(s))
}

// renderOutlineSection chọn layout theo số chương: ít thì cột đơn (kèm nhãn "đang làm"), nhiều thì lưới nhiều cột.
func renderOutlineSection(snap host.UISnapshot, contentW int) string {
	if len(snap.Outline) < outlineGridThreshold {
		return renderOutlineList(snap, contentW)
	}
	return renderOutlineGrid(snap, contentW)
}

// renderOutlineList là danh sách chương cột đơn (dùng cho truyện ngắn). Cuối mỗi dòng có nhãn "đang làm",
// nhịp đọc dọc gần với mục lục hơn.
func renderOutlineList(snap host.UISnapshot, contentW int) string {
	var b strings.Builder
	for _, e := range snap.Outline {
		ch := fmt.Sprintf("%2d", e.Chapter)
		var marker, chStyle string
		titleStyle := cardContentStyle
		switch {
		case snap.CompletedCount >= e.Chapter:
			marker = lipgloss.NewStyle().Foreground(colorSuccess).Render("●")
			chStyle = lipgloss.NewStyle().Foreground(colorDim).Render(ch)
		case snap.InProgressChapter == e.Chapter:
			marker = lipgloss.NewStyle().Foreground(colorAccent).Bold(true).Render("▸")
			chStyle = lipgloss.NewStyle().Foreground(colorAccent).Bold(true).Render(ch)
			titleStyle = lipgloss.NewStyle().Foreground(colorAccent).Bold(true)
		default:
			marker = lipgloss.NewStyle().Foreground(colorDim).Render("○")
			chStyle = lipgloss.NewStyle().Foreground(colorDim).Render(ch)
			titleStyle = lipgloss.NewStyle().Foreground(colorMuted)
		}
		title := truncate(e.Title, contentW-6)
		line := marker + chStyle + " " + titleStyle.Render(title)
		if snap.InProgressChapter == e.Chapter {
			line += lipgloss.NewStyle().Foreground(colorAccent).Italic(true).Render(i18n.T(" đang làm"))
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}

// renderOutlineGrid xếp chương dàn ý thành lưới nhiều cột theo kiểu "ưu tiên cột", tránh màn hình rộng mà cột đơn để trắng nhiều.
// Số cột tự thích ứng theo contentW (1-4), chương trong cột tăng liên tục ("đọc hết một cột rồi sang cột tiếp").
// Đánh đổi với layout cột đơn: bỏ nhãn đuôi " đang làm" —— ở nhiều cột nhãn sẽ phá vỡ căn cột,
// mà dấu ▸ + màu vàng + "Đang viết Chương N" ở cột tổng quan trái đã nói rõ thông tin đang làm.
func renderOutlineGrid(snap host.UISnapshot, contentW int) string {
	n := len(snap.Outline)
	if n == 0 {
		return ""
	}
	chNumW := 2
	titleW := 0
	for _, e := range snap.Outline {
		if w := len(strconv.Itoa(e.Chapter)); w > chNumW {
			chNumW = w
		}
		if w := lipgloss.Width(e.Title); w > titleW {
			titleW = w
		}
	}
	// Giới hạn rộng tiêu đề 14 (khoảng 7 ký tự CJK); tiêu đề dài thỉnh thoảng xuất hiện thì cắt bớt,
	// tránh một hai tiêu đề dài kéo giãn toàn bộ cell
	if titleW > 14 {
		titleW = 14
	} else if titleW < 4 {
		titleW = 4
	}
	cellW := 3 + chNumW + titleW // marker(1) + trắng(1) + số chương + trắng(1) + tiêu đề
	gutter := 4
	cols := (contentW + gutter) / (cellW + gutter)
	if cols < 1 {
		cols = 1
	} else if cols > 4 {
		cols = 4
	}
	rows := (n + cols - 1) / cols

	var b strings.Builder
	cellStyle := lipgloss.NewStyle().Width(cellW)
	gutterStr := strings.Repeat(" ", gutter)
	for r := 0; r < rows; r++ {
		for c := 0; c < cols; c++ {
			idx := c*rows + r
			if idx >= n {
				break
			}
			cell := renderOutlineCell(snap.Outline[idx], snap, chNumW, titleW)
			// Khi cột sau còn cell thì đệm đủ cellW + gutter; ngược lại cell hiện tại là cuối dòng thì không đệm
			if c < cols-1 && (c+1)*rows+r < n {
				b.WriteString(cellStyle.Render(cell))
				b.WriteString(gutterStr)
			} else {
				b.WriteString(cell)
			}
		}
		b.WriteString("\n")
	}
	return b.String()
}

// renderOutlineCell render một cell chương: xong (● xanh) / đang làm (▸ vàng) / chưa bắt đầu (○ mờ).
func renderOutlineCell(e host.OutlineSnapshot, snap host.UISnapshot, chNumW, titleW int) string {
	chStr := fmt.Sprintf("%*d", chNumW, e.Chapter)
	title := truncateWidth(e.Title, titleW)
	var marker, chRendered, titleRendered string
	switch {
	case snap.CompletedCount >= e.Chapter:
		marker = lipgloss.NewStyle().Foreground(colorSuccess).Render("●")
		chRendered = lipgloss.NewStyle().Foreground(colorDim).Render(chStr)
		titleRendered = cardContentStyle.Render(title)
	case snap.InProgressChapter == e.Chapter:
		marker = lipgloss.NewStyle().Foreground(colorAccent).Bold(true).Render("▸")
		chRendered = lipgloss.NewStyle().Foreground(colorAccent).Bold(true).Render(chStr)
		titleRendered = lipgloss.NewStyle().Foreground(colorAccent).Bold(true).Render(title)
	default:
		marker = lipgloss.NewStyle().Foreground(colorDim).Render("○")
		chRendered = lipgloss.NewStyle().Foreground(colorDim).Render(chStr)
		titleRendered = lipgloss.NewStyle().Foreground(colorMuted).Render(title)
	}
	return marker + " " + chRendered + " " + titleRendered
}

// truncateWidth cắt theo "độ rộng thị giác" (ký tự CJK tính 2 cột), cùng nguồn với lipgloss.Width.
// Không thêm dấu ba chấm, dùng chung cho căn cột cell lưới và truncate.
func truncateWidth(s string, maxW int) string {
	if lipgloss.Width(s) <= maxW {
		return s
	}
	var b strings.Builder
	cur := 0
	for _, r := range s {
		rw := lipgloss.Width(string(r))
		if cur+rw > maxW {
			break
		}
		b.WriteRune(r)
		cur += rw
	}
	return b.String()
}

// renderDetailContent dựng nội dung panel chi tiết bên phải.
// Ưu tiên hiện thiết lập nền (dàn ý, nhân vật), sau đó là thông tin runtime (chốt, duyệt...).
func renderDetailContent(snap host.UISnapshot, contentW int) string {
	var b strings.Builder

	// Dàn ý
	if len(snap.Outline) > 0 {
		outlineHeader := i18n.T(":: Dàn ý")
		if snap.Layered {
			outlineHeader = fmt.Sprintf(i18n.T(":: Dàn ý (%s · dàn ý động)"), snap.CurrentVolumeArc)
		}
		b.WriteString(panelTitleStyle.Render(outlineHeader))
		b.WriteString("\n")
		b.WriteString(renderOutlineSection(snap, contentW))
		// Gợi ý quy hoạch cuộn
		compassStyle := lipgloss.NewStyle().Foreground(colorDim).Italic(true)
		if snap.Layered {
			if snap.NextVolumeTitle != "" {
				b.WriteString(compassStyle.Render(i18n.T("  ┄ Tập tiếp: ") + snap.NextVolumeTitle))
				b.WriteString("\n")
			}
			b.WriteString(compassStyle.Render(i18n.T("  ··· Chương tiếp theo sẽ tự sinh khi viết tiếp")))
			b.WriteString("\n")
			if snap.CompassDirection != "" {
				direction := fmt.Sprintf(i18n.T("  → Kết truyện: %s"), snap.CompassDirection)
				if scale := compassScaleRange(snap.CompassScale); scale != "" {
					direction += " (" + i18n.Tf("dự kiến %s tập", scale) + ")"
				}
				b.WriteString(compassStyle.Render(truncate(direction, contentW)))
				b.WriteString("\n")
			}
		}
		b.WriteString("\n")
	}

	// Nhân vật
	if len(snap.Characters) > 0 {
		b.WriteString(panelTitleStyle.Render(i18n.T(":: Nhân vật")))
		b.WriteString("\n")
		for _, c := range snap.Characters {
			writeBulletWrapped(&b, c, contentW, cardContentStyle)
		}
		b.WriteString("\n")
	}

	// Hệ sinh thái vai phụ: tổng số vai phụ đã xuất hiện + top 5 hoạt động gần nhất
	if snap.SupportingCount > 0 {
		b.WriteString(panelTitleStyle.Render(i18n.T(":: Vai phụ")))
		b.WriteString("\n")
		b.WriteString(cardContentStyle.Render(truncate(fmt.Sprintf(i18n.T("Đã xuất hiện: %d"), snap.SupportingCount), contentW)))
		b.WriteString("\n")
		for _, name := range snap.RecentSupporting {
			writeBulletWrapped(&b, name, contentW, cardContentStyle)
		}
		b.WriteString("\n")
	}

	if snap.Synopsis != "" {
		b.WriteString(panelTitleStyle.Render(i18n.T(":: Tóm tắt")))
		b.WriteString("\n")
		for _, line := range wrapStreamText(snap.Synopsis, contentW) {
			b.WriteString(lipgloss.NewStyle().Foreground(colorDim).Render(line))
			b.WriteString("\n")
		}
		b.WriteString("\n\n")
	}

	// Tiền đề
	if snap.Premise != "" {
		b.WriteString(panelTitleStyle.Render(i18n.T(":: Tiền đề")))
		b.WriteString("\n")
		for _, line := range wrapStreamText(snap.Premise, contentW) {
			b.WriteString(lipgloss.NewStyle().Foreground(colorDim).Render(line))
			b.WriteString("\n")
		}
		b.WriteString("\n\n")
	}

	if snap.LastCommitSummary != "" {
		b.WriteString(cardTitleStyle.Render(i18n.T("~ Chốt gần nhất ~")))
		b.WriteString("\n")
		writeWrapped(&b, snap.LastCommitSummary, contentW, cardContentStyle)
		b.WriteString("\n")
	}

	if snap.LastReviewSummary != "" {
		b.WriteString(cardTitleStyle.Render(i18n.T("~ Duyệt gần nhất ~")))
		b.WriteString("\n")
		writeWrapped(&b, snap.LastReviewSummary, contentW, cardContentStyle)
		b.WriteString("\n")
	}

	if len(snap.RecentSummaries) > 0 {
		b.WriteString(cardTitleStyle.Render(i18n.T("~ Tóm tắt ~")))
		b.WriteString("\n")
		for _, s := range snap.RecentSummaries {
			writeWrapped(&b, s, contentW, cardContentStyle)
		}
	}

	return b.String()
}

// writeWrapped ghi một đoạn văn bản xuống dòng theo độ rộng thị giác, mỗi dòng render style riêng.
func writeWrapped(b *strings.Builder, text string, contentW int, style lipgloss.Style) {
	for _, line := range wrapStreamText(text, max(8, contentW)) {
		b.WriteString(style.Render(line))
		b.WriteString("\n")
	}
}

// writeBulletWrapped ghi một mục "· ": xuống dòng theo độ rộng thị giác, dòng tiếp theo thụt treo 2 cột trắng.
func writeBulletWrapped(b *strings.Builder, text string, contentW int, style lipgloss.Style) {
	for i, line := range wrapStreamText(text, max(8, contentW-2)) {
		prefix := "· "
		if i > 0 {
			prefix = "  "
		}
		b.WriteString(style.Render(prefix + line))
		b.WriteString("\n")
	}
}
