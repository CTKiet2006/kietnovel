package tui

import (
	"fmt"
	"github.com/CTKiet2006/kietnovel/internal/i18n"
	"log/slog"
	"time"

	"github.com/CTKiet2006/kietnovel/assets"
	"github.com/CTKiet2006/kietnovel/internal/bootstrap"
	"github.com/CTKiet2006/kietnovel/internal/host"
	buildversion "github.com/CTKiet2006/kietnovel/internal/version"
	tea "github.com/charmbracelet/bubbletea"
)

// Run khởi động TUI.
// Quy ước phân tầng chế độ khởi động:
// 1. Chế độ nhanh, chế độ đồng sáng tác thuộc "điều phối khởi động";
// 2. Phiên viết chính thức vào host.Host;
// 3. Sau này thêm chế độ dùng chung như "viết tiếp truyện có sẵn" thì gom về internal/entry/startup.
func Run(cfg bootstrap.Config, bundle assets.Bundle, build buildversion.Info) error {
	// Giữ lại đúng bộ tuỳ chọn này: khi /books chuyển truyện, Host mới phải được
	// dựng y hệt, nếu không file log sẽ mất thông tin version/commit/built.
	hostOpts := []host.NewOption{host.WithFileLog("tui.log", false,
		slog.String("version", build.Version),
		slog.String("commit", build.Commit),
		slog.String("built", build.Date),
	)}
	rt, err := host.New(cfg, bundle, hostOpts...)
	if err != nil {
		return err
	}
	defer rt.Close()

	m := NewModel(rt, build.Version)
	m.cfg = cfg
	m.bundle = bundle
	m.hostOpts = hostOpts
	m.disableUpdateCheck = cfg.DisableUpdateCheck
	if logErr := rt.FileLogError(); logErr != nil {
		logWarning := fmt.Errorf(i18n.T("File log không dùng được, đã chuyển sang log terminal: %w"), logErr)
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
