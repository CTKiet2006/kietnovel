package tui

import (
	"os"
	"path/filepath"
	"strings"
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

// TestStartChan khi truyen da co noi dung: /start ghi đè nên phải chặn, và thông
// báo phải chỉ ra lệnh thay thế (/new) chứ không trả lời chung chung.
func TestStartChanKhiTruyenDaCoNoiDung(t *testing.T) {
	m := NewModel(nil, "")
	m.mode = modeRunning
	m.snapshot = host.UISnapshot{Phase: "writing", CompletedCount: 12}

	cmd, _ := parseSlashCommand("/start " + startTestPromptFile(t))
	next, startCmd := m.handleSlashCommand(cmd)
	got := next.(Model)
	if startCmd != nil {
		t.Fatal("truyện đã có chương thì /start không được chạy")
	}
	if got.starting {
		t.Error("starting phải false khi bị chặn")
	}
	if len(got.events) == 0 {
		t.Fatal("phải báo lý do chặn")
	}
	ev := got.events[len(got.events)-1]
	if ev.Category != "ERROR" {
		t.Errorf("category = %q, muốn ERROR", ev.Category)
	}
	if !strings.Contains(ev.Summary, "/new") {
		t.Errorf("thông báo chặn phải chỉ /new, được %q", ev.Summary)
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
