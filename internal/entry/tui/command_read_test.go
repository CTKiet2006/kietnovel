package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// newTestModel dựng Model đủ để thao tác khung đọc, không cần host thật.
func newTestModel(w, h int) Model {
	m := NewModel(nil, "test")
	m.width, m.height = w, h
	return m
}

// TestLenhReadDaDangKy: /read phải xuất hiện trong bảng lệnh, không thể là lệnh
// gõ tay được mà không ai thấy.
func TestLenhReadDaDangKy(t *testing.T) {
	var found bool
	for _, spec := range commandSpecs() {
		if spec.Name == "read" {
			found = true
			if !spec.AutoExecute {
				t.Error("/read phải AutoExecute, gõ là chạy chứ không chỉ điền vào ô nhập")
			}
			break
		}
	}
	if !found {
		t.Fatal("không tìm thấy lệnh /read trong bảng lệnh")
	}
}

// TestKhungDocDongKhiEsc: Esc phải đóng khung và trả focus về ô nhập, nếu không
// người đọc bị kẹt trong khung không gõ được lệnh.
func TestKhungDocDongKhiEsc(t *testing.T) {
	m := newTestModel(120, 40)
	m.reader = newReaderState(nil, 120, 40, "")

	next, _ := m.handleReaderKey(tea.KeyMsg{Type: tea.KeyEsc})
	got := next.(Model)
	if got.reader != nil {
		t.Fatal("Esc chưa đóng khung đọc")
	}
}

// TestKhungDocChePhimKhiMo: khi khung đọc đang mở, phím phải đi vào khung chứ không
// rơi xuống ô nhập — nếu không, gõ "j" để cuộn sẽ chèn chữ vào ô nhập.
func TestKhungDocChePhimKhiMo(t *testing.T) {
	m := newTestModel(120, 40)
	m.reader = newReaderState(nil, 120, 40, "")

	_, _, handled := m.handleOverlayKeyMsg(tea.KeyMsg{Type: tea.KeyDown, Runes: []rune("j")})
	if !handled {
		t.Fatal("phím chưa bị khung đọc chiếm")
	}
}

// TestKhungDocRongHienHuDan: chưa có chương nào thì khung phải nói rõ, không vẽ
// trang trắng khiến người đọc tưởng treo.
func TestKhungDocRongHienHuDan(t *testing.T) {
	s := newReaderState(nil, 120, 40, "")
	out := ansi.Strip(renderReadModal(120, 40, s))
	if !strings.Contains(out, "Chưa có chương nào") {
		t.Fatalf("khung rong khong huong dan: %q", out)
	}
}

// TestKhungDocMoThangKhongCoThuSo: /read không tham số phải mở danh sách chọn, còn
// có số thì mở thẳng. Ở đây không có sách nên phải giữ ở danh sách và báo lý do,
// không mở nhầm chương 0.
func TestKhungDocMoThangKhongCoThuSo(t *testing.T) {
	s := newReaderState(nil, 120, 40, "")
	if s.mode != readerList {
		t.Fatalf("mode = %v, mong readerList", s.mode)
	}
	if len(s.chapters) != 0 {
		t.Fatalf("khong co sach nhung ra %d chuong", len(s.chapters))
	}
}

// TestLenhDocKhongDungEngine: /read cố tình không đặt NeedsIdle, nên vẫn mở được
// khi engine đang chạy. Test khoá quyết định thiết kế này.
func TestLenhDocKhongDungEngine(t *testing.T) {
	for _, spec := range commandSpecs() {
		if spec.Name == "read" {
			if spec.NeedsIdle {
				t.Error("/read không nên yêu cầu rảnh — đọc không đụng tới Engine")
			}
			return
		}
	}
	t.Fatal("không tìm thấy /read")
}
