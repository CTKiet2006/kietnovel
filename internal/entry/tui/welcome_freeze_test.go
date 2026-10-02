package tui

import (
	"path/filepath"
	"testing"

	"github.com/CTKiet2006/kietnovel/internal/host"
	tea "github.com/charmbracelet/bubbletea"
)

// TestChonTruyenKhacKhongKet hồi quy cho lỗi người dùng báo "chọn truyện khác sau
// đó kẹt luôn". Mô phỏng đúng chuỗi phím thật: màn chào → chọn "Chọn truyện khác"
// → bấm Enter vào truyện khác. Chạy trực tiếp (không goroutine): nếu luồng treo thì
// go test timeout sẽ báo, không cần nút cứu phức tạp.
func TestChonTruyenKhacKhongKet(t *testing.T) {
	base := t.TempDir()
	rt := newTestHost(t, base)
	defer rt.Close()

	m := NewModel(rt, "test")
	m.cfg = testCfg(base)
	m.width, m.height = 120, 40

	// Màn chào đang mở, truyện hiện tại có tiến độ.
	m.mode = modeNew
	m.welcome = newWelcomeState(host.UISnapshot{Phase: "writing", CompletedCount: 3})

	// Phím "Chọn truyện khác" = lựa chọn thứ 2, bấm Enter.
	m.welcome.cursor = 1
	out, _, _ := m.welcomeChoose(welcomeOtherBook)
	m = out.(Model)

	if m.books == nil {
		t.Fatal("chọn truyện khác phải mở khung /books")
	}
	if len(m.books.list) == 0 {
		t.Fatal("danh sách truyện rỗng, không chọn được gì")
	}
	// Khung phải vẽ được, không panic vì kích thước 0.
	_ = m.View()

	// Bấm Enter vào truyện đang mở: phải có thông báo, không treo, không im lặng.
	out2, _ := m.handleBooksKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = out2.(Model)
	if m.booksErr == "" {
		t.Errorf("bấm Enter vào truyện đang mở phải có thông báo, err=%q", m.booksErr)
	}

	// Tạo truyện thứ 2 rồi bấm Enter vào nó: đường chuyển truyện thật.
	newDir, err := rt.NewBookDir("truyen-hai")
	if err != nil {
		t.Fatalf("NewBookDir: %v", err)
	}
	if !sameDir(newDir, filepath.Join(base, "output", "truyen-hai")) {
		t.Fatalf("NewBookDir sai đường dẫn: %s", newDir)
	}
	bks, err := rt.Books()
	if err != nil {
		t.Fatalf("Books: %v", err)
	}
	m.books.list = bks
	for i := range m.books.list {
		if sameDir(m.books.list[i].Dir, newDir) {
			m.books.cursor = i
		}
	}

	out3, _ := m.handleBooksKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = out3.(Model)
	if m.booksErr != "" {
		t.Errorf("chuyển truyện phải thành công, err=%q", m.booksErr)
	}
	if !sameDir(m.runtime.Dir(), newDir) {
		t.Errorf("chưa chuyển sang truyện đã chọn:\n  đang mở = %s\n  chọn    = %s", m.runtime.Dir(), newDir)
	}
	m.runtime.Close()
}
