package tui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/CTKiet2006/kietnovel/assets"
	"github.com/CTKiet2006/kietnovel/internal/host"
)

// TestTruyenMoiKhongBiCoiLaTruyenCu hồi quy cho lỗi người dùng gặp: sau khi
// /new tạo truyện rồi dán đoạn mở đầu vào, app chỉ ghi "can thiệp chờ xử lý"
// mà không viết gì.
//
// Nguyên nhân: truyện /new có meta/book.json (writeBookTitle ghi tên hiển thị)
// nhưng CHƯA có progress.json. bootstrapMsg xét existing = Phase khác rỗng HOẶC
// BookTitle khác rỗng → BookTitle = "tuất" làm existing = true → TUI vào thẳng
// bàn viết (modeRunning) thay vì trang bắt đầu. Gõ chữ khi đó đi vào
// Continue/Arbiter, mà truyện chưa có tiều đề nên Arbiter không có việc gì để
// làm, chỉ ghi can thiệp rồi bỏ đó.
func TestTruyenMoiKhongBiCoiLaTruyenCu(t *testing.T) {
	base := t.TempDir()

	// Mô phỏng đúng trạng thái /new tạo ra: book.json có tên hiển thị, chưa có
	// progress.json. Dựng Host thẳng trên thư mục này để Snapshot đọc đúng.
	dir := filepath.Join(base, "output", "truyen-moi")
	if err := os.MkdirAll(filepath.Join(dir, "meta"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "meta", "book.json"),
		[]byte(`{"title":"Tuất"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := testCfg(base)
	cfg.OutputDir = dir
	rt, err := host.New(cfg, assets.LoadWithLanguage("vi", "default", assets.DefaultLoadOptions(dir)))
	if err != nil {
		t.Fatalf("host.New: %v", err)
	}
	defer rt.Close()

	// Trước hết: host phải đọc được book.json kiểu /new (không có synopsis).
	snap := rt.Snapshot()
	if snap.BookTitle != "Tuất" {
		t.Fatalf("BookTitle = %q, muốn đọc được tên đã lưu", snap.BookTitle)
	}
	if snap.Phase != "" {
		t.Fatalf("truyện mới chưa có progress.json nên Phase phải rỗng, được %q", snap.Phase)
	}

	if bookHasContent(snap) {
		t.Error("truyện mới (chỉ có book.json) không được coi là truyện cũ — " +
			"nếu không, TUI vào bàn viết và gõ chữ sẽ đi nhầm vào Arbiter")
	}
}

// TestTruyenDaKhoiDongCoNoiDung: truyện đã có progress thì vẫn phải coi là
// truyện cũ — không thì app mở lên cũng rơi vào trang bắt đầu mới.
func TestTruyenDaKhoiDongCoNoiDung(t *testing.T) {
	if !bookHasContent(host.UISnapshot{Phase: "premise", BookTitle: "Có sẵn"}) {
		t.Error("truyện đã qua giai đoạn premise phải được coi là có nội dung")
	}
	if !bookHasContent(host.UISnapshot{Phase: "writing"}) {
		t.Error("truyện đang ở giai đoạn writing phải được coi là có nội dung")
	}
}

// TestTruyenChiCoTenKhongPhaiDaChay: truyện chỉ có tên hiển thị nhưng chưa có
// progress thì vẫn là truyện MỚI, dù title có ghi.
func TestTruyenChiCoTenKhongPhaiDaChay(t *testing.T) {
	if bookHasContent(host.UISnapshot{BookTitle: "Chỉ mới có tên"}) {
		t.Error("chỉ có title chưa đủ để coi là truyện cũ")
	}
}
