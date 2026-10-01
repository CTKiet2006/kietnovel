package tui

import (
	"errors"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/CTKiet2006/kietnovel/internal/host"
	"github.com/CTKiet2006/kietnovel/internal/i18n"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// errAlreadyOpen báo người dùng bấm Enter trên đúng truyện đang mở.
var errAlreadyOpen = errors.New("already-open")

// sanitizeBookName đổi tên người dùng gõ thành tên thư mục an toàn trên Windows.
//
// Vì sao không dùng nguyên chuỗi: tên có dấu, có khoảng trắng thì vẫn tạo được
// thư mục trên Windows, nhưng sẽ vỡ khi người dùng gõ NOVEL_DIR trong PowerShell,
// và rắc rối khi sao lưu. Tên hiển thị vẫn giữ nguyên có dấu vì lấy từ book.json,
// còn tên thư mục thì phẳng cho dễ dùng.
// foldViet bỏ dấu tiếng Việt thành chữ cái gốc. Bảng tra cứu tường minh thay vì
// dùng unicode/norm: dùng norm sẽ đúng hơn cho nhiều script hơn, nhưng ở đây
// chỉ cần tiếng Việt, mà bảng tra cho ta thấy đúng những gì mình hỗ trợ — không có
// ký tự nào rơi ra ngoài bảng mà ta tưởng đã xử lý.
var foldViet = map[rune]string{
	'đ': "d", 'Đ': "D",
	'á': "a", 'à': "a", 'ả': "a", 'ã': "a", 'ạ': "a",
	'â': "a", 'ấ': "a", 'ầ': "a", 'ẩ': "a", 'ẫ': "a", 'ậ': "a", 'ă': "a", 'ắ': "a", 'ằ': "a", 'ẳ': "a", 'ẵ': "a", 'ặ': "a",
	'é': "e", 'è': "e", 'ẻ': "e", 'ẽ': "e", 'ẹ': "e",
	'ê': "e", 'ế': "e", 'ề': "e", 'ể': "e", 'ễ': "e", 'ệ': "e",
	'í': "i", 'ì': "i", 'ỉ': "i", 'ĩ': "i", 'ị': "i",
	'ó': "o", 'ò': "o", 'ỏ': "o", 'õ': "o", 'ọ': "o",
	'ô': "o", 'ố': "o", 'ồ': "o", 'ổ': "o", 'ỗ': "o", 'ộ': "o",
	'ơ': "o", 'ớ': "o", 'ờ': "o", 'ở': "o", 'ỡ': "o", 'ợ': "o",
	'ú': "u", 'ù': "u", 'ủ': "u", 'ũ': "u", 'ụ': "u",
	'ư': "u", 'ứ': "u", 'ừ': "u", 'ử': "u", 'ữ': "u", 'ự': "u",
	'ý': "y", 'ỳ': "y", 'ỷ': "y", 'ỹ': "y", 'ỵ': "y",
}

func sanitizeBookName(raw string) string {
	var b strings.Builder
	lastDash := false
	// write chỉ nhận chữ thường và số. Hạ chữ thường ở đây một lần cho cả hai
	// đường vào: nhánh ASCII và nhánh foldViet. Nếu không, 'Đ' -> "D" sẽ bị
	// write loại vì "D" hoa, và "Đêm" ra thành "em" — mất chữ đầu tiên.
	write := func(s string) {
		for _, r := range strings.ToLower(s) {
			if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
				b.WriteRune(r)
				lastDash = false
			}
		}
	}
	for _, r := range strings.TrimSpace(raw) {
		switch {
		case r == ' ' || r == '-' || r == '_':
			// Gộp mọi khoảng trắng/dấu gạch thành một gạch ngang duy nhất.
			if !lastDash && b.Len() > 0 {
				b.WriteRune('-')
				lastDash = true
			}
		case r < utf8.RuneSelf:
			// Chữ ASCII: hạ chữ thường rồi lọc. 'Đ' là ngoại lệ — nó là chữ
			// Việt có dấu, không phải ASCII, nên đã đi vào nhánh foldViet ở dưới.
			write(strings.ToLower(string(r)))
		default:
			// Ký tự có dấu: bỏ dấu trước, rồi mới lọc chữ/số.
			if f, ok := foldViet[r]; ok {
				write(f)
			} else {
				// Ký tự ngoài tiếng Việt (Hán, Nôm, emoji): bỏ hẳn, không đoán.
				if lastDash && b.Len() > 0 {
					b.WriteRune('-')
				}
				lastDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "truyen-moi"
	}
	return out
}

// baseOf lùy 2 cấp từ <base>/output/<tên> để ra <base>.
func baseOf(dir string) string {
	return filepath.Dir(filepath.Dir(dir))
}

// askSwitch là bước giữa của luồng chuyển truyện: chỉ hỏi khi truyện đang mở còn
// việc dở. Không có việc dở thì chuyển thẳng, hỏi luôn chỉ làm mất công.
// runtime nil (đường khởi động lạ) thì báo lỗi thay vì panic nil pointer.
func (m Model) askSwitch(dir, name string) (Model, tea.Cmd) {
	if m.runtime == nil {
		m.booksErr = i18n.T("Chưa mở truyện nào để chuyển đi.")
		m.textarea.Focus()
		return m, nil
	}
	if sameDir(m.runtime.Dir(), dir) {
		out, _ := renderBooksNotice(m, i18n.Tf("Truyện %q đang mở rồi.", name))
		return out.(Model), nil
	}
	if busy, why := m.hasUncommittedWork(); busy {
		return m.enterSwitchConfirm(dir, name, why), nil
	}
	return m.doSwitch(dir)
}

// enterSwitchConfirm đặt khung xác nhận chuyển truyện.
//
// Tách riêng vì: createBookConfirmed đóng modal (m.books = nil) rồi mới gọi hỏi,
// nên hàm này KHÔNG được giả định state còn sống. Panic nil pointer ở
// switch_confirm.go:103 chính là s.mode khi s == nil. Dựng lại và nạp danh sách,
// để bấm Esc còn có khung để quay về.
func (m Model) enterSwitchConfirm(dir, name, why string) Model {
	if m.books == nil {
		m.books = newBooksState(m.width, m.height, booksList)
		if m.runtime != nil {
			if books, err := m.runtime.Books(); err == nil {
				m.books.list = books
			}
		}
	}
	s := m.books
	s.mode = booksSwitchConfirm
	s.target = dir
	s.targetName = name
	s.switchWhy = why
	return m
}

// doSwitch thực hiện chuyển và báo kết quả.
// Thất bại thì MỞ LẠI modal /books kèm lỗi trong booksErr (thấy ngay trong
// khung, không chìm trong luồng sự kiện) và refocus ô nhập — trước đây trả về
// model cũ vẫn blurred khiến toàn bộ bàn phím chết mà màn hình trông bình thường.
func (m Model) doSwitch(dir string) (Model, tea.Cmd) {
	next, cmd, err := m.switchBook(dir)
	if err != nil {
		msg := i18n.Tf("Không mở được truyện: %v", err)
		if errors.Is(err, errAlreadyOpen) {
			msg = i18n.T("Truyện này đang mở rồi.")
		}
		if m.books == nil {
			m.books = newBooksState(m.width, m.height, booksList)
			if m.runtime != nil {
				if books, err := m.runtime.Books(); err == nil {
					m.books.list = books
				}
			}
		}
		m.booksErr = msg + " " + i18n.T("Xem chi tiết trong last-error.log.")
		m.textarea.Focus()
		return m, nil
	}
	// Báo trước rồi mới vẽ, để dòng sự kiện không bị Modal che mất.
	next.applyEvent(host.Event{
		Time:     timeNow(),
		Category: "SYSTEM",
		Level:    "ok",
		Summary:  i18n.Tf("Đã chuyển sang truyện %q.", filepath.Base(dir)),
	})
	next.refreshEventViewport()
	return next, cmd
}

// renderSwitchConfirm nói rõ điều gì sắp mất và điều gì được giữ, để người dùng
// quyết có cần hỏi thêm không.
func (s *booksState) renderSwitchConfirm() string {
	warn := lipgloss.NewStyle().Foreground(colorAccent).Bold(true)
	body := lipgloss.NewStyle().Foreground(bodyTextColor)
	dim := lipgloss.NewStyle().Foreground(colorDim)
	good := lipgloss.NewStyle().Foreground(colorSuccess)

	var b strings.Builder
	b.WriteString(warn.Render(i18n.T("CHUYỂN TRUYỆN — CÓ VIỆC ĐANG DỞ")))
	b.WriteString("\n\n")
	b.WriteString(body.Render(i18n.Tf("Mở: %s", s.targetName)))
	b.WriteString("\n")
	b.WriteString(dim.Render(s.target))
	b.WriteString("\n\n")
	if s.switchWhy != "" {
		b.WriteString(body.Render(i18n.Tf("Truyện hiện tại: %s.", s.switchWhy)))
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(good.Render(i18n.T("Truyện này vẫn nằm nguyên trên đĩa, không mất.")))
	b.WriteString("\n")
	b.WriteString(warn.Render(i18n.T("Nhưng phần đang chạy dở sẽ bị dừng. Enter hoặc gõ y để chuyển, Esc để ở lại.")))
	return b.String()
}
