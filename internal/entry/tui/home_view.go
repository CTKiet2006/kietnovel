package tui

import (
	"fmt"
	"slices"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	domainmodel "github.com/voocel/ainovel-cli/internal/domain/model"
)

// 首页几何：页眉（品牌、模型）、左栏写新故事、右栏作品库、底栏（反馈与按键）。
// 每一行的文字与热区在同一遍里生成，渲染与鼠标命中不会漂移。

const (
	premiseRows = 3 // 故事输入框的可见行数
	entryRows   = 3 // 作品库每部作品占的行数：标题、进度与状态、留白
	homeHeader  = 3 // 品牌行、分隔线、留白
	homeFooter  = 3 // 分隔线、反馈、按键
)

type homeHit struct {
	x, y, width, focus int
	row                int // 作品库下标，-1 表示不是作品
	option             int // 篇幅或推进方式的选项下标，-1 表示不是选项
}

type homeRect struct{ x, y, width, height int }

func (r homeRect) contains(x, y int) bool {
	return x >= r.x && x < r.x+r.width && y >= r.y && y < r.y+r.height
}

type homeFrame struct {
	text     string
	hits     []homeHit
	capacity int      // 作品库一屏能放下的作品数
	list     homeRect // 作品库列表区：滚轮在这里翻动
}

// homeLayout 是两栏的水平几何：内容区居中，左栏宽度随终端在 56–68 列之间。
type homeLayout struct{ x, inner, left, gap, right int }

func layoutHome(width int) homeLayout {
	inner := min(160, max(1, width-6))
	l := homeLayout{x: (max(1, width) - inner) / 2, inner: inner, gap: 4}
	if inner >= 140 {
		l.gap = 6
	}
	l.left = min(68, max(56, inner*2/5))
	l.right = max(1, inner-l.left-l.gap)
	return l
}

// premiseWidth 是故事输入框的文字宽度：输入面左右各留两格。
func premiseWidth(width int) int { return max(1, layoutHome(width).left-4) }

// homeColumn 是一栏的行缓冲：热区按栏内坐标记录，拼页时再平移。
type homeColumn struct {
	width int
	lines []string
	hits  []homeHit
}

func (c *homeColumn) add(text string) { c.lines = append(c.lines, fitLine(text, c.width)) }

// hit 登记下一行（即将 add 的那一行）上的热区。
func (c *homeColumn) hit(x, width, focus, row, option int) {
	c.hits = append(c.hits, homeHit{x: x, y: len(c.lines), width: width, focus: focus, row: row, option: option})
}

func (m model) homeFrame() homeFrame {
	w, h := max(1, m.width), max(1, m.height)
	l := layoutHome(w)
	rows := max(1, h-homeHeader-homeFooter)
	var frame homeFrame

	config := homeControl("模型设置", m.home.focus == focusConfig)
	binding := benchTheme.Muted.Render(truncate(m.bindingLabel(), l.inner/2))
	lines := []string{
		alignRight(benchTheme.Accent.Bold(true).Render("AINOVEL"), binding+"   "+config, l.inner),
		benchRule(l.inner), "",
	}
	configWidth := lipgloss.Width(config)
	frame.hits = append(frame.hits, homeHit{x: l.x + l.inner - configWidth, y: 0, width: configWidth, focus: focusConfig, row: -1, option: -1})

	composer := m.composerColumn(l.left)
	library, listTop, capacity := m.libraryColumn(l.right, rows)
	// 内容不满一屏时整体落在上三分之一处，不把空白全堆在底部；作品铺满时贴顶。
	top := homeHeader + max(0, rows-max(len(composer.lines), len(library.lines)))/3
	frame.capacity = capacity
	frame.list = homeRect{x: l.x + l.left + l.gap, y: top + listTop, width: l.right, height: min(capacity*entryRows, homeHeader+rows-top-listTop)}
	for y := homeHeader; y < homeHeader+rows; y++ {
		left, right := lineAt(composer.lines, y-top), lineAt(library.lines, y-top)
		lines = append(lines, left+strings.Repeat(" ", max(0, l.left-lipgloss.Width(left))+l.gap)+right)
	}
	for _, hit := range composer.hits {
		hit.x, hit.y = hit.x+l.x, hit.y+top
		frame.hits = append(frame.hits, hit)
	}
	for _, hit := range library.hits {
		hit.x, hit.y = hit.x+l.x+l.left+l.gap, hit.y+top
		if hit.y < homeHeader+rows { // 末一部作品的留白行可能落在屏外
			frame.hits = append(frame.hits, hit)
		}
	}

	lines = append(lines, benchRule(l.inner), m.homeStatus(capacity), benchTheme.Muted.Render(m.homeHint()))
	pad := strings.Repeat(" ", l.x)
	for i, line := range lines {
		lines[i] = pad + fitLine(line, l.inner)
	}
	frame.text = strings.Join(fitBlock(strings.Join(lines, "\n"), w, h), "\n")
	return frame
}

func lineAt(lines []string, i int) string {
	if i < 0 || i >= len(lines) {
		return ""
	}
	return lines[i]
}

// composerColumn 左栏：故事输入、篇幅、推进方式、更多设定与开始按钮。每个选项都摊开，
// 下方一句话说明所选项意味着什么。
func (m model) composerColumn(width int) homeColumn {
	home := m.home
	c := homeColumn{width: width}
	c.add(benchTheme.Title.Render("开始一个新故事"))
	c.add("")

	// 输入框是一块铺底色的面，上下各留一行、左右各留两格。
	surface := lipgloss.NewStyle().Background(benchColors.InputBackground)
	block := append([]string{""}, strings.Split(strings.TrimSuffix(home.premise.View(), "\n"), "\n")...)
	for _, line := range append(block, "") {
		c.hit(0, width, focusPremise, -1, -1)
		c.add(surface.Render("  ") + line + surface.Render(strings.Repeat(" ", max(0, width-2-lipgloss.Width(line)))))
	}
	c.add("")

	fixed, note, selected := "固定章数", "AI 按故事容量定篇幅与终局，写到合适处收官", 0
	switch {
	case home.fixed && home.chapters > 0:
		fixed, note, selected = fmt.Sprintf("固定 %d 章", home.chapters), fmt.Sprintf("全书写满 %d 章收官，AI 只定终局方向", home.chapters), 1
	case home.fixed:
		fixed, note, selected = "固定 __ 章", "直接输入章数：全书写满这么多章收官", 1
	}
	c.options(home.focus == focusLength, "篇幅", focusLength, []string{"AI 决定", fixed}, selected, note)
	c.add("")

	labels := make([]string, len(approvalOrder))
	for i, policy := range approvalOrder {
		labels[i] = approvalLabel(policy)
	}
	c.options(home.focus == focusApproval, "推进", focusApproval, labels, slices.Index(approvalOrder, home.approval), approvalNote(home.approval))
	c.add("")

	var filled []string
	for i, input := range home.settings {
		if strings.TrimSpace(input.Value()) != "" {
			filled = append(filled, settingFields[i].label)
		}
	}
	summary := benchTheme.Muted.Render("受众 · 期待体验 · 必须出现 · 禁止出现 · 结局方向")
	if len(filled) > 0 {
		summary = benchTheme.Text.Render("已填 " + strings.Join(filled, "、"))
	}
	c.hit(0, width, focusSettings, -1, -1)
	c.add(rowLabel(home.focus == focusSettings, "设定") + summary)
	c.add(strings.Repeat(" ", 8) + benchTheme.Muted.Render("可选 · 你的原话会作为创作意图保留，AI 不会改写"))
	c.add("")
	c.add("")

	start := benchTheme.Accent.Bold(true).Render("开始创作 ↵")
	if home.focus == focusStart {
		start = benchTheme.Selected.Render(" 开始创作 ↵ ")
	}
	size := lipgloss.Width(start)
	c.hit(width-size, size, focusStart, -1, -1)
	c.add(strings.Repeat(" ", max(0, width-size)) + start)
	return c
}

// rowLabel 是选项行的行首：焦点所在行用强调色竖线与标签，其余行灰色。
func rowLabel(focused bool, label string) string {
	if focused {
		return benchTheme.Accent.Render("▎ ") + benchTheme.Accent.Bold(true).Render(label) + "  "
	}
	return "  " + benchTheme.Muted.Render(label) + "  "
}

// options 渲染一行单选（● 选中、○ 未选）与所选项的一句说明；每个选项可直接点选。
func (c *homeColumn) options(focused bool, label string, focus int, options []string, selected int, note string) {
	line := rowLabel(focused, label)
	c.hit(0, lipgloss.Width(line), focus, -1, -1)
	for i, option := range options {
		if i > 0 {
			line += "    "
		}
		text := benchTheme.Muted.Render("○ " + option)
		if i == selected {
			text = benchTheme.Accent.Render("● ") + benchTheme.Text.Bold(focused).Render(option)
		}
		c.hit(lipgloss.Width(line), lipgloss.Width(text), focus, -1, i)
		line += text
	}
	c.add(line)
	c.add(strings.Repeat(" ", 8) + benchTheme.Muted.Render(note))
}

// approvalNote 说清每种推进方式会在哪里停下来等你（§5.4）。
func approvalNote(policy domainmodel.ApprovalPolicy) string {
	switch policy {
	case domainmodel.ApprovalMilestone:
		return "蓝图和每个新故事弧先给你过目，章节自动写"
	case domainmodel.ApprovalManual:
		return "蓝图和每一章都等你确认后才入稿"
	default:
		return "一路写下去，只在越界时停下来问你"
	}
}

func (h *homeState) choose(focus, option int) {
	switch focus {
	case focusLength:
		h.fixed = option == 1
	case focusApproval:
		h.approval = approvalOrder[option]
	}
}

// libraryColumn 右栏：标题行（搜索与导入）、作品列表或空状态。返回列表起始行与一屏容量。
func (m model) libraryColumn(width, rows int) (homeColumn, int, int) {
	home := m.home
	c := homeColumn{width: width}
	title := benchTheme.Title.Render("作品库")
	if count := len(home.library); count > 0 {
		title += benchTheme.Muted.Render(fmt.Sprintf("  %d 部", count))
	}
	if home.searching {
		field := home.search
		field.Width, field.Prompt = min(28, width/2), "/ "
		field.SetCursor(field.Position())
		c.add(alignRight(title, field.View(), width))
	} else {
		search := "/ 搜索"
		if query := home.search.Value(); query != "" {
			search = "/ " + truncate(query, 16)
		}
		search, importing := homeControl(search, home.focus == focusSearch), homeControl("导入作品", home.focus == focusImport)
		controls := search + "   " + importing
		c.hit(width-lipgloss.Width(controls), lipgloss.Width(search), focusSearch, -1, -1)
		c.hit(width-lipgloss.Width(importing), lipgloss.Width(importing), focusImport, -1, -1)
		c.add(alignRight(title, controls, width))
	}
	c.add("")
	top := len(c.lines)
	capacity := max(1, (rows-top+1)/entryRows)

	indices := m.libraryIndices()
	switch {
	case !home.loaded:
		c.add(benchTheme.Muted.Render("正在读取作品库…"))
	case len(indices) == 0 && home.search.Value() != "":
		c.add(benchTheme.Muted.Render("没有匹配「" + home.search.Value() + "」的作品"))
		c.add("")
		c.add(benchTheme.Muted.Render("Esc 清除搜索，显示全部作品"))
	case len(indices) == 0:
		c.add(benchTheme.Text.Render("还没有作品。"))
		c.add("")
		c.add(benchTheme.Muted.Render("在左边写下一个故事想法，回车就开始："))
		c.add("")
		for i, step := range []string{
			"AI 先规划蓝图：卷、故事弧、章节与终局",
			"逐章写作；写完一段自动审阅，按意见重写",
			"途中随时提要求、改方向，关键处等你确认",
		} {
			c.add(benchTheme.Accent.Render(fmt.Sprintf("  %d  ", i+1)) + benchTheme.Text.Render(step))
			c.add("")
		}
	default:
		start, end := libraryWindow(indices, home.cursor, capacity)
		now := time.Now()
		for _, index := range indices[start:end] {
			entry := home.library[index]
			selected := home.focus == focusLibrary && index == home.cursor
			marker, titleStyle := "  ", benchTheme.Text
			if selected {
				marker, titleStyle = benchTheme.Accent.Render("▎ "), benchTheme.Title
			}
			status := entryProgress(entry) + "   " + entryState(entry.state)
			for _, line := range [entryRows]string{
				marker + titleStyle.Render(truncate(entry.title(), width-2)),
				marker + alignRight(status, benchTheme.Muted.Render(relativeTime(entry.updated, now)), width-2),
				"",
			} {
				c.hit(0, width, focusLibrary, index, -1)
				c.add(line)
			}
		}
	}
	return c, top, capacity
}

// libraryWindow 是作品列表的可见区间：装不下时让选中作品留在窗口中部。
func libraryWindow(indices []int, cursor, capacity int) (int, int) {
	selected := max(0, slices.Index(indices, cursor))
	start := min(max(0, selected-capacity/2), max(0, len(indices)-capacity))
	return start, min(len(indices), start+capacity)
}

func (e libraryEntry) title() string {
	if e.premise == "" {
		return e.id
	}
	return oneLine(e.premise)
}

// entryProgress 全书章数已知时画进度条；篇幅交给 AI 且尚未收官时只有已写章数（D63）。
func entryProgress(entry libraryEntry) string {
	if entry.target == 0 {
		return benchTheme.Muted.Render(fmt.Sprintf("已写 %d 章 · 篇幅由 AI 定", entry.written))
	}
	return progressBar(entry.written, entry.target, 10) + benchTheme.Muted.Render(fmt.Sprintf("  %d / %d 章", entry.written, entry.target))
}

// entryState 把最近一轮创作的状态画成带符号的彩色短语，与工作台的符号同一套。
func entryState(state domainmodel.CreationRunState) string {
	switch state {
	case "":
		return benchTheme.Muted.Render("○ 未开始")
	case domainmodel.RunWaitingUser:
		return benchTheme.Warning.Render("◇ " + runStateLabel(state))
	case domainmodel.RunRunning:
		return benchTheme.Accent.Render("◉ " + runStateLabel(state))
	case domainmodel.RunCompleted:
		return styleNotice.Render("✓ " + runStateLabel(state))
	case domainmodel.RunFailed:
		return benchTheme.Error.Render("! " + runStateLabel(state))
	case domainmodel.RunPaused:
		return benchTheme.Muted.Render("‖ " + runStateLabel(state))
	default:
		return benchTheme.Muted.Render("· " + runStateLabel(state))
	}
}

// relativeTime 用人说话的方式写时间：刚刚、3 小时前、昨天、9月3日。
func relativeTime(at, now time.Time) string {
	if at.IsZero() {
		return ""
	}
	at, now = at.Local(), now.Local()
	switch d := now.Sub(at); {
	case d < time.Minute:
		return "刚刚"
	case d < time.Hour:
		return fmt.Sprintf("%d 分钟前", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d 小时前", int(d.Hours()))
	case d < 48*time.Hour:
		return "昨天"
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%d 天前", int(d.Hours()/24))
	case at.Year() == now.Year():
		return at.Format("1月2日")
	default:
		return at.Format("2006年1月2日")
	}
}

// homeStatus 底栏反馈行：错误 > 提示 > 作品库位置。
func (m model) homeStatus(capacity int) string {
	switch {
	case m.home.err != "":
		return benchTheme.Error.Render(m.home.err)
	case m.home.notice != "":
		return benchTheme.Accent.Render(m.home.notice)
	}
	indices := m.libraryIndices()
	if len(indices) <= capacity {
		return ""
	}
	start, end := libraryWindow(indices, m.home.cursor, capacity)
	return benchTheme.Muted.Render(fmt.Sprintf("作品 %d–%d / %d", start+1, end, len(indices)))
}

// homeHint 底栏按键：只列当前焦点能做的事。
func (m model) homeHint() string {
	if m.home.searching {
		return "输入筛选 · Enter 确定 · Esc 清除"
	}
	hint := map[int]string{
		focusPremise:  "Enter 开始创作 · Tab 下一项",
		focusLength:   "←→ 切换 · 直接输入数字固定章数 · Enter 开始创作 · Tab 下一项",
		focusApproval: "←→ 切换 · Enter 开始创作 · Tab 下一项",
		focusSettings: "Enter 填写更多设定 · Tab 下一项",
		focusStart:    "Enter 开始创作 · Tab 下一项",
		focusLibrary:  "↑↓ 选择 · Enter 打开 · / 搜索 · d 删除",
		focusSearch:   "Enter 搜索作品 · Tab 下一项",
		focusImport:   "Enter 导入作品文件 · Tab 下一项",
		focusConfig:   "Enter 设置模型连接 · Tab 下一项",
	}[m.home.focus]
	return hint + " · Esc 退出"
}

func homeControl(text string, selected bool) string {
	if selected {
		return benchTheme.Selected.Render(text)
	}
	return benchTheme.Muted.Render(text)
}

func (m model) libraryIndices() []int {
	query := strings.ToLower(strings.TrimSpace(m.home.search.Value()))
	indices := make([]int, 0, len(m.home.library))
	for i, entry := range m.home.library {
		if query == "" || strings.Contains(strings.ToLower(entry.premise+" "+entry.id), query) {
			indices = append(indices, i)
		}
	}
	return indices
}

func (m model) beginLibrarySearch() (tea.Model, tea.Cmd) {
	m.home.setFocus(focusSearch)
	m.home.searching = true
	m.home.confirmDelete, m.home.notice = "", ""
	return m, m.home.search.Focus()
}

// handleSearchKey 搜索框：输入即筛选并选中第一部；Enter 确定，Esc 清空并回到作品库。
func (m model) handleSearchKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	h := &m.home
	switch key.Type {
	case tea.KeyEsc, tea.KeyEnter:
		if key.Type == tea.KeyEsc {
			h.search.SetValue("")
		}
		h.searching = false
		h.search.Blur()
		h.focus = focusLibrary
		if indices := m.libraryIndices(); len(indices) > 0 && key.Type == tea.KeyEsc {
			h.cursor = indices[0]
		}
		return m, nil
	}
	var cmd tea.Cmd
	h.search, cmd = h.search.Update(key)
	if indices := m.libraryIndices(); len(indices) > 0 {
		h.cursor = indices[0]
	}
	return m, cmd
}

func (m model) moveLibrary(key tea.KeyType) model {
	indices := m.libraryIndices()
	if len(indices) == 0 {
		return m
	}
	pos := max(0, slices.Index(indices, m.home.cursor))
	switch key {
	case tea.KeyUp:
		pos--
	case tea.KeyDown:
		pos++
	case tea.KeyHome:
		pos = 0
	case tea.KeyEnd:
		pos = len(indices) - 1
	case tea.KeyPgUp:
		pos -= m.homeFrame().capacity
	case tea.KeyPgDown:
		pos += m.homeFrame().capacity
	}
	m.home.cursor = indices[min(max(0, pos), len(indices)-1)]
	return m
}

// handleHomeMouse 滚轮翻作品库；点击：故事框聚焦、选项直接选中、作品直接打开，
// 其余控件与回车同一入口。
func (m model) handleHomeMouse(mouse tea.MouseMsg) (tea.Model, tea.Cmd) {
	if m.home.mode != homeMain || m.home.searching {
		return m, nil
	}
	frame := m.homeFrame()
	if mouse.Button == tea.MouseButtonWheelUp || mouse.Button == tea.MouseButtonWheelDown {
		if !frame.list.contains(mouse.X, mouse.Y) {
			return m, nil
		}
		m.home.setFocus(focusLibrary)
		key := tea.KeyDown
		if mouse.Button == tea.MouseButtonWheelUp {
			key = tea.KeyUp
		}
		return m.moveLibrary(key), nil
	}
	if mouse.Action != tea.MouseActionPress || mouse.Button != tea.MouseButtonLeft {
		return m, nil
	}
	for _, hit := range frame.hits {
		if mouse.Y != hit.y || mouse.X < hit.x || mouse.X >= hit.x+hit.width {
			continue
		}
		m.home.confirmDelete, m.home.notice = "", ""
		cmd := m.home.setFocus(hit.focus)
		switch {
		case hit.option >= 0:
			m.home.choose(hit.focus, hit.option)
		case hit.row >= 0:
			m.home.cursor = hit.row
			return m.activate(focusLibrary)
		case hit.focus != focusPremise && hit.focus != focusLength && hit.focus != focusApproval:
			return m.activate(hit.focus)
		}
		return m, cmd
	}
	return m, nil
}
