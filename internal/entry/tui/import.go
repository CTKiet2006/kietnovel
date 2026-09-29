package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/CTKiet2006/kietnovel/internal/host"
	"github.com/CTKiet2006/kietnovel/internal/host/imp"
)

// importState là trạng thái modal trong lúc lệnh /import chạy.
//
// Modal tạo khi nhập bắt đầu, tiến theo dòng sự kiện; xong hoặc lỗi thì ở lại màn hình chờ user Esc đóng.
// Esc lúc đang chạy sẽ hủy (ctx.Cancel), để runner chốt ở điểm sự kiện tiếp theo.
type importState struct {
	reqID      int
	source     string
	stage      imp.Stage
	current    int
	total      int
	startedAt  time.Time
	finishedAt time.Time
	history    []importLine
	totalLines int // Tổng số dòng log (history chạm importHistoryMax rồi vẫn đếm tiếp)
	err        error
	done       bool // Trạng thái cuối (xong/lỗi)
	paused     bool // Pipeline dừng ở điểm awaiting, kênh sự kiện đã đóng: panel đóng được, chưa phải trạng thái cuối
	frame      int  // Khung đồng bộ animation chính: sao đuôi và đếm ngược dựa vào tick này để tính lại
	cancel     context.CancelFunc
	viewport   viewport.Model
}

type importLine struct {
	at      time.Time
	stage   imp.Stage
	current int
	total   int
	message string
	level   string    // "warn" cảnh báo thử lại/backoff
	key     string    // Khác rỗng thì các dòng liên tiếp cùng key cập nhật tại chỗ (khớp cơ chế ID panel sự kiện)
	retryAt time.Time // Khác zero = hạn lần thử lại tiếp theo, lúc vẽ tính số giây còn lại thành đếm ngược
	err     error

	rendered  string // Kết quả vẽ cache theo renderedW; lịch sử tới nghìn dòng, mỗi tick vẽ lại hết sẽ treo panel
	renderedW int
}

// importHistoryMax là trần dòng log giữ trong RAM panel: sách nghìn chương mỗi chương dội về + từng chương xuất bản
// sẽ phình không giới hạn, vừa tốn RAM vừa chậm vẽ lại. File log (logs/import.log) luôn giữ đủ bản ghi.
const importHistoryMax = 1000

func newImportState(reqID int, source string, width, height int, cancel context.CancelFunc) *importState {
	boxW, boxH := reportModalSize(width, height)
	contentW := paddedModalContentWidth(boxW)
	vp := viewport.New(contentW, boxH-4)
	s := &importState{
		reqID:     reqID,
		source:    source,
		startedAt: time.Now(),
		stage:     imp.StageIngesting,
		cancel:    cancel,
		viewport:  vp,
	}
	s.refresh(contentW)
	return s
}

func (s *importState) appendEvent(ev imp.Event, contentW int) {
	s.stage = ev.Stage
	s.current = ev.Current
	s.total = ev.Total
	if ev.Err != nil {
		s.err = ev.Err
	}
	line := importLine{
		at: ev.Time, stage: ev.Stage, current: ev.Current, total: ev.Total,
		message: ev.Message, level: ev.Level, key: ev.Key, retryAt: ev.RetryAt, err: ev.Err,
	}
	// Cùng Key và kề nhau → cập nhật tại chỗ (7 lần backoff nhảy trên một dòng); bị dòng tiến độ khác chen giữa thì mở dòng mới, giữ thứ tự thời gian.
	if ev.Key != "" && len(s.history) > 0 && s.history[len(s.history)-1].key == ev.Key {
		s.history[len(s.history)-1] = line
	} else {
		s.totalLines++
		s.history = append(s.history, line)
		if len(s.history) > importHistoryMax {
			s.history = append(s.history[:0], s.history[len(s.history)-importHistoryMax:]...)
		}
	}
	if ev.Stage == imp.StageDone || ev.Stage == imp.StageError {
		s.done = true
		s.finishedAt = ev.Time
	}
	s.refresh(contentW)
}

func (s *importState) refresh(contentW int) {
	titleStyle := lipgloss.NewStyle().Foreground(colorAccent).Bold(true)
	dimStyle := lipgloss.NewStyle().Foreground(colorDim)
	mutedStyle := lipgloss.NewStyle().Foreground(colorMuted)
	okStyle := lipgloss.NewStyle().Foreground(colorSuccess)
	errStyle := lipgloss.NewStyle().Foreground(colorError)
	stageStyle := lipgloss.NewStyle().Foreground(colorAccent2)

	var b strings.Builder
	b.WriteString(titleStyle.Render("Nhập truyện ngoài"))
	b.WriteString("\n\n")
	b.WriteString(dimStyle.Render("File nguồn "))
	b.WriteString(s.source)
	b.WriteString("\n")
	b.WriteString(dimStyle.Render("Bắt đầu "))
	b.WriteString(formatReportTime(s.startedAt))
	if !s.finishedAt.IsZero() {
		b.WriteString(dimStyle.Render("  Xong "))
		b.WriteString(formatReportTime(s.finishedAt))
	}
	b.WriteString("\n\n")

	// Dòng giai đoạn hiện tại
	b.WriteString(mutedStyle.Render("Giai đoạn "))
	b.WriteString(stageStyle.Render(string(s.stage)))
	if s.total > 0 {
		b.WriteString(mutedStyle.Render("  Tiến độ "))
		if s.current > 0 {
			b.WriteString(fmt.Sprintf("%d/%d", s.current, s.total))
		} else {
			b.WriteString(fmt.Sprintf("0/%d", s.total))
		}
	}
	b.WriteString("\n\n")

	// Nhật ký lịch sử. Mỗi dòng một cột icon ngữ nghĩa (khớp dáng panel sự kiện):
	// ✗ đỏ=thất bại · ↻ cam=backoff thử lại/kiểm tra hỏi lại (cùng key nhảy tại chỗ) · ✓ xanh lá=xong · · xám=tiến độ thường.
	b.WriteString(titleStyle.Render("Nhật ký chạy"))
	b.WriteString(" ")
	if s.totalLines > len(s.history) {
		b.WriteString(dimStyle.Render(fmt.Sprintf("(%d dòng, chỉ hiện %d gần nhất, đủ ở logs/import.log)", s.totalLines, len(s.history))))
	} else {
		b.WriteString(dimStyle.Render(fmt.Sprintf("(%d dòng)", s.totalLines)))
	}
	b.WriteString("\n")
	now := time.Now()
	for i := range s.history {
		ln := &s.history[i]
		// Dòng đã chốt cache kết quả vẽ theo rộng: refresh chạy mỗi tick animation, lịch sử nghìn dòng mà
		// vẽ lại hết (wrapText+vẽ màu từng dòng) tốn bậc hai, giai đoạn publish sẽ giật thấy rõ.
		// Chỉ dòng đếm ngược còn sống mới tính lại mỗi tick (quá hạn tính thêm 2s để xóa nhãn).
		live := !ln.retryAt.IsZero() && now.Before(ln.retryAt.Add(2*time.Second))
		if ln.rendered == "" || ln.renderedW != contentW || live {
			ln.rendered = renderImportLine(*ln, contentW, now)
			ln.renderedW = contentW
		}
		b.WriteString("\n")
		b.WriteString(ln.rendered)
	}

	running := !s.done && !s.paused
	if running {
		// Con trỏ đuôi: một sao kiểu panel stream theo sau dòng log cuối, nhảy theo từng khung animation chính,
		// khớp với dòng chỉ báo "đang chạy" trên đỉnh — cuối log có nó, lúc chờ backoff cũng thấy ngay pipeline còn sống.
		b.WriteString("\n\n")
		b.WriteString(lipgloss.NewStyle().Foreground(colorAccent).Bold(true).
			Render(streamCursorFrames[s.frame%len(streamCursorFrames)]))
	}

	// Gợi ý chốt
	b.WriteString("\n\n")
	switch {
	case s.err != nil:
		b.WriteString(errStyle.Render("Nhập thất bại"))
		b.WriteString("\n")
		b.WriteString(dimStyle.Render("Esc đóng panel"))
	case s.paused && s.stage == imp.StageAwaitingConfirmation:
		b.WriteString(okStyle.Render("Cắt chương xong, chờ bạn đối chiếu"))
		b.WriteString("\n")
		b.WriteString(dimStyle.Render("y chốt cắt chương và chạy tiếp; muốn chỉnh thì Esc rồi /import --guide=<mô tả bằng lời>; Esc đóng panel"))
	case s.paused:
		// Pipeline dừng ở điểm chờ phán quyết, kênh đã đóng: làm theo gợi ý trong panel rồi Esc đóng.
		b.WriteString(okStyle.Render("Đã tạm dừng nhập, chờ bạn thao tác"))
		b.WriteString("\n")
		b.WriteString(dimStyle.Render("Làm theo hướng dẫn trên để tiếp tục (vd /import --story=open|closed); Esc đóng panel"))
	case s.done:
		b.WriteString(okStyle.Render("Nhập xong, Foundation và chương đã sẵn sàng"))
		b.WriteString("\n")
		b.WriteString(dimStyle.Render("Esc đóng panel và nối cổng viết tiếp (engine dừng ở biên chương sau, chờ bạn duyệt)"))
	default:
		b.WriteString(dimStyle.Render("Esc hủy nhập"))
	}

	// Đuôi chỉ bám khi user đang ở đáy: refresh giờ chạy mỗi tick (animation/đếm ngược),
	// GotoBottom vô điều kiện sẽ lôi user đang đọc lên trên về đáy mỗi 350ms.
	atBottom := s.viewport.AtBottom()
	s.viewport.SetContent(b.String())
	if running && atBottom {
		s.viewport.GotoBottom()
	}
}

// renderImportLine vẽ một dòng log chạy: timestamp + cột icon ngữ nghĩa + giai đoạn (+tiến độ) + nội dung.
// Nội dung xuống dòng theo rộng còn lại sau tiền tố, dòng tiếp canh theo đầu nội dung; quá rộng chỉ xuống dòng không cắt —
// viewport cắt cứng dòng quá rộng, HTTP status/provider/model trong lỗi là căn cứ tra lỗi, cắt đi bằng báo lỗi uổng.
func renderImportLine(ln importLine, contentW int, now time.Time) string {
	dimStyle := lipgloss.NewStyle().Foreground(colorDim)
	mutedStyle := lipgloss.NewStyle().Foreground(colorMuted)
	okStyle := lipgloss.NewStyle().Foreground(colorSuccess)
	errStyle := lipgloss.NewStyle().Foreground(colorError)
	warnStyle := lipgloss.NewStyle().Foreground(colorReview)
	stageStyle := lipgloss.NewStyle().Foreground(colorAccent2)

	var p strings.Builder
	p.WriteString(dimStyle.Render(ln.at.Format("15:04:05")))
	p.WriteString(" ")
	switch {
	case ln.err != nil:
		p.WriteString(errStyle.Bold(true).Render("✗"))
	case ln.level == "warn":
		p.WriteString(warnStyle.Bold(true).Render("↻"))
	case ln.stage == imp.StageDone:
		p.WriteString(okStyle.Bold(true).Render("✓"))
	default:
		p.WriteString(dimStyle.Render("·"))
	}
	p.WriteString(" ")
	p.WriteString(stageStyle.Render(string(ln.stage)))
	if ln.total > 0 && ln.current > 0 {
		p.WriteString(mutedStyle.Render(fmt.Sprintf(" %d/%d", ln.current, ln.total)))
	}
	p.WriteString(" ")
	prefix := p.String()

	var text string
	style := lipgloss.NewStyle()
	switch {
	case ln.err != nil:
		text = ln.message + " — " + ln.err.Error()
		style = errStyle
	case ln.level == "warn":
		text = ln.message
		if cd := retryCountdown(ln.retryAt, now); cd != "" {
			text += " · " + cd
		}
		style = warnStyle
	default:
		text = ln.message
	}
	// Vẽ màu từng dòng rồi tự nối: lipgloss với chuỗi nhiều dòng sẽ đệm mỗi dòng tới bằng dòng rộng nhất trong khối,
	// tiền tố chỉ ở dòng đầu, vẽ cả khối sẽ làm dòng đầu vượt contentW bị viewport cắt.
	prefixW := lipgloss.Width(prefix)
	wrapW := contentW - prefixW
	if wrapW < 20 {
		// Terminal hẹp tiền tố (timestamp+icon+tên giai đoạn dài+tiến độ) đã chiếm quá nửa rộng dòng: nội dung xuống dòng thụt nhẹ,
		// rộng xuống dòng luôn bị chặn bởi contentW — cố theo ngưỡng 20 cột sẽ làm dòng đầu quá rộng bị viewport cắt,
		// lại cắt đúng HTTP status/provider ở đuôi lỗi cần để tra.
		var out strings.Builder
		out.WriteString(prefix)
		for _, l := range strings.Split(wrapText(text, max(10, contentW-4)), "\n") {
			out.WriteString("\n    ")
			out.WriteString(style.Render(l))
		}
		return out.String()
	}
	// Tin nhiều dòng (vd xem trước chốt cắt chương): dòng đầu theo sau tiền tố, các dòng còn lại thụt nhẹ cả khối — nếu canh
	// dòng tiếp theo rộng tiền tố, tiền tố 40+ cột sẽ ép cả khối nội dung sang nửa phải panel, nửa trái trống trơn.
	head, body := text, ""
	if i := strings.IndexByte(text, '\n'); i >= 0 {
		head, body = text[:i], strings.TrimRight(text[i+1:], "\n")
	}
	lines := strings.Split(wrapText(head, wrapW), "\n")
	var out strings.Builder
	out.WriteString(prefix)
	out.WriteString(style.Render(lines[0]))
	pad := strings.Repeat(" ", prefixW)
	for _, l := range lines[1:] {
		out.WriteString("\n")
		out.WriteString(pad)
		out.WriteString(style.Render(l))
	}
	if body != "" {
		for _, l := range strings.Split(wrapText(body, contentW-2), "\n") {
			out.WriteString("\n  ")
			out.WriteString(style.Render(l))
		}
	}
	return out.String()
}

func renderImportModal(width, height int, s *importState, frame int) string {
	if s == nil {
		return ""
	}
	boxW, boxH := reportModalSize(width, height)
	contentW := paddedModalContentWidth(boxW)
	running := !s.done && !s.paused
	if s.viewport.Width != contentW {
		s.viewport.Width = contentW
		s.refresh(contentW)
	}
	vpH := boxH - 4
	if running {
		vpH -= 2 // Dòng chỉ báo hoạt động trên đỉnh + dòng trống
	}
	if s.viewport.Height != vpH {
		s.viewport.Height = vpH
	}

	hint := "  ↑↓ cuộn · Esc hủy/đóng"
	switch {
	case s.paused && s.stage == imp.StageAwaitingConfirmation:
		hint = "  ↑↓ cuộn · y chốt cắt chương · Esc đóng"
	case running:
		hint = "  ↑↓ cuộn · Esc hủy"
	}

	body := strings.Split(s.viewport.View(), "\n")
	if running {
		// Chỉ báo hoạt động lúc đang chạy: một sao kiểu panel stream + giờ đã chạy, cập nhật theo animation chính tần thấp.
		// Treo ở dòng cố định ngoài viewport — nội dung viewport chỉ mới khi có sự kiện, animation để trong đó không nhúc nhích;
		// thiếu nó, lúc gọi model lâu/backoff thử lại panel đứng yên, user tưởng treo.
		star := lipgloss.NewStyle().Foreground(colorAccent).Bold(true).
			Render(streamCursorFrames[frame%len(streamCursorFrames)])
		status := lipgloss.NewStyle().Foreground(colorMuted).
			Render(fmt.Sprintf(" Đang chạy · đã chạy %s", formatElapsed(time.Since(s.startedAt))))
		body = append([]string{star + status, ""}, body...)
	}
	modal := renderPaddedModalFrame(boxW, boxH, "Nhập truyện ngoài", hint, body)
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, modal)
}

// formatElapsed vẽ giờ đã chạy mm:ss (quá 1 giờ lên h:mm:ss).
func formatElapsed(d time.Duration) string {
	d = d.Round(time.Second)
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	sec := int(d.Seconds()) % 60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, sec)
	}
	return fmt.Sprintf("%02d:%02d", m, sec)
}

func (m Model) handleImportKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.importer == nil {
		return m, nil
	}
	switch msg.Type {
	case tea.KeyEsc:
		// Còn chạy (chưa cuối, chưa dừng) → Esc hủy, để runner chốt; đã cuối hoặc đã dừng ở điểm awaiting
		// (kênh đóng) → Esc đóng panel. Thiếu nhánh paused thì sau khi dừng ở awaiting panel không đóng được (treo).
		if !m.importer.done && !m.importer.paused && m.importer.cancel != nil {
			m.importer.cancel()
			return m, nil
		}
		succeeded := m.importer.stage == imp.StageDone && m.importer.err == nil
		m.importer = nil
		// Nhập từ trang chào xong: trang chào không có lối viết tiếp (Resume của bootstrap chỉ chạy
		// một lần lúc khởi động), lúc đóng panel chạy bù khôi phục để user rơi vào cổng Hold nhập xong ở bàn viết,
		// chứ không ở lại trang chào mà Enter bậy là "tạo sách mới".
		if succeeded && m.mode == modeNew {
			return m, tea.Batch(m.textarea.Focus(), resumeBook(m.runtime))
		}
		return m, m.textarea.Focus()
	case tea.KeyUp:
		m.importer.viewport.ScrollUp(1)
	case tea.KeyDown:
		m.importer.viewport.ScrollDown(1)
	case tea.KeyPgUp:
		m.importer.viewport.HalfPageUp()
	case tea.KeyPgDown:
		m.importer.viewport.HalfPageDown()
	case tea.KeyRunes:
		// Chỗ dừng chốt cắt chương bấm y = chạy lại /import --yes tại chỗ (khôi phục không đường dẫn), chốt một lần cắt hiện tại.
		if len(msg.Runes) == 1 && (msg.Runes[0] == 'y' || msg.Runes[0] == 'Y') &&
			m.importer.paused && m.importer.stage == imp.StageAwaitingConfirmation {
			return m.confirmImportSegmentation()
		}
	}
	return m, nil
}

// confirmImportSegmentation gọn "xem trước rồi chốt" thành một phím: chạy lại nhập tại chỗ kèm
// AcceptSegmentation (khôi phục phi trạng thái, pipeline tiếp từ chỗ thiếu confirmation). Khác --yes ở chỗ
// "phán quyết rõ ràng đã xem trước" — cắt có ghi chú dung sai (Notes) thì --yes không cho qua, y cho qua;
// chỉ hiệu lực với Options lần này, không ghi intent.json, cắt mới do --guide cắt lại sau vẫn dừng để đối chiếu.
// Giữ tên file nguồn và log chạy của panel cũ để xem trước chương lúc phân tích tiếp vẫn cuộn lại xem được.
func (m Model) confirmImportSegmentation() (tea.Model, tea.Cmd) {
	prev := m.importer
	m.importSeq++
	state, listenCmd, err := startImportRun(m.runtime, m.importSeq, imp.Options{AcceptSegmentation: true}, m.width, m.height)
	if err != nil {
		m.applyEvent(host.Event{
			Time: time.Now(), Category: "ERROR", Summary: "Chốt cắt chương thất bại: " + err.Error(), Level: "error",
		})
		return m, nil
	}
	state.source = prev.source
	state.history = append([]importLine(nil), prev.history...)
	state.totalLines = prev.totalLines
	boxW, _ := reportModalSize(m.width, m.height)
	state.refresh(paddedModalContentWidth(boxW))
	m.importer = state
	return m, listenCmd
}

// importEventMsg một lần gửi imp.Event.
type importEventMsg struct {
	reqID int
	ev    imp.Event
	ch    <-chan imp.Event // Cùng kênh nghe tiếp dòng sau
}

// importClosedMsg tín hiệu kênh sự kiện đóng (goroutine nhập dừng). Dù dừng ở trạng thái cuối hay điểm awaiting,
// kênh đóng đều nhờ nó báo chắc cho panel biết đóng được, tránh chỉ nhận trạng thái cuối làm panel treo sau khi dừng ở awaiting.
type importClosedMsg struct {
	reqID int
}

// startImport khởi động một lần nhập truyện ngoài: phân tích tham số → tạo modal state → nghe dòng sự kiện.
func startImport(rt *host.Host, reqID int, args []string, width, height int) (*importState, tea.Cmd, error) {
	opts, err := parseImportArgs(args)
	if err != nil {
		return nil, nil, err
	}
	return startImportRun(rt, reqID, opts, width, height)
}

// startImportRun chạy nhập với Options chốt sẵn (y chốt và các lần vào lại nội bộ không qua phân tích tham số).
// width/height để khởi tạo viewport; hàm cancel treo trên state cho Esc hủy.
func startImportRun(rt *host.Host, reqID int, opts imp.Options, width, height int) (*importState, tea.Cmd, error) {
	ctx, cancel := context.WithCancel(context.Background())
	ch, err := rt.ImportFrom(ctx, opts)
	if err != nil {
		cancel()
		return nil, nil, err
	}
	state := newImportState(reqID, opts.SourcePath, width, height, cancel)
	return state, listenImportEvent(reqID, ch), nil
}

func listenImportEvent(reqID int, ch <-chan imp.Event) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return importClosedMsg{reqID: reqID}
		}
		return importEventMsg{reqID: reqID, ev: ev, ch: ch}
	}
}

// parseImportArgs phân tích `/import <path> [--yes] [--story=open|closed] [--continue] [--guide=<mô tả>]`.
// Không tham số coi như "khôi phục từ workspace đang làm", đường dẫn nguồn không bắt buộc để khôi phục (RFC §18).
// --guide là hướng dẫn cắt chương bằng lời, có thể chứa dấu cách: từ --guide= trở đi gom hết vào chữ hướng dẫn, phải để cuối.
func parseImportArgs(args []string) (imp.Options, error) {
	var opts imp.Options
	for i := range args {
		a := args[i]
		switch {
		case a == "--yes":
			opts.AutoConfirm = true
		case a == "--continue":
			opts.ContinueAfter = true
		case strings.HasPrefix(a, "--story="):
			v := strings.TrimPrefix(a, "--story=")
			if v != "open" && v != "closed" {
				return imp.Options{}, fmt.Errorf("--story chỉ là open hoặc closed: %q", v)
			}
			opts.StoryResolution = v
		case strings.HasPrefix(a, "--guide="):
			parts := append([]string{strings.TrimPrefix(a, "--guide=")}, args[i+1:]...)
			g := strings.TrimSpace(strings.Join(parts, " "))
			if g == "" {
				return imp.Options{}, fmt.Errorf("--guide cần hướng dẫn cắt chương bằng lời, vd --guide=đoạn nghỉ·X cũng là chương riêng")
			}
			opts.Guidance = g
			return opts, nil
		case strings.HasPrefix(a, "--"):
			return imp.Options{}, fmt.Errorf("Tùy chọn không rõ %q (hỗ trợ: --yes / --story=open|closed / --continue / --guide=<hướng dẫn cắt chương>)", a)
		default:
			if opts.SourcePath != "" {
				return imp.Options{}, fmt.Errorf("Chỉ nhận một đường dẫn file nguồn: thừa %q", a)
			}
			opts.SourcePath = a
		}
	}
	return opts, nil
}
