package tui

import (
	"context"
	"errors"
	"fmt"
	"github.com/CTKiet2006/kietnovel/internal/i18n"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/CTKiet2006/kietnovel/internal/entry/startup"
	"github.com/CTKiet2006/kietnovel/internal/host"
	"github.com/CTKiet2006/kietnovel/internal/host/imp"
	"github.com/CTKiet2006/kietnovel/internal/utils"
	tea "github.com/charmbracelet/bubbletea"
)

const maxPromptEventCols = 160

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Cao body phụ thuộc chiều cao thực của top/bottom bar (thanh chế độ trang mới, nhập nhiều dòng đều đổi nó),
	// đồng bộ một lần trước mỗi tin để viewport khỏi kẹt chiều cao cũ, đáy panel bù dòng trống. Idempotent mà rẻ.
	if m.width > 0 {
		m.updateViewportSize()
	}
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.resizeTextarea()
		m.updateViewportSize()
		m.refreshDetailViewport()
		m.refreshStateViewport()
		return m, nil
	case tea.KeyMsg:
		return m.handleKeyMsg(msg)
	case tea.MouseMsg:
		return m.handleMouseMsg(msg)
	default:
		if next, cmd, handled := m.handleRuntimeMsg(msg); handled {
			return next, cmd
		}
		return m.handleTextareaMsg(msg)
	}
}

func (m Model) handleKeyMsg(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if next, cmd, handled := m.handleOverlayKeyMsg(msg); handled {
		return next, cmd
	}

	if msg.Type == tea.KeyCtrlC {
		if m.quitPending {
			return m, tea.Quit
		}
		m.quitPending = true
		return m, tea.Tick(time.Second, func(time.Time) tea.Msg { return quitResetMsg{} })
	}
	m.quitPending = false

	if next, cmd, handled := m.handleCommandPaletteKey(msg); handled {
		return next, cmd
	}

	return m.handleBaseKeyMsg(msg)
}

func (m Model) handleOverlayKeyMsg(msg tea.KeyMsg) (tea.Model, tea.Cmd, bool) {
	switch {
	case m.cocreate != nil:
		return m.handleBlockingModalKey(msg, m.handleCoCreateKey)
	case m.modelConfig != nil:
		return m.handleBlockingModalKey(msg, m.handleModelConfigKey)
	case m.help != nil:
		return m.handleBlockingModalKey(msg, m.handleHelpKey)
	case m.reader != nil:
		return m.handleBlockingModalKey(msg, m.handleReaderKey)
	case m.books != nil:
		return m.handleBlockingModalKey(msg, m.handleBooksKey)
	case m.bookLang != nil:
		// Hỏi ngôn ngữ phải chặn mọi phím khác: nếu lọt một phím gửi, người dùng
		// gõ yêu cầu xong bấm Enter lại thì yêu cầu đó mất.
		out, cmd, _ := m.handleBookLanguageKey(msg)
		return out, cmd, true
	case m.welcome != nil:
		// Màn chào chặn mọi phím khác: đang hỏi thì không cho gõ lệnh.
		out, cmd, _ := m.handleWelcomeKey(msg)
		return out, cmd, true
	case m.modelSwitch != nil:
		return m.handleBlockingModalKey(msg, m.handleModelSwitchKey)
	case m.report != nil:
		return m.handleBlockingModalKey(msg, m.handleReportKey)
	case m.importer != nil:
		return m.handleBlockingModalKey(msg, m.handleImportKey)
	case m.simulator != nil:
		return m.handleBlockingModalKey(msg, m.handleSimulationKey)
	default:
		return m, nil, false
	}
}

func (m Model) handleBlockingModalKey(msg tea.KeyMsg, next func(tea.KeyMsg) (tea.Model, tea.Cmd)) (tea.Model, tea.Cmd, bool) {
	if msg.Type == tea.KeyCtrlC {
		if m.quitPending {
			return m, tea.Quit, true
		}
		m.quitPending = true
		return m, tea.Tick(time.Second, func(time.Time) tea.Msg { return quitResetMsg{} }), true
	}
	m.quitPending = false
	// Phím tắt toàn cục xuyên modal: lúc modal mở vẫn chuyển được báo cáo chuột, nếu không các modal
	// khóa màn hình như đồng sáng tác/help/report sẽ không kéo chuột bôi đen copy nguyên bản được.
	if msg.Type == tea.KeyCtrlR {
		next, cmd := m.toggleMouseReporting()
		return next, cmd, true
	}
	model, cmd := next(msg)
	return model, cmd, true
}

// toggleMouseReporting bật/tắt báo cáo chuột. Bật → tắt để user kéo chuột bôi đen copy nguyên bản;
// tắt → bật để click đổi focus / cuộn lại. Đường base và đường blocking modal dùng chung.
func (m Model) toggleMouseReporting() (Model, tea.Cmd) {
	// Trang chào (modeNew) vốn không mở báo cáo chuột, kéo chuột copy nguyên bản được; ở đây bỏ qua Ctrl+R,
	// tránh mở nhầm báo cáo lại phá copy nguyên bản. Báo cáo chuột do enterRunning mở khi vào bàn viết.
	if m.mode == modeNew {
		return m, nil
	}
	m.mouseOff = !m.mouseOff
	if m.mouseOff {
		return m, tea.DisableMouse
	}
	return m, tea.EnableMouseCellMotion
}

// donePlaceholder() gợi ý khung nhập lúc xong: xong trong phiên (doneMsg) và khởi động lại vào sách đã xong (bootstrap) dùng chung.
// Là hàm chứ không phải hằng số, vì i18n.T không phải hằng số, và placeholder phải
// dịch lại được khi đổi ngôn ngữ giữa phiên.
func donePlaceholder() string {
	return i18n.T("Đã viết xong · gõ yêu cầu sửa (vd \"viết lại chương 3\"), /reopen viết tập mới, /export xuất")
}

// enterRunning vào bàn viết: mở báo cáo chuột (bàn viết cần click đổi panel / cuộn /
// kéo sidebar). Lệnh trả về bên gọi phải Batch vào giá trị trả cuối.
func (m *Model) enterRunning() tea.Cmd {
	m.mode = modeRunning
	m.mouseOff = false
	return tea.EnableMouseCellMotion
}

func (m Model) handleCommandPaletteKey(msg tea.KeyMsg) (tea.Model, tea.Cmd, bool) {
	if !m.compActive {
		return m, nil, false
	}

	switch msg.Type {
	case tea.KeyEsc:
		m.clearCommandPalette()
		return m, nil, true
	case tea.KeyUp:
		if m.compIdx > 0 {
			m.compIdx--
		}
		return m, nil, true
	case tea.KeyDown:
		if m.compIdx < len(m.compItems)-1 {
			m.compIdx++
		}
		return m, nil, true
	case tea.KeyTab:
		m.acceptCommandCompletion()
		return m, nil, true
	case tea.KeyEnter:
		item, ok := m.acceptCommandCompletion()
		if !ok {
			return m, nil, true
		}
		if item.AutoExecute {
			m.textarea.Reset()
			next, cmd := m.handleSlashCommand(slashCommand{name: item.Name})
			return next, cmd, true
		}
		return m, nil, true
	default:
		return m, nil, false
	}
}

func (m Model) handleBaseKeyMsg(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Chặn tiết lưu: dán \n ở terminal không hỗ trợ bracketed paste sẽ rã thành KeyEnter liên tiếp;
	// người thật gõ Enter cách ký tự trước thường > 100ms, <50ms rất có thể là mảnh dư của luồng dán.
	// Chỉ ghi KeyRunes (luồng ký tự) — phím chức năng (↑↓/Tab/Ctrl-x) không được làm bẩn tiết lưu,
	// nếu không user lật lịch sử chọn xong Enter ngay sẽ bị nuốt oan.
	if msg.Type == tea.KeyRunes {
		m.lastKeyAt = time.Now()
	}
	switch msg.Type {
	case tea.KeyEscape:
		if m.mode == modeRunning && m.snapshot.IsRunning {
			return m, abortRuntime(m.runtime)
		}
		m.textarea.Reset()
		m.historyIdx = len(m.inputHistory)
		m.historyDraft = ""
		m.refitTextareaHeight()
		m.clearCommandPalette()
		return m, nil
	case tea.KeyCtrlL:
		m.resetOutputPanels()
		return m, nil
	case tea.KeyCtrlU:
		// Xóa nhập hiện tại; đồng thời thoát trạng thái duyệt lịch sử.
		m.textarea.Reset()
		m.historyIdx = len(m.inputHistory)
		m.historyDraft = ""
		m.refitTextareaHeight()
		m.clearCommandPalette()
		return m, nil
	case tea.KeyCtrlR:
		return m.toggleMouseReporting()
	case tea.KeyTab:
		if m.mode == modeNew {
			if m.cocreate != nil {
				return m, nil
			}
			if m.startupMode == startupModeQuick {
				m.startupMode = startupModeCoCreate
			} else {
				m.startupMode = startupModeQuick
			}
			m.textarea.Placeholder = placeholderForNewMode(m.startupMode)
			return m, nil
		}
		m.focusPane = (m.focusPane + 1) % focusPaneCount
		return m, nil
	case tea.KeyEnter:
		// Alt+Enter là xuống dòng chủ động, để textarea.Update lo (KeyMap.InsertNewline đã gắn phím này).
		if msg.Alt {
			break
		}
		// Cách phím thường trước đó quá ngắn → coi như mảnh \n dư của luồng dán:
		// thay bằng dấu cách giữ khoảng nhìn, cùng ngữ nghĩa với đường cleanHumanKeyRunes ("abc\ndef" → "abc def").
		// Chặn cho terminal hỏng bracketed paste (SSH cũ/một số cấu hình tmux).
		if !m.lastKeyAt.IsZero() && time.Since(m.lastKeyAt) < 50*time.Millisecond {
			var cmd tea.Cmd
			m.textarea, cmd = m.textarea.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
			m.refitTextareaHeight()
			return m, cmd
		}
		return m.handleEnterKey()
	case tea.KeyUp:
		// Nhập nhiều dòng: để textarea lo di chuyển con trỏ trong dòng (rơi xuống textarea.Update ở cuối switch)
		if m.textareaIsMultiline() {
			break
		}
		// Một dòng: ưu tiên lật lịch sử, không có lịch sử thì về cuộn dòng sự kiện
		if m.tryHistoryUp() {
			return m, nil
		}
		return m.handleVerticalScrollKey(msg, true)
	case tea.KeyDown:
		if m.textareaIsMultiline() {
			break
		}
		if m.tryHistoryDown() {
			return m, nil
		}
		return m.handleVerticalScrollKey(msg, false)
	case tea.KeyPgUp:
		return m.handleVerticalScrollKey(msg, true)
	case tea.KeyPgDown:
		return m.handleVerticalScrollKey(msg, false)
	case tea.KeyEnd:
		switch m.focusPane {
		case focusStream:
			m.streamScroll = true
			m.streamVP.GotoBottom()
		case focusDetail:
			m.detailVP.GotoBottom()
		case focusState:
			m.stateVP.GotoBottom()
		default:
			m.autoScroll = true
			m.viewport.GotoBottom()
		}
		return m, nil
	}

	if msg.Type == tea.KeyRunes && (containsSGRFragment(string(msg.Runes)) || isCSILeak(msg.Runes)) {
		return m, nil
	}
	var ok bool
	if msg, ok = cleanHumanKeyRunes(msg); !ok {
		return m, nil
	}

	var cmd tea.Cmd
	m.textarea, cmd = m.textarea.Update(msg)
	m.refitTextareaHeight()
	m.updateCommandPalette()
	return m, cmd
}

func (m Model) handleEnterKey() (tea.Model, tea.Cmd) {
	text := utils.CleanInputLine(m.textarea.Value())
	if text == "" {
		return m, nil
	}
	m.clearCommandPalette()
	if cmd, ok := parseSlashCommand(text); ok {
		m.pushInputHistory(text)
		m.textarea.Reset()
		m.refitTextareaHeight()
		return m.handleSlashCommand(cmd)
	}

	m.pushInputHistory(text)
	m.textarea.Reset()
	m.refitTextareaHeight()
	switch m.mode {
	case modeNew:
		m.err = nil
		if m.startupMode == startupModeQuick {
			prompt, err := startup.PrepareQuick(text)
			if err != nil {
				m.err = err
				return m, nil
			}
			cmd := m.enterStarting(prompt)
			return m, tea.Batch(startRuntime(m.runtime, prompt), cmd)
		}
		m.cocreate = newCoCreateState(text)
		return m, m.sendCoCreate()
	case modeRunning:
		// Không dội USER event ở local —— Host.Continue/Steer đã emit sự kiện "USER",
		// chảy về TUI qua events channel. Kiến trúc §2.3: tầng quan sát chỉ quan sát, không sinh sự thật.
		if !m.snapshot.IsRunning {
			return m, continueRuntime(m.runtime, text)
		}
		return m, steerRuntime(m.runtime, text)
	case modeDone:
		// User nhập sau khi xong (yêu cầu sửa/viết tiếp): đánh thức một vòng run mới. Continue ở trạng thái dừng đi đường Inject
		// tự khôi phục, Arbiter phán quyết can thiệp của user; lúc viết lại chương đã xong Engine mở lại cả sách để xếp hàng.
		// Về modeRunning vào lại bàn viết; vòng này chạy xong
		// doneMsg(complete) sẽ đặt lại modeDone. Lệnh gạch chéo đã xử lý sớm ở trên, không qua nhánh này.
		m.mode = modeRunning
		return m, continueRuntime(m.runtime, text)
	default:
		return m, nil
	}
}

func (m Model) handleVerticalScrollKey(msg tea.KeyMsg, upward bool) (tea.Model, tea.Cmd) {
	if m.focusPane == focusStream {
		if upward {
			m.streamScroll = false
		}
		var cmd tea.Cmd
		m.streamVP, cmd = m.streamVP.Update(msg)
		if !upward && m.streamVP.AtBottom() {
			m.streamScroll = true
		}
		return m, cmd
	}
	if m.focusPane == focusDetail {
		var cmd tea.Cmd
		m.detailVP, cmd = m.detailVP.Update(msg)
		return m, cmd
	}
	if m.focusPane == focusState {
		var cmd tea.Cmd
		m.stateVP, cmd = m.stateVP.Update(msg)
		return m, cmd
	}
	if upward {
		m.autoScroll = false
	}
	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	if !upward && m.viewport.AtBottom() {
		m.autoScroll = true
	}
	return m, cmd
}

func (m Model) handleMouseMsg(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if m.cocreate != nil {
		// Chuột chia theo tọa độ X: nửa trái màn hình = panel conv, nửa phải = panel prompt.
		// Modal căn giữa và conv chiếm ~58% trái, lấy đường giữa màn hình để phân đủ chuẩn.
		// User cuộn ở vùng conv thì tự tắt follow (để đứng yên xem vị trí cũ).
		var cmd tea.Cmd
		if msg.X < m.width/2 {
			m.cocreate.convFollow = false
			m.cocreate.convVP, cmd = m.cocreate.convVP.Update(msg)
			if m.cocreate.convVP.AtBottom() {
				m.cocreate.convFollow = true
			}
		} else {
			m.cocreate.promptVP, cmd = m.cocreate.promptVP.Update(msg)
		}
		return m, cmd
	}
	if m.modelSwitch != nil || m.modelConfig != nil {
		return m, nil
	}
	if pane, ok := m.paneAtMouse(msg.X, msg.Y); ok {
		m.hoverPane = pane
		m.hoverActive = true
		if msg.Action == tea.MouseActionPress {
			m.focusPane = pane
		}
	} else {
		m.hoverActive = false
	}

	var cmd tea.Cmd
	if m.focusPane == focusStream {
		m.streamVP, cmd = m.streamVP.Update(msg)
		if msg.Action == tea.MouseActionPress {
			m.streamScroll = m.streamVP.AtBottom()
		}
		return m, cmd
	}
	if m.focusPane == focusDetail {
		m.detailVP, cmd = m.detailVP.Update(msg)
		return m, cmd
	}
	if m.focusPane == focusState {
		m.stateVP, cmd = m.stateVP.Update(msg)
		return m, cmd
	}
	m.viewport, cmd = m.viewport.Update(msg)
	if msg.Action == tea.MouseActionPress {
		m.autoScroll = m.viewport.AtBottom()
	}
	return m, cmd
}

func (m Model) handleRuntimeMsg(msg tea.Msg) (tea.Model, tea.Cmd, bool) {
	switch msg := msg.(type) {
	case eventMsg:
		hadRunningEvent := m.hasRunningEvent()
		ev := host.Event(msg)
		m.applyEventProjection(ev)
		m.refreshEventViewport()
		cmd := listenEvents(m.runtime)
		if !hadRunningEvent && m.hasRunningEvent() && !m.eventSpinnerActive {
			m.eventSpinnerActive = true
			cmd = tea.Batch(cmd, tickEventSpinner())
		}
		return m, cmd, true
	case bootstrapMsg:
		// Có tác phẩm hay không quyết chỗ rơi giao diện, khôi phục thành công chỉ quyết engine có chạy không. Lỗi nâng dữ liệu,
		// ngân sách hay cổng sửa đổi thì vẫn ở bàn viết hiện sách cũ, không về trang chào.
		if (msg.existing || msg.resumed) && m.mode == modeNew && !msg.completed {
			enableMouse := m.enterRunning()
			m.resizeTextarea()
			m.textarea.Placeholder = defaultSteerPlaceholder()
			if msg.err != nil {
				m.err = msg.err
			}
			return m, tea.Batch(fetchSnapshot(m.runtime), enableMouse), true
		}
		// Sách đã xong: rơi vào bàn viết trạng thái xong (enterRunning mở chuột rồi đổi sang modeDone), không rơi về trang chào —
		// trang chào không nhắc gì tới sách cũ, user tưởng mất sách; /reopen, /export, nhập sửa đều ở bàn viết.
		if msg.completed && m.mode == modeNew {
			enableMouse := m.enterRunning()
			m.mode = modeDone
			m.resizeTextarea()
			m.textarea.Placeholder = donePlaceholder()
			if msg.err != nil {
				m.err = msg.err
			}
			return m, tea.Batch(fetchSnapshot(m.runtime), enableMouse, m.textarea.Focus()), true
		}
		// /reopen và các lần khôi phục trong phiên từ trạng thái xong vào lại bàn viết.
		if msg.resumed && m.mode == modeDone {
			enableMouse := m.enterRunning()
			m.resizeTextarea()
			m.textarea.Placeholder = defaultSteerPlaceholder()
			return m, tea.Batch(fetchSnapshot(m.runtime), enableMouse), true
		}
		if msg.err != nil {
			m.err = msg.err
		}
		return m, fetchSnapshot(m.runtime), true
	case snapshotMsg:
		next := host.UISnapshot(msg)
		// Lúc khởi động Host chưa vào running (chuẩn hóa quy tắc/phán quyết khởi động đều trước hoặc trong StartPrepared),
		// đắp snapshot thẳng sẽ hiện bàn viết thành "rảnh", làm lần khởi động đang chạy trông như treo.
		if m.starting {
			next.IsRunning = true
			next.RuntimeState = "starting"
		}
		// Màn chào: snapshot đầu tiên đã biết có sách thì hỏi, không tự chạy.
		// Chỉ khi mở app (welcomeSeen=false); chuyển truyện trong phiên thì
		// switchBook đã đặt welcomeSeen=true để không hỏi lại.
		if !m.welcomeSeen && m.welcome == nil && m.mode == modeNew &&
			(next.Phase != "" || next.BookTitle != "") {
			m.snapshot = next
			m.welcome = newWelcomeState(next)
			m.textarea.Blur()
			m.refreshStateViewport()
			return m, tickSnapshot(m.runtime), true
		}
		detailChanged := !sameDetailSnapshot(m.snapshot, next)
		runningChanged := m.snapshot.IsRunning != next.IsRunning
		m.snapshot = next
		m.syncRuntimePlaceholder()
		m.refreshEventViewport()
		if runningChanged {
			m.refreshStreamViewport()
		}
		if detailChanged {
			m.refreshDetailViewport()
		}
		m.refreshStateViewport()
		return m, tickSnapshot(m.runtime), true
	case doneMsg:
		m.snapshot.IsRunning = false
		m.refreshEventViewport()
		m.refreshStreamViewport()
		m.refreshStateViewport()
		if msg.complete {
			m.abortPending = false
			m.mode = modeDone
			// Trạng thái xong không khóa khung nhập: dừng tự viết tiếp, nhưng user vẫn gõ được yêu cầu sửa (nhập ở modeDone qua
			// Continue đánh thức vòng run mới, Arbiter phán quyết sửa hay viết tiếp; lệnh /export, /model
			// cũng cần dùng được, khung nhập phải giữ focus (issue #27, #38)).
			m.textarea.Placeholder = donePlaceholder()
			return m, tea.Batch(fetchSnapshot(m.runtime), listenDone(m.runtime), m.textarea.Focus()), true
		}
		if m.abortPending {
			m.abortPending = false
			m.snapshot.RuntimeState = "paused"
			m.syncRuntimePlaceholder()
		} else {
			m.textarea.Placeholder = i18n.T("Bị ngắt, gõ gì đó để viết tiếp")
		}
		return m, tea.Batch(fetchSnapshot(m.runtime), listenDone(m.runtime)), true
	case abortResultMsg:
		if msg.stopped {
			m.abortPending = true
			m.textarea.Placeholder = i18n.T("Đang dừng viết...")
		}
		return m, nil, true
	case reportLoadedMsg:
		if m.report == nil || msg.reqID != m.report.reqID {
			return m, nil, true
		}
		boxW, _ := reportModalSize(m.width, m.height)
		m.report.load(msg.report, paddedModalContentWidth(boxW), msg.exportPath, msg.exportErr, msg.finishedAt)
		return m, nil, true
	case importEventMsg:
		if m.importer == nil || msg.reqID != m.importer.reqID {
			return m, nil, true
		}
		boxW, _ := reportModalSize(m.width, m.height)
		m.importer.appendEvent(msg.ev, paddedModalContentWidth(boxW))
		if msg.ev.Stage == imp.StageError {
			return m, nil, true
		}
		if msg.ev.Stage == imp.StageDone {
			if msg.ev.Continued {
				// host đã khởi động Engine thật để viết nối (Continued do host đặt theo quyết định chính chủ, không phải TUI đoán).
				// Đóng panel rơi về bàn viết, các listenEvents/listenDone thường trực của Init đỡ sự kiện engine, tickSnapshot mới trạng thái chạy.
				m.importer = nil
				enableMouse := m.enterRunning()
				m.resizeTextarea()
				m.textarea.Placeholder = defaultSteerPlaceholder()
				return m, tea.Batch(enableMouse, m.textarea.Focus()), true
			}
			// Không nối (mặc định/duyệt/nối thất bại): đứng ở panel chờ user đối chiếu Foundation với chương, Esc đóng.
			return m, nil, true
		}
		return m, listenImportEvent(msg.reqID, msg.ch), true
	case importClosedMsg:
		// Kênh đóng mà chưa tới trạng thái cuối → pipeline dừng ở điểm awaiting (chờ --yes / --story). Đánh dấu panel đóng được,
		// nếu không Esc chỉ hủy ctx đã xong, panel không bao giờ đóng (treo).
		if m.importer == nil || msg.reqID != m.importer.reqID || m.importer.done {
			return m, nil, true
		}
		m.importer.paused = true
		boxW, _ := reportModalSize(m.width, m.height)
		m.importer.refresh(paddedModalContentWidth(boxW))
		return m, nil, true
	case simEventMsg:
		if m.simulator == nil || msg.reqID != m.simulator.reqID {
			return m, nil, true
		}
		boxW, _ := reportModalSize(m.width, m.height)
		m.simulator.appendEvent(msg.ev, paddedModalContentWidth(boxW))
		if msg.terminal() {
			return m, nil, true
		}
		return m, listenSimulationEvent(msg.reqID, msg.ch), true
	case exportDoneMsg:
		if msg.err != nil {
			m.applyEvent(host.Event{
				Time: time.Now(), Category: "ERROR", Summary: i18n.T("Xuất thất bại: ") + msg.err.Error(), Level: "error",
			})
		} else if msg.result != nil {
			m.applyEvent(host.Event{
				Time: time.Now(), Category: "SYSTEM", Summary: formatExportSuccess(msg.result), Level: "success",
			})
		}
		m.refreshEventViewport()
		return m, nil, true
	case updateCheckMsg:
		if msg.err != nil {
			message := i18n.T("Kiểm tra bản mới lúc khởi động thất bại")
			if msg.result != nil {
				message = i18n.T("Kiểm tra bản mới lúc khởi động xong, nhưng cache bất thường")
			}
			slog.Warn(message, "module", "version", "err", msg.err)
		}
		if msg.result == nil || !msg.result.UpdateAvailable {
			return m, nil, true
		}
		notice := formatUpdateNotice(msg.result)
		m.updateHint = notice
		ev := host.Event{
			Time: time.Now(), Category: "SYSTEM", Level: "info",
			Summary: notice,
		}
		m.applyEvent(ev)
		m.refreshEventViewport()
		return m, nil, true
	case revisionDoneMsg:
		if msg.err != nil {
			m.applyEvent(host.Event{Time: time.Now(), Category: "ERROR", Summary: i18n.T("Đồng bộ chương thất bại: ") + msg.err.Error(), Level: "error"})
		} else if msg.checkOnly {
			summary := i18n.T("Không phát hiện chương nào bị sửa từ bên ngoài")
			if len(msg.chapters) > 0 {
				summary = fmt.Sprintf(i18n.T("Phát hiện nội dung chương bị sửa từ bên ngoài: %v; chạy /sync để nhận"), msg.chapters)
			}
			m.applyEvent(host.Event{Time: time.Now(), Category: "SYSTEM", Summary: summary, Level: "info"})
		} else {
			m.applyEvent(host.Event{Time: time.Now(), Category: "SYSTEM", Summary: formatRevisionResult(msg.result), Level: "success"})
		}
		m.refreshEventViewport()
		return m, tea.Batch(fetchSnapshot(m.runtime), m.textarea.Focus()), true
	case modelConfigSavedMsg:
		if m.modelConfig == nil {
			return m, nil, true
		}
		if msg.err != nil {
			m.modelConfig.saving = false
			m.modelConfig.message = msg.err.Error()
			return m, nil, true
		}
		m.modelConfig = nil
		return m, tea.Batch(fetchSnapshot(m.runtime), m.textarea.Focus()), true
	case modelConfigConnectionMsg:
		if m.modelConfig == nil {
			return m, nil, true
		}
		m.modelConfig.testing = false
		m.modelConfig.testCancel = nil
		if errors.Is(msg.err, context.Canceled) {
			m.modelConfig.message = i18n.T("Đã hủy test kết nối")
		} else if msg.err != nil {
			m.modelConfig.message = msg.err.Error()
		} else {
			m.modelConfig.message = i18n.T("Test kết nối thành công: ") + msg.model
		}
		return m, nil, true
	case startResultMsg:
		next, cmd := m.handleStartResultMsg(msg)
		return next, cmd, true
	case cocreateDeltaMsg:
		if m.cocreate == nil || msg.reqID != m.cocreate.reqID {
			return m, nil, true
		}
		m.cocreate.applyDelta(msg.kind, msg.text)
		return m, listenCoCreateDelta(m.cocreate), true
	case cocreateDoneMsg:
		next, cmd := m.handleCoCreateDoneMsg(msg)
		return next, cmd, true
	case steerResultMsg:
		if msg.err != nil {
			m.err = msg.err
			m.applyEvent(host.Event{Time: time.Now(), Category: "ERROR", Summary: msg.err.Error(), Level: "error"})
			m.refreshEventViewport()
			return m, tea.Batch(fetchSnapshot(m.runtime), m.textarea.Focus()), true
		}
		return m, tea.Batch(fetchSnapshot(m.runtime), listenDone(m.runtime)), true
	case continueResultMsg:
		if msg.err != nil {
			m.err = msg.err
			m.applyEvent(host.Event{
				Time: time.Now(), Category: "ERROR", Summary: msg.err.Error(), Level: "error",
			})
			m.refreshEventViewport()
			return m, tea.Batch(fetchSnapshot(m.runtime), m.textarea.Focus()), true
		}
		m.err = nil
		m.textarea.Placeholder = defaultSteerPlaceholder()
		return m, tea.Batch(fetchSnapshot(m.runtime), listenDone(m.runtime), m.textarea.Focus()), true
	case spinnerTickMsg:
		m.spinnerIdx = (m.spinnerIdx + 1) % len(spinnerFrames)
		m.cursorIdx++
		if m.snapshot.IsRunning {
			// Top bar, gợi ý hoạt động và con trỏ stream dùng chung animation tần thấp, tránh nhiều timer thường trực kích View toàn màn hình lặp lại.
			m.refreshEventViewport()
			m.refreshStreamViewport()
			m.streamDirty = false
		}
		if s := m.importer; s != nil && !s.done && !s.paused {
			s.frame = m.cursorIdx
			boxW, _ := reportModalSize(m.width, m.height)
			s.refresh(paddedModalContentWidth(boxW))
		}
		return m, tickSpinner(), true
	case eventSpinnerTickMsg:
		m.eventSpinnerIdx = (m.eventSpinnerIdx + 1) % len(eventSpinnerFrames)
		if m.hasRunningEvent() {
			m.refreshEventViewport()
			return m, tickEventSpinner(), true
		}
		m.eventSpinnerActive = false
		return m, nil, true
	case streamDeltaMsg:
		if len(m.streamRounds) == 0 {
			m.streamRounds = append(m.streamRounds, "")
		}
		m.streamRounds[len(m.streamRounds)-1] += string(msg)
		// Không vẽ ngay; delta đầu mở một cửa sổ gộp 16ms, delta sau dùng lại timer đó.
		m.streamDirty = true
		cmd := listenStream(m.runtime)
		if !m.flushPending {
			m.flushPending = true
			cmd = tea.Batch(cmd, tickStreamFlush())
		}
		return m, cmd, true
	case streamClearMsg:
		// Biên round: xả delta dồn trước, round mới mới canh nhìn khớp
		if m.flushStreamIfDirty() && m.streamScroll {
			m.streamVP.GotoBottom()
		}
		if len(m.streamRounds) == 0 {
			m.streamRounds = append(m.streamRounds, "")
		} else if strings.TrimSpace(m.streamRounds[len(m.streamRounds)-1]) != "" {
			m.streamRounds = append(m.streamRounds, "")
		}
		m.trimStreamRounds()
		m.streamRound = len(m.streamRounds)
		m.refreshStreamViewport()
		if m.streamScroll {
			m.streamVP.GotoBottom()
		}
		return m, listenStream(m.runtime), true
	case streamFlushTickMsg:
		m.flushPending = false
		if m.flushStreamIfDirty() && m.streamScroll {
			m.streamVP.GotoBottom()
		}
		return m, nil, true
	case quitResetMsg:
		m.quitPending = false
		return m, nil, true
	default:
		return m, nil, false
	}
}

func sameDetailSnapshot(a, b host.UISnapshot) bool {
	return a.Synopsis == b.Synopsis &&
		a.Premise == b.Premise &&
		a.Layered == b.Layered &&
		a.CurrentVolumeArc == b.CurrentVolumeArc &&
		a.NextVolumeTitle == b.NextVolumeTitle &&
		a.CompassDirection == b.CompassDirection &&
		a.CompassScale == b.CompassScale &&
		a.SupportingCount == b.SupportingCount &&
		a.CompletedCount == b.CompletedCount &&
		a.InProgressChapter == b.InProgressChapter &&
		a.LastCommitSummary == b.LastCommitSummary &&
		a.LastReviewSummary == b.LastReviewSummary &&
		slices.Equal(a.Outline, b.Outline) &&
		slices.Equal(a.Characters, b.Characters) &&
		slices.Equal(a.RecentSupporting, b.RecentSupporting) &&
		slices.Equal(a.RecentSummaries, b.RecentSummaries)
}

func (m Model) handleStartResultMsg(msg startResultMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		// Truyện chưa khoá ngôn ngữ sáng tác: hỏi, không báo lỗi. Hỏi ở LẦN VIẾT
		// đầu tiên — /read là thao tác chỉ đọc, không đi qua đây nên không bị hỏi.
		if errors.Is(msg.err, host.ErrNeedBookLanguage) {
			prompt := m.textarea.Value()
			if m.cocreate != nil {
				// Cocreate giữ ý nháp riêng, không nằm trong textarea.
				prompt = m.cocreate.draftPrompt()
			}
			m.starting = false
			m.mode = modeRunning
			m.snapshot.IsRunning = false
			m.bookLang = newBookLanguageState(prompt, m.runtime.Dir())
			m.cocreate = nil
			m.textarea.Blur()
			return m, nil
		}
		m.err = msg.err
		wasStarting := m.starting
		m.starting = false
		if m.mode != modeNew {
			m.applyEvent(host.Event{
				Time: time.Now(), Category: "ERROR", Summary: msg.err.Error(), Level: "error",
			})
			m.refreshEventViewport()
		}
		if m.cocreate != nil {
			m.cocreate.awaiting = false
			m.textarea.Placeholder = placeholderForCoCreate(m.cocreate)
			return m, tea.Batch(fetchSnapshot(m.runtime), m.textarea.Focus())
		}
		if wasStarting {
			// Enter xong đã vào bàn viết; lỗi LLM giai đoạn khởi động hiện ngay ở bàn viết hiện tại,
			// không về trang chào nữa.
			m.mode = modeRunning
			m.snapshot.IsRunning = false
			m.snapshot.RuntimeState = "idle"
			m.textarea.Placeholder = i18n.T("Khởi động thất bại, kiểm tra cấu hình model hoặc /model đổi model")
			m.refreshStreamViewport()
			m.refreshStateViewport()
			return m, m.textarea.Focus()
		}
		if m.mode == modeNew {
			m.textarea.Placeholder = placeholderForNewMode(m.startupMode)
			return m, tea.Batch(fetchSnapshot(m.runtime), m.textarea.Focus())
		}
		return m, fetchSnapshot(m.runtime)
	}
	m.starting = false

	if m.mode == modeNew {
		m.cocreate = nil
		enableMouse := m.enterRunning()
		m.resizeTextarea()
		m.textarea.Placeholder = defaultSteerPlaceholder()
		return m, tea.Batch(fetchSnapshot(m.runtime), m.textarea.Focus(), enableMouse)
	}

	return m, fetchSnapshot(m.runtime)
}

func (m *Model) enterStarting(rawPrompt string) tea.Cmd {
	m.cocreate = nil
	m.err = nil
	m.starting = true
	m.snapshot.IsRunning = true
	m.snapshot.RuntimeState = "starting"
	enableMouse := m.enterRunning()
	m.resetOutputPanels()
	m.resizeTextarea()
	m.textarea.Placeholder = i18n.T("Đang khởi tạo truyện...")
	m.applyStartupPromptEvent(rawPrompt)
	m.applyEvent(host.Event{
		Time: time.Now(), Category: "SYSTEM", Summary: i18n.T("Đang khởi tạo truyện"), Level: "info",
	})
	m.refreshEventViewport()
	m.refreshStreamViewport()
	m.refreshStateViewport()
	return tea.Batch(m.textarea.Focus(), enableMouse)
}

func (m *Model) applyStartupPromptEvent(rawPrompt string) {
	text := utils.CleanInputLine(rawPrompt)
	if text == "" {
		return
	}
	m.applyEvent(host.Event{
		Time:     time.Now(),
		Category: "USER",
		Summary:  i18n.T("Yêu cầu truyện: ") + truncate(text, maxPromptEventCols),
		Detail:   text,
		Level:    "info",
	})
}

func (m Model) handleCoCreateDoneMsg(msg cocreateDoneMsg) (tea.Model, tea.Cmd) {
	if m.cocreate == nil || msg.reqID != m.cocreate.reqID {
		return m, nil
	}
	if msg.err != nil {
		m.err = msg.err
		m.cocreate.awaiting = false
		m.textarea.Placeholder = placeholderForCoCreate(m.cocreate)
		return m, m.textarea.Focus()
	}
	m.err = nil
	m.cocreate.apply(msg.reply)
	m.textarea.Placeholder = placeholderForCoCreate(m.cocreate)
	return m, m.textarea.Focus()
}

func (m Model) handleTextareaMsg(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	m.textarea, cmd = m.textarea.Update(msg)
	m.refitTextareaHeight()
	m.updateCommandPalette()
	return m, cmd
}

// applyEvent ghi một sự kiện do TUI sinh ở local rồi cập nhật projection. Sự kiện Host đã
// ghi log ở đầu sinh, đường subscribe sự kiện nên gọi thẳng applyEventProjection, tránh ghi lặp.
func (m *Model) applyEvent(ev host.Event) {
	host.LogEvent(ev)
	m.applyEventProjection(ev)
}

// applyEventProjection áp một sự kiện vào m.events:
// - Có ID và đã có → cập nhật tại chỗ (gộp trường trạng thái xong, giữ Time / Summary lần đầu)
// - Sự kiện mới → thêm vào, khi cần ghi vào eventIndex
// - Quá maxEvents thì cắt trượt và dựng lại index
func (m *Model) applyEventProjection(ev host.Event) {
	if ev.ID != "" {
		if idx, ok := m.eventIndex[ev.ID]; ok && idx >= 0 && idx < len(m.events) {
			existing := &m.events[idx]
			if !ev.FinishedAt.IsZero() {
				existing.FinishedAt = ev.FinishedAt
			}
			if ev.Duration > 0 {
				existing.Duration = ev.Duration
			}
			if ev.Failed {
				existing.Failed = true
			}
			if ev.Level != "" {
				existing.Level = ev.Level
			}
			if ev.Detail != "" {
				existing.Detail = ev.Detail
			}
			if ev.Kind != "" {
				existing.Kind = ev.Kind
			}
			// Summary khác rỗng thì cho ghi đè (trạng thái kết thúc có thể kèm thêm tin); còn không giữ lần đầu
			if ev.Summary != "" {
				existing.Summary = ev.Summary
			}
			// Sự kiện thử lại cùng ID cập nhật xuyên attempt, hạn mới phải theo cùng, đếm ngược mới reset theo
			if !ev.RetryAt.IsZero() {
				existing.RetryAt = ev.RetryAt
			}
			return
		}
	}

	m.events = append(m.events, ev)
	if ev.ID != "" {
		m.eventIndex[ev.ID] = len(m.events) - 1
	}
	if len(m.events) > maxEvents {
		drop := len(m.events) - maxEvents
		m.events = m.events[drop:]
		m.rebuildEventIndex()
	}
}

// trimStreamRounds cắt streamRounds về tối đa maxStreamRounds đoạn; thừa bỏ từ đầu.
// Gọi lúc: mỗi lần streamClear mở vòng mới.
func (m *Model) trimStreamRounds() {
	if len(m.streamRounds) <= maxStreamRounds {
		return
	}
	drop := len(m.streamRounds) - maxStreamRounds
	m.streamRounds = m.streamRounds[drop:]
}

func (m *Model) rebuildEventIndex() {
	m.eventIndex = make(map[string]int, len(m.events))
	for i, e := range m.events {
		if e.ID != "" {
			m.eventIndex[e.ID] = i
		}
	}
}

func (m *Model) resetOutputPanels() {
	m.events = nil
	m.eventIndex = make(map[string]int)
	m.viewport.SetContent("")
	m.viewport.GotoTop()
	m.streamBuf.Reset()
	m.streamRounds = nil
	m.streamVP.SetContent("")
	m.streamVP.GotoTop()
	m.streamRound = 0
}
