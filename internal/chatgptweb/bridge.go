// Package chatgptweb manages the codex-chatgpt-web bridge lifecycle.
// When the user selects the chatgpt-web provider, kietnovel automatically
// downloads the bridge runtime (if missing), launches it in the background,
// and ensures the user is logged in to ChatGPT.
package chatgptweb

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	// BridgePort is the fixed loopback port of the bridge server.
	BridgePort = 17841
	// BridgeURL is the base URL clients should use.
	BridgeURL = "http://127.0.0.1:17841/v1"
	// DefaultModel is the model identifier for free-tier ChatGPT Web.
	DefaultModel = "chatgpt-web/gpt-5.6-luna"
	// DefaultContextWindow is the context window for Luna.
	DefaultContextWindow = 1_050_000
)

// bridgeDir returns ~/.kietnovel/bridge/.
func bridgeDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".kietnovel", "bridge")
}

// configDir returns ~/.codex-chatgpt-web/.
func configDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".codex-chatgpt-web")
}

// bridgeCmd returns the path to the bridge executable.
func bridgeCmd() string {
	if runtime.GOOS == "windows" {
		return filepath.Join(bridgeDir(), "bin", "codex-chatgpt-web.cmd")
	}
	return filepath.Join(bridgeDir(), "bin", "codex-chatgpt-web")
}

// IsBridgeInstalled checks if the bridge binary exists on disk.
func IsBridgeInstalled() bool {
	_, err := os.Stat(bridgeCmd())
	return err == nil
}

// IsLoggedIn checks if a verified storage-state exists.
func IsLoggedIn() bool {
	verifiedPath := filepath.Join(configDir(), "browser", "storage-state.json.verified.json")
	_, err := os.Stat(verifiedPath)
	if err != nil {
		return false
	}
	statePath := filepath.Join(configDir(), "browser", "storage-state.json")
	info, err := os.Stat(statePath)
	if err != nil {
		return false
	}
	return info.Size() > 100
}

// IsBridgeRunning checks if the bridge server is responding on its port.
func IsBridgeRunning() bool {
	client := &http.Client{Timeout: 2 * time.Second}
	// Use a POST to /v1/responses with empty body — any response (even 400) means the server is up.
	// /v1/models returns 401 because it tries to proxy to api.openai.com.
	resp, err := client.Post(BridgeURL+"/responses", "application/json", strings.NewReader("{}"))
	if err != nil {
		return false
	}
	resp.Body.Close()
	return true
}

// EnsureConfig creates ~/.codex-chatgpt-web/config.json if it doesn't exist.
func EnsureConfig() error {
	dir := configDir()
	if err := os.MkdirAll(filepath.Join(dir, "browser"), 0o755); err != nil {
		return fmt.Errorf("tạo thư mục bridge: %w", err)
	}
	cfgPath := filepath.Join(dir, "config.json")
	if _, err := os.Stat(cfgPath); err == nil {
		return nil // already exists
	}

	// Find Chrome
	chromePath := findChrome()

	cfg := map[string]any{
		"version":                              3,
		"releaseVersion":                       "6.1.4",
		"mode":                                 "browser-only",
		"subagentProtocol":                     "compatibility-v1",
		"host":                                 "127.0.0.1",
		"port":                                 BridgePort,
		"contextWindow":                        256000,
		"appName":                              "Codex Native2",
		"automaticAppName":                     "Codex Native2",
		"manualAppName":                        "Codex Zero Risk",
		"browserHost":                          "managed-chrome",
		"browserInteractionMode":               "automatic",
		"chromeExecutablePath":                 chromePath,
		"storageStatePath":                     filepath.Join(dir, "browser", "storage-state.json"),
		"brokerSocketPath":                     brokerSocketPath(),
		"headed":                               true,
		"solAvailable":                         false,
		"extraHighAvailable":                   false,
		"proAvailable":                         false,
		"experimentalBiggerContext":            false,
		"experimentalSkillAttachments":         false,
		"experimentalFreshConversationPerTurn": false,
		"useSavedChats":                        false,
		"zeroRiskProEnabled":                   false,
		"autoApproveToolCalls":                 false,
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(cfgPath, data, 0o644)
}

// StartBridge launches the bridge server in the background.
// It returns nil if the bridge is already running.
func StartBridge() error {
	if IsBridgeRunning() {
		return nil
	}
	if !IsBridgeInstalled() {
		return fmt.Errorf("bridge chưa được cài đặt tại %s; hãy chạy kietnovel setup-bridge trước", bridgeDir())
	}
	if err := EnsureConfig(); err != nil {
		return fmt.Errorf("khởi tạo config bridge: %w", err)
	}

	cmd := exec.Command(bridgeCmd(), "serve")
	cmd.Dir = bridgeDir()
	cmd.Stdout = nil
	cmd.Stderr = nil
	// Detach from parent process
	setSysProcAttr(cmd)

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("khởi chạy bridge: %w", err)
	}
	// Release so parent doesn't wait
	go cmd.Wait()

	// Wait for the server to become ready (up to 30s)
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if IsBridgeRunning() {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("bridge không phản hồi sau 30 giây trên %s", BridgeURL)
}

// RunLogin launches the bridge login flow so the user can authenticate with ChatGPT.
func RunLogin() error {
	if !IsBridgeInstalled() {
		return fmt.Errorf("bridge chưa được cài đặt")
	}
	if err := EnsureConfig(); err != nil {
		return err
	}
	cmd := exec.Command(bridgeCmd(), "login")
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// EnsureReady makes sure the bridge is installed, logged in, and running.
// Returns a user-facing status message for each step.
func EnsureReady(log func(string)) error {
	if !IsBridgeInstalled() {
		return fmt.Errorf("ChatGPT Web bridge chưa được cài đặt.\nTải gói về: https://github.com/miuuyy/codex-chatgpt-web/releases\nGiải nén vào: %s", bridgeDir())
	}

	if !IsLoggedIn() {
		log("Chưa đăng nhập ChatGPT — đang mở trình duyệt để đăng nhập...")
		if err := RunLogin(); err != nil {
			return fmt.Errorf("đăng nhập ChatGPT thất bại: %w", err)
		}
		if !IsLoggedIn() {
			return fmt.Errorf("đăng nhập ChatGPT chưa hoàn tất, vui lòng thử lại")
		}
		log("Đã đăng nhập ChatGPT thành công!")
	}

	if !IsBridgeRunning() {
		log("Đang khởi chạy ChatGPT Web bridge...")
		if err := StartBridge(); err != nil {
			return err
		}
		log("ChatGPT Web bridge đã sẵn sàng!")
	}

	return nil
}

// --- platform helpers ---

func findChrome() string {
	if runtime.GOOS == "windows" {
		candidates := []string{
			filepath.Join(os.Getenv("PROGRAMFILES"), "Google", "Chrome", "Application", "chrome.exe"),
			filepath.Join(os.Getenv("PROGRAMFILES(X86)"), "Google", "Chrome", "Application", "chrome.exe"),
			filepath.Join(os.Getenv("LOCALAPPDATA"), "Google", "Chrome", "Application", "chrome.exe"),
		}
		for _, p := range candidates {
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
	}
	if runtime.GOOS == "darwin" {
		p := "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	// Linux: try PATH
	if p, err := exec.LookPath("google-chrome"); err == nil {
		return p
	}
	if p, err := exec.LookPath("chromium"); err == nil {
		return p
	}
	return "chrome"
}

func brokerSocketPath() string {
	if runtime.GOOS == "windows" {
		return `\\.\pipe\codex-chatgpt-web-kietnovel`
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".codex-chatgpt-web", "broker.sock")
}
