package tui

import (
	"context"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	"github.com/charmbracelet/lipgloss"
	"github.com/voocel/ainovel-cli/internal/entry/startup"
	"github.com/voocel/ainovel-cli/internal/host"
)

type startupMode int

const (
	startupModeQuick startupMode = iota
	startupModeCoCreate
)

func (m startupMode) label() string {
	switch m {
	case startupModeCoCreate:
		return "Đồng sáng tác"
	default:
		return "Bắt đầu nhanh"
	}
}

func (m startupMode) subtitle() string {
	switch m {
	case startupModeCoCreate:
		return "Trò chuyện với AI cho rõ ý rồi mới viết"
	default:
		return "Một câu là viết luôn"
	}
}

func placeholderForNewMode(mode startupMode) string {
	switch mode {
	case startupModeCoCreate:
		return "Nhập ý tưởng cốt lõi, Enter để cùng AI sáng tác"
	default:
		return "Nhập một câu nhu cầu truyện, Enter là viết luôn"
	}
}

func placeholderForCoCreate(state *cocreateState) string {
	if state == nil {
		return placeholderForNewMode(startupModeCoCreate)
	}
	switch {
	case state.awaiting:
		return "AI đang sắp xếp yêu cầu của bạn..."
	case state.canStart():
		if state.stage {
			return "Gõ thêm, hoặc Ctrl+S để chốt hướng và viết tiếp"
		}
		return "Gõ thêm, hoặc Ctrl+S để bắt đầu viết"
	default:
		return "Gõ thêm yêu cầu, Enter gửi cho AI"
	}
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return strings.TrimSpace(err.Error())
}

type cocreateState struct {
	session             *startup.CoCreateSession
	stage               bool // true=đồng sáng tác giai đoạn (đang viết thì lên hướng tiếp); false=đồng sáng tác khởi động lạnh (làm rõ nhu cầu trước khi viết)
	awaiting            bool
	reqID               int
	cancel              context.CancelFunc // Hủy request LLM hiện tại
	deltaCh             chan cocreateStreamItem
	doneCh              chan cocreateDoneMsg
	convVP              viewport.Model
	promptVP            viewport.Model
	convFollow          bool // true: nội dung stream mới tự lăn xuống đáy; user cuộn lên thì false dừng theo
	selectedSuggestions []string
	// focusPrompt quyết định ↑↓/PgUp/PgDn/Home/End cuộn cột nào: false=cột hội thoại trái (mặc định),
	// true=cột chỉ đạo viết phải. Trang chào đã tắt báo cáo chuột (giữ copy nguyên bản), cột phải tràn thì Tab đổi focus rồi cuộn phím.
	focusPrompt bool
}

func newCoCreateState(initial string) *cocreateState {
	makeVP := func() viewport.Model {
		vp := viewport.New(0, 0)
		vp.MouseWheelEnabled = true
		vp.MouseWheelDelta = 3
		return vp
	}
	return &cocreateState{
		session:    startup.NewCoCreateSession(strings.TrimSpace(initial)),
		awaiting:   true,
		convVP:     makeVP(),
		promptVP:   makeVP(),
		convFollow: true,
	}
}

// stageCoCreateOpener là câu mở tổng hợp của đồng sáng tác giai đoạn, gửi cho LLM như lượt user kickoff,
// để trợ lý dựa "trạng thái truyện hiện tại" mở lời chủ động, thay vì để hội thoại trống chờ user nói trước.
const stageCoCreateOpener = "Tôi tạm dừng chút, muốn cùng bạn lên hướng cho đoạn tiếp."

// stageCoCreateSystemLine là cách hiện trung tính của câu mở này trong UI: câu mở thực chất do hệ thống tổng hợp,
// user chưa từng gõ, nên không giả làm phát ngôn của "bạn", mà ghi một dòng hệ thống cho rõ ngữ cảnh (nó vẫn gửi cho LLM
// bằng stageCoCreateOpener, xem nhánh đặc biệt i==0 trong renderCoCreateConversationPanel).
const stageCoCreateSystemLine = "Đã dừng viết, vào đồng sáng tác giai đoạn — AI sẽ dựa tiến độ hiện tại cùng bạn lên hướng tiếp theo."

// newStageCoCreateState tạo trạng thái đồng sáng tác giai đoạn: gieo câu mở và đánh dấu stage, để runCoCreate đi
// StageCoCreateStream, Ctrl+S đi ResumeFromCoCreate.
func newStageCoCreateState() *cocreateState {
	s := newCoCreateState(stageCoCreateOpener)
	s.stage = true
	return s
}

func (s *cocreateState) appendUser(text string) {
	s.resetSuggestionInput()
	s.session.AppendUser(text)
}

func (s *cocreateState) apply(reply host.CoCreateReply) {
	s.awaiting = false
	s.resetSuggestionInput()
	s.session.ApplyReply(reply)
}

func (s *cocreateState) applyDelta(kind, text string) {
	s.session.ApplyDelta(kind, text)
}

func (s *cocreateState) canStart() bool {
	return s.session.CanStart()
}

func (s *cocreateState) initialInput() string {
	return s.session.InitialInput()
}

func (s *cocreateState) streamReply() string {
	return s.session.StreamReply()
}

func (s *cocreateState) draftPrompt() string {
	return s.session.DraftPrompt()
}

func (s *cocreateState) ready() bool {
	return s.session.Ready()
}

func (s *cocreateState) suggestions() []string {
	return s.session.Suggestions()
}

// appendSuggestion thêm gợi ý ứng với phím số vào ô nhập sinh bởi phím tắt.
// User vừa sửa tay ô nhập, current không còn bằng tổ hợp với gợi ý đã chọn, phím số về lại ngữ nghĩa nhập thường.
func (s *cocreateState) appendSuggestion(index int, current string) (string, bool) {
	suggestions := s.suggestions()
	if index < 0 || index >= len(suggestions) {
		return "", false
	}
	if len(s.selectedSuggestions) == 0 {
		if strings.TrimSpace(current) != "" {
			return "", false
		}
	} else if current != strings.Join(s.selectedSuggestions, "; ") {
		s.resetSuggestionInput()
		return "", false
	}

	suggestion := strings.TrimSpace(suggestions[index])
	for _, selected := range s.selectedSuggestions {
		if selected == suggestion {
			return current, true
		}
	}

	s.selectedSuggestions = append(s.selectedSuggestions, suggestion)
	return strings.Join(s.selectedSuggestions, "; "), true
}

func (s *cocreateState) resetSuggestionInput() {
	s.selectedSuggestions = nil
}

func (s *cocreateState) buildPrompt() (string, error) {
	return s.session.BuildPrompt()
}

func renderStartupModeBar(width int, mode startupMode) string {
	quick := renderStartupModePill(mode == startupModeQuick, "Bắt đầu nhanh")
	cocreate := renderStartupModePill(mode == startupModeCoCreate, "Đồng sáng tác")
	title := lipgloss.NewStyle().
		Foreground(colorAccent).
		Bold(true).
		Render("Chế độ khởi động")
	divider := lipgloss.NewStyle().
		Foreground(colorDim).
		Render("·")
	line := title + " " + divider + " " + quick + "  " + cocreate
	return lipgloss.NewStyle().
		Width(width).
		Padding(0, 1).
		Render(line)
}

func renderStartupModePill(active bool, label string) string {
	style := lipgloss.NewStyle().Padding(0, 1)
	if active {
		style = style.Foreground(lipgloss.Color("#1c1a14")).Background(colorAccent).Bold(true)
	} else {
		style = style.Foreground(colorMuted)
	}
	return style.Render(label)
}

// coCreateColumns cắt vùng nội dung modal thành hai cột rộng.
// Cột trái gánh hội thoại với khung nhập (chồng dọc), cột phải gánh nháp chỉ đạo viết; tổng bằng rộng nội dung modal.
func coCreateColumns(bodyW int) (leftW, rightW int) {
	leftW = bodyW * 58 / 100
	if leftW < 42 {
		leftW = bodyW / 2
	}
	rightW = bodyW - leftW
	if rightW < 28 {
		rightW = 28
		leftW = bodyW - rightW
	}
	return leftW, rightW
}

func renderCoCreateBody(width, height int, state *cocreateState, errMsg, inputView string, spinnerFrame int) string {
	if state == nil {
		return ""
	}
	leftW, rightW := coCreateColumns(width)

	// Viền phải do khung leftCol ngoài vẽ, xuyên từ đỉnh body tới đáy; conversation / suggestions /
	// input đều không vẽ viền phải riêng. input vẫn là khung bo tròn đủ, lề trái phải mỗi bên 1 cột canh với
	// padding của conversation, nhìn khoảng cách tới đường biên hai bên đều nhau.
	// Chế độ đồng sáng tác textarea cố định 1 dòng (xem nhánh trong model.refitTextareaHeight),
	// cao input = 1 (textarea) + 2 (viền trên/dưới) = 3 dòng, không bao giờ trôi.
	innerW := leftW - 1 // Chừa 1 cột cho viền đứng phải ngoài

	inputBox := lipgloss.NewStyle().
		Width(innerW-6). // -2 margin -2 padding -2 border
		Border(baseBorder).
		BorderForeground(colorDim).
		Padding(0, 1).
		Margin(0, 1).
		Render(inputView)

	suggestionsBox := renderCoCreateSuggestions(innerW, state)
	suggestionsH := 0
	if suggestionsBox != "" {
		suggestionsH = lipgloss.Height(suggestionsBox)
	}

	convH := height - lipgloss.Height(inputBox) - suggestionsH
	if convH < 4 {
		convH = 4
	}

	convPanel := renderCoCreateConversationPanel(innerW, convH, state, errMsg, spinnerFrame)

	var stack string
	if suggestionsBox == "" {
		stack = lipgloss.JoinVertical(lipgloss.Left, convPanel, inputBox)
	} else {
		stack = lipgloss.JoinVertical(lipgloss.Left, convPanel, suggestionsBox, inputBox)
	}

	leftCol := lipgloss.NewStyle().
		Border(baseBorder, false, true, false, false).
		BorderForeground(colorDim).
		Render(stack)

	rightPanel := renderCoCreatePromptPanel(rightW, height, state)
	return lipgloss.JoinHorizontal(lipgloss.Top, leftCol, rightPanel)
}

// extractReplyForDisplay cắt đoạn <reply>...</reply> từ nội dung lịch sử assistant.
// Các thẻ khác (<draft>/<ready>/<suggestions>) là trường giao thức cho lượt model sau, không phơi trần cho user.
// Model tuân nửa vời (rớt thẻ mở <reply>) thì từ đầu tới </reply> hoặc thẻ mở tiếp theo đều tính là reply.
// Không chứa thẻ nào (đường dự phòng) thì trả nguyên.
func extractReplyForDisplay(content string) string {
	rest := content
	if rIdx := strings.Index(content, "<reply>"); rIdx >= 0 {
		rest = content[rIdx+len("<reply>"):]
	}
	if cIdx := strings.Index(rest, "</reply>"); cIdx >= 0 {
		return strings.TrimSpace(rest[:cIdx])
	}
	cut := len(rest)
	for _, mark := range []string{"<draft>", "<ready>", "<suggestions>"} {
		if idx := strings.Index(rest, mark); idx >= 0 && idx < cut {
			cut = idx
		}
	}
	if cut == len(rest) && !strings.Contains(content, "<") {
		return content
	}
	return strings.TrimSpace(rest[:cut])
}

// renderCoCreateSuggestions vẽ dòng gợi ý AI trên khung nhập. Lúc awaiting hoặc không có gợi ý
// trả chuỗi rỗng để layout tự xẹp không chừa dòng trống. Tối đa 3 gợi ý, bấm phím số 1/2/3 để chọn.
func renderCoCreateSuggestions(width int, state *cocreateState) string {
	if state == nil || state.awaiting {
		return ""
	}
	sugs := state.suggestions()
	if len(sugs) == 0 {
		return ""
	}
	if len(sugs) > 3 {
		sugs = sugs[:3]
	}

	digits := []string{"❶", "❷", "❸"}
	digitStyle := lipgloss.NewStyle().Foreground(colorAccent).Bold(true)
	bodyStyle := lipgloss.NewStyle().Foreground(colorMuted)
	hintStyle := lipgloss.NewStyle().Foreground(colorDim).Italic(true)

	lines := []string{hintStyle.Render("Gợi ý của AI (bấm 1/2/3 để ghép, sửa rồi gửi):")}
	for i, s := range sugs {
		lines = append(lines, digitStyle.Render(digits[i]+" ")+bodyStyle.Render(strings.TrimSpace(s)))
	}

	// Canh với inputBox ở lề/padding trái phải: trái 2 cột (margin1+padding1), phải cũng vậy.
	return lipgloss.NewStyle().
		Width(width-2).
		Padding(0, 2).
		Render(strings.Join(lines, "\n"))
}

func coCreateModalSize(width, height int) (boxW, boxH int) {
	if width <= 0 {
		width = 100
	}
	if height <= 0 {
		height = 24
	}
	boxW = min(max(width*76/100, 88), width-4)
	boxH = min(max(height*72/100, 22), height-4)
	if boxW < 64 {
		boxW = max(width-2, 42)
	}
	if boxH < 14 {
		boxH = max(height-2, 12)
	}
	return boxW, boxH
}

// coCreateInputWidth tính rộng ký tự nhập thực của textarea.
// Trang trí cột trái: viền đứng phải ngoài 1 + lề trái phải input 2 + viền 2 + padding 2 = 7 cột;
// bản thân textarea prompt+cursor chiếm 2 cột; nên textareaW = leftW - 9.
func coCreateInputWidth(width, height int) int {
	boxW, _ := coCreateModalSize(width, height)
	bodyW := boxW - 4
	leftW, _ := coCreateColumns(bodyW)
	inputW := leftW - 9
	if inputW < 20 {
		inputW = 20
	}
	return inputW
}

func renderCoCreateModal(width, height int, state *cocreateState, errMsg, inputView string, spinnerFrame int, quitPending bool) string {
	if state == nil {
		return ""
	}

	boxW, boxH := coCreateModalSize(width, height)

	// title / subtitle / hint để ngoài modal (căn giữa trên và dưới), để trong modal
	// toàn cho body —— viền đứng phải cột trái với cột phải xuyên từ đỉnh modal tới đáy.
	// Modal chiếm thực = boxH (nội dung) + 2 (padding 1*2) + 2 (viền) = boxH+4 dòng;
	// stack tổng = title(1) + subtitle(1) + trống(1) + modal(boxH+4) + trống(1) + hint(1) = boxH+9.
	// Nên bớt boxH 5 dòng ngân sách cho đồ trang ngoài modal, tránh tràn terminal.
	contentH := boxH - 5
	if contentH < 10 {
		contentH = 10
	}

	titleText, subtitleText := "Đồng sáng tác", "Nói rõ nhu cầu rồi mới viết"
	if state.stage {
		titleText, subtitleText = "Đồng sáng tác giai đoạn", "Lên hướng tiếp theo rồi viết tiếp"
	}
	headerStyle := lipgloss.NewStyle().Width(boxW).AlignHorizontal(lipgloss.Center)
	title := headerStyle.Foreground(colorMuted).Bold(true).Render(titleText)
	subtitle := headerStyle.Foreground(colorDim).Italic(true).Render(subtitleText)

	var hintLine string
	hintStyle := lipgloss.NewStyle().Width(boxW).AlignHorizontal(lipgloss.Center)
	if quitPending {
		// quitPending đồng bộ với inputHints(); nếu không modal đồng sáng tác che đáy, user không cảm được "bấm Ctrl+C lần nữa".
		hintLine = hintStyle.Foreground(lipgloss.Color("243")).Bold(true).Render("Nhấn Ctrl+C lần nữa để thoát")
	} else {
		hintLine = hintStyle.Foreground(colorDim).Italic(true).Render(coCreateHint(state))
	}

	body := renderCoCreateBody(boxW-4, contentH, state, errMsg, inputView, spinnerFrame)
	box := lipgloss.NewStyle().
		Width(boxW).
		Height(contentH).
		Border(baseBorder).
		BorderForeground(colorAccent).
		Padding(1, 2).
		Render(body)

	stack := lipgloss.JoinVertical(lipgloss.Center, title, subtitle, "", box, "", hintLine)
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, stack)
}

// coCreateHint sinh gợi ý phím ngắn theo trạng thái, tránh lặp ngữ nghĩa với placeholder.
func coCreateHint(state *cocreateState) string {
	switch {
	case state == nil:
		return "Enter gửi · Esc thoát"
	case state.awaiting:
		return "AI đang trả lời · ↑↓ cuộn hội thoại · lăn chuột cuộn chỉ đạo · Esc thoát"
	case state.canStart():
		action := "Ctrl+S bắt đầu viết"
		if state.stage {
			action = "Ctrl+S chốt và viết tiếp"
		}
		return "Enter bổ sung tiếp · " + action + " · ↑↓ cuộn hội thoại · lăn chuột cuộn chỉ đạo · Esc thoát"
	default:
		return "Enter gửi · ↑↓ cuộn hội thoại · lăn chuột cuộn chỉ đạo · Esc thoát"
	}
}

func renderCoCreateConversationPanel(width, height int, state *cocreateState, errMsg string, spinnerFrame int) string {
	// Không vẽ viền riêng — viền đứng phải do khung leftCol ngoài vẽ thống nhất.
	// Tổng rộng cột = width; style.Width = contentW = width-2; Padding(0,1) xong vùng nội dung = contentW-2.
	// Trong dòng còn trừ tiền tố 2 cột kiểu "▌ " / "  ", nếu không mỗi dòng + tiền tố tràn vùng nội dung 2 cột,
	// kích hoạt terminal gập dòng vật lý — lipgloss vẫn tưởng cao modal cố định, nhưng cao vẽ thực của terminal tăng,
	// lúc stream thinking kích liên tục sẽ thấy khung ngoài "giật cao". Nên wrapW = contentW - 4.
	contentW := width - 2
	if contentW < 12 {
		contentW = 12
	}
	wrapW := max(12, contentW-4)

	userRole := lipgloss.NewStyle().Foreground(colorAccent2).Bold(true).Render("Bạn")
	aiRole := lipgloss.NewStyle().Foreground(colorAccent).Bold(true).Render("AI")
	userBody := lipgloss.NewStyle().Foreground(colorAccent2)
	aiBody := lipgloss.NewStyle().Foreground(bodyTextColor)
	thinkingStyle := lipgloss.NewStyle().Foreground(colorDim).Italic(true)
	thinkingTag := lipgloss.NewStyle().Foreground(colorDim).Bold(true).Render("AI đang nghĩ")

	sysStyle := lipgloss.NewStyle().Foreground(colorDim).Italic(true)

	var lines []string
	for i, item := range state.session.History() {
		isUser := item.Role != "assistant"
		// Câu mở tổng hợp của đồng sáng tác giai đoạn (luôn là tin user history[0]) hiện bằng dòng hệ thống trung tính,
		// không giả làm nhập của user; nó vẫn gửi cho LLM như lượt user kickoff.
		if isUser && state.stage && i == 0 {
			for j, line := range wrapStreamText(stageCoCreateSystemLine, wrapW) {
				prefix := "· "
				if j > 0 {
					prefix = "  "
				}
				lines = append(lines, sysStyle.Render(prefix+line))
			}
			lines = append(lines, "")
			continue
		}
		if isUser {
			lines = append(lines, userRole)
			for _, line := range wrapStreamText(strings.TrimSpace(item.Content), wrapW) {
				// Cả dòng Render một lần, tránh ANSI reset màu tiền tố nối với màu chữ rỉ màu.
				lines = append(lines, userBody.Render("▌ "+line))
			}
		} else {
			lines = append(lines, aiRole)
			// assistant trong history giữ Raw đủ bốn đoạn (cho ngữ cảnh model), UI chỉ hiện đoạn [REPLY].
			display := extractReplyForDisplay(item.Content)
			for _, line := range wrapStreamText(strings.TrimSpace(display), wrapW) {
				lines = append(lines, aiBody.Render("  "+line))
			}
		}
		lines = append(lines, "")
	}

	if state.awaiting {
		if t := state.session.StreamThinking(); t != "" {
			lines = append(lines, thinkingTag)
			for _, line := range wrapStreamText(t, wrapW) {
				lines = append(lines, thinkingStyle.Render("  "+line))
			}
			lines = append(lines, "")
		}
		if state.streamReply() != "" {
			lines = append(lines, aiRole)
			for _, line := range wrapStreamText(state.streamReply(), wrapW) {
				lines = append(lines, aiBody.Render("  "+line))
			}
			lines = append(lines, "")
		}
		// Trang trí sparkle: để user luôn thấy "AI đang làm"
		lines = append(lines, strings.TrimLeft(renderEventSparkle(spinnerFrame, contentW), " "))
	}
	if errMsg != "" {
		lines = append(lines, "")
		lines = append(lines, lipgloss.NewStyle().Foreground(colorError).Render("! "+errMsg))
	}

	// Dùng viewport thay cắt tay, để user cuộn lại xem.
	// Cao vp = cao panel - 1 dòng tiêu đề. SetContent xong nếu user vốn ở đáy,
	// tự lăn tới mới nhất (theo stream); user cuộn lên tắt convFollow thì dừng theo.
	vpH := height - 1
	if vpH < 1 {
		vpH = 1
	}
	if state.convVP.Width != contentW || state.convVP.Height != vpH {
		state.convVP.Width = contentW
		state.convVP.Height = vpH
	}
	state.convVP.SetContent(strings.Join(lines, "\n"))
	if state.convFollow {
		state.convVP.GotoBottom()
	}

	style := lipgloss.NewStyle().
		Width(contentW).
		Height(height).
		Padding(0, 1)
	return style.Render(panelTitleStyle.Render(":: Hội thoại đồng sáng tác") + "\n" + state.convVP.View())
}

func renderCoCreatePromptPanel(width, height int, state *cocreateState) string {
	readyLabel := "Đã viết được"
	if state.stage {
		readyLabel = "Đã chốt được để viết tiếp"
	}
	status := lipgloss.NewStyle().Foreground(colorDim).Render("Đang trò chuyện")
	if state.ready() {
		status = lipgloss.NewStyle().Foreground(colorAccent).Render(readyLabel)
	}
	if state.awaiting {
		status = lipgloss.NewStyle().Foreground(colorMuted).Italic(true).Render("AI đang tổng hợp")
	}

	// Rộng nội dung = tổng rộng cột - 2 (padding 0,1 chiếm 2 cột, không viền).
	contentW := width - 2
	if contentW < 8 {
		contentW = 8
	}

	emptyHint := "AI sẽ tổng hợp dần ở đây thành chỉ đạo chốt để vào viết."
	panelTitle := ":: Chỉ đạo viết hiện tại"
	if state.stage {
		emptyHint = "AI sẽ tổng hợp dần ở đây thành hướng cho giai đoạn tiếp."
		panelTitle = ":: Hướng tiếp theo"
	}
	text := strings.TrimSpace(state.draftPrompt())
	if text == "" {
		text = lipgloss.NewStyle().Foreground(colorDim).Italic(true).Render(emptyHint)
	} else {
		text = renderMarkdownPreview(text, max(12, contentW-2))
	}
	vpHeight := height - 5
	if vpHeight < 3 {
		vpHeight = 3
	}
	if state.promptVP.Width != contentW || state.promptVP.Height != vpHeight {
		state.promptVP.Width = contentW
		state.promptVP.Height = vpHeight
	}
	state.promptVP.MouseWheelEnabled = true
	state.promptVP.SetContent(text)

	hint := ""
	if state.promptVP.TotalLineCount() > state.promptVP.VisibleLineCount() {
		switch {
		case state.promptVP.AtTop():
			hint = "↓ Còn nội dung dưới, lăn chuột hoặc PgDn để xem"
		case state.promptVP.AtBottom():
			hint = "↑ Còn nội dung trên, lăn chuột hoặc PgUp để xem"
		default:
			hint = "↑↓ cuộn tiếp để xem"
		}
	}

	style := lipgloss.NewStyle().
		Width(contentW).
		Height(height).
		Padding(0, 1)

	body := panelTitleStyle.Render(panelTitle) + "\n" + status + "\n\n" + state.promptVP.View()
	if hint != "" {
		body += "\n\n" + lipgloss.NewStyle().
			Width(contentW).
			AlignHorizontal(lipgloss.Center).
			Foreground(colorDim).
			Italic(true).
			Render(hint)
	}
	return style.Render(body)
}

func renderMarkdownPreview(text string, width int) string {
	lines := strings.Split(strings.ReplaceAll(strings.TrimSpace(text), "\r\n", "\n"), "\n")
	if len(lines) == 0 {
		return ""
	}

	h1Style := lipgloss.NewStyle().Foreground(colorAccent).Bold(true)
	h2Style := lipgloss.NewStyle().Foreground(colorAccent2).Bold(true)
	h3Style := lipgloss.NewStyle().Foreground(colorMuted).Bold(true)
	bulletStyle := lipgloss.NewStyle().Foreground(colorAccent2).Bold(true)
	codeStyle := lipgloss.NewStyle().Foreground(colorMuted).Italic(true)

	var out []string
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" {
			out = append(out, "")
			continue
		}

		switch {
		case strings.HasPrefix(line, "# "):
			title := strings.TrimSpace(strings.TrimPrefix(line, "# "))
			out = append(out, h1Style.Render(title))
		case strings.HasPrefix(line, "## "):
			title := strings.TrimSpace(strings.TrimPrefix(line, "## "))
			out = append(out, h2Style.Render(title))
		case strings.HasPrefix(line, "### "):
			title := strings.TrimSpace(strings.TrimPrefix(line, "### "))
			out = append(out, h3Style.Render(title))
		case strings.HasPrefix(line, "- "), strings.HasPrefix(line, "* "):
			body := strings.TrimSpace(line[2:])
			wrapped := wrapStreamText(body, max(8, width-4))
			for i, item := range wrapped {
				if i == 0 {
					out = append(out, bulletStyle.Render("• ")+cardContentStyle.Render(item))
				} else {
					out = append(out, "  "+cardContentStyle.Render(item))
				}
			}
		case isOrderedMarkdownItem(line):
			prefix, body := splitOrderedMarkdownItem(line)
			wrapped := wrapStreamText(body, max(8, width-len(prefix)-2))
			for i, item := range wrapped {
				if i == 0 {
					out = append(out, bulletStyle.Render(prefix+" ")+cardContentStyle.Render(item))
				} else {
					out = append(out, strings.Repeat(" ", len(prefix)+1)+cardContentStyle.Render(item))
				}
			}
		case strings.HasPrefix(line, "> "):
			body := strings.TrimSpace(strings.TrimPrefix(line, "> "))
			for _, item := range wrapStreamText(body, max(8, width-4)) {
				out = append(out, codeStyle.Render("│ "+item))
			}
		default:
			for _, item := range wrapStreamText(line, width) {
				out = append(out, cardContentStyle.Render(item))
			}
		}
	}
	return strings.Join(out, "\n")
}

func isOrderedMarkdownItem(line string) bool {
	if len(line) < 3 {
		return false
	}
	i := 0
	for i < len(line) && line[i] >= '0' && line[i] <= '9' {
		i++
	}
	return i > 0 && i+1 < len(line) && line[i] == '.' && line[i+1] == ' '
}

func splitOrderedMarkdownItem(line string) (prefix, body string) {
	i := 0
	for i < len(line) && line[i] >= '0' && line[i] <= '9' {
		i++
	}
	if i == 0 || i+1 >= len(line) {
		return "", strings.TrimSpace(line)
	}
	return line[:i+1], strings.TrimSpace(line[i+2:])
}
