package arbiter

import (
	"os"
	"path/filepath"
	"testing"

	storepkg "github.com/CTKiet2006/kietnovel/internal/store"
)

// TestCollectFactsChoTruyenMoiKhongThatBai hồi quy cho lỗi người dùng gặp:
// sau khi /new tạo truyện (book.json mới chỉ có title, chưa có synopsis), gõ
// prompt hướng đi thì Arbiter phải thu thập được dữ kiện, không lỗi.
//
// Trước fix: Book.Load() validate bắt buộc synopsis -> trả lỗi -> Arbiter
// không bao giờ được gọi -> "Thu thập dữ kiện can thiệp thất bại" lặp vô hạn,
// engine kẹt, người dùng không gõ tiếp được nữa.
func TestCollectFactsChoTruyenMoiKhongThatBai(t *testing.T) {
	dir := t.TempDir()
	st := storepkg.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init(10); err != nil {
		t.Fatal(err)
	}
	// Đúng trạng thái /new tạo ra: chỉ có title, chưa có synopsis.
	if err := os.WriteFile(filepath.Join(dir, "meta", "book.json"),
		[]byte(`{"title":"Tuất"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	facts, err := CollectInterventionFacts(st)
	if err != nil {
		t.Fatalf("truyện mới chưa có synopsis không được chặn can thiệp: %v", err)
	}
	if facts.Title != "Tuất" {
		t.Errorf("title = %q, muốn đọc được title đã có", facts.Title)
	}
}

// TestCollectFactsVanLoiThat: book.json hỏng (JSON hỏng) thì vẫn phải báo lỗi —
// im lặng nuốt lỗi sẽ biến dữ liệu hỏng thành truyện trắng.
func TestCollectFactsVanLoiThat(t *testing.T) {
	dir := t.TempDir()
	st := storepkg.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "meta", "book.json"),
		[]byte(`{khong phai json`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := CollectInterventionFacts(st); err == nil {
		t.Fatal("book.json hỏng phải báo lỗi, không được bỏ qua")
	}
}
