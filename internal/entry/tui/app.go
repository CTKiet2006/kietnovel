package tui

import (
	"fmt"
	"log/slog"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/voocel/ainovel-cli/assets"
	"github.com/voocel/ainovel-cli/internal/bootstrap"
	"github.com/voocel/ainovel-cli/internal/host"
	buildversion "github.com/voocel/ainovel-cli/internal/version"
)

// Run khởi động TUI.
// Quy ước phân tầng chế độ khởi động:
// 1. Chế độ nhanh, chế độ đồng sáng tác thuộc "điều phối khởi động";
// 2. Phiên viết chính thức vào host.Host;
// 3. Sau này thêm chế độ dùng chung như "viết tiếp truyện có sẵn" thì gom về internal/entry/startup.
func Run(cfg bootstrap.Config, bundle assets.Bundle, build buildversion.Info) error {
	rt, err := host.New(cfg, bundle, host.WithFileLog("tui.log", false,
		slog.String("version", build.Version),
		slog.String("commit", build.Commit),
		slog.String("built", build.Date),
	))
	if err != nil {
		return err
	}
	defer rt.Close()

	m := NewModel(rt, build.Version)
	m.disableUpdateCheck = cfg.DisableUpdateCheck
	if logErr := rt.FileLogError(); logErr != nil {
		logWarning := fmt.Errorf("File log không dùng được, đã chuyển sang log terminal: %w", logErr)
		m.err = logWarning
		m.applyEvent(host.Event{
			Time: time.Now(), Category: "SYSTEM", Level: "warn",
			Summary: logWarning.Error(), Detail: logWarning.Error(),
		})
	}
	// Không bật báo cáo chuột toàn cục lúc khởi động: màn hình chào không cần chuột, tắt báo cáo để giữ
	// kéo chuột bôi đen copy nguyên bản của terminal. Vào bàn viết (modeRunning) mới mở báo cáo qua enterRunning,
	// để hỗ trợ click đổi panel / cuộn / kéo sidebar.
	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err = p.Run()
	return err
}
