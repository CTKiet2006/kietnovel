package tui

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/CTKiet2006/kietnovel/internal/host"
	"github.com/CTKiet2006/kietnovel/internal/i18n"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func timeNow() time.Time { return time.Now() }

// booksState là khung quản lý truyện: /new (tạo mới), /delete (xoá, có xác nhận),
// /books (chuyển qua lại). Cả ba dùng chung một khung chọn để không phải viết
// ba bố cục khác nhau.
type booksState struct {
	mode   booksMode
	list   []host.Book
	cursor int

	// Xác nhận xoá
	pending *host.DeleteBookResult
	// Tạo mới
	draft string

	// Xác nhận chuyển truyện. Chỉ hiện khi truyện đang mở còn việc dở: nếu không
	// có gì dở thì chuyển thẳng, hỏi thêm chỉ làm chậm.
	target     string
	targetName string
	switchWhy  string

	listVP   viewport.Model
	contentW int
	boxH     int
}

type booksMode int

const (
	booksList booksMode = iota
	booksDeleteConfirm
	booksNewDraft
	booksSwitchConfirm
)

func newBooksState(w, h int, mode booksMode) *booksState {
	boxW, boxH := reportModalSize(w, h)
	contentW := paddedModalContentWidth(boxW)
	s := &booksState{mode: mode, contentW: contentW, boxH: boxH}
	s.listVP = viewport.New(contentW, boxH-4)
	return s
}

func (s *booksState) resize(w, h int) {
	boxW, boxH := reportModalSize(w, h)
	s.contentW, s.boxH = paddedModalContentWidth(boxW), boxH
	s.listVP.Width, s.listVP.Height = s.contentW, boxH-4
}

// selected trả về truyện đang trỏ, hoặc nil.
func (s *booksState) selected() *host.Book {
	if s.cursor < 0 || s.cursor >= len(s.list) {
		return nil
	}
	return &s.list[s.cursor]
}

func renderBooksModal(w, h int, s *booksState, errMsg string) string {
	s.resize(w, h)
	title := i18n.T("Truyện")
	hint := i18n.T("  ↑↓ Chọn · Enter Mở · d Xoá · n Tạo mới · Esc Đóng")

	var body string
	switch s.mode {
	case booksDeleteConfirm:
		title = i18n.T("Xoá truyện — xác nhận")
		hint = i18n.T("  y Xoá vĩnh viễn · Esc Huỷ")
		body = s.renderDeleteConfirm()
	case booksNewDraft:
		title = i18n.T("Tạo truyện mới")
		hint = i18n.T("  Enter Tạo và mở · Esc Huỷ")
		body = s.renderNewDraft()
	case booksSwitchConfirm:
		title = i18n.T("Chuyển truyện — xác nhận")
		hint = i18n.T("  Enter/y Chuyển · Esc Ở lại")
		body = s.renderSwitchConfirm()
	default:
		body = s.renderList()
	}
	if errMsg != "" {
		body += "\n\n" + lipgloss.NewStyle().Foreground(colorError).Render("! "+errMsg)
	}

	modal := renderPaddedModalFrame(
		s.contentW+2, s.boxH, title, hint, strings.Split(body, "\n"))
	return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, modal)
}

func (s *booksState) renderList() string {
	if len(s.list) == 0 {
		return i18n.T("Chưa có truyện nào trong output/. Gõ n để tạo truyện mới.")
	}
	cur := lipgloss.NewStyle().Foreground(colorAccent).Bold(true)
	dim := lipgloss.NewStyle().Foreground(colorDim)

	var b strings.Builder
	for i, bk := range s.list {
		marker := "  "
		if i == s.cursor {
			marker = "❯ "
		}
		line := fmt.Sprintf("%s%s", marker, bk.Name)
		var tags []string
		if bk.Chapters > 0 {
			tags = append(tags, i18n.Tf("%d chương", bk.Chapters))
		}
		if bk.Words > 0 {
			tags = append(tags, i18n.Tf("%d chữ", bk.Words))
		}
		if !bk.HasOutline {
			tags = append(tags, i18n.T("chưa có dàn ý"))
		}
		if len(tags) > 0 {
			line += dim.Render("  · " + strings.Join(tags, " · "))
		}
		if i == s.cursor {
			line = cur.Render(line)
		}
		b.WriteString(line)
		if i < len(s.list)-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}

// renderDeleteConfirm nói rõ đang xoá cái gì, bao nhiêu dữ liệu, trước khi cần yêu
// cầu gõ "y". Người dùng phải thấy hậu quả rồi mới quyết, không phải bấm Enter
// rồi mới biết mất gì.
func (s *booksState) renderDeleteConfirm() string {
	if s.pending == nil {
		return ""
	}
	p := s.pending
	warn := lipgloss.NewStyle().Foreground(colorError).Bold(true)
	body := lipgloss.NewStyle().Foreground(bodyTextColor)
	dim := lipgloss.NewStyle().Foreground(colorDim)

	var b strings.Builder
	b.WriteString(warn.Render(i18n.T("BẠN ĐANG XOÁ TRUYỆN NÀY")))
	b.WriteString("\n\n")
	b.WriteString(body.Render(p.Name))
	b.WriteString("\n")
	b.WriteString(dim.Render(p.Dir))
	b.WriteString("\n\n")
	b.WriteString(body.Render(i18n.Tf("Số chương đã chốt: %d", p.Chapters)))
	b.WriteString("\n")
	b.WriteString(body.Render(i18n.Tf("Tổng số chữ: %d", p.Words)))
	b.WriteString("\n")
	b.WriteString(body.Render(i18n.Tf("Dung lượng: %.1f MB", float64(p.SizeBytes)/(1024*1024))))
	b.WriteString("\n\n")
	b.WriteString(warn.Render(i18n.T("Xoá KHÔNG khôi phục được. Gõ y để xác nhận.")))
	return b.String()
}

// renderNewDraft cho phép gõ tên có dấu và có khoảng trắng. Tên thư mục thật sẽ
// được sanitize khi bấm Enter, còn tên hiển thị giữ nguyên như gõ.
func (s *booksState) renderNewDraft() string {
	dim := lipgloss.NewStyle().Foreground(colorDim)
	ok := lipgloss.NewStyle().Foreground(colorSuccess)
	var b strings.Builder
	b.WriteString(i18n.T("Tên truyện mới:"))
	b.WriteString("\n\n❯ " + s.draft)
	b.WriteString("\n\n")
	if slug := sanitizeBookName(s.draft); slug != "" {
		b.WriteString(dim.Render(i18n.Tf("Sẽ tạo thư mục: %s", slug)))
		if s.draft != slug {
			b.WriteString("\n")
			b.WriteString(ok.Render(i18n.T("Tên hiển thị giữ nguyên như bạn gõ, có dấu cũng được.")))
		}
	} else {
		b.WriteString(dim.Render(i18n.T("Để trống rồi Enter: tên ngẫu nhiên, đổi lại sau bằng /rename.")))
	}
	return b.String()
}

// runRenameBook đổi tên hiển thị của truyện ĐANG MỞ (ghi meta/book.json).
// Chỉ đổi tiêu đề, không đổi thư mục — an toàn khi engine đang chạy vì mọi
// đường dẫn trong phiên đều trỏ theo thư mục, không theo tên.
func (m Model) runRenameBook(args []string) (tea.Model, tea.Cmd) {
	if m.runtime == nil {
		return renderBooksError(m, i18n.T("Chưa mở truyện nào để đổi tên."))
	}
	title := strings.TrimSpace(strings.Join(args, " "))
	if title == "" {
		return renderBooksError(m, i18n.T("Cần nhập tên mới: /rename <tên mới>."))
	}
	if err := writeBookTitle(m.runtime.Dir(), title); err != nil {
		return renderBooksError(m, i18n.Tf("Đổi tên hỏng: %v", err))
	}
	out, _ := renderBooksNotice(m, i18n.Tf("Đã đổi tên truyện thành %q.", title))
	return out, fetchSnapshot(m.runtime)
}

func (m Model) handleBooksKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.books == nil {
		return m, nil
	}
	s := m.books

	switch s.mode {
	case booksDeleteConfirm:
		switch msg.Type {
		case tea.KeyEsc:
			s.mode = booksList
			s.pending = nil
		case tea.KeyRunes:
			if string(msg.Runes) == "y" {
				return m.deleteConfirmedBook()
			}
		}
		return m, nil

	case booksNewDraft:
		switch msg.Type {
		case tea.KeyEsc:
			s.mode = booksList
			s.draft = ""
		case tea.KeyEnter:
			return m.createBookConfirmed()
		case tea.KeyBackspace:
			if s.draft != "" {
				r := []rune(s.draft)
				s.draft = string(r[:len(r)-1])
			}
		case tea.KeySpace:
			// Giữ khoảng trắng: tên hiển thị lấy nguyên văn. Sanitize lo ở
			// bước tạo thư mục, không sửa ở đây — nếu ép dấu gạch ngay khi
			// gõ thì tên hiển thị mất dấu, mất đúng thứ người dùng muốn.
			s.draft += " "
		case tea.KeyRunes:
			s.draft += string(msg.Runes)
		}
		return m, nil

	case booksSwitchConfirm:
		switch msg.Type {
		case tea.KeyEsc:
			s.mode = booksList
			s.target, s.targetName, s.switchWhy = "", "", ""
		case tea.KeyEnter:
			// Enter = đồng ý chuyển. An toàn vì chuyển truyện không xoá gì:
			// truyện cũ nằm nguyên trên đĩa. (Khác với booksDeleteConfirm,
			// nơi Enter cố ý KHÔNG có tác dụng để tránh xoá nhầm.)
			return m.confirmSwitchFromModal()
		case tea.KeyRunes:
			if string(msg.Runes) == "y" {
				return m.confirmSwitchFromModal()
			}
		}
		return m, nil
	}

	// Danh sách
	switch msg.Type {
	case tea.KeyEsc:
		m.books = nil
		return m, m.textarea.Focus()
	case tea.KeyUp:
		if s.cursor > 0 {
			s.cursor--
			m.booksErr = ""
		}
	case tea.KeyDown:
		if s.cursor < len(s.list)-1 {
			s.cursor++
			m.booksErr = ""
		}
	case tea.KeyEnter:
		if bk := s.selected(); bk != nil {
			if sameDir(bk.Dir, m.runtime.Dir()) {
				// Báo NGAY TRONG MODAL qua booksErr, không qua dòng sự kiện:
				// modal đang che toàn màn hình nên báo ra sự kiện thì người
				// dùng không thấy, tưởng bấm Enter bị đơ.
				m.booksErr = i18n.Tf("Truyện %q đang mở rồi — chọn truyện khác để chuyển.", bk.Name)
				return m, nil
			}
			return m.askSwitch(bk.Dir, bk.Name)
		}
	case tea.KeyRunes:
		switch string(msg.Runes) {
		case "d":
			if bk := s.selected(); bk != nil {
				res, err := m.runtime.InspectBook(bk.Dir)
				if err != nil {
					m.booksErr = err.Error()
					return m, nil
				}
				s.pending = &res
				s.mode = booksDeleteConfirm
				m.booksErr = ""
			}
		case "n":
			s.mode = booksNewDraft
			s.draft = ""
		}
	}
	return m, nil
}

// confirmSwitchFromModal chốt việc chuyển từ khung xác nhận: dọn state rồi đi.
// Tách riêng để Enter và 'y' dùng chung một đường, khỏi lệch nhau lần nữa.
func (m Model) confirmSwitchFromModal() (tea.Model, tea.Cmd) {
	dir := m.books.target
	m.books.mode, m.books.target, m.books.targetName, m.books.switchWhy = booksList, "", "", ""
	m.books = nil
	return m.doSwitch(dir)
}

func renderBooksError(m Model, msg string) (tea.Model, tea.Cmd) {
	m.applyEvent(host.Event{Time: timeNow(), Category: "ERROR", Summary: msg, Level: "error"})
	m.refreshEventViewport()
	return m, nil
}

// renderBooksNotice báo tin thường, không phải lỗi — dùng cho "đã chuyển sang",
// "đang mở rồi". Cùng hàm renderBooksError sẽ tô đỏ và làm người dùng tưởng hỏng.
func renderBooksNotice(m Model, msg string) (tea.Model, tea.Cmd) {
	m.applyEvent(host.Event{Time: timeNow(), Category: "SYSTEM", Summary: msg, Level: "info"})
	m.refreshEventViewport()
	return m, nil
}

// openBooks mở khung quản lý truyện và nạp danh sách từ đĩa.
func openBooks(m Model, mode booksMode) (tea.Model, tea.Cmd) {
	s := newBooksState(m.width, m.height, mode)
	// Phòng khi runtime chưa có (test, hoặc đường khởi động lạ): mở khung rỗng
	// kèm thông báo thay vì panic nil pointer.
	if m.runtime == nil {
		m.books, m.booksErr = s, i18n.T("Chưa mở truyện nào để liệt kê.")
		m.textarea.Blur()
		return m, nil
	}
	if books, err := m.runtime.Books(); err == nil {
		s.list = books
		// Mặc định trỏ vào truyện đang mở để không lỡ bấm d là xoá nhầm.
		cur := m.outputDir()
		for i := range s.list {
			if sameDir(s.list[i].Dir, cur) {
				s.cursor = i
				break
			}
		}
	} else {
		m.booksErr = err.Error()
	}
	m.books, m.booksErr = s, ""
	if mode != booksList {
		m.booksErr = ""
	}
	m.textarea.Blur()
	return m, nil
}

func sameDir(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	aa, err1 := filepath.Abs(a)
	bb, err2 := filepath.Abs(b)
	return err1 == nil && err2 == nil && aa == bb
}

// deleteConfirmedBook gọi xoá thật. Chỉ tới đây sau khi người dùng đã gõ "y".
func (m Model) deleteConfirmedBook() (tea.Model, tea.Cmd) {
	if m.books == nil || m.books.pending == nil {
		return m, nil
	}
	p := *m.books.pending
	if err := m.runtime.DeleteBook(p.Dir); err != nil {
		return renderBooksError(m, err.Error())
	}
	m.books.pending = nil
	m.books.mode = booksList
	m.books.cursor = 0
	if books, err := m.runtime.Books(); err == nil {
		m.books.list = books
	}
	return renderBooksError(m, i18n.Tf("Đã xoá truyện %q (%d chương).", p.Name, p.Chapters))
}

// resolveNewBookName tách tên người gõ thành (slug thư mục, tên hiển thị, tự động).
// Draft trống nghĩa là "đặt ngẫu nhiên cho tôi": slug ngẫu nhiên, tiêu đề để
// trống để UI hiện "Chưa đặt tên" nhắc đổi sau bằng /rename.
func resolveNewBookName(raw string) (slug, title string, auto bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return randomBookSlug(), "", true
	}
	slug = sanitizeBookName(raw)
	if slug == "" {
		return "", "", false
	}
	return slug, raw, false
}

// randomBookSlug sinh "truyen-xxxx" (4 ký tự a-z0-9, đã sạch để làm thư mục).
// Đụng độ hiếm gặp do vòng lặp ở createBookConfirmed thử lại tên khác.
func randomBookSlug() string {
	const chars = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 4)
	for i := range b {
		b[i] = chars[rand.IntN(len(chars))]
	}
	return "truyen-" + string(b)
}

// createBookConfirmed tạo truyện rồi MỞ LUÔN, không chỉ tạo thư mục rồi đứng ngoài.
// Người dùng gõ /new là muốn bắt đầu viết, không phải muốn tạo rồi ngồi nhìn.
func (m Model) createBookConfirmed() (tea.Model, tea.Cmd) {
	if m.books == nil {
		return m, nil
	}
	if m.runtime == nil {
		return renderBooksError(m, i18n.T("Chưa mở truyện nào để tạo mới."))
	}
	slug, title, auto := resolveNewBookName(m.books.draft)
	if slug == "" {
		return renderBooksError(m, i18n.T("Tên truyện phải có ít nhất một chữ cái hoặc số."))
	}
	var (
		dir string
		err error
	)
	if auto {
		// Tên ngẫu nhiên có thể đụng truyện cũ (hiếm): thử tên khác thay vì báo lỗi.
		for i := 0; i < 10; i++ {
			if i > 0 {
				slug = randomBookSlug()
			}
			dir, err = m.runtime.NewBookDir(slug)
			if err == nil {
				break
			}
			if !errors.Is(err, host.ErrBookExists) {
				break
			}
		}
	} else {
		dir, err = m.runtime.NewBookDir(slug)
	}
	if err != nil {
		return renderBooksError(m, err.Error())
	}
	// Ghi tên hiển thị (có dấu) vào book.json ngay, vì tên thư mục đã bị flatten.
	// Truyện tự động để trống tiêu đề: UI hiện "Chưa đặt tên" nhắc đổi sau.
	if err := writeBookTitle(dir, title); err != nil {
		return renderBooksError(m, i18n.Tf("Tạo được thư mục nhưng ghi tên hỏng: %v", err))
	}
	// Trước khi chuyển, đóng modal để không giữ trạng thái của khung cũ.
	m.books = nil
	m.textarea.Blur()
	display := title
	if display == "" {
		display = slug
	}
	return m.askSwitch(dir, display)
}

// writeBookTitle ghi tên hiển thị vào meta/book.json, giữ nguyên dấu tiếng Việt.
// Tên thư mục phải phẳng để dễ dùng, nhưng tên hiển thị thì không nên mất dấu —
// đó là cái người dùng gõ và cũng là cái họ muốn đọc lại sau này.
func writeBookTitle(dir, title string) error {
	path := filepath.Join(dir, "meta", "book.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	// Không ghi đè file đã có: book.json có thể đang chứa nhiều trường khác.
	if data, err := os.ReadFile(path); err == nil {
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			return err
		}
		m["title"] = title
		out, err := json.MarshalIndent(m, "", "  ")
		if err != nil {
			return err
		}
		return os.WriteFile(path, out, 0o644)
	}
	out, err := json.MarshalIndent(map[string]any{"title": title}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, out, 0o644)
}
