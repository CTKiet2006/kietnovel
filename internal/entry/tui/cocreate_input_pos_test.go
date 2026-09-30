package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// TestStripInlineMarkdown: loi user bao o khoi dong sang tac lo nguyen dau sao
// "**đêm 3/11 là thời điểm mở đầu**". Cột chỉ đạo co renderMarkdownPreview nen
// hien duoc, con cot hoi thoai khong dung duoc (noi dung stream tung khung).
func TestStripInlineMarkdown(t *testing.T) {
	cases := []struct{ in, want string }{
		{"**đêm 3/11** là mở đầu", "đêm 3/11 là mở đầu"},
		{"__đậm__ rồi *nghiêng*", "đậm rồi nghiêng"},
		{"dùng `save_book` nhé", "dùng save_book nhé"},
		{"không có markdown gì", "không có markdown gì"},
		{"", ""},
	}
	for _, c := range cases {
		if got := stripInlineMarkdown(c.in); got != c.want {
			t.Errorf("stripInlineMarkdown(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	// Dấu sao lẻ kiểu danh sách đầu dòng không được nuốt mất.
	if got := stripInlineMarkdown("• a\n* b"); got != "• a\n* b" {
		t.Errorf("bỏ nhầm ký hiệu đầu dòng: %q", got)
	}
}

// TestPadToHeight: hàm ép chiều cao phải thêm dòng trống vào cuối khi thiếu, và
// không cắt bớt khi dài hơn (nội dung dài đã tự cuộn trong viewport).
func TestPadToHeight(t *testing.T) {
	if got := lipgloss.Height(padToHeight("a\nb", 5)); got != 5 {
		t.Errorf("thieu dong -> cao %d, mong 5", got)
	}
	if got := lipgloss.Height(padToHeight("a\nb", 2)); got != 2 {
		t.Errorf("vua du -> cao %d, mong 2", got)
	}
	long := "1\n2\n3\n4\n5"
	if got := padToHeight(long, 2); got != long {
		t.Errorf("bi cat bom khi dai hon: %q", got)
	}
	if got := padToHeight("", 3); got != "\n\n" {
		t.Errorf("chuoi rong -> %q", got)
	}
}

// TestOenhDauCungSatDay la loi user bao: o nhaps cua khung dong sang tac lơ lửng
// giua cot chat thay vi nam sat day. Nguyen nhan: viewport hoi thoai tra ve it dong
// hon chieu cao duoc cap khi hoi thoai con ngan, nen cot trai ngan hon ngan sach
// dong, khung modal giu chieu cao -> ho lo, va o nhaps bi day len giua.
//
// Test do vi tri dong cua o nhaps: phai la dong gan day nhat cua cot.
func TestOenhDauCungSatDay(t *testing.T) {
	for _, size := range [][2]int{{120, 40}, {140, 50}, {100, 30}} {
		w, h := size[0], size[1]
		// Phiên rất ngắn: chưa có hội thoại nào, chắc chắn ngắn hơn ngân sách
		// dòng — đúng điều kiện làm lỗi xuất hiện.
		s := newCoCreateState("")

		out := renderCoCreateModal(w, h, s, "", "X", 0, false)
		lines := strings.Split(ansi.Strip(out), "\n")

		inputBottom, modalBottom := -1, -1
		for i, l := range lines {
			if strings.Contains(l, "X") {
				inputBottom = i + 1 // dòng kế tiếp là viền dưới của ô nhập
			}
			// Viền đáy khung: ╰────╯
			if strings.Contains(l, "╰") {
				modalBottom = i
			}
		}
		if inputBottom < 0 || modalBottom < 0 {
			t.Fatalf("%dx%d: khong tim thay o nhap / day khung", w, h)
		}
		// Ô nhập phải neo sát đáy khung: chỉ được chừa 1 dòng padding của modal.
		// Không tính dòng hint nằm NGOÀI khung, phía dưới — nó cố ý nằm ở đó.
		if gap := modalBottom - inputBottom; gap > 2 {
			t.Errorf("%dx%d: o nhap cach day khung %d dong — dang loi\n%s", w, h, gap, out)
		}
	}
}
