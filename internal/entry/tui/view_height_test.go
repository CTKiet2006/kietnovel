package tui

import (
	"fmt"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

// TestKhungCaoDungBangTerminal là hồi quy cho lỗi người dùng báo: gõ chữ thì
// ký tự hiện ở cùng hàng với dòng "◆ model" bên dưới, gõ xong mới "bay" về dòng
// nhập.
//
// Nguyên nhân không nằm ở vị trí ô nhập, mà ở chiều cao: View() vẽ khối thấp hơn
// chiều cao terminal, nên con trỏ thật của terminal nằm dưới khung. Ký tự gõ
// vào được terminal in ở dòng vật lý đó (ngang dòng trạng thái), rồi lần repaint
// sau mới vẽ đúng chỗ — đúng triệu chứng "bay trở lại".
//
// Nếu View() trả về đúng m.height thì con trỏ thật luôn nằm trong khung.
func TestKhungCaoDungBangTerminal(t *testing.T) {
	for _, size := range [][2]int{{120, 40}, {160, 50}, {200, 60}, {100, 30}} {
		w, h := size[0], size[1]
		m := newRenderableModel()
		m.width, m.height = w, h
		m.updateViewportSize()

		view := m.View()
		got := lipgloss.Height(view)
		if got != h {
			t.Errorf("terminal %dx%d: khung cao %d dong, phai bang %d ( lech %d dong)",
				w, h, got, h, h-got)
			if h-got > 6 {
				fmt.Printf("--- %dx%d, thieu %d dong:\n%s\n---\n", w, h, h-got, view)
			}
		}
	}
}
