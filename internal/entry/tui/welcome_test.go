package tui

import (
	"testing"

	"github.com/CTKiet2006/kietnovel/internal/host"
	tea "github.com/charmbracelet/bubbletea"
)

// TestWelcomeChonVietTiepMoiChayEngine — hồi quy cho "mở lên là tự chạy".
//
// Trước đây Init gọi bootstrapRuntime ngay, nên mở terminal là engine chạy tiếp
// kể cả khi người dùng chỉ muốn xem. Giờ chọn "Viết tiếp" mới trả về cmd chạy;
// các lựa chọn khác không được kèm cmd chạy engine.
func TestWelcomeChonVietTiepMoiChayEngine(t *testing.T) {
	m := newTestModel(120, 30)
	m.welcome = newWelcomeState(host.UISnapshot{BookTitle: "Truyen X", Phase: "viet"})

	key := func(k tea.KeyType) tea.KeyMsg { return tea.KeyMsg{Type: k} }

	// Xuống tới "Thoát" rồi Enter: phải là tea.Quit, không phải bootstrap.
	m.welcome.cursor = 2
	out, cmd, handled := m.handleWelcomeKey(key(tea.KeyEnter))
	if !handled {
		t.Fatal("welcome phai chan phim")
	}
	_ = out
	if cmd == nil {
		t.Fatal("Thoat phai tra cmd (tea.Quit)")
	}

	// Esc = Viết tiếp: trả cmd chạy (bootstrapRuntime), giữ hành vi cũ.
	m2 := newTestModel(120, 30)
	m2.welcome = newWelcomeState(host.UISnapshot{BookTitle: "Truyen X"})
	_, cmd2, _ := m2.handleWelcomeKey(key(tea.KeyEsc))
	if cmd2 == nil {
		t.Fatal("Esc phai chay tiep (hanh vi cu), khong duoc tra nil")
	}
}

// TestWelcomeChonTruyenKhacMoBooks — chọn "Truyện khác" phải mở khung /books,
// không chạy engine, không thoát.
func TestWelcomeChonTruyenKhacMoBooks(t *testing.T) {
	m := newTestModel(120, 30)
	m.welcome = newWelcomeState(host.UISnapshot{BookTitle: "Truyen X"})
	m.welcome.cursor = 1

	out, _, handled := m.handleWelcomeKey(tea.KeyMsg{Type: tea.KeyEnter})
	if !handled {
		t.Fatal("welcome phai chan phim")
	}
	got := out.(Model)
	if got.books == nil {
		t.Error("chon truyen khac phai mo khung /books")
	}
	if got.welcome != nil {
		t.Error("da chon thi man chao phai tat")
	}
	if !got.welcomeSeen {
		t.Error("da chon thi welcomeSeen phai true, khong hien lai")
	}
}

// TestWelcomeVeKhung — modal phải vẽ được, không rỗng, có tên truyện.
func TestWelcomeVeKhung(t *testing.T) {
	s := newWelcomeState(host.UISnapshot{BookTitle: "Dem Ba Thang Muoi Mot", Phase: "viet"})
	out := s.view(120, 30)
	if out == "" {
		t.Fatal("welcome view rong")
	}
	for _, must := range []string{"Dem Ba Thang Muoi Mot", "Viết tiếp", "Chọn truyện khác", "Thoát"} {
		if !contains(out, must) {
			t.Errorf("welcome thieu %q", must)
		}
	}
}
