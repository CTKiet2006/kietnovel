package tui

import (
	"github.com/CTKiet2006/kietnovel/internal/i18n"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type helpState struct {
	viewport viewport.Model
}

func newHelpState(width, height int) *helpState {
	boxW, boxH := reportModalSize(width, height)
	contentW := paddedModalContentWidth(boxW)
	text := renderHelpText(contentW)

	vp := viewport.New(contentW, boxH-4)
	vp.SetContent(text)
	return &helpState{viewport: vp}
}

func renderHelpText(width int) string {
	titleStyle := lipgloss.NewStyle().Foreground(colorAccent).Bold(true)
	nameStyle := lipgloss.NewStyle().Foreground(colorAccent2).Bold(true)
	usageStyle := lipgloss.NewStyle().Foreground(colorMuted)
	descStyle := lipgloss.NewStyle().Foreground(bodyTextColor)
	hintStyle := lipgloss.NewStyle().Foreground(colorDim)

	var b strings.Builder
	b.WriteString(titleStyle.Render(i18n.T("Trợ giúp lệnh")))
	b.WriteString("\n\n")

	for i, spec := range commandSpecs() {
		if i > 0 {
			b.WriteString("\n")
		}
		lang := i18n.Language()
		b.WriteString(nameStyle.Render("/" + spec.DisplayName(lang)))
		aliases := append([]string(nil), spec.Aliases...)
		if spec.Name != spec.DisplayName(lang) {
			aliases = append([]string{spec.Name}, aliases...)
		}
		if len(aliases) > 0 {
			b.WriteString(usageStyle.Render("  alias: /" + strings.Join(aliases, " /")))
		}
		b.WriteString("\n")
		b.WriteString(usageStyle.Render(i18n.T("Cách dùng: ") + spec.UsageText(lang)))
		b.WriteString("\n")
		b.WriteString(descStyle.Render(wrapText(spec.Description, width)))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(titleStyle.Render(i18n.T("Phím tắt")))
	b.WriteString("\n\n")
	for _, line := range []string{
		i18n.T("Gõ / để tìm lệnh"),
		i18n.T("↑↓ chọn lệnh gợi ý"),
		i18n.T("Tab/Enter nhận gợi ý"),
		i18n.T("Esc đóng bảng lệnh đang mở"),
		i18n.T("Ctrl+R bật chế độ bôi đen để copy (tắt báo chuột để kéo chọn, nhấn lần nữa để về như cũ)"),
	} {
		b.WriteString(hintStyle.Render(line))
		b.WriteString("\n")
	}
	return b.String()
}

func renderHelpModal(width, height int, state *helpState) string {
	if state == nil {
		return ""
	}

	boxW, boxH := reportModalSize(width, height)
	contentW := paddedModalContentWidth(boxW)

	if state.viewport.Width != contentW {
		state.viewport.Width = contentW
	}
	if state.viewport.Height != boxH-4 {
		state.viewport.Height = boxH - 4
	}

	modal := renderPaddedModalFrame(
		boxW,
		boxH,
		i18n.T("Trợ giúp lệnh"),
		i18n.T("  ↑↓ Cuộn · Esc Đóng"),
		strings.Split(state.viewport.View(), "\n"),
	)
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, modal)
}

func (m Model) handleHelpKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.help == nil {
		return m, nil
	}
	switch msg.Type {
	case tea.KeyEsc:
		m.help = nil
		return m, m.textarea.Focus()
	case tea.KeyUp:
		m.help.viewport.ScrollUp(1)
		return m, nil
	case tea.KeyDown:
		m.help.viewport.ScrollDown(1)
		return m, nil
	case tea.KeyPgUp:
		m.help.viewport.HalfPageUp()
		return m, nil
	case tea.KeyPgDown:
		m.help.viewport.HalfPageDown()
		return m, nil
	default:
		return m, nil
	}
}
