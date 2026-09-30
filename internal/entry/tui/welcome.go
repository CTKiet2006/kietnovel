package tui

import (
	"github.com/CTKiet2006/kietnovel/internal/host"
	"github.com/CTKiet2006/kietnovel/internal/i18n"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// welcomeState là màn chào khi mở app: hỏi muốn làm gì thay vì tự chạy engine.
//
// Trước đây Init gọi bootstrapRuntime ngay, nên mở terminal lên là engine tự chạy
// tiếp — kể cả khi người dùng chỉ muốn xem dàn ý. Muốn dừng phải bấm Esc, mà lúc
// đó model đã gọi xong một lượt (tốn tiền).
type welcomeState struct {
	choices []welcomeChoice
	cursor  int
	title   string
	phase   string
}

type welcomeChoice int

const (
	welcomeContinue welcomeChoice = iota
	welcomeOtherBook
	welcomeQuit
)

func newWelcomeState(snap host.UISnapshot) *welcomeState {
	return &welcomeState{
		choices: []welcomeChoice{welcomeContinue, welcomeOtherBook, welcomeQuit},
		title:   snap.BookTitle,
		phase:   snap.Phase,
	}
}

func (s *welcomeState) labels() []string {
	return []string{
		i18n.T("Viết tiếp"),
		i18n.T("Chọn truyện khác"),
		i18n.T("Thoát"),
	}
}

// handleWelcomeKey trả (model, cmd, đã xử lý). Esc = Viết tiếp: giữ hành vi cũ
// cho người quen bấm nhanh, không kẹt ở màn hình không làm gì được.
func (m Model) handleWelcomeKey(msg tea.KeyMsg) (tea.Model, tea.Cmd, bool) {
	s := m.welcome
	if s == nil {
		return m, nil, false
	}
	switch msg.Type {
	case tea.KeyEsc:
		return m.welcomeChoose(welcomeContinue)
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
		return m.welcomeChoose(s.choices[s.cursor])
	}
	return m, nil, true
}

func (m Model) welcomeChoose(c welcomeChoice) (tea.Model, tea.Cmd, bool) {
	m.welcome = nil
	m.welcomeSeen = true
	switch c {
	case welcomeContinue:
		// Chạy đúng luồng cũ: Resume + engine, qua bootstrapMsg như trước.
		return m, bootstrapRuntime(m.runtime), true
	case welcomeOtherBook:
		out, cmd := openBooks(m, booksList)
		return out, cmd, true
	default:
		return m, tea.Quit, true
	}
}

func (s *welcomeState) view(w, h int) string {
	boxW, boxH := reportModalSize(w, h)
	contentW := paddedModalContentWidth(boxW)
	cur := lipgloss.NewStyle().Foreground(colorAccent).Bold(true)
	dim := lipgloss.NewStyle().Foreground(colorDim)
	body := lipgloss.NewStyle().Foreground(bodyTextColor)

	var b []string
	if s.title != "" {
		b = append(b, body.Render(s.title))
	}
	if s.phase != "" {
		b = append(b, dim.Render(i18n.Tf("Giai đoạn: %s", s.phase)))
	}
	b = append(b, "")
	labels := s.labels()
	for i, c := range s.choices {
		marker := "  "
		line := labels[i]
		if i == s.cursor {
			marker = "❯ "
			line = cur.Render(line)
		}
		_ = c
		b = append(b, marker+line)
	}
	b = append(b, "")
	b = append(b, dim.Render(i18n.T("  ↑↓ chọn · Enter xác nhận · Esc viết tiếp")))

	modal := renderPaddedModalFrame(contentW+2, boxH, i18n.T("Chào mừng trở lại"), "", b)
	return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, modal)
}
