package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/CTKiet2006/kietnovel/internal/host"
	"github.com/CTKiet2006/kietnovel/internal/i18n"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// readerMode là trạng thái của khung đọc: đang chọn chương, hay đang đọc.
type readerMode int

const (
	readerList readerMode = iota
	readerBody
)

// readerState là khung đọc truyện đã lưu, theo đúng mẫu overlay của TUI: giữ
// viewport riêng, View() phủ kín màn hình, Escape trả focus về ô nhập.
//
// Nội dung chương được nạp sẵn vào memory mỗi lần chuyển chương. Một chương trăm
// ký tự chỉ vài chục KB, nên không cần tải dần hay cache phức tạp; đổi lại lật
// chương tức thì không phải chờ I/O.
type readerState struct {
	rt       *host.Host
	mode     readerMode
	viewport viewport.Model
	listVP   viewport.Model
	chapters []int
	// index là vị trí trong chapters, không phải số chương — số chương có thể
	// cách nhau (chương 3 và 7 có, 4-6 chưa) nên không dùng chỉ số làm số chương.
	index    int
	current  host.ChapterText
	contentW int
	boxH     int
	readErr  string
}

func newReaderState(rt *host.Host, width, height int, arg string) *readerState {
	boxW, boxH := reportModalSize(width, height)
	contentW := paddedModalContentWidth(boxW)

	s := &readerState{rt: rt, contentW: contentW, boxH: boxH}
	s.viewport = viewport.New(contentW, boxH-4)
	s.listVP = viewport.New(contentW, boxH-4)
	s.resize(width, height)

	if rt != nil {
		if list, err := rt.ReadableChapters(); err == nil {
			s.chapters = list
		}
	}

	// /read <n> mở thẳng chương n; /read không tham số thì mở danh sách chọn.
	if n, err := strconv.Atoi(strings.TrimSpace(arg)); err == nil {
		s.open(rt, n)
	}
	return s
}

func (s *readerState) resize(width, height int) {
	boxW, boxH := reportModalSize(width, height)
	contentW := paddedModalContentWidth(boxW)
	s.contentW, s.boxH = contentW, boxH
	if s.viewport.Width != contentW || s.viewport.Height != boxH-4 {
		s.viewport.Width, s.viewport.Height = contentW, boxH-4
	}
	if s.listVP.Width != contentW || s.listVP.Height != boxH-4 {
		s.listVP.Width, s.listVP.Height = contentW, boxH-4
	}
}

// open nạp chương n và chuyển sang chế độ đọc. Không tìm thấy thì giữ nguyên
// chế độ danh sách và ghi lý do, để người đọc hiểu vì sao không mở được.
func (s *readerState) open(rt *host.Host, n int) {
	if rt == nil {
		s.readErr = i18n.T("Chưa mở sách nào để đọc.")
		return
	}
	text, err := rt.ReadChapter(n)
	if err != nil {
		s.readErr = err.Error()
		return
	}
	if !text.Exists {
		s.readErr = i18n.Tf("Chương %d chưa có nội dung.", n)
		return
	}
	s.readErr = ""
	s.current = text
	s.mode = readerBody
	s.viewport.SetContent(text.Body)
	s.viewport.GotoTop()
	s.syncIndex(n)
}

// syncIndex đặt con trỏ danh sách về chương đang đọc, để quay lại danh sách thì
// thấy đúng dòng đang ở.
func (s *readerState) syncIndex(n int) {
	for i, c := range s.chapters {
		if c == n {
			s.index = i
			return
		}
	}
}

// step chuyển chương theo bước, có vòng lại đầu/cuối cho tiện khi đọc liên tục.
func (s *readerState) step(rt *host.Host, delta int) {
	if len(s.chapters) == 0 {
		return
	}
	s.index = (s.index + delta + len(s.chapters)) % len(s.chapters)
	s.open(rt, s.chapters[s.index])
}

func (s *readerState) renderList() string {
	if len(s.chapters) == 0 {
		var b strings.Builder
		b.WriteString(i18n.T("Chưa có chương nào đã chốt."))
		if s.readErr != "" {
			b.WriteString("\n\n")
			b.WriteString(lipgloss.NewStyle().Foreground(colorError).Render(s.readErr))
		}
		return b.String()
	}
	curStyle := lipgloss.NewStyle().Foreground(colorAccent).Bold(true)
	nameStyle := lipgloss.NewStyle().Foreground(colorAccent2)
	wordStyle := lipgloss.NewStyle().Foreground(colorDim)
	draftStyle := lipgloss.NewStyle().Foreground(colorAccent)

	var b strings.Builder
	for i, n := range s.chapters {
		marker := "  "
		if i == s.index {
			marker = "❯ "
		}
		label := fmt.Sprintf(i18n.Tf("Chương %d", n), n)
		line := marker + nameStyle.Render(label)
		if text, err := s.rt.ReadChapter(n); err == nil {
			line += wordStyle.Render(" · " + i18n.Tf("%d chữ", text.WordCount))
			if !text.Committed {
				// Bản nháp chưa qua duyệt. Ghi rõ để người đọc không tưởng đây
				// là bản cuối — và để nhớ rằng đây mới là thứ cần đọc để quyết
				// định có chốt hay không.
				line += draftStyle.Render("  " + i18n.T("(bản nháp, chưa chốt)"))
			}
		}
		if i == s.index {
			line = curStyle.Render(line)
		}
		b.WriteString(line)
		if i < len(s.chapters)-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}

func renderReadModal(width, height int, s *readerState) string {
	s.resize(width, height)

	title := i18n.T("Đọc truyện")
	hint := i18n.T("  ↑↓ Cuộn · ←→ Chương · / Danh sách · Esc Đóng")

	var body string
	if s.mode == readerList {
		s.listVP.SetContent(s.renderList())
		hint = i18n.T("  ↑↓ Chọn · Enter Đọc · Esc Đóng")
		body = s.listVP.View()
	} else {
		body = s.viewport.View()
		title = s.readerTitle()
	}

	modal := renderPaddedModalFrame(
		s.contentW+2, s.boxH, title, hint, strings.Split(body, "\n"))
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, modal)
}

func (s *readerState) readerTitle() string {
	if s.current.Title != "" {
		return i18n.Tf("Chương %d · %s", s.current.Chapter, s.current.Title)
	}
	return i18n.Tf("Chương %d", s.current.Chapter)
}

func (m Model) handleReaderKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.reader == nil {
		return m, nil
	}
	rt := m.runtime
	s := m.reader

	// Đóng khung: Esc trong cả hai chế độ đều đóng luôn, không lùi từng bước —
	// người đọc bấm Esc là muốn thoát khung, dùng / để quay lại danh sách.
	if msg.Type == tea.KeyEsc {
		m.reader = nil
		return m, m.textarea.Focus()
	}

	if s.mode == readerList {
		switch msg.Type {
		case tea.KeyUp:
			s.listVP.ScrollUp(1)
			return m, nil
		case tea.KeyDown:
			s.listVP.ScrollDown(1)
			return m, nil
		case tea.KeyEnter:
			if len(s.chapters) > 0 {
				s.open(rt, s.chapters[s.index])
			}
			return m, nil
		case tea.KeyRunes:
			// q đóng, giống phần còn lại của TUI.
			if len(msg.Runes) == 1 && msg.Runes[0] == 'q' {
				m.reader = nil
				return m, m.textarea.Focus()
			}
		}
		return m, nil
	}

	switch msg.Type {
	case tea.KeyUp:
		s.viewport.ScrollUp(1)
	case tea.KeyDown:
		s.viewport.ScrollDown(1)
	case tea.KeyPgUp:
		s.viewport.HalfPageUp()
	case tea.KeyPgDown:
		s.viewport.HalfPageDown()
	case tea.KeyHome:
		s.viewport.GotoTop()
	case tea.KeyEnd:
		s.viewport.GotoBottom()
	case tea.KeyLeft:
		s.step(rt, -1)
	case tea.KeyRight:
		s.step(rt, 1)
	case tea.KeyRunes:
		switch string(msg.Runes) {
		case "q":
			m.reader = nil
			return m, m.textarea.Focus()
		case "/":
			s.mode = readerList
		case "j":
			s.viewport.ScrollDown(1)
		case "k":
			s.viewport.ScrollUp(1)
		}
	}
	return m, nil
}
