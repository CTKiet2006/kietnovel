package tui

import (
	"github.com/CTKiet2006/kietnovel/internal/i18n"
	"github.com/charmbracelet/lipgloss"
)

// Bảng màu chủ đề — tông ấm mùi sách
// AdaptiveColor: Light = giá trị nền sáng, Dark = giá trị nền tối
//
// Nguyên tắc thiết kế: Light giữ yên ổn định (nền sáng đã chỉnh ưng); Dark sáng hơn Light
// ~25% lightness, nhích bão hòa để nền tối đủ tương phản (colorDim trước #6b6355
// trên nền đen #1c1c1c gần như tàng hình, đường kẻ/chữ phụ biến mất).
//
// colorAccent2 nền tối đổi từ #7a9e7e sang xanh ngọc #5fb8a3, tách khỏi xanh "khỏe" của colorSuccess
// — trước hai màu y hệt làm lẫn mốc màu của architect agent với cảm giác vui "trúng cao".
// bodyTextColor là chiến lược chữ thường trung tính:
//   - Terminal tối → NoColor, thừa hưởng chữ mặc định của terminal, tránh nhét trắng sữa #e8e0d0 lên theme
//     nền ấm/lạnh user tự phối gây lệch (user test thực tế thấy màu mặc định nền tối dễ đọc hơn).
//   - Terminal sáng → dùng nấc Light của colorText (nâu đậm #3d3529), giữ tông ấm thương hiệu;
//     đen mặc định nền sáng tương phản quá gắt, nâu đậm chỉnh sẵn nhìn nền sáng dịu hơn.
//
// Hai đầu AdaptiveColor đều phải cho giá trị màu, không có nấc "không màu", nên ở đây xét nền một lần lúc khởi động,
// sau mọi giá trị tổng quan/chữ chương/mô tả lệnh dạng "chữ thường trung tính" đều dùng bodyTextColor.
var bodyTextColor lipgloss.TerminalColor = func() lipgloss.TerminalColor {
	if lipgloss.HasDarkBackground() {
		return lipgloss.NoColor{}
	}
	return lipgloss.Color("#3d3529")
}()

var (
	colorText    = lipgloss.AdaptiveColor{Light: "#3d3529", Dark: "#e8e0d0"}
	colorDim     = lipgloss.AdaptiveColor{Light: "#8a7e6b", Dark: "#8a8175"}
	colorMuted   = lipgloss.AdaptiveColor{Light: "#7a7060", Dark: "#b8b09c"}
	colorAccent  = lipgloss.AdaptiveColor{Light: "#b8860b", Dark: "#e5b449"}
	colorAccent2 = lipgloss.AdaptiveColor{Light: "#3d7a42", Dark: "#5fb8a3"}
	colorRunning = lipgloss.AdaptiveColor{Light: "#6f8641", Dark: "#b5d075"}
	colorSuccess = lipgloss.AdaptiveColor{Light: "#3d7a42", Dark: "#7ec488"}
	colorError   = lipgloss.AdaptiveColor{Light: "#b5433a", Dark: "#e07060"}
	colorReview  = lipgloss.AdaptiveColor{Light: "#b07530", Dark: "#e09b5a"}
	colorContext = lipgloss.AdaptiveColor{Light: "#6b5a9e", Dark: "#a890d8"}
	colorTool    = lipgloss.AdaptiveColor{Light: "#3a7a8a", Dark: "#7ec5d8"}
)

// Ánh xạ màu nhãn trạng thái
var statusColors = map[string]lipgloss.AdaptiveColor{
	"READY":    colorDim,
	"PAUSING":  colorAccent,
	"PAUSED":   colorAccent,
	"RUNNING":  colorRunning,
	"REVIEW":   colorReview,
	"REWRITE":  colorReview,
	"COMPLETE": colorSuccess,
	"ERROR":    colorError,
}

// statusDisplay là hàm chứ không phải biến package: nhãn phải dịch sau khi
// SetLanguage chạy, còn biến package khởi tại lúc import nên luôn kẹt tiếng Việt.
// Khoá trạng thái là hằng số nội bộ, không dịch.
//
// Icon của RUNNING để trống, do spinner frame điền động để cảm giác chuyển
// động hòa vào chỉ báo trạng thái.
func statusDisplay() map[string]struct {
	icon  string
	label string
} {
	return map[string]struct {
		icon  string
		label string
	}{
		"READY":    {"○", i18n.T("Sẵn sàng")},
		"RUNNING":  {"", i18n.T("Đang viết")},
		"REVIEW":   {"◆", i18n.T("Chờ duyệt")},
		"REWRITE":  {"◆", i18n.T("Viết lại")},
		"COMPLETE": {"●", "Xong"},
		"PAUSED":   {"⏸", i18n.T("Tạm dừng")},
		"PAUSING":  {"⏸", i18n.T("Đang dừng")},
		"ERROR":    {"✕", i18n.T("Lỗi")},
	}
}

// Ánh xạ màu nhóm sự kiện
var categoryColors = map[string]lipgloss.AdaptiveColor{
	"DISPATCH": colorAccent,
	"MODEL":    colorContext,
	"DECISION": colorContext,
	"TOOL":     colorTool,
	"SYSTEM":   colorAccent,
	"USER":     colorAccent2,
	"REVIEW":   colorReview,
	"CHECK":    colorSuccess,
	"ERROR":    colorError,
	"AGENT":    colorMuted,
	"CONTEXT":  colorContext,
	"COMPACT":  colorContext,
}

// Kiểu cơ bản
var (
	baseBorder = lipgloss.RoundedBorder()

	topBarStyle = lipgloss.NewStyle().
			Foreground(colorText).
			Padding(0, 1)

	statusIconStyle = lipgloss.NewStyle().
			Bold(true)

	statusLabelStyle = lipgloss.NewStyle().
				Foreground(colorText)

	panelTitleStyle = lipgloss.NewStyle().
			Foreground(colorAccent).
			Bold(true)

	fieldLabelStyle = lipgloss.NewStyle().
			Foreground(colorMuted).
			Width(10)

	// fieldValueStyle / cardContentStyle dùng bodyTextColor —— giá trị vùng tổng quan (trạng thái chạy,
	// số chương xong, số chữ...), mục dàn ý, danh sách nhân vật, tóm tắt chương dạng "nội dung chữ thường trung tính"
	// nền tối theo chữ mặc định terminal (tránh nhét trắng sữa lệch theme), nền sáng theo nâu đậm giữ tông ấm.
	// Thành phần ngữ nghĩa mạnh (tiêu đề, giá trị nổi, trạng thái, lỗi, nhuộm tỉ lệ trúng...) vẫn đi màu chủ đề colorAccent /
	// colorError.
	fieldValueStyle = lipgloss.NewStyle().Foreground(bodyTextColor)

	highlightValueStyle = lipgloss.NewStyle().
				Foreground(colorAccent).
				Bold(true)

	cardTitleStyle = lipgloss.NewStyle().
			Foreground(colorMuted).
			Italic(true)

	cardContentStyle = lipgloss.NewStyle().Foreground(bodyTextColor)
)
