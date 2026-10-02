package host

import (
	"os"
	"path/filepath"
	"testing"

	storepkg "github.com/CTKiet2006/kietnovel/internal/store"
)

// TestMigrateLegacyBookChoTruyenMoi: truyện vừa tạo bằng /new có meta/book.json
// chỉ chứa title (tên hiển thị), chưa có synopsis — synopsis do Architect viết
// sau. Nâng cấp dữ liệu dự án phải bỏ qua trường hợp này.
//
// Trước fix: Book.Load() trả lỗi "book synopsis is required", upgradeProject
// fail, host.New fail, nên /new tạo được thư mục nhưng không bao giờ mở được
// truyện đó — người dùng thấy TUI vẫn bám truyện cũ.
func TestMigrateLegacyBookChoTruyenMoi(t *testing.T) {
	dir := t.TempDir()
	st := storepkg.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	// Đúng trạng thái /new tạo ra: chỉ có title, không synopsis.
	if err := os.WriteFile(filepath.Join(dir, "meta", "book.json"),
		[]byte(`{"title":"Truyen Moi"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := migrateLegacyBook(st); err != nil {
		t.Fatalf("truyện mới chưa có synopsis không được chặn nâng cấp: %v", err)
	}
}

// TestMigrateLegacyBookVanLoiThat: nếu bản thân book.json hỏng (JSON hỏng, không
// phải thiếu field) thì vẫn phải báo lỗi — im lặng nuốt lỗi sẽ biến dữ liệu hỏng
// thành truyện trắng.
func TestMigrateLegacyBookVanLoiThat(t *testing.T) {
	dir := t.TempDir()
	st := storepkg.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "meta", "book.json"),
		[]byte(`{khong phai json`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := migrateLegacyBook(st); err == nil {
		t.Fatal("book.json hỏng phải báo lỗi, không được bỏ qua")
	}
}
