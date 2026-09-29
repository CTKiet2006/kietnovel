package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

func TestCommandInputHighlightsOnlyRegisteredCommands(t *testing.T) {
	oldProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(oldProfile) })

	m := Model{textarea: textarea.New()}
	m.textarea.Focus()

	for _, input := range []string{"/config", "/model writer", "/plan"} { // /plan là alias của /cocreate
		m.textarea.SetValue(input)
		m.syncCommandInputHighlight()
		if m.commandToken == "" {
			t.Errorf("Lệnh đã đăng ký %q phải được nhận diện", input)
		}
		plain := m.textarea.View()
		if colored := highlightCommandToken(plain, input, m.commandToken); colored == plain {
			t.Errorf("Kết xuất thực tế của lệnh đã đăng ký %q không đổi màu", input)
		}
	}

	for _, input := range []string{"普通输入", "/con", "/unknown"} {
		m.textarea.SetValue(input)
		m.syncCommandInputHighlight()
		if m.commandToken != "" {
			t.Errorf("Lệnh chưa đầy đủ %q không được highlight, token=%q", input, m.commandToken)
		}
	}
}

func TestCommandInputDoesNotHighlightArguments(t *testing.T) {
	oldProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(oldProfile) })

	m := Model{textarea: textarea.New()}
	m.textarea.Focus()
	m.textarea.SetValue("/reopen 继续创作")
	m.textarea.CursorEnd()
	m.syncCommandInputHighlight()

	plainView := m.textarea.View()
	view := highlightCommandToken(plainView, m.textarea.Value(), m.commandToken)
	if stripped := ansi.Strip(view); stripped != ansi.Strip(plainView) {
		t.Fatalf("Highlight không được làm đổi nội dung nhập: %q", stripped)
	}
	if !strings.Contains(view, "/reopen"+resetForeground+" 继续创作") {
		t.Fatalf("Tham số sau lệnh chưa khôi phục màu chữ thường: %q", view)
	}
}
