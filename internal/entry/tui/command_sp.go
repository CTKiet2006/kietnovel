package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

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

func newStoryPartnerState(w, h int, reqID uint64, question, lang string) *storyPartnerState {
	boxW, boxH := reportModalSize(w, h)
	contentW := paddedModalContentWidth(boxW)
	s := &storyPartnerState{
		reqID: reqID, question: question, lang: lang,
		loading: true, started: time.Now(),
		contentW: contentW, boxH: boxH,
	}
	s.viewport = viewport.New(contentW, boxH-8)
	return s
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
func parseSPArgs(args []string) (mode, question string, ok bool) {
	text := strings.TrimSpace(strings.Join(args, " "))
	if text == "" {
		return "", "", false
	}
	first := firstWord(text)
	if m, found := lookupSubcommand("story_partner", first); found {
		rest := strings.TrimSpace(strings.TrimPrefix(text, first))
		if rest == "" {
			return "", "", false
		}
		return m, rest, true
	}
	return "ask", text, true
}
func runSPCommand(m Model, args []string) (tea.Model, tea.Cmd) {
	if m.runtime == nil {
		return renderBooksError(m, i18n.T("Chưa mở sách nào để hỏi."))
	}
	mode, question, ok := parseSPArgs(args)
	if !ok || mode != "ask" {
		// /sp không question (hoặc subcommand lạ mai sau) → usage, không gọi Host.
		m.applyEvent(host.Event{Time: time.Now(), Category: "SYSTEM", Level: "info",
			Summary: i18n.T("Dùng: /sp [hỏi] <câu hỏi> — hỏi Story Partner mà không dừng máy đang viết.")})
		m.refreshEventViewport()
		return m, nil
	}

	// Capture UI language lúc request bắt đầu: đổi UI giữa chừng thì request đang
	// chạy vẫn đúng ngôn ngữ cũ. Service không tự đọc global trong lúc chạy.
	lang := i18n.Language()
	m.spSeq++
	s := newStoryPartnerState(m.width, m.height, m.spSeq, question, lang)
	m.spState = s
	m.textarea.Blur()
	return m, askStoryPartnerCmd(m.runtime, s.reqID, question, lang)
}

func firstWord(s string) string {
	if f := strings.Fields(s); len(f) > 0 {
		return f[0]
	}
	return ""
}

// askStoryPartnerCmd gọi Host không block TUI. Single-flight nằm ở Host;
// TUI không tạo mutex riêng.
func askStoryPartnerCmd(rt *host.Host, reqID uint64, question, lang string) tea.Cmd {
	return func() tea.Msg {
		res, err := rt.AskStoryPartner(context.Background(), sp.Request{
			Mode: sp.ModeAsk, Question: question, Language: lang,
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
		if m.runtime != nil {
			m.runtime.CancelStoryPartner()
		}
		m.spState = nil
		return m, m.textarea.Focus(), true
	}
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
	b = append(b, dim.Render(i18n.T("Hỏi: ")+s.question))
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
			meta += fmt.Sprintf(" · chương %d", s.chapter)
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
