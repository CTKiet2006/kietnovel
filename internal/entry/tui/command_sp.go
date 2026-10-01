package tui

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/CTKiet2006/kietnovel/internal/host"
	"github.com/CTKiet2006/kietnovel/internal/i18n"
	"github.com/CTKiet2006/kietnovel/internal/sp"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// spResultMsg mang kết quả advisor về UI. reqID để bỏ result cũ: Esc đóng modal
// rồi result mới về thì không được mở lại hay ghi đè state mới (F5).
type spResultMsg struct {
	reqID  uint64
	result sp.Result
	err    error
}

// storyPartnerState là modal /sp hỏi. Overlay trên bàn viết — Engine bên dưới
// vẫn chạy, không pause/abort/steer gì cả.
type storyPartnerState struct {
	reqID    uint64
	mode     string
	question string
	lang     string
	loading  bool
	answer   string
	digest   string
	chapter  int
	provider string
	model    string
	errMsg   string
	started  time.Time

	viewport viewport.Model
	contentW int
	boxH     int
}

func newStoryPartnerState(w, h int, reqID uint64, mode, question, lang string) *storyPartnerState {
	boxW, boxH := reportModalSize(w, h)
	contentW := paddedModalContentWidth(boxW)
	s := &storyPartnerState{
		reqID: reqID, mode: mode, question: question, lang: lang,
		loading: true, started: time.Now(),
		contentW: contentW, boxH: boxH,
	}
	s.viewport = viewport.New(contentW, boxH-8)
	return s
}

// spModeLabel trả nhãn dòng đầu modal theo mode. soi/gợi ý không có question nên
// không thể hiện "Hỏi: " trống — hiện tên mode đã localize thay thế.
func spModeLabel(mode string) string {
	switch mode {
	case "inspect":
		return i18n.T("Soi: ")
	case "suggest":
		return i18n.T("Gợi ý: ")
	default:
		return i18n.T("Hỏi: ")
	}
}

func (s *storyPartnerState) resize(w, h int) {
	boxW, boxH := reportModalSize(w, h)
	s.contentW, s.boxH = paddedModalContentWidth(boxW), boxH
	s.viewport.Width, s.viewport.Height = s.contentW, boxH-8
}

// parseSPArgs tách args thành (subcommand, question, có question).
// "/sp hỏi X" → ("ask", "X", true). "/sp X" (từ đầu không phải subcommand biết)
// → ("ask", toàn bộ, true) — shortcut khỏi nhớ từ hỏi/ask/问.
// "/sp" hoặc "/sp hỏi" không question → ("", "", false) — hiện usage.
// "/sp soi" / "/sp gợi ý" → ("inspect"/"suggest", "", true) — hai mode này tự
// xác định context từ story state, text thừa sau đó được lờ đi có chủ ý
// (quyết định đã chốt: linh hoạt thay vì báo usage).
//
// Khớp longest-match trên cụm từ, KHÔNG chỉ từ đầu tiên: subcommand đa từ như
// "gợi ý" mà chỉ đọc từ đầu ("gợi") sẽ lặng lẽ rơi thành ask — bug.
// Nếu hai mode trùng từ (không được phép, có test chặn), mode sort trước thắng
// để deterministic.
func parseSPArgs(args []string) (mode, question string, ok bool) {
	text := strings.TrimSpace(strings.Join(args, " "))
	if text == "" {
		return "", "", false
	}
	if m, rest, found := matchSubcommandPrefix("story_partner", text); found {
		if m == "ask" {
			if rest == "" {
				return "", "", false
			}
			return m, rest, true
		}
		// inspect/suggest tự xác định context từ story state, không nhận input
		// thêm — text thừa được lờ đi có chủ ý (quyết định đã chốt), không báo usage.
		return m, "", true
	}
	return "ask", text, true
}

// matchSubcommandPrefix khớp cụm subcommand dài nhất ở đầu text.
// Trả (mode, phần còn lại đã trim, true). Không khớp → ("", "", false).
//
// So khớp theo word-boundary từ dài đến ngắn, dùng EqualFold (không phân biệt
// hoa thường) qua lookupSubcommand. Không dùng strings.HasPrefix trực tiếp vì
// nó phân biệt hoa thường ("HỎI x?" sẽ trượt).
func matchSubcommandPrefix(commandID, text string) (string, string, bool) {
	// Thu thập độ dài (số từ) tối đa của các cụm trong catalog để giới hạn vòng lặp.
	maxWords := 1
	if modes, ok := subcommandCatalog[commandID]; ok {
		for _, words := range modes {
			for _, w := range words {
				if n := len(strings.Fields(w)); n > maxWords {
					maxWords = n
				}
			}
		}
	}
	fields := strings.Fields(text)
	// Thử từ cụm dài nhất xuống 1 từ: ưu tiên "gợi ý" trước "gợi" (nếu sau này
	// có từ đơn trùng tiền tố).
	for n := maxWords; n >= 1; n-- {
		if len(fields) < n {
			continue
		}
		// Byte offset sau từ thứ n: đi qua text gốc để giữ nguyên spacing phần còn lại.
		end := wordEndOffset(text, n)
		if mode, ok := lookupSubcommand(commandID, text[:end]); ok {
			return mode, strings.TrimSpace(text[end:]), true
		}
	}
	return "", "", false
}

// wordEndOffset trả byte offset ngay sau từ thứ n (1-indexed) trong text.
// Text đã trim nên từ đầu tiên bắt đầu ở 0. Dùng unicode.IsSpace để khớp đúng
// định nghĩa "từ" của strings.Fields (cả Unicode space như NBSP) — nếu không,
// text chứa NBSP sẽ lệch offset và lookup trượt oan.
func wordEndOffset(text string, n int) int {
	i, count := 0, 0
	for i < len(text) {
		// Bỏ whitespace.
		for i < len(text) {
			r, size := utf8.DecodeRuneInString(text[i:])
			if !unicode.IsSpace(r) {
				break
			}
			i += size
		}
		if i >= len(text) {
			break
		}
		// Qua một từ (đoạn non-whitespace), nhảy theo rune để không chẻ đôi
		// ký tự có dấu.
		for i < len(text) {
			r, size := utf8.DecodeRuneInString(text[i:])
			if unicode.IsSpace(r) {
				break
			}
			i += size
		}
		count++
		if count == n {
			return i
		}
	}
	return len(text)
}
func runSPCommand(m Model, args []string) (tea.Model, tea.Cmd) {
	if m.runtime == nil {
		return renderBooksError(m, i18n.T("Chưa mở sách nào để hỏi."))
	}
	mode, question, ok := parseSPArgs(args)
	if !ok || (mode != "ask" && mode != "inspect" && mode != "suggest") {
		// /sp không question (hoặc subcommand lạ mai sau) → usage, không gọi Host.
		m.applyEvent(host.Event{Time: time.Now(), Category: "SYSTEM", Level: "info",
			Summary: i18n.T("Dùng: /sp [hỏi] <câu hỏi> | /sp soi | /sp gợi ý — hỏi Story Partner mà không dừng máy đang viết.")})
		m.refreshEventViewport()
		return m, nil
	}

	// Capture UI language lúc request bắt đầu: đổi UI giữa chừng thì request đang
	// chạy vẫn đúng ngôn ngữ cũ. Service không tự đọc global trong lúc chạy.
	lang := i18n.Language()
	m.spSeq++
	s := newStoryPartnerState(m.width, m.height, m.spSeq, mode, question, lang)
	m.spState = s
	m.textarea.Blur()
	return m, askStoryPartnerCmd(m.runtime, s.reqID, sp.Mode(mode), question, lang)
}

// askStoryPartnerCmd gọi Host không block TUI. Single-flight nằm ở Host;
// TUI không tạo mutex riêng.
func askStoryPartnerCmd(rt *host.Host, reqID uint64, mode sp.Mode, question, lang string) tea.Cmd {
	return func() tea.Msg {
		res, err := rt.AskStoryPartner(context.Background(), sp.Request{
			Mode: mode, Question: question, Language: lang,
		})
		return spResultMsg{reqID: reqID, result: res, err: err}
	}
}

// handleSPResultMsg nhận kết quả. Stale (modal đã đóng hoặc reqID khác) thì bỏ.
func (m Model) handleSPResultMsg(msg spResultMsg) (tea.Model, tea.Cmd) {
	s := m.spState
	if s == nil || s.reqID != msg.reqID {
		return m, nil
	}
	s.loading = false
	if msg.err != nil {
		s.errMsg = msg.err.Error()
	} else {
		s.answer = msg.result.Answer
		s.digest = msg.result.SnapshotDigest
		s.chapter = msg.result.Chapter
		s.provider = msg.result.Provider
		s.model = msg.result.Model
		s.viewport.SetContent(s.answer)
		s.viewport.GotoTop()
	}
	return m, nil
}

func (m Model) handleSPKey(msg tea.KeyMsg) (tea.Model, tea.Cmd, bool) {
	if m.spState == nil {
		return m, nil, false
	}
	switch msg.Type {
	case tea.KeyEsc:
		// Esc: hủy request rồi đóng modal NGAY, không đợi model.
		m.quitPending = false
		if m.runtime != nil {
			m.runtime.CancelStoryPartner()
		}
		m.spState = nil
		return m, m.textarea.Focus(), true
	case tea.KeyCtrlC:
		// Thoát 2 lần như mọi modal khác (handleBlockingModalKey): không có
		// nhánh này thì mở /sp rồi bấm Ctrl+C không tác dụng, phải Esc trước.
		if m.quitPending {
			return m, tea.Quit, true
		}
		m.quitPending = true
		return m, tea.Tick(time.Second, func(time.Time) tea.Msg { return quitResetMsg{} }), true
	case tea.KeyCtrlR:
		next, cmd := m.toggleMouseReporting()
		return next, cmd, true
	}
	m.quitPending = false
	// Viewport cuộn bằng phím thường của viewport.
	var cmd tea.Cmd
	m.spState.viewport, cmd = m.spState.viewport.Update(msg)
	return m, cmd, true
}

func (s *storyPartnerState) view(w, h int) string {
	s.resize(w, h)
	title := i18n.T("Story Partner")
	hint := i18n.T("  Cuộn: ↑↓ · Đóng: Esc")
	dim := lipgloss.NewStyle().Foreground(colorDim)
	accent := lipgloss.NewStyle().Foreground(colorAccent).Bold(true)

	var b []string
	b = append(b, dim.Render(spModeLabel(s.mode)+s.question))
	switch {
	case s.loading:
		b = append(b, accent.Render(i18n.T("Story Partner đang nghĩ… (máy viết bên dưới vẫn chạy)")))
	case s.errMsg != "":
		b = append(b, lipgloss.NewStyle().Foreground(colorError).Render("! "+s.errMsg))
	default:
		b = append(b, s.viewport.View())
		b = append(b, "")
		meta := fmt.Sprintf("digest %s · %s %s",
			shortDigest(s.digest), s.provider, s.model)
		if s.chapter > 0 {
			meta += i18n.Tf(" · chương %d", s.chapter)
		}
		b = append(b, dim.Render(meta))
	}

	modal := renderPaddedModalFrame(s.contentW+2, s.boxH, title, hint, b)
	return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, modal)
}

func shortDigest(d string) string {
	if len(d) > 12 {
		return d[:12]
	}
	return d
}
