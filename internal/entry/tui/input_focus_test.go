package tui

import (
	"errors"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Hồi quy cho lỗi "bàn phím chết 100%": textarea bị Blur mà không Focus lại,
// bubbles textarea nuốt mọi phím khi blurred — gõ "/" cũng không vào, palette
// không bao giờ mở. Mỗi test dưới đây khóa một đường Blur phải refocus.

// TestAskSwitchRuntimeNilKhongPanic: runtime nil thì báo lỗi thấy được + refocus,
// không panic nil pointer ở m.runtime.Dir().
func TestAskSwitchRuntimeNilKhongPanic(t *testing.T) {
	m := newTestModel(120, 30)
	m.runtime = nil
	m.textarea.Blur()
	if m.textarea.Focused() {
		t.Fatal("setup: textarea phải blurred trước khi test")
	}
	out, _ := m.askSwitch("/khong-ton-tai", "Sách ma")
	if !out.textarea.Focused() {
		t.Error("askSwitch runtime nil: phải Focus lại ô nhập")
	}
	if out.booksErr == "" {
		t.Error("askSwitch runtime nil: phải báo lỗi thấy được trong modal")
	}
}

// TestBooksEscFocusLai: đóng khung /books bằng Esc luôn refocus.
func TestBooksEscFocusLai(t *testing.T) {
	m := newTestModel(120, 30)
	m.books = newBooksState(m.width, m.height, booksList)
	m.textarea.Blur()
	out, _ := m.handleBooksKey(tea.KeyMsg{Type: tea.KeyEsc})
	if !out.(Model).textarea.Focused() {
		t.Error("đóng /books bằng Esc: phải Focus lại ô nhập")
	}
}

// TestBookLangEscFocusLai: hủy khung hỏi ngôn ngữ refocus.
func TestBookLangEscFocusLai(t *testing.T) {
	m := newTestModel(120, 30)
	m.bookLang = newBookLanguageState("", "/tmp/sach")
	m.textarea.Blur()
	out, _, _ := m.handleBookLanguageKey(tea.KeyMsg{Type: tea.KeyEsc})
	if pm, ok := out.(*Model); !ok || !pm.textarea.Focused() {
		t.Error("hủy bookLang bằng Esc: phải Focus lại ô nhập")
	}
}

// TestWelcomeTiepTucChuyenTiep: chọn "Viết tiếp" phải trả cmd bootstrap tiếp
// (không đứng im), và modal phải đóng để không nuốt phím.
func TestWelcomeTiepTucChuyenTiep(t *testing.T) {
	m := newTestModel(120, 30)
	m.welcome = newWelcomeState(m.snapshot)
	out, cmd, handled := m.handleWelcomeKey(tea.KeyMsg{Type: tea.KeyEnter})
	if !handled {
		t.Fatal("welcome phải chặn Enter")
	}
	if out.(Model).welcome != nil {
		t.Error("chọn xong modal welcome phải đóng")
	}
	if cmd == nil {
		t.Error("chọn Viết tiếp phải trả cmd bootstrapRuntime")
	}
}

// TestPadToHeightKepCaHaiChieu: padToHeight giữ semantics chỉ-đệm (các khối
// con như cocreate dựa vào đó để không mất nội dung cuộn); việc kẹp tràn do
// khung cuối ở View() lo (clipToHeight + padToHeight).
func TestPadToHeightKepCaHaiChieu(t *testing.T) {
	short := "a\nb"
	if got := padToHeight(short, 5); lipgloss.Height(got) != 5 {
		t.Errorf("thiếu dòng phải đệm lên 5, được %d", lipgloss.Height(got))
	}
	if got := padToHeight("x", 0); got != "" {
		t.Errorf("n<=0 phải trả rỗng, được %q", got)
	}
}

// TestWelcomeCaoDungBangTerminal: màn chào có thêm dòng updateHint/errMsg vẫn
// phải vừa đúng terminal (dòng notice động là nghi phạm số 1 gây tràn khung).
func TestWelcomeCaoDungBangTerminal(t *testing.T) {
	for _, size := range [][2]int{{120, 30}, {100, 24}} {
		w, h := size[0], size[1]
		m := newRenderableModel()
		m.width, m.height = w, h
		m.mode = modeNew
		m.updateHint = "Phiên bản mới v1.5.5 đã phát hành · What's Changed · Chạy kietnovel update để nâng cấp"
		m.err = errors.New("lỗi thử")
		m.updateViewportSize()
		view := m.View()
		if got := lipgloss.Height(view); got != h {
			t.Errorf("welcome %dx%d: khung cao %d, phải bằng %d", w, h, got, h)
		}
	}
}
