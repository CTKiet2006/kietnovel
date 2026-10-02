package tui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/CTKiet2006/kietnovel/internal/host"
)

func startTestPromptFile(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "outline")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "outline.md")
	if err := os.WriteFile(path, []byte("世界设定\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestStartKhongChay tren man chao: hồi quy cho lỗi người dùng báo. /start bị
// chặn với thông báo "chỉ dùng ở màn hình chào" ngay cả khi truyện đang mở còn
// trống — vì sau khi chọn "Viết tiếp" ở màn chào thì mode đã đổi sang
// modeRunning và /start bị chặn vĩnh viễn.
func TestStartKhongChanTrenManChao(t *testing.T) {
	m := NewModel(nil, "")
	// Sau khi qua màn chào: mode là modeRunning, nhưng truyện chưa có gì.
	m.mode = modeRunning
	m.snapshot = host.UISnapshot{Phase: "premise"}

	cmd, ok := parseSlashCommand("/start " + startTestPromptFile(t))
	if !ok {
		t.Fatal("/start phải phân tích được")
	}
	next, startCmd := m.handleSlashCommand(cmd)
	got := next.(Model)
	if startCmd == nil || !got.starting {
		t.Fatalf("truyện còn trống thì /start phải chạy, starting=%v cmd=%v", got.starting, startCmd)
	}
	if got.mode != modeRunning {
		t.Errorf("mode = %v, muốn modeRunning", got.mode)
	}
}

// TestStartTrenTruyenDaCoNoiDungMoKhungTaoMoi: /start trên truyện đã có nội dung
// không báo lỗi nữa, mà mở luôn khung tạo truyện mới và nhớ prompt — để người
// dùng gõ MỘT lệnh /start thay vì phải tự nối /new rồi /start.
func TestStartTrenTruyenDaCoNoiDungMoKhungTaoMoi(t *testing.T) {
	m := NewModel(nil, "")
	m.mode = modeRunning
	m.snapshot = host.UISnapshot{Phase: "writing", CompletedCount: 12}
	path := startTestPromptFile(t)

	cmd, _ := parseSlashCommand("/start " + path)
	next, _ := m.handleSlashCommand(cmd)
	got := next.(Model)

	if got.starting {
		t.Error("chưa tạo truyện mới thì chưa được chạy engine")
	}
	if got.books == nil {
		t.Fatal("phải mở khung tạo truyện mới, không báo lỗi rồi đứng ngoài")
	}
	if got.books.mode != booksNewDraft {
		t.Errorf("mode khung = %v, muốn booksNewDraft", got.books.mode)
	}
	if got.books.startAfterCreate == "" {
		t.Fatal("phải nhớ prompt để chạy nốt sau khi tạo truyện")
	}
}

// TestPendingStartChaySauBootstrap: prompt /start phải thực sự được chạy sau khi
// Host bootstrap xong — không chỉ mang đi mà bị rơi. Đây là mắt xích cuối của
// luồng 1 lệnh: tạo truyện mới → chuyển → bootstrapMsg → engine chạy với prompt.
func TestPendingStartChaySauBootstrap(t *testing.T) {
	m := NewModel(nil, "")
	m.mode = modeNew
	m.pendingStart = "Cốt truyện cần chạy"

	next, startCmd, handled := m.handleRuntimeMsg(bootstrapMsg{existing: true})
	if !handled {
		t.Fatal("bootstrapMsg phải được xử lý")
	}
	got := next.(Model)
	if !got.starting {
		t.Error("phải chạy engine sau khi bootstrap")
	}
	if got.pendingStart != "" {
		t.Error("pendingStart phải được xoá sau khi dùng, không lặp lại mỗi snapshot")
	}
	if startCmd == nil {
		t.Error("phải có cmd khởi động engine")
	}
}

// TestPendingStartKhongChayKhiDaDungHet: bootstrap lần sau không có prompt chờ
// thì không được chạy lại — nếu không engine khởi động lại mỗi lần bật app.
func TestPendingStartKhongChayKhiDaDungHet(t *testing.T) {
	m := NewModel(nil, "")
	m.mode = modeRunning

	next, _, _ := m.handleRuntimeMsg(bootstrapMsg{existing: true})
	got := next.(Model)
	if got.starting {
		t.Error("không có prompt chờ thì không được khởi động engine")
	}
}

// TestStartChoPhepOmanChao: hành vi cũ (màn chào, truyện trống) vẫn phải chạy.
func TestStartChoPhepOmanChao(t *testing.T) {
	m := NewModel(nil, "")
	m.mode = modeNew
	m.snapshot = host.UISnapshot{}

	cmd, _ := parseSlashCommand("/start " + startTestPromptFile(t))
	next, startCmd := m.handleSlashCommand(cmd)
	got := next.(Model)
	if startCmd == nil || !got.starting {
		t.Fatalf("màn chào + truyện trống: /start phải chạy, starting=%v cmd=%v", got.starting, startCmd)
	}
}
