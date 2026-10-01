package tui

import (
	"github.com/CTKiet2006/kietnovel/internal/host"
	"github.com/CTKiet2006/kietnovel/internal/i18n"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// bookLanguageState là khung hỏi ngôn ngữ sáng tác cho truyện chưa khoá.
//
// Cần hỏi thay vì tự chọn: nếu người dùng đã đổi ngôn ngữ giữa chừng rồi mở
// truyện cũ, tự ghi mặc định sẽ khoá sai — và chương đã viết không tự dịch được.
type bookLanguageState struct {
	prompt  string // yêu cầu gõ ban đầu, giữ lại để sau khi chọn xong thì chạy tiếp
	choices []string
	cursor  int
	bookDir string
}

func newBookLanguageState(prompt, bookDir string) *bookLanguageState {
	return &bookLanguageState{
		prompt:  prompt,
		bookDir: bookDir,
		choices: []string{i18n.LangVietnamese, i18n.LangEnglish, i18n.LangChinese},
	}
}

// handleBookLanguageKey trả về true nếu khung đã xử lý xong phím.
func (m *Model) handleBookLanguageKey(msg tea.KeyMsg) (tea.Model, tea.Cmd, bool) {
	s := m.bookLang
	if s == nil {
		return m, nil, false
	}
	switch msg.Type {
	case tea.KeyEsc:
		m.bookLang = nil
		m.applyEvent(host.Event{
			Category: "SYSTEM", Level: "info",
			Summary: i18n.T("Đã hủy chọn ngôn ngữ. Truyện này chưa khoá ngôn ngữ sáng tác."),
		})
		m.refreshEventViewport()
		return m, m.textarea.Focus(), true
	case tea.KeyUp:
		if s.cursor > 0 {
			s.cursor--
		}
		return m, nil, true
	case tea.KeyDown:
		if s.cursor < len(s.choices)-1 {
			s.cursor++
		}
		return m, nil, true
	case tea.KeyEnter:
		out, cmd := m.confirmBookLanguage(s.choices[s.cursor])
		return out, cmd, true
	case tea.KeyRunes:
		if len(msg.Runes) > 0 {
			r := string(msg.Runes)
			for i, c := range s.choices {
				if c == r {
					s.cursor = i
					out, cmd := m.confirmBookLanguage(c)
					return out, cmd, true
				}
			}
		}
		return m, nil, true
	}
	return m, nil, true
}

// confirmBookLanguage khoá ngôn ngữ rồi chạy tiếp yêu cầu đã gõ dở.
func (m Model) confirmBookLanguage(lang string) (tea.Model, tea.Cmd) {
	prompt := ""
	if m.bookLang != nil {
		prompt = m.bookLang.prompt
	}
	m.bookLang = nil
	if err := m.runtime.SetBookLanguage(lang); err != nil {
		out, _ := renderBooksNotice(m, i18n.Tf("Không lưu được ngôn ngữ: %v", err))
		return out, nil
	}
	m.applyEvent(host.Event{
		Category: "SYSTEM", Level: "ok",
		Summary: i18n.Tf("Đã khoá ngôn ngữ sáng tác của truyện này: %s. Truyện khác giữ ngôn ngữ riêng.", languageLabel(lang)),
	})
	m.refreshEventViewport()
	// Chạy tiếp yêu cầu gõ dở, thay vì bắt người dùng gõ lại.
	// Focus lại ô nhập: khung hỏi đã Blur, engine chạy xong mà không Focus thì
	// bàn phím chết (textarea nuốt phím khi blurred).
	if prompt != "" {
		m.textarea.Reset()
		return m, tea.Batch(startRuntime(m.runtime, prompt), m.textarea.Focus())
	}
	return m, m.textarea.Focus()
}

func (s *bookLanguageState) view(w, h int) string {
	boxW, boxH := reportModalSize(w, h)
	contentW := paddedModalContentWidth(boxW)
	cur := lipgloss.NewStyle().Foreground(colorAccent).Bold(true)
	dim := lipgloss.NewStyle().Foreground(colorDim)

	var b []string
	b = append(b, lipgloss.NewStyle().Foreground(bodyTextColor).
		Render(i18n.T("Chọn ngôn ngữ sáng tác cho truyện này.")))
	b = append(b, "")
	b = append(b, dim.Render(i18n.T("Khoá lại để chương sau không lệch giọng với chương trước. Đổi sau sẽ không dịch lại những gì đã viết.")))
	b = append(b, "")
	for i, c := range s.choices {
		marker := "  "
		line := languageLabel(c) + "  " + dim.Render("("+c+")")
		if i == s.cursor {
			marker = "❯ "
			line = cur.Render(line)
		}
		b = append(b, marker+line)
	}
	b = append(b, "")
	b = append(b, dim.Render(i18n.T("  ↑↓ chọn · Enter xác nhận · gõ vi|en|zh · Esc huỷ")))

	modal := renderPaddedModalFrame(contentW+2, boxH, i18n.T("Ngôn ngữ sáng tác"), "", b)
	return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, modal)
}
