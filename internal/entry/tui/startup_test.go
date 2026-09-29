package tui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/voocel/ainovel-cli/internal/host"
)

func TestStartCommandLoadsPromptFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "outline files")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "story outline.md")
	want := "世界设定\n\n第一卷大纲"
	if err := os.WriteFile(path, []byte("  "+want+"  "), 0o644); err != nil {
		t.Fatal(err)
	}

	m := NewModel(nil, "")
	cmd, ok := parseSlashCommand("/start " + path)
	if !ok {
		t.Fatal("/start phải được phân tích thành slash command")
	}
	prompt, err := prepareFileStart(cmd.args)
	if err != nil {
		t.Fatal(err)
	}
	if prompt != want {
		t.Fatalf("prompt = %q, muốn toàn bộ nội dung file", prompt)
	}
	next, startCmd := m.handleSlashCommand(cmd)
	got := next.(Model)
	if startCmd == nil || !got.starting || got.mode != modeRunning {
		t.Fatalf("trạng thái start = mode %v, starting %v, cmd %v", got.mode, got.starting, startCmd)
	}
}

func TestEnterStartingSwitchesToWorkbenchImmediately(t *testing.T) {
	m := NewModel(nil, "")
	m.width = 120
	m.height = 40
	m.resizeTextarea()
	m.updateViewportSize()

	m.enterStarting("写一本东方玄幻长篇")

	if m.mode != modeRunning {
		t.Fatalf("mode = %v, muốn modeRunning", m.mode)
	}
	if !m.starting {
		t.Fatal("starting phải true trong lúc lệnh khởi động của host đang chạy")
	}
	if !m.snapshot.IsRunning {
		t.Fatal("snapshot phải vẽ dạng đang chạy trong lúc khởi động cục bộ")
	}
	if got := m.textarea.Placeholder; got != "Đang khởi tạo truyện..." {
		t.Fatalf("placeholder = %q", got)
	}
	if len(m.events) != 2 {
		t.Fatalf("events = %+v, muốn sự kiện user khởi động + sự kiện hệ thống", m.events)
	}
	if m.events[0].Category != "USER" || !strings.HasPrefix(m.events[0].Summary, "Yêu cầu truyện: ") {
		t.Fatalf("sự kiện đầu = %+v, muốn sự kiện prompt USER", m.events[0])
	}
}

func TestStartupFailureStaysInWorkbench(t *testing.T) {
	m := NewModel(nil, "")
	m.width = 120
	m.height = 40
	m.resizeTextarea()
	m.updateViewportSize()

	m.enterStarting("写一本东方玄幻长篇")

	next, _ := m.handleStartResultMsg(startResultMsg{err: errors.New("模型账户未激活")})
	got := next.(Model)
	if got.mode != modeRunning {
		t.Fatalf("Khởi động thất bại mode = %v, muốn modeRunning", got.mode)
	}
	if got.starting {
		t.Fatal("Khởi động thất bại starting phải reset")
	}
	if got.snapshot.IsRunning {
		t.Fatal("Khởi động thất bại snapshot không được vẫn hiện đang chạy")
	}
	if !strings.Contains(got.textarea.Placeholder, "Khởi động thất bại") {
		t.Fatalf("placeholder = %q", got.textarea.Placeholder)
	}
	if len(got.events) == 0 || got.events[len(got.events)-1].Category != "ERROR" {
		t.Fatalf("Workbench phải giữ sự kiện lỗi khởi động: %+v", got.events)
	}
}

// issue #125 hồi quy: trong lúc khởi động Host chưa vào running (chuẩn hóa rule/quyết định khởi động đều ở trước đó),
// snapshot thật trả về không được vẽ workbench thành "nhàn rỗi", nếu không lần khởi động đang chạy trông như treo.
func TestStartingSnapshotShowsStartingNotIdle(t *testing.T) {
	m := NewModel(nil, "")
	m.width = 120
	m.height = 40
	m.resizeTextarea()
	m.updateViewportSize()

	m.enterStarting("写一本东方玄幻长篇")

	next, _, handled := m.handleRuntimeMsg(snapshotMsg(host.UISnapshot{RuntimeState: "idle"}))
	if !handled {
		t.Fatal("snapshotMsg phải được handleRuntimeMsg xử lý")
	}
	got := next.(Model)
	if got.snapshot.RuntimeState != "starting" {
		t.Fatalf("Trạng thái chạy lúc khởi động = %q, muốn starting", got.snapshot.RuntimeState)
	}
	if !got.snapshot.IsRunning {
		t.Fatal("Lúc khởi động không được hiện đã dừng")
	}
	if label := snapshotRuntimeStateLabel(got.snapshot.RuntimeState); label != "Đang khởi động" {
		t.Fatalf("Nhãn sidebar = %q, muốn Đang khởi động", label)
	}
}

// Khởi động xong (thành công hay thất bại) starting reset, snapshot phản ánh Host trung thực.
func TestSnapshotResumesTruthAfterStartingCleared(t *testing.T) {
	m := NewModel(nil, "")
	m.width = 120
	m.height = 40
	m.resizeTextarea()
	m.updateViewportSize()

	m.enterStarting("写一本东方玄幻长篇")
	failed, _ := m.handleStartResultMsg(startResultMsg{err: errors.New("模型账户未激活")})
	m = failed.(Model)

	next, _, _ := m.handleRuntimeMsg(snapshotMsg(host.UISnapshot{RuntimeState: "idle"}))
	got := next.(Model)
	if got.snapshot.RuntimeState != "idle" {
		t.Fatalf("Khởi động xong phải hiện trung thực, được %q", got.snapshot.RuntimeState)
	}
}

func TestApplyStartupPromptEventTruncatesSummaryButKeepsDetail(t *testing.T) {
	m := NewModel(nil, "")
	prompt := strings.Repeat("设", maxPromptEventCols+50)

	m.applyStartupPromptEvent(prompt)

	if len(m.events) != 1 {
		t.Fatalf("events = %+v, muốn một sự kiện", m.events)
	}
	ev := m.events[0]
	if ev.Detail != prompt {
		t.Fatalf("detail phải giữ full prompt, được len=%d muốn=%d", len([]rune(ev.Detail)), len([]rune(prompt)))
	}
	maxSummaryRunes := len([]rune("Yêu cầu truyện: ")) + maxPromptEventCols
	if got := len([]rune(ev.Summary)); got > maxSummaryRunes {
		t.Fatalf("summary runes = %d, muốn <= %d", got, maxSummaryRunes)
	}
	if !strings.HasSuffix(ev.Summary, "...") {
		t.Fatalf("summary phải cắt kèm dấu ba chấm, được %q", ev.Summary)
	}
}

func TestStreamFlushTimerRunsOnlyForPendingData(t *testing.T) {
	m := NewModel(nil, "")
	next, cmd, handled := m.handleRuntimeMsg(streamDeltaMsg("正文"))
	if !handled || cmd == nil {
		t.Fatal("Delta streaming phải khởi động một lần refresh")
	}
	got := next.(Model)
	if !got.streamDirty || !got.flushPending {
		t.Fatal("Delta streaming phải đánh dấu chờ refresh")
	}
	next, cmd, handled = got.handleRuntimeMsg(streamFlushTickMsg{})
	got = next.(Model)
	if !handled || cmd != nil || got.streamDirty || got.flushPending {
		t.Fatal("Refresh xong timer phải dừng")
	}
}
