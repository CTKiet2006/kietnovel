// Package notify 提供无人值守告警通道。
//
// 合宪定位（architecture.md §2.3）：纯观察层动作——告警永不介入控制流
// （不重试、不改派、不停机），只是把 TUI 内已有的事件"喊"到屏幕之外。
// Send 异步执行、永不阻塞 Host、失败只记 slog。
package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// Notification 一条告警的全部事实。
type Notification struct {
	Kind  string `json:"kind"`  // Kinds 返回的稳定事件名
	Level string `json:"level"` // info / warn / error
	Title string `json:"title"`
	Body  string `json:"body"`
}

const (
	KindRunEnd        = "run_end"
	KindBudget        = "budget"
	KindAdvanceGate   = "advance_gate"
	KindStopGuard     = "stop_guard"
	KindPlanStart     = "plan_start"
	KindDeadlock      = "deadlock"
	KindWorkerFailure = "worker_failure"
)

// Kinds 返回当前版本可用于 notify.events 的全部事件名。
// 这里是通知事件契约的唯一事实源。
func Kinds() []string {
	return []string{
		KindRunEnd,
		KindBudget,
		KindAdvanceGate,
		KindStopGuard,
		KindPlanStart,
		KindDeadlock,
		KindWorkerFailure,
	}
}

func IsKnownKind(kind string) bool {
	for _, known := range Kinds() {
		if kind == known {
			return true
		}
	}
	return false
}

// Notifier 按配置分发通知。零值不可用，必须经 New 创建；nil 安全（Send noop）。
type Notifier struct {
	command string          // 非空时替代 system 通道（手机推送走这里）
	events  map[string]bool // nil = 全部 kind 放行
	timeout time.Duration
}

// New 创建 Notifier。command 为空走内置 system 通道（Windows 通知气泡 /
// macOS osascript / Linux notify-send）；events 非空时只放行列出的 kind。
func New(command string, events []string) *Notifier {
	n := &Notifier{command: strings.TrimSpace(command), timeout: 10 * time.Second}
	if len(events) > 0 {
		n.events = make(map[string]bool, len(events))
		for _, ev := range events {
			n.events[ev] = true
		}
	}
	return n
}

// Send 异步发送一条通知。过滤、执行、失败处理全部不影响调用方。
func (n *Notifier) Send(nt Notification) {
	if !n.allows(nt.Kind) {
		return
	}
	go n.deliver(nt)
}

// allows 返回该 kind 是否放行（nil Notifier / 未列入 events 时拦截）。
func (n *Notifier) allows(kind string) bool {
	if n == nil {
		return false
	}
	return n.events == nil || n.events[kind]
}

// deliver 同步执行一次发送并记录失败，由 Send 在 goroutine 中调用。
func (n *Notifier) deliver(nt Notification) {
	if err := n.deliverError(nt); err != nil {
		slog.Warn("通知发送失败", "module", "notify", "kind", nt.Kind, "err", err)
	}
}

// deliverError 同步执行一次发送并返回原始错误。Send 在 goroutine
// 中调用 deliver 记录失败；测试直接调用本方法，避免错误被二次症状掩盖。
func (n *Notifier) deliverError(nt Notification) error {
	ctx, cancel := context.WithTimeout(context.Background(), n.timeout)
	defer cancel()

	if n.command != "" {
		return runCommand(ctx, n.command, nt)
	}
	return runSystem(ctx, nt)
}

// runCommand 执行用户配置的命令：字段经环境变量传入（一行 curl 零依赖、无注入
// 风险），完整 JSON 同时写 stdin（复杂分发场景自行解析）。超时由 ctx 强杀。
func runCommand(ctx context.Context, command string, nt Notification) error {
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		powershell, err := findPowerShell()
		if err != nil {
			return err
		}
		cmd = exec.CommandContext(ctx, powershell, "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", command)
	} else {
		cmd = exec.CommandContext(ctx, "sh", "-c", command)
	}
	cmd.Env = notificationEnv(nt)
	payload, _ := json.Marshal(nt)
	cmd.Stdin = strings.NewReader(string(payload))
	if err := cmd.Run(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("通知命令超时: %w", ctxErr)
		}
		return err
	}
	return nil
}

func notificationEnv(nt Notification) []string {
	return append(os.Environ(),
		"NOTIFY_KIND="+nt.Kind,
		"NOTIFY_LEVEL="+nt.Level,
		"NOTIFY_TITLE="+nt.Title,
		"NOTIFY_BODY="+nt.Body,
	)
}

// runSystem 内置桌面通知：只覆盖"人在电脑旁"的场景，找不到命令静默降级。
func runSystem(ctx context.Context, nt Notification) error {
	switch runtime.GOOS {
	case "windows":
		return runWindowsNotification(ctx, nt)
	case "darwin":
		script := "display notification " + appleScriptString(nt.Body) + " with title " + appleScriptString(nt.Title)
		return exec.CommandContext(ctx, "osascript", "-e", script).Run()
	case "linux":
		if _, err := exec.LookPath("notify-send"); err != nil {
			slog.Info("通知降级为日志（无 notify-send）", "module", "notify", "title", nt.Title, "body", nt.Body)
			return nil
		}
		return exec.CommandContext(ctx, "notify-send", nt.Title, nt.Body).Run()
	default:
		slog.Info("通知降级为日志（平台无 system 通道）", "module", "notify", "title", nt.Title, "body", nt.Body)
		return nil
	}
}

// runWindowsNotification 使用系统自带 PowerShell + WinForms NotifyIcon。
// Windows 10/11 会把气泡显示在右上角并纳入系统通知体验；无需安装模块、注册应用
// 或携带额外二进制。调用方本就异步执行，短暂保活只用于让系统接收气泡消息。
func runWindowsNotification(ctx context.Context, nt Notification) error {
	powershell, err := findPowerShell()
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, powershell,
		"-NoLogo", "-NoProfile", "-NonInteractive", "-STA", "-Command", windowsNotificationScript)
	cmd.Env = notificationEnv(nt)
	return cmd.Run()
}

func findPowerShell() (string, error) {
	// 优先 PowerShell 7：GitHub Windows runner 和现代 Windows 环境中 pwsh
	// 对重定向 stdin 的行为更稳定；Windows PowerShell 5.1 仅作兼容后备。
	for _, name := range []string{"pwsh.exe", "pwsh", "powershell.exe", "powershell"} {
		if path, err := exec.LookPath(name); err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("Windows 通知需要 PowerShell，但系统未找到 powershell.exe 或 pwsh.exe")
}

const windowsNotificationScript = `$ErrorActionPreference = 'Stop'
Add-Type -AssemblyName System.Windows.Forms
Add-Type -AssemblyName System.Drawing
$notify = New-Object System.Windows.Forms.NotifyIcon
$notify.Icon = [System.Drawing.SystemIcons]::Information
$notify.BalloonTipTitle = $env:NOTIFY_TITLE
$notify.BalloonTipText = $env:NOTIFY_BODY
$notify.BalloonTipIcon = switch ($env:NOTIFY_LEVEL) {
  'error' { [System.Windows.Forms.ToolTipIcon]::Error; break }
  'warn'  { [System.Windows.Forms.ToolTipIcon]::Warning; break }
  default { [System.Windows.Forms.ToolTipIcon]::Info }
}
$notify.Visible = $true
$notify.ShowBalloonTip(4000)
Start-Sleep -Milliseconds 4500
$notify.Dispose()`

// appleScriptString 把任意文本包装为 AppleScript 字符串字面量。
func appleScriptString(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}
