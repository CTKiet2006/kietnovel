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

// Run 启动 TUI。
// 启动模式分层约定：
// 1. 快速模式、共创模式属于“启动编排”；
// 2. 正式创作会话进入 host.Host；
// 3. 未来若新增“续写已有小说”等共享模式，统一落到 internal/entry/startup。
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
		logWarning := fmt.Errorf("文件日志不可用，已继续使用终端日志：%w", logErr)
		m.err = logWarning
		m.applyEvent(host.Event{
			Time: time.Now(), Category: "SYSTEM", Level: "warn",
			Summary: logWarning.Error(), Detail: logWarning.Error(),
		})
	}
	// 不在启动时全局开启鼠标上报：欢迎页用不到鼠标，关闭上报可保留终端原生
	// 拖拽选中复制。进入创作工作台（modeRunning）时再由 enterRunning 打开上报，
	// 以支持点击切面板 / 滚轮 / 拖拽侧边栏。
	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err = p.Run()
	return err
}
