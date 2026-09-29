// Package notify provides an unattended alert channel.
//
// Constitutional role (architecture.md 2.3): a purely observational-layer action -- alerts never intervene in control flow
// (no retry, no reassignment, no shutdown); they only "shout" events that already exist inside the TUI out to the screen.
// Send runs asynchronously, never blocks the Host, and only records failures with slog.
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

// Notification holds every fact about a single alert.
type Notification struct {
	Kind  string `json:"kind"`  // the stable event name returned by Kinds
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

// Kinds returns every event name that this version accepts in notify.events.
// This is the single source of truth for the notification event contract.
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

// Notifier dispatches notifications according to configuration. The zero value is unusable and must be built with New; nil is safe (Send is a noop).
type Notifier struct {
	command string          // when non-empty, replaces the system channel (phone push goes through here)
	events  map[string]bool // nil = every kind passes
	timeout time.Duration
}

// New builds a Notifier. An empty command uses the built-in system channel (Windows toast bubbles /
// macOS osascript / Linux notify-send); a non-empty events map only lets the listed kinds through.
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

// Send delivers one notification asynchronously. Filtering, execution and failure handling never affect the caller.
func (n *Notifier) Send(nt Notification) {
	if !n.allows(nt.Kind) {
		return
	}
	go n.deliver(nt)
}

// allows reports whether a kind passes (a nil Notifier, or a kind missing from events, is blocked).
func (n *Notifier) allows(kind string) bool {
	if n == nil {
		return false
	}
	return n.events == nil || n.events[kind]
}

// deliver performs one synchronous send and records failures; Send calls it from a goroutine.
func (n *Notifier) deliver(nt Notification) {
	if err := n.deliverError(nt); err != nil {
		slog.Warn("通知发送失败", "module", "notify", "kind", nt.Kind, "err", err)
	}
}

// deliverError performs one synchronous send and returns the raw error. Send calls deliver from a
// goroutine to record failures; tests call this method directly, so the error is not masked by a secondary symptom.
func (n *Notifier) deliverError(nt Notification) error {
	ctx, cancel := context.WithTimeout(context.Background(), n.timeout)
	defer cancel()

	if n.command != "" {
		return runCommand(ctx, n.command, nt)
	}
	return runSystem(ctx, nt)
}

// runCommand runs the user-configured command: the fields come in through environment variables (a one-line curl has zero
// dependencies and no injection risk), and the full JSON is also written to stdin (for complex fan-out scenarios to parse). ctx force-kills it on timeout.
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

// runSystem is the built-in desktop notification: it only covers the "person is at the computer" case and degrades silently when the command is missing.
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

// runWindowsNotification uses the PowerShell that ships with Windows plus a WinForms NotifyIcon.
// Windows 10/11 shows the bubble in the top-right corner and folds it into the system notification experience; no module to install, no app to
// register and no extra binary to carry. The caller already runs asynchronously; the short-lived process only exists so the system can receive the bubble.
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
	// Prefer PowerShell 7: on GitHub Windows runners and modern Windows, pwsh
	// behaves more predictably with redirected stdin; Windows PowerShell 5.1 is only the compatibility fallback.
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

// appleScriptString wraps arbitrary text as an AppleScript string literal.
func appleScriptString(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}
