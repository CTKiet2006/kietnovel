package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/voocel/agentcore"
	"github.com/voocel/ainovel-cli/internal/host"
)

type modelRuntime interface {
	ConfiguredProviders() []string
	ConfiguredModelOptions(provider string) []host.ConfiguredModel
	CurrentModelSelection(role string) (string, string, bool)
	AvailableThinking(role string) []agentcore.ThinkingLevel
	CurrentThinking(role string) string
	SwitchModel(role, provider, model string) error
	SetRoleThinking(role, level string) error
}

type modelSwitchFocus int

const (
	modelFocusRole modelSwitchFocus = iota
	modelFocusProvider
	modelFocusModel
	modelFocusThinking
)

type modelRoleOption struct {
	Key   string
	Label string
}

var modelRoleOptions = []modelRoleOption{
	{Key: "default", Label: "Mặc định"},

	{Key: "architect", Label: "Architect"},
	{Key: "writer", Label: "Writer"},
	{Key: "editor", Label: "Editor"},
}

type thinkingOption struct{ Key, Label string }

var allThinkingOptions = []thinkingOption{
	{"", "Mặc định (kế thừa)"},
	{"off", "Tắt"},
	{"low", "Thấp"},
	{"medium", "Trung bình"},
	{"high", "Cao"},
	{"xhigh", "Rất cao"},
	{"max", "Cao nhất"},
}

func thinkingOptionsFor(rt modelRuntime, role string) []thinkingOption {
	levels := rt.AvailableThinking(role)
	if len(levels) == 0 {
		return []thinkingOption{allThinkingOptions[0]}
	}
	out := make([]thinkingOption, 0, len(levels))
	for _, level := range levels {
		key := string(level)
		for _, option := range allThinkingOptions {
			if option.Key == key {
				out = append(out, option)
				break
			}
		}
	}
	if len(out) == 0 {
		return []thinkingOption{allThinkingOptions[0]}
	}
	return out
}

func thinkingIndexOf(options []thinkingOption, level string) int {
	level = strings.ToLower(strings.TrimSpace(level))
	for i, o := range options {
		if o.Key == level {
			return i
		}
	}
	return 0 // Giá trị lạ → kế thừa
}

type modelSwitchState struct {
	focus       modelSwitchFocus
	roleIdx     int
	providerIdx int
	modelIdx    int
	thinkingIdx int
	providers   []string
	models      []host.ConfiguredModel
	thinking    []thinkingOption
	// initialThinkingKey ghi lại giá trị chọn ban đầu của trường mức suy luận khi mở panel. Chỉ khi user thực sự di chuyển
	// trường này mới ghi lại — ý định mức suy luận đã lưu có thể cao hơn khả năng model hiện tại, panel không hiển thị được, không được xóa nhầm vì "chưa động".
	initialThinkingKey string
	message            string
}

func newModelSwitchState(rt modelRuntime, roleHint string) *modelSwitchState {
	state := &modelSwitchState{
		providers: rt.ConfiguredProviders(),
	}
	if len(state.providers) == 0 {
		state.message = "Hiện không có provider khả dụng"
	}

	roleHint = normalizeRoleKey(roleHint)
	for i, opt := range modelRoleOptions {
		if opt.Key == roleHint {
			state.roleIdx = i
			break
		}
	}
	state.syncSelection(rt)
	return state
}

func normalizeRoleKey(role string) string {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "", "default":
		return "default"
	case "architect", "writer", "editor":
		return strings.ToLower(strings.TrimSpace(role))
	default:
		return ""
	}
}

func (s *modelSwitchState) role() string {
	return modelRoleOptions[s.roleIdx].Key
}

func (s *modelSwitchState) roleLabel() string {
	return modelRoleOptions[s.roleIdx].Label
}

func (s *modelSwitchState) provider() string {
	if len(s.providers) == 0 || s.providerIdx < 0 || s.providerIdx >= len(s.providers) {
		return ""
	}
	return s.providers[s.providerIdx]
}

func (s *modelSwitchState) model() string {
	if len(s.models) == 0 || s.modelIdx < 0 || s.modelIdx >= len(s.models) {
		return ""
	}
	return s.models[s.modelIdx].Name
}

func (s *modelSwitchState) modelLabel() string {
	if len(s.models) == 0 || s.modelIdx < 0 || s.modelIdx >= len(s.models) {
		return ""
	}
	model := s.models[s.modelIdx]
	if window := formatContextWindow(model.ContextWindow); window != "" {
		return model.Name + " · " + window
	}
	return model.Name
}

func (s *modelSwitchState) thinkingKey() string {
	if s.thinkingIdx < 0 || s.thinkingIdx >= len(s.thinking) {
		return ""
	}
	return s.thinking[s.thinkingIdx].Key
}

func (s *modelSwitchState) thinkingLabel() string {
	if s.thinkingIdx < 0 || s.thinkingIdx >= len(s.thinking) {
		return allThinkingOptions[0].Label
	}
	return s.thinking[s.thinkingIdx].Label
}

func (s *modelSwitchState) moveFocus(delta int) {
	total := 4
	s.focus = modelSwitchFocus((int(s.focus) + delta + total) % total)
}

func (s *modelSwitchState) cycle(delta int, rt modelRuntime) {
	switch s.focus {
	case modelFocusRole:
		total := len(modelRoleOptions)
		s.roleIdx = (s.roleIdx + delta + total) % total
		s.syncSelection(rt)
	case modelFocusProvider:
		if len(s.providers) == 0 {
			return
		}
		total := len(s.providers)
		s.providerIdx = (s.providerIdx + delta + total) % total
		s.syncModels(rt, "")
	case modelFocusModel:
		if len(s.models) == 0 {
			return
		}
		total := len(s.models)
		s.modelIdx = (s.modelIdx + delta + total) % total
	case modelFocusThinking:
		total := len(s.thinking)
		if total == 0 {
			return
		}
		s.thinkingIdx = (s.thinkingIdx + delta + total) % total
	}
}

func (s *modelSwitchState) syncSelection(rt modelRuntime) {
	provider, model, _ := rt.CurrentModelSelection(s.role())
	if len(s.providers) > 0 {
		s.providerIdx = 0
		for i, candidate := range s.providers {
			if candidate == provider {
				s.providerIdx = i
				break
			}
		}
	}
	s.syncModels(rt, model)
	s.syncThinking(rt)
	s.message = ""
}

func (s *modelSwitchState) syncModels(rt modelRuntime, preferred string) {
	s.models = rt.ConfiguredModelOptions(s.provider())
	s.modelIdx = 0
	if len(s.models) == 0 {
		return
	}
	preferred = strings.TrimSpace(preferred)
	for i, model := range s.models {
		if model.Name == preferred {
			s.modelIdx = i
			return
		}
	}
}

func (s *modelSwitchState) syncThinking(rt modelRuntime) {
	s.thinking = thinkingOptionsFor(rt, s.role())
	s.thinkingIdx = thinkingIndexOf(s.thinking, rt.CurrentThinking(s.role()))
	s.initialThinkingKey = s.thinkingKey()
}

func (s *modelSwitchState) apply(rt modelRuntime) error {
	if len(s.providers) == 0 {
		return fmt.Errorf("Hiện không có provider khả dụng")
	}
	if len(s.models) == 0 {
		return fmt.Errorf("provider %q chưa có model nào", s.provider())
	}
	wantThinking := s.thinkingKey()
	if err := rt.SwitchModel(s.role(), s.provider(), s.model()); err != nil {
		return err
	}
	// Mức suy luận trực giao với model: chỉ khi user thực sự di chuyển trường mức suy luận mới ghi lại, tránh xóa nhầm
	// ý định cao (model hiện tại chưa đủ khả năng) thành giá trị mặc định ban đầu mà panel không hiển thị được.
	if wantThinking != s.initialThinkingKey {
		if err := rt.SetRoleThinking(s.role(), wantThinking); err != nil {
			return err
		}
	}
	s.syncThinking(rt)
	return nil
}

func (m Model) handleModelSwitchKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.modelSwitch == nil {
		return m, nil
	}
	state := m.modelSwitch

	switch msg.Type {
	case tea.KeyEsc:
		m.modelSwitch = nil
		return m, m.textarea.Focus()
	case tea.KeyTab, tea.KeyDown:
		state.moveFocus(1)
		return m, nil
	case tea.KeyShiftTab, tea.KeyUp:
		state.moveFocus(-1)
		return m, nil
	case tea.KeyLeft:
		state.cycle(-1, m.runtime)
		return m, nil
	case tea.KeyRight:
		state.cycle(1, m.runtime)
		return m, nil
	case tea.KeyEnter:
		if err := state.apply(m.runtime); err != nil {
			state.message = err.Error()
			return m, nil
		}
		m.modelSwitch = nil
		return m, tea.Batch(m.textarea.Focus(), fetchSnapshot(m.runtime))
	default:
		return m, nil
	}
}

func renderModelSwitchBar(width int, state *modelSwitchState) string {
	if state == nil || width <= 0 {
		return ""
	}

	title := lipgloss.NewStyle().
		Foreground(colorMuted).
		Bold(true).
		Render("/model Đổi model")

	row1 := renderModelField("Vai trò", state.roleLabel(), state.focus == modelFocusRole)
	row2 := renderModelField("Provider", state.provider(), state.focus == modelFocusProvider)
	row3 := renderModelField("Model", state.modelLabel(), state.focus == modelFocusModel)
	row4 := renderModelField("Mức suy luận", state.thinkingLabel(), state.focus == modelFocusThinking)
	hint := lipgloss.NewStyle().
		Foreground(colorDim).
		Italic(true).
		Render("Tab Đổi trường   ←→ Đổi lựa chọn   Enter Áp dụng   Esc Hủy")
	lines := []string{
		row1,
		row2,
		row3,
		row4,
		hint,
	}
	if state.message != "" {
		lines = append(lines, lipgloss.NewStyle().Foreground(colorError).Italic(true).Render(truncate(state.message, width-8)))
	}

	content := strings.Join(lines, "\n")
	boxW := lipgloss.Width(content) + 8
	maxW := width - 2
	if maxW > 68 {
		maxW = 68
	}
	if boxW > maxW {
		boxW = maxW
	}
	if boxW < 56 {
		boxW = 56
	}

	innerW := boxW - 2
	if innerW < 16 {
		innerW = 16
	}
	sepW := innerW - lipgloss.Width(title) - 3
	if sepW < 0 {
		sepW = 0
	}
	lineStyle := lipgloss.NewStyle().Foreground(colorDim)
	topBorder := lineStyle.Render("┌─ ") + title + lineStyle.Render(" "+strings.Repeat("─", sepW)+"┐")
	bottomBorder := lineStyle.Render("└" + strings.Repeat("─", innerW) + "┘")

	body := make([]string, 0, len(lines))
	for _, line := range lines {
		padding := innerW - lipgloss.Width(line)
		if padding < 0 {
			padding = 0
		}
		body = append(body, lineStyle.Render("│")+line+strings.Repeat(" ", padding)+lineStyle.Render("│"))
	}

	return strings.Join(append(append([]string{topBorder}, body...), bottomBorder), "\n")
}

func renderModelField(label, value string, focused bool) string {
	if strings.TrimSpace(value) == "" {
		value = "Chưa đặt"
	}
	labelText := lipgloss.NewStyle().
		Foreground(colorMuted).
		Width(12).
		Render(label + ":")
	style := lipgloss.NewStyle().Padding(0, 1).Foreground(bodyTextColor)
	if focused {
		style = style.Foreground(colorAccent).Bold(true).Underline(true)
	}
	return labelText + style.Render("["+value+"]")
}
