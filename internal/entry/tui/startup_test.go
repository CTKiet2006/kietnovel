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
		t.Fatal("/start should parse as slash command")
	}
	prompt, err := prepareFileStart(cmd.args)
	if err != nil {
		t.Fatal(err)
	}
	if prompt != want {
		t.Fatalf("prompt = %q, want full file content", prompt)
	}
	next, startCmd := m.handleSlashCommand(cmd)
	got := next.(Model)
	if startCmd == nil || !got.starting || got.mode != modeRunning {
		t.Fatalf("start state = mode %v, starting %v, cmd %v", got.mode, got.starting, startCmd)
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
		t.Fatalf("mode = %v, want modeRunning", m.mode)
	}
	if !m.starting {
		t.Fatal("starting should be true while host startup command is running")
	}
	if !m.snapshot.IsRunning {
		t.Fatal("snapshot should render as running during local startup")
	}
	if got := m.textarea.Placeholder; got != "正在初始化创作..." {
		t.Fatalf("placeholder = %q", got)
	}
	if len(m.events) != 2 {
		t.Fatalf("events = %+v, want startup user + system events", m.events)
	}
	if m.events[0].Category != "USER" || !strings.HasPrefix(m.events[0].Summary, "创作需求: ") {
		t.Fatalf("first event = %+v, want USER prompt event", m.events[0])
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
		t.Fatalf("启动失败后 mode = %v, want modeRunning", got.mode)
	}
	if got.starting {
		t.Fatal("启动失败后 starting 应复位")
	}
	if got.snapshot.IsRunning {
		t.Fatal("启动失败后 snapshot 不应仍显示运行中")
	}
	if !strings.Contains(got.textarea.Placeholder, "启动失败") {
		t.Fatalf("placeholder = %q", got.textarea.Placeholder)
	}
	if len(got.events) == 0 || got.events[len(got.events)-1].Category != "ERROR" {
		t.Fatalf("工作台应保留启动错误事件: %+v", got.events)
	}
}

// issue #125 回归：启动期 Host 尚未进入 running（规则归一化/启动裁定都在其之前），
// 真实快照回包不得把工作台显示成"空闲"，否则正在进行的启动看起来像卡死。
func TestStartingSnapshotShowsStartingNotIdle(t *testing.T) {
	m := NewModel(nil, "")
	m.width = 120
	m.height = 40
	m.resizeTextarea()
	m.updateViewportSize()

	m.enterStarting("写一本东方玄幻长篇")

	next, _, handled := m.handleRuntimeMsg(snapshotMsg(host.UISnapshot{RuntimeState: "idle"}))
	if !handled {
		t.Fatal("snapshotMsg 应被 handleRuntimeMsg 处理")
	}
	got := next.(Model)
	if got.snapshot.RuntimeState != "starting" {
		t.Fatalf("启动期运行态 = %q, want starting", got.snapshot.RuntimeState)
	}
	if !got.snapshot.IsRunning {
		t.Fatal("启动期不应显示为已停止")
	}
	if label := snapshotRuntimeStateLabel(got.snapshot.RuntimeState); label != "启动中" {
		t.Fatalf("侧栏标签 = %q, want 启动中", label)
	}
}

// 启动结束（成功或失败）后 starting 复位，快照恢复如实反映 Host。
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
		t.Fatalf("启动结束后应如实显示，got %q", got.snapshot.RuntimeState)
	}
}

func TestApplyStartupPromptEventTruncatesSummaryButKeepsDetail(t *testing.T) {
	m := NewModel(nil, "")
	prompt := strings.Repeat("设", maxPromptEventCols+50)

	m.applyStartupPromptEvent(prompt)

	if len(m.events) != 1 {
		t.Fatalf("events = %+v, want one event", m.events)
	}
	ev := m.events[0]
	if ev.Detail != prompt {
		t.Fatalf("detail should keep full prompt, got len=%d want=%d", len([]rune(ev.Detail)), len([]rune(prompt)))
	}
	maxSummaryRunes := len([]rune("创作需求: ")) + maxPromptEventCols
	if got := len([]rune(ev.Summary)); got > maxSummaryRunes {
		t.Fatalf("summary runes = %d, want <= %d", got, maxSummaryRunes)
	}
	if !strings.HasSuffix(ev.Summary, "...") {
		t.Fatalf("summary should be truncated with ellipsis, got %q", ev.Summary)
	}
}

func TestStreamFlushTimerRunsOnlyForPendingData(t *testing.T) {
	m := NewModel(nil, "")
	next, cmd, handled := m.handleRuntimeMsg(streamDeltaMsg("正文"))
	if !handled || cmd == nil {
		t.Fatal("流式增量应启动一次刷新")
	}
	got := next.(Model)
	if !got.streamDirty || !got.flushPending {
		t.Fatal("流式增量应标记待刷新")
	}
	next, cmd, handled = got.handleRuntimeMsg(streamFlushTickMsg{})
	got = next.(Model)
	if !handled || cmd != nil || got.streamDirty || got.flushPending {
		t.Fatal("刷新完成后 timer 应停止")
	}
}
