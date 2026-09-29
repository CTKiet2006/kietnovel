package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/CTKiet2006/kietnovel/internal/host"
	"github.com/CTKiet2006/kietnovel/internal/utils"
)

const maxEvents = 500

// maxStreamRounds giới hạn số vòng panel stream giữ. Mỗi LLM call xong kích một lần streamClear
// mở vòng mới, một chương writer khoảng 3~5 vòng (agent header / suy nghĩ / draft / commit), 32 vòng tương đương
// xem lại output stream của 6~10 chương gần nhất. Chữ chương đã commit nằm đĩa ở store/drafts, thừa thì bỏ để khỏi
// mỗi token delta kích vẽ lại O(cả văn bản). Trần RAM ổn định khoảng 512KB, thấp xa ngưỡng giật.
const maxStreamRounds = 32

type focusPane int

const (
	focusEvents focusPane = iota
	focusStream
	focusDetail
	focusState // Cột trạng thái trái (cuộn được)

	focusPaneCount // Tổng số focus, Tab xoay vòng
)

type appMode int

const (
	modeNew     appMode = iota // Chờ user nhập nhu cầu truyện
	modeRunning                // Đang viết (kể cả dừng do lỗi, nhập để tiếp tục)
	modeDone                   // Đã viết xong
)

// Chuỗi khung spinner dùng chung top bar / hoạt động stream (bubbles.Spinner.MiniDot).
var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// Chuỗi khung spinner riêng cho dòng "đang chạy" của dòng sự kiện (bubbles.Spinner.Dot).
// 7 chấm + 1 khuyết xoay chiều kim đồng hồ trên lưới 3×3, nhìn như vòng tải tròn đủ.
// Dùng index khung riêng + tick nhanh hơn, không ảnh hưởng nhịp top bar và animation sao.
var eventSpinnerFrames = []string{"⣾", "⣽", "⣻", "⢿", "⡿", "⣟", "⣯", "⣷"}

// Model là trạng thái đỉnh của TUI.
type Model struct {
	runtime            *host.Host
	cocreate           *cocreateState
	help               *helpState
	modelSwitch        *modelSwitchState
	modelConfig        *modelConfigState
	report             *reportState
	version            string
	importer           *importState
	importSeq          int
	simulator          *simulationState
	simSeq             int
	compItems          []commandPaletteItem
	compIdx            int
	compActive         bool
	commandToken       string // Token lệnh đã đăng ký hiện tại; chỉ vẽ đoạn này, không nhuộm tham số
	snapshot           host.UISnapshot
	events             []host.Event
	eventIndex         map[string]int   // event.ID → chỉ số m.events; sự kiện dạng gọi tới thì cập nhật tại chỗ
	viewport           viewport.Model   // viewport dòng sự kiện
	streamVP           viewport.Model   // viewport output stream
	detailVP           viewport.Model   // viewport chi tiết phải
	stateVP            viewport.Model   // viewport cột trạng thái trái (cuộn được)
	streamBuf          *strings.Builder // Bộ đệm dồn chữ stream
	streamRounds       []string
	textarea           textarea.Model
	width              int
	height             int
	autoScroll         bool
	streamScroll       bool      // Panel stream tự bám theo
	streamDirty        bool      // streamRounds có delta chưa xả
	flushPending       bool      // Đã đặt một lần xả stream, tránh mỗi delta mở lại timer
	lastKeyAt          time.Time // Giờ phím thường gần nhất; tiết lưu KeyEnter chống \n dán bậy kích gửi
	inputHistory       []string  // Lịch sử nhập đã gửi (khử trùng: kề nhau không lặp)
	historyIdx         int       // Chỉ số đang duyệt; == len(inputHistory) là "chưa duyệt, đang sửa nháp"
	historyDraft       string    // Nháp giữ lúc vào duyệt lịch sử, về cuối thì hồi
	focusPane          focusPane
	hoverPane          focusPane
	hoverActive        bool
	mode               appMode
	starting           bool // UI đã vào bàn viết, Host đang chạy khởi tạo khởi động
	startupMode        startupMode
	importHint         string // Gợi ý nhập dở phát hiện lúc khởi động (hiện ở màn chào; đã vào nhập thì xóa)
	updateHint         string // Gợi ý có bản mới từ kiểm tra lúc khởi động (hiện ở màn chào và dòng sự kiện)
	disableUpdateCheck bool   // Cấu hình tắt kiểm tra bản mới lúc khởi động (bootstrap.Config.DisableUpdateCheck)
	cocreateSeq        int
	reportSeq          int
	err                error
	spinnerIdx         int
	eventSpinnerIdx    int  // Index khung riêng cho dòng đang chạy của dòng sự kiện (tick 150ms, không ảnh hưởng top bar/sao)
	eventSpinnerActive bool // Đã bật timer animation sự kiện; không có sự kiện chạy thì tự dừng
	cursorIdx          int  // Index khung con trỏ stream (tiến theo animation chính)
	streamRound        int  // Đếm vòng output stream
	quitPending        bool // Xác nhận thoát bằng hai lần Ctrl+C
	abortPending       bool // Tạm dừng tay chờ Done về
	mouseOff           bool // true là đã tắt báo cáo chuột, để user kéo chuột bôi đen copy nguyên bản; bật lại thì hồi
}

// NewModel tạo Model TUI.
func NewModel(rt *host.Host, version string) Model {
	ta := textarea.New()
	ta.Placeholder = placeholderForNewMode(startupModeQuick)
	ta.CharLimit = 5000
	ta.SetHeight(1)
	// MaxHeight=6 để nhập siêu dài tự wrap thành nhiều dòng theo rộng (trần nhìn 6 dòng).
	ta.MaxHeight = 6
	ta.ShowLineNumbers = false
	ta.Focus()

	// Mặc định Enter không xuống dòng (do handleEnterKey gửi);
	// xuống dòng chủ động gắn lại vào ctrl+j (unix \n) và alt+enter (thói quen GUI).
	// Tầng giao thức terminal không phân biệt Shift+Enter với Enter, nên không hỗ trợ Shift+Enter.
	ta.KeyMap.InsertNewline.SetKeys("ctrl+j", "alt+enter")

	vp := viewport.New(80, 20)
	vp.SetContent("")

	svp := viewport.New(80, 10)
	svp.SetContent("")

	dvp := viewport.New(40, 20)
	dvp.SetContent("")

	stvp := viewport.New(32, 20)
	stvp.SetContent("")

	// Lúc khởi động quét một lần nhập dở (LoadState tính lại digest artifact, không vào poll snapshot);
	// sách dở mà không chủ động báo, user chỉ phát hiện khi bị cổng chặn từ chối lúc viết (RFC §18.2).
	importHint := ""
	if rt != nil {
		importHint = rt.ImportResumeHint()
	}

	return Model{
		runtime:      rt,
		version:      strings.TrimSpace(version),
		autoScroll:   true,
		streamScroll: true,
		mode:         modeNew,
		startupMode:  startupModeQuick,
		importHint:   importHint,
		textarea:     ta,
		viewport:     vp,
		streamVP:     svp,
		detailVP:     dvp,
		stateVP:      stvp,
		streamBuf:    &strings.Builder{},
		eventIndex:   make(map[string]int),
	}
}

func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{
		textarea.Blink,
		listenEvents(m.runtime),
		listenDone(m.runtime),
		listenStream(m.runtime),
		tickSnapshot(m.runtime),
		bootstrapRuntime(m.runtime),
		tickSpinner(),
	}
	// Kiểm tra bản mới lúc khởi động: một lần ở nền; lỗi chỉ ghi log, trúng bản mới mới nổi nhắc.
	if !m.disableUpdateCheck {
		cmds = append(cmds, checkForUpdate(m.version))
	}
	return tea.Batch(cmds...)
}

func (m *Model) paneAtMouse(x, y int) (focusPane, bool) {
	if m.width == 0 || m.height == 0 {
		return focusEvents, false
	}

	topH, _, bodyH := m.layoutHeights()
	if bodyH < 1 {
		return focusEvents, false
	}

	bodyStartY := topH
	bodyEndY := topH + bodyH
	if y < bodyStartY || y >= bodyEndY {
		return focusEvents, false
	}

	leftW := m.sidebarWidth()
	rightW := m.detailWidth()
	centerStartX := leftW
	rightStartX := m.width - rightW

	if x >= rightStartX {
		return focusDetail, true
	}
	if x < centerStartX {
		return focusState, true
	}

	eventH, _ := m.splitHeights(bodyH)
	if y-bodyStartY < eventH {
		return focusEvents, true
	}
	return focusStream, true
}

func (m *Model) paneHighlighted(pane focusPane) bool {
	if m.focusPane == pane {
		return true
	}
	return m.hoverActive && m.hoverPane == pane
}

// hasRunningEvent còn sự kiện dạng gọi chưa xong (spinner còn quay) không.
// tickEventSpinner dùng nó để biết có đáng vẽ lại: không có running event thì khung spinner không ảnh hưởng output,
// cả refreshEventViewport là việc vô ích chắc chắn.
func (m *Model) hasRunningEvent() bool {
	for i := range m.events {
		if m.events[i].Running() {
			return true
		}
	}
	return false
}

// flushStreamIfDirty xả streamRounds dồn vào viewport; đánh dấu đã xả.
// Trả về có xả thật không, để bên gọi quyết có GotoBottom không.
func (m *Model) flushStreamIfDirty() bool {
	if !m.streamDirty {
		return false
	}
	m.refreshStreamViewport()
	m.streamDirty = false
	return true
}

// refreshEventViewport vẽ lại nội dung dòng sự kiện rồi đặt vào viewport.
func (m *Model) refreshEventViewport() {
	centerW := m.eventFlowWidth()
	content := renderEventContent(m.events, centerW, m.eventSpinnerIdx)
	snap := m.snapshot
	if m.starting {
		snap.IsRunning = true
	}
	if activity := renderEventActivity(snap, m.spinnerIdx, centerW); activity != "" {
		if strings.TrimSpace(content) != "" {
			content += "\n" + activity
		} else {
			content = activity
		}
	}
	m.viewport.SetContent(content)
	if m.autoScroll {
		m.viewport.GotoBottom()
	}
}

func (m *Model) refreshStreamViewport() {
	cursor := ""
	if m.snapshot.IsRunning {
		cursor = renderStreamCursor(m.cursorIdx)
	}
	m.streamVP.SetContent(renderStreamContent(m.streamRounds, m.streamVP.Width, cursor))
}

func (m *Model) refreshDetailViewport() {
	rightW := m.detailWidth()
	if rightW <= 4 {
		return
	}
	m.detailVP.SetContent(renderDetailContent(m.snapshot, rightW-4))
}

// refreshStateViewport xả nội dung cột trạng thái trái vào viewport.
// Nội dung cột suy thuần từ snapshot, nên snapshot hay kích thước đổi đều phải xả lại.
func (m *Model) refreshStateViewport() {
	leftW := m.sidebarWidth()
	if leftW <= 4 {
		return
	}
	m.stateVP.SetContent(renderStateContent(m.snapshot, leftW-4))
}

// updateViewportSize cập nhật cỡ viewport theo cỡ cửa sổ hiện tại.
func (m *Model) updateViewportSize() {
	centerW := m.eventFlowWidth()
	rightW := m.detailWidth()
	bodyH := m.bodyHeight()
	eventH, streamH := m.splitHeights(bodyH)
	m.viewport.Width = centerW - 2
	m.viewport.Height = eventH - 1 // -1 cho dòng header panel event
	m.streamVP.Width = centerW - 2
	m.streamVP.Height = streamH - 1 // -1 cho dòng header panel stream
	m.detailVP.Width = rightW - 2
	m.detailVP.Height = bodyH
	leftW := m.sidebarWidth()
	m.stateVP.Width = max(1, leftW-2)
	m.stateVP.Height = max(1, bodyH-1) // -1 cho lề trắng đỉnh, dòng đáy hiện nội dung thẳng
	// Cao thay đổi hay nội dung ngắn lại, hai cột cuộn tự do có thể kẹt offset vượt giới (SetContent của bubbles
	// chỉ chặn vượt dòng cuối), viewport sẽ đệm dòng trống đầy đáy. SetYOffset tự kẹp.
	m.stateVP.SetYOffset(m.stateVP.YOffset)
	m.detailVP.SetYOffset(m.detailVP.YOffset)
}

// splitHeights tính chia cao cho dòng sự kiện và output stream.
func (m *Model) splitHeights(bodyH int) (eventH, streamH int) {
	eventH = bodyH * 40 / 100
	if eventH < 3 {
		eventH = 3
	}
	streamH = bodyH - eventH - 1 // -1 cho đường phân cách
	if streamH < 3 {
		streamH = 3
	}
	return
}

func (m *Model) inputWidth() int {
	if m.width == 0 {
		return 60
	}
	return m.width - 6 // border + padding + ký hiệu nhắc "❯ "
}

func (m *Model) currentInputWidth() int {
	if m.cocreate != nil {
		return coCreateInputWidth(m.width, m.height)
	}
	return m.inputWidth()
}

// refitTextareaHeight ước dòng nhìn theo nội dung hiện tại, SetHeight động.
// Dòng nhìn = dòng logic (cắt \n) mỗi đoạn cộng dồn sau wrap theo rộng. Phối MaxHeight=6
// thành "nội dung siêu dài/xuống dòng chủ động tự hiện nhiều dòng, tối đa 6 dòng".
func (m *Model) refitTextareaHeight() {
	w := m.textarea.Width()
	if w <= 0 {
		return
	}
	// Chế độ đồng sáng tác input cố định 1 dòng: nội dung nhiều dòng của textarea để textarea tự
	// cuộn theo con trỏ. Nếu không cao inputBox chạy theo nội dung, làm cột conversation trái co lại,
	// input trôi theo chiều dọc, phá ổn định bố cục.
	if m.cocreate != nil {
		m.textarea.SetHeight(1)
		return
	}
	text := m.textarea.Value()
	if text == "" {
		m.textarea.SetHeight(1)
		return
	}
	// Trừ 2 cột hao (prompt symbol + cursor trong textarea), dư 1 dòng chấp nhận được.
	contentW := w - 2
	if contentW < 1 {
		contentW = 1
	}
	total := 0
	for line := range strings.SplitSeq(text, "\n") {
		lw := lipgloss.Width(line)
		if lw == 0 {
			total++
			continue
		}
		total += (lw + contentW - 1) / contentW
	}
	if total < 1 {
		total = 1
	}
	m.textarea.SetHeight(total) // SetHeight tự kẹp theo MaxHeight
}

// resizeTextarea đặt đồng bộ rộng với cao theo nội dung.
// Thay các gọi SetWidth(currentInputWidth()) rải rác, bảo đảm rộng đổi thì cao theo.
func (m *Model) resizeTextarea() {
	m.textarea.SetWidth(m.currentInputWidth())
	m.refitTextareaHeight()
}

// maxInputHistory giới hạn dài lịch sử, tránh phiên dài phình RAM.
const maxInputHistory = 200

// pushInputHistory thêm nội dung gửi thành công vào lịch sử, khử trùng kề nhau. Đồng bộ reset index duyệt.
func (m *Model) pushInputHistory(text string) {
	if text == "" {
		return
	}
	if n := len(m.inputHistory); n == 0 || m.inputHistory[n-1] != text {
		m.inputHistory = append(m.inputHistory, text)
		if len(m.inputHistory) > maxInputHistory {
			m.inputHistory = m.inputHistory[len(m.inputHistory)-maxInputHistory:]
		}
	}
	m.historyIdx = len(m.inputHistory)
	m.historyDraft = ""
}

// tryHistoryUp về một mục lịch sử cũ hơn; trả về có xử lý phím không.
// Lần đầu vào duyệt lịch sử thì giữ nội dung textarea hiện tại làm draft, về cuối thì hồi.
// Bên gọi tự xét cảnh nhiều dòng có nên tránh (để textarea lo di chuyển con trỏ trong dòng).
func (m *Model) tryHistoryUp() bool {
	if len(m.inputHistory) == 0 || m.historyIdx <= 0 {
		return false
	}
	if m.historyIdx == len(m.inputHistory) {
		m.historyDraft = m.textarea.Value()
	}
	m.historyIdx--
	m.textarea.SetValue(m.inputHistory[m.historyIdx])
	m.textarea.CursorEnd()
	m.syncCommandInputHighlight()
	m.refitTextareaHeight()
	return true
}

// tryHistoryDown về một mục lịch sử mới hơn; tới cuối thì hồi draft.
func (m *Model) tryHistoryDown() bool {
	if m.historyIdx >= len(m.inputHistory) {
		return false
	}
	m.historyIdx++
	if m.historyIdx == len(m.inputHistory) {
		m.textarea.SetValue(m.historyDraft)
		m.historyDraft = ""
	} else {
		m.textarea.SetValue(m.inputHistory[m.historyIdx])
	}
	m.textarea.CursorEnd()
	m.syncCommandInputHighlight()
	m.refitTextareaHeight()
	return true
}

// textareaIsMultiline nội dung textarea hiện tại có xuống dòng chủ động không; để quyết ↑↓ đi lịch sử hay di chuyển trong dòng.
func (m *Model) textareaIsMultiline() bool {
	return strings.Contains(m.textarea.Value(), "\n")
}

// inputHints sinh chữ gợi ý đáy theo trạng thái hiện tại.
// Cuối thống nhất thêm copySuffix, để user ở trạng thái không khẩn nào cũng thấy cách bôi đen copy;
// lúc tắt chuột thì hiện gợi ý đỏ nổi, nhắc bấm lại để hồi tương tác chuột.
func (m *Model) inputHints() string {
	dimStyle := lipgloss.NewStyle().Foreground(colorDim)
	if m.quitPending {
		return lipgloss.NewStyle().Foreground(lipgloss.Color("243")).Bold(true).Render("Nhấn Ctrl+C lần nữa để thoát")
	}
	limitHint := m.inputLimitHint()
	// Trang chào (modeNew) không mở báo cáo chuột, terminal kéo bôi đen copy nguyên bản được, khỏi gợi ý Ctrl+R;
	// bàn viết mới mở báo cáo, copy phải Ctrl+R tắt tạm.
	suffix := limitHint + " · Ctrl+R để bôi đen copy"
	if m.mode == modeNew {
		suffix = limitHint
	}
	if m.mouseOff && m.mode != modeNew {
		// Bàn viết chuyển tay sang bôi đen copy: dùng màu nhấn báo đang ở trạng thái "kéo tự do để chọn", bấm Ctrl+R để hồi
		return lipgloss.NewStyle().Foreground(colorAccent).Bold(true).
			Render("✂ Chế độ bôi đen copy: kéo chuột chọn chữ để copy · Ctrl+R để về như cũ")
	}
	if m.cocreate != nil {
		scrollHint := " · Tab cuộn: hội thoại"
		if m.cocreate.focusPrompt {
			scrollHint = " · Tab cuộn: chỉ đạo viết"
		}
		switch {
		case m.cocreate.awaiting:
			return dimStyle.Render("Đang chờ AI trả lời · Esc thoát đồng sáng tác" + scrollHint + suffix)
		case m.cocreate.canStart():
			startLabel := "Ctrl+S bắt đầu viết"
			if m.cocreate.stage {
				startLabel = "Ctrl+S chốt và viết tiếp"
			}
			return dimStyle.Render("Enter gửi · " + startLabel + " · Esc thoát đồng sáng tác" + scrollHint + suffix)
		default:
			return dimStyle.Render("Enter gửi · Esc thoát đồng sáng tác" + scrollHint + suffix)
		}
	}
	if m.mode == modeNew {
		if m.startupMode == startupModeQuick {
			return dimStyle.Render("Tab đổi chế độ · gõ / tìm lệnh · Enter viết luôn · Esc xóa ô nhập" + suffix)
		}
		return dimStyle.Render("Tab đổi chế độ · gõ / tìm lệnh · Enter trò chuyện đồng sáng tác · Esc xóa ô nhập" + suffix)
	}
	switch m.snapshot.RuntimeState {
	case "pausing":
		return dimStyle.Render("Đang dừng viết · chờ lượt này xong" + suffix)
	case "paused":
		return dimStyle.Render("Gõ / tìm lệnh · Enter viết tiếp · Esc xóa ô nhập" + suffix)
	}
	return dimStyle.Render("Gõ / tìm lệnh · click/Tab đổi panel · ↑↓ cuộn · End xuống cuối · Ctrl+L xóa màn hình · Esc tạm dừng · Enter gửi" + suffix)
}

func (m *Model) inputLimitHint() string {
	limit := m.textarea.CharLimit
	if limit <= 0 {
		return ""
	}
	used := m.textarea.Length()
	if used < limit*4/5 {
		return ""
	}
	return fmt.Sprintf(" · đã nhập %d/%d", used, limit)
}

func (m *Model) eventFlowWidth() int {
	if m.width == 0 {
		return 80
	}
	leftW := m.sidebarWidth()
	rightW := m.detailWidth()
	return m.width - leftW - rightW
}

func (m *Model) sidebarWidth() int {
	if m.width == 0 {
		return 32
	}
	return m.width * 23 / 100
}

func (m *Model) detailWidth() int {
	if m.width == 0 {
		return 40
	}
	return m.width * 27 / 100
}

func (m *Model) bodyHeight() int {
	_, _, bodyH := m.layoutHeights()
	return bodyH
}

func (m *Model) currentSpinnerFrame() string {
	if !m.snapshot.IsRunning && !m.starting {
		return ""
	}
	return spinnerFrames[m.spinnerIdx%len(spinnerFrames)]
}

func (m *Model) outputDir() string {
	if m.runtime == nil {
		return ""
	}
	return m.runtime.Dir()
}

func defaultSteerPlaceholder() string {
	return "Gõ can thiệp cốt truyện, vd: đẩy tuyến tình cảm lên chương 4"
}

func (m *Model) syncRuntimePlaceholder() {
	if m.mode != modeRunning || m.cocreate != nil {
		return
	}
	if m.starting {
		m.textarea.Placeholder = "Đang khởi tạo truyện..."
		return
	}
	switch m.snapshot.RuntimeState {
	case "completed":
		m.textarea.Placeholder = donePlaceholder
	case "pausing":
		m.textarea.Placeholder = "Đang dừng viết..."
	case "paused":
		if m.snapshot.AdvanceMode == "review" && m.snapshot.Phase == "writing" {
			m.textarea.Placeholder = "Đang chờ duyệt từng chương: gõ góp ý, hoặc /next cho viết tiếp"
		} else {
			m.textarea.Placeholder = "Đã dừng, gõ gì đó để viết tiếp"
		}
	default:
		if !m.snapshot.IsRunning {
			if m.snapshot.AdvanceMode == "review" && m.snapshot.Phase == "writing" {
				m.textarea.Placeholder = "Đang chờ duyệt từng chương: gõ góp ý, hoặc /next cho viết tiếp"
			} else {
				m.textarea.Placeholder = "Bị ngắt, gõ gì đó để viết tiếp"
			}
		} else {
			m.textarea.Placeholder = defaultSteerPlaceholder()
		}
	}
}

func (m *Model) renderBottomBar() string {
	inputView := highlightCommandToken(m.textarea.View(), m.textarea.Value(), m.commandToken)
	inputBox := renderInputBox(
		inputView,
		m.inputHints(),
		m.snapshot,
		m.outputDir(),
		m.width,
	)
	if m.mode != modeNew || m.cocreate != nil {
		return inputBox
	}
	return renderStartupModeBar(m.width, m.startupMode) + "\n" + inputBox
}

func (m *Model) layoutHeights() (topH, inputH, bodyH int) {
	if m.width == 0 || m.height == 0 {
		return 1, 4, 20
	}
	topH = lipgloss.Height(renderTopBar(m.snapshot, m.width, m.currentSpinnerFrame(), m.version))
	inputH = lipgloss.Height(m.renderBottomBar())
	bodyH = m.height - topH - inputH
	if bodyH < 3 {
		bodyH = 3
	}
	return
}

func (m Model) View() string {
	if m.width == 0 || m.height == 0 {
		return "Đang tải..."
	}
	if m.width < 100 {
		return lipgloss.NewStyle().
			Width(m.width).Height(m.height).
			AlignHorizontal(lipgloss.Center).
			AlignVertical(lipgloss.Center).
			Render("Terminal quá hẹp, nới ra ít nhất 100 cột")
	}
	if m.cocreate != nil {
		return renderCoCreateModal(m.width, m.height, m.cocreate, errorText(m.err), m.textarea.View(), m.spinnerIdx, m.quitPending)
	}
	if m.help != nil {
		return renderHelpModal(m.width, m.height, m.help)
	}
	if m.report != nil {
		return renderReportModal(m.width, m.height, m.report)
	}
	if m.importer != nil {
		// Nhập không phụ thuộc trạng thái chạy Engine, khung animation lấy thẳng spinnerIdx (currentSpinnerFrame lúc engine dừng trả rỗng).
		return renderImportModal(m.width, m.height, m.importer, m.spinnerIdx)
	}
	if m.simulator != nil {
		return renderSimulationModal(m.width, m.height, m.simulator)
	}

	topBar := renderTopBar(m.snapshot, m.width, m.currentSpinnerFrame(), m.version)
	inputBox := m.renderBottomBar()
	_, inputH, bodyH := m.layoutHeights()

	var body string
	if m.mode == modeNew {
		errMsg := ""
		if m.err != nil {
			errMsg = m.err.Error()
		}
		body = renderWelcome(m.width, bodyH, errMsg, m.startupMode, m.importHint, m.updateHint)
	} else {
		leftW := m.sidebarWidth()
		rightW := m.detailWidth()
		centerW := m.width - leftW - rightW
		eventH, streamH := m.splitHeights(bodyH)

		if m.viewport.Width != centerW-2 || m.viewport.Height != eventH-1 {
			m.viewport.Width = centerW - 2
			m.viewport.Height = eventH - 1 // -1 cho dòng header panel event
		}
		if m.streamVP.Width != centerW-2 || m.streamVP.Height != streamH-1 {
			m.streamVP.Width = centerW - 2
			m.streamVP.Height = streamH - 1 // -1 cho dòng header panel stream
		}

		eventFlow := renderEventFlowViewport(m.viewport, centerW, eventH, m.paneHighlighted(focusEvents))
		streamPanel := renderStreamPanel(m.streamVP, centerW, streamH, m.paneHighlighted(focusStream), m.snapshot.IsRunning || m.starting, m.spinnerIdx)
		center := lipgloss.JoinVertical(lipgloss.Left, eventFlow, streamPanel)

		left := renderStatePanel(m.stateVP, leftW, bodyH, m.paneHighlighted(focusState))
		right := renderDetailPanel(m.detailVP, rightW, bodyH, m.paneHighlighted(focusDetail))
		body = lipgloss.JoinHorizontal(lipgloss.Top, left, center, right)
	}

	view := lipgloss.JoinVertical(lipgloss.Left, topBar, body, inputBox)

	// Popup phủ chồng: nổi trên đáy body, không ảnh hưởng bố cục
	if m.modelSwitch != nil {
		commandBar := renderModelSwitchBar(m.width, m.modelSwitch)
		view = overlayAboveInput(view, commandBar, inputH)
	} else if m.modelConfig != nil {
		view = overlayAboveInput(view, renderModelConfigModal(m.width, m.modelConfig), inputH)
	} else if m.compActive {
		commandBar := renderCommandPalette(m.width, m.compItems, m.compIdx)
		view = overlayAboveInput(view, commandBar, inputH)
	}
	return view
}

// sendCoCreate mở một vòng request đồng sáng tác, lo thống nhất reqID, textarea, placeholder.
func (m *Model) sendCoCreate() tea.Cmd {
	m.cocreateSeq++
	m.cocreate.reqID = m.cocreateSeq
	m.cocreate.awaiting = true
	m.resizeTextarea()
	m.textarea.Placeholder = placeholderForCoCreate(m.cocreate)
	m.textarea.Blur()
	return runCoCreate(m.runtime, m.cocreate)
}

func (m Model) handleCoCreateKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.cocreate == nil {
		return m, nil
	}
	state := m.cocreate

	// Phím ↑↓/PgUp/PgDn/Home/End để cuộn; Tab đổi focus cuộn giữa cột hội thoại trái ↔ cột chỉ đạo viết phải
	// (mặc định cột trái, user xem lại là chính). Trang chào đã tắt báo cáo chuột để giữ copy nguyên bản, cột phải tràn thì Tab
	// đổi focus rồi cuộn phím. Cột trái: cuộn lên tắt follow, lăn tới đáy mở lại follow (bám stream).
	switch msg.Type {
	case tea.KeyTab:
		state.focusPrompt = !state.focusPrompt
		return m, nil
	case tea.KeyUp, tea.KeyPgUp:
		if state.focusPrompt {
			var cmd tea.Cmd
			state.promptVP, cmd = state.promptVP.Update(msg)
			return m, cmd
		}
		state.convFollow = false
		var cmd tea.Cmd
		state.convVP, cmd = state.convVP.Update(msg)
		return m, cmd
	case tea.KeyDown, tea.KeyPgDown:
		if state.focusPrompt {
			var cmd tea.Cmd
			state.promptVP, cmd = state.promptVP.Update(msg)
			return m, cmd
		}
		var cmd tea.Cmd
		state.convVP, cmd = state.convVP.Update(msg)
		if state.convVP.AtBottom() {
			state.convFollow = true
		}
		return m, cmd
	case tea.KeyHome:
		if state.focusPrompt {
			state.promptVP.GotoTop()
			return m, nil
		}
		state.convFollow = false
		state.convVP.GotoTop()
		return m, nil
	case tea.KeyEnd:
		if state.focusPrompt {
			state.promptVP.GotoBottom()
			return m, nil
		}
		state.convFollow = true
		state.convVP.GotoBottom()
		return m, nil
	case tea.KeyEsc:
		return m.exitCoCreate()
	}

	// Lúc chờ AI trả lời, dạng sửa (nhập ký tự/xóa/con trỏ/Ctrl+U/xuống dòng nhiều dòng) cho qua —
	// user gõ trước được câu tiếp trong lúc AI nghĩ. Chặn dạng gửi chìm xuống từng case,
	// để tiết lưu Enter chạy trước chặn awaiting — mảnh \n dư do dán vẫn đệm được dấu cách.

	switch msg.Type {
	case tea.KeyCtrlS:
		if state.awaiting {
			return m, nil
		}
		if !state.canStart() {
			return m, nil
		}
		// Đồng sáng tác giai đoạn: bơm "brief hướng tiếp theo" vào rồi hồi viết, về bàn chạy.
		if state.stage {
			draft := state.draftPrompt()
			m.cocreate = nil
			m.err = nil
			m.resizeTextarea()
			m.textarea.Placeholder = defaultSteerPlaceholder()
			return m, tea.Batch(resumeFromCoCreate(m.runtime, draft), m.textarea.Focus())
		}
		// Đồng sáng tác khởi động lạnh: lấy chỉ đạo viết đã gọn để bắt đầu viết.
		prompt, err := state.buildPrompt()
		if err != nil {
			m.err = err
			return m, nil
		}
		cmd := m.enterStarting(prompt)
		return m, tea.Batch(startRuntime(m.runtime, prompt), cmd)
	case tea.KeyEnter:
		// Alt+Enter → xuống dòng chủ động, để textarea.Update lo (KeyMap.InsertNewline đã gắn phím này)
		if msg.Alt {
			break
		}
		// Cách phím ký tự trước quá ngắn → coi như mảnh \n dư của luồng dán: đệm dấu cách thay gửi.
		// Phải xét trước chặn awaiting — nếu không mảnh \n dán lúc awaiting bị chặn,
		// làm "abc\ndef" nuốt thành "abcdef", lệch ngữ nghĩa đường base.
		if !m.lastKeyAt.IsZero() && time.Since(m.lastKeyAt) < 50*time.Millisecond {
			var cmd tea.Cmd
			state.resetSuggestionInput()
			m.textarea, cmd = m.textarea.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
			m.refitTextareaHeight()
			return m, cmd
		}
		// Ý định gửi thật: lúc awaiting thì chặn (không gửi request đồng thời)
		if state.awaiting {
			return m, nil
		}
		text := utils.CleanInputLine(m.textarea.Value())
		if text == "" {
			return m, nil
		}
		m.err = nil
		state.appendUser(text)
		m.textarea.Reset()
		m.refitTextareaHeight()
		cmd := m.sendCoCreate()
		return m, cmd
	case tea.KeyCtrlU:
		state.resetSuggestionInput()
		m.textarea.Reset()
		m.refitTextareaHeight()
		return m, nil
	}

	// Phím số 1/2/3 ghép liên tiếp được gợi ý: lần đầu điền vào, sau thêm bằng chấm phẩy, chọn trùng thì bỏ qua.
	// Sửa tay bất kỳ đều thoát trạng thái ghép nhanh, phím số sau giữ ngữ nghĩa nhập thường.
	if msg.Type == tea.KeyRunes && len(msg.Runes) == 1 && !state.awaiting {
		if r := msg.Runes[0]; r >= '1' && r <= '3' {
			if value, handled := state.appendSuggestion(int(r-'1'), m.textarea.Value()); handled {
				m.textarea.SetValue(value)
				m.textarea.CursorEnd()
				m.refitTextareaHeight()
				return m, nil
			}
		}
	}

	// Nhập thường chuyển cho textarea
	if msg.Type == tea.KeyRunes && (containsSGRFragment(string(msg.Runes)) || isCSILeak(msg.Runes)) {
		return m, nil
	}
	var ok bool
	if msg, ok = cleanHumanKeyRunes(msg); !ok {
		return m, nil
	}
	state.resetSuggestionInput()
	if msg.Type == tea.KeyRunes {
		m.lastKeyAt = time.Now()
	}
	var cmd tea.Cmd
	m.textarea, cmd = m.textarea.Update(msg)
	m.refitTextareaHeight()
	return m, cmd
}

// exitCoCreate thoát chế độ đồng sáng tác, hủy request LLM đang chạy, hồi trạng thái khung nhập.
func (m Model) exitCoCreate() (tea.Model, tea.Cmd) {
	if m.cocreate.cancel != nil {
		m.cocreate.cancel()
	}
	stage := m.cocreate.stage
	initial := m.cocreate.initialInput()
	m.cocreate = nil
	m.resizeTextarea()
	// Hủy đồng sáng tác giai đoạn: xóa mốc chiếm, giữ tạm dừng, về trạng thái nhập ở bàn chạy (không điền lại câu mở tổng hợp).
	if stage {
		m.textarea.SetValue("")
		m.textarea.Placeholder = defaultSteerPlaceholder()
		return m, tea.Batch(cancelCoCreate(m.runtime), fetchSnapshot(m.runtime), m.textarea.Focus())
	}
	m.textarea.SetValue(initial)
	m.textarea.Placeholder = placeholderForNewMode(m.startupMode)
	return m, m.textarea.Focus()
}

// overlayAboveInput phủ overlay nổi lên đáy base view (trên inputBox),
// không đổi tổng cao bố cục. Chỉ che đúng rộng thẻ overlay, bên phải lộ nội dung nền.
func overlayAboveInput(base, overlay string, inputLineCount int) string {
	baseLines := strings.Split(base, "\n")
	overLines := strings.Split(strings.TrimRight(overlay, "\n"), "\n")

	endY := len(baseLines) - inputLineCount
	startY := endY - len(overLines)
	if startY < 0 {
		startY = 0
	}

	for i, ol := range overLines {
		y := startY + i
		if y >= 0 && y < endY {
			olW := lipgloss.Width(ol)
			// Cắt olW ký tự nhìn bên trái đường nền, nối overlay + phần nội dung phải còn lại
			right := ansi.TruncateLeft(baseLines[y], olW, "")
			baseLines[y] = ol + right
		}
	}
	return strings.Join(baseLines, "\n")
}

// isCSILeak phát hiện KeyRunes có phải mảnh rò rỉ của chuỗi escape CSI không.
// Terminal gửi phím mũi tên \x1b[A, bấm nhanh có thể chẻ chuỗi:
// \x1b thành Escape, "[" hay "[A" rò thành KeyRunes vào textarea.
func isCSILeak(runes []rune) bool {
	if len(runes) == 0 || runes[0] != '[' {
		return false
	}
	for _, r := range runes[1:] {
		if (r >= '0' && r <= '9') || r == ';' ||
			(r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || r == '~' {
			continue
		}
		return false
	}
	return true
}

// containsSGRFragment phát hiện chữ có chứa mảnh chuỗi chuột SGR (mẫu "<số;số;").
func containsSGRFragment(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] != '<' {
			continue
		}
		j := i + 1
		if j >= len(s) || s[j] < '0' || s[j] > '9' {
			continue
		}
		for j < len(s) && s[j] >= '0' && s[j] <= '9' {
			j++
		}
		if j < len(s) && s[j] == ';' {
			return true
		}
	}
	return false
}

func cleanHumanKeyRunes(msg tea.KeyMsg) (tea.KeyMsg, bool) {
	if msg.Type != tea.KeyRunes {
		return msg, true
	}
	cleaned := utils.CleanInputRunes(msg.Runes)
	if cleaned == "" {
		return msg, false
	}
	msg.Runes = []rune(cleaned)
	return msg, true
}
