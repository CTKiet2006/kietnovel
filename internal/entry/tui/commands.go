package tui

import (
	"errors"
	"github.com/CTKiet2006/kietnovel/internal/i18n"
	"path/filepath"
	"strings"
	"time"

	"github.com/CTKiet2006/kietnovel/internal/bootstrap"
	"github.com/CTKiet2006/kietnovel/internal/domain"
	"github.com/CTKiet2006/kietnovel/internal/entry/startup"
	"github.com/CTKiet2006/kietnovel/internal/host"
	tea "github.com/charmbracelet/bubbletea"
)

type slashCommandSpec struct {
	Name    string
	Aliases []string
	// ID là tên nội bộ bất biến của lệnh. Runtime chỉ xử lý ID.
	// Trống thì lấy Name (mọi lệnh hiện tại đều dùng tên tiếng Anh làm ID).
	ID string
	// Names là tên hiển thị theo UI language: {"vi": {"đọc"}, "zh": {"阅读"}}.
	// Parser khớp MỌI tên trong mọi ngôn ngữ (đổi UI không mất lệnh cũ),
	// còn palette/help hiện tên của đúng ngôn ngữ đang dùng.
	Names map[string][]string
	Group string
	Usage string
	// UsageByLang là cách dùng theo locale, key "vi"/"en"/"zh". Trống thì dùng
	// Usage (chuỗi chuẩn). Help hiển thị bản đúng locale thay vì chuỗi raw.
	UsageByLang map[string]string
	Description string
	AutoExecute bool
	Hidden      bool
	NeedsIdle   bool
	Run         func(m Model, args []string) (tea.Model, tea.Cmd)
}

// CommandID trả ID nội bộ của lệnh. Mọi logic runtime phải dùng ID, không dùng
// Name/Aliases — tên hiển thị đổi theo locale nhưng ID thì không.
func (s slashCommandSpec) CommandID() string {
	if s.ID != "" {
		return s.ID
	}
	return s.Name
}

// DisplayName trả tên hiển thị theo UI language. Không có thì rơi về Name.
// Lang lạ ("EN", "en-US") được chuẩn hoá trước nên vẫn khớp; ngôn ngữ không hỗ
// trợ ("fr") thì không có tên — về Name chứ không lấy tên locale khác oan.
func (s slashCommandSpec) DisplayName(lang string) string {
	if names, ok := s.Names[lang]; ok && len(names) > 0 {
		return names[0]
	}
	if code, ok := i18n.SupportedCode(lang); ok {
		if names, ok := s.Names[code]; ok && len(names) > 0 {
			return names[0]
		}
	}
	return s.Name
}

// UsageText trả cách dùng theo UI language. Không có thì rơi về Usage.
func (s slashCommandSpec) UsageText(lang string) string {
	if u, ok := s.UsageByLang[lang]; ok && u != "" {
		return u
	}
	if code, ok := i18n.SupportedCode(lang); ok {
		if u, ok := s.UsageByLang[code]; ok && u != "" {
			return u
		}
	}
	return s.Usage
}

type slashCommand struct {
	name string
	args []string
}

func parseSlashCommand(text string) (slashCommand, bool) {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "/") {
		return slashCommand{}, false
	}
	fields := strings.Fields(strings.TrimPrefix(text, "/"))
	if len(fields) == 0 {
		return slashCommand{}, false
	}
	return slashCommand{name: strings.ToLower(fields[0]), args: fields[1:]}, true
}

func (s slashCommandSpec) matches(name string) bool {
	if s.Name == name {
		return true
	}
	for _, alias := range s.Aliases {
		if strings.EqualFold(alias, name) {
			return true
		}
	}
	// Khớp tên mọi ngôn ngữ: người đổi UI vẫn gõ được tên cũ.
	for _, names := range s.Names {
		for _, n := range names {
			if strings.EqualFold(n, name) {
				return true
			}
		}
	}
	return false
}

// resolveSubcommand chuẩn hoá từ subcommand (từ đầu tiên sau tên lệnh) về mode
// chuẩn, xuyên ngôn ngữ. Logic lệnh sau đó không biết gì về tiếng Việt nữa.
// Không khớp thì trả ("", false) — caller tự quyết.
func resolveSubcommand(arg string, table map[string][]string) (string, bool) {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return "", false
	}
	for mode, words := range table {
		for _, w := range words {
			if strings.EqualFold(w, arg) {
				return mode, true
			}
		}
	}
	return "", false
}

// subcommandCatalog: commandID → subcommandID → các từ được chấp nhận ở mọi
// ngôn ngữ. Key là ID chuẩn ("story_partner" → "ask"), KHÔNG phải tiếng Việt —
// logic lệnh không bao giờ thấy tiếng nào cả.
//
// Phase 3 (/sp) dùng bảng này: "/sp hỏi", "/sp ask", "/sp 问" đều về ask.
// Định nghĩa trước để parser ổn định, khỏi migrate lần hai.
var subcommandCatalog = map[string]map[string][]string{
	"story_partner": {
		"ask":     {"hỏi", "ask", "问"},
		"inspect": {"soi", "inspect", "检查"},
		"suggest": {"gợi ý", "suggest", "建议"},
	},
}

// lookupSubcommand resolve (commandID, từ đầu tiên) về subcommandID.
// Không khớp thì ("", false) — caller tự quyết (với /sp: coi như câu hỏi,
// mặc định ask nên người dùng khỏi nhớ từ hỏi/ask/问).
func lookupSubcommand(commandID, arg string) (string, bool) {
	modes, ok := subcommandCatalog[commandID]
	if !ok {
		return "", false
	}
	return resolveSubcommand(arg, modes)
}

func commandRegistryInstance() commandRegistry {
	return newCommandRegistry([]slashCommandSpec{
		{
			Name: "help",
			ID:   "help",
			Names: map[string][]string{
				"vi": {"giúp"},
				"zh": {"帮助"},
			},
			UsageByLang: map[string]string{
				"vi": "/help",
				"en": "/help",
				"zh": "/help",
			},
			Group:       "system",
			Usage:       "/help",
			Description: i18n.T("Xem danh sách lệnh"),
			AutoExecute: true,
			Run: func(m Model, _ []string) (tea.Model, tea.Cmd) {
				m.help = newHelpState(m.width, m.height)
				m.textarea.Blur()
				return m, nil
			},
		},
		{
			Name: "model",
			ID:   "model",
			Names: map[string][]string{
				"vi": {"môhình"},
				"zh": {"模型"},
			},
			UsageByLang: map[string]string{
				"vi": "/model [vai-trò]",
				"en": "/model [role]",
				"zh": "/model [角色]",
			},
			Group:       "system",
			Usage:       "/model [role]",
			Description: i18n.T("Đổi model và mức suy luận của từng vai trò"),
			AutoExecute: true,
			Run: func(m Model, args []string) (tea.Model, tea.Cmd) {
				roleHint := ""
				if len(args) > 0 {
					roleHint = args[0]
					if normalizeRoleKey(roleHint) == "" {
						m.applyEvent(host.Event{
							Time: time.Now(), Category: "ERROR", Summary: i18n.T("Vai trò không rõ: ") + roleHint, Level: "error",
						})
						m.refreshEventViewport()
						return m, nil
					}
				}
				m.modelSwitch = newModelSwitchState(m.runtime, roleHint)
				m.textarea.Blur()
				return m, nil
			},
		},
		{
			Name: "config",
			ID:   "config",
			Names: map[string][]string{
				"vi": {"cấuhình"},
				"zh": {"配置"},
			},
			UsageByLang: map[string]string{
				"vi": "/config",
				"en": "/config",
				"zh": "/config",
			},
			Group:       "system",
			Usage:       "/config",
			Description: i18n.T("Thêm/sửa Provider, model và context window"),
			AutoExecute: true,
			Run: func(m Model, args []string) (tea.Model, tea.Cmd) {
				if len(args) != 0 {
					m.applyEvent(host.Event{Time: time.Now(), Category: "ERROR", Summary: i18n.T("Cách dùng: /config"), Level: "error"})
					m.refreshEventViewport()
					return m, nil
				}
				m.modelConfig = newModelConfigState(m.runtime)
				m.textarea.Blur()
				return m, nil
			},
		},
		{
			Name: "diag",
			ID:   "diag",
			Names: map[string][]string{
				"vi": {"chẩnđoán"},
				"zh": {"诊断"},
			},
			UsageByLang: map[string]string{
				"vi": "/diag",
				"en": "/diag",
				"zh": "/diag",
			},
			Group:       "analysis",
			Usage:       "/diag",
			Description: i18n.T("Chẩn đoán sức khỏe truyện đang viết"),
			AutoExecute: true,
			Run: func(m Model, _ []string) (tea.Model, tea.Cmd) {
				m.reportSeq++
				m.report = newReportState(m.width, m.height, m.reportSeq, time.Now())
				m.textarea.Blur()
				return m, loadReport(m.runtime.Dir(), m.reportSeq)
			},
		},
		{
			Name: "review",
			ID:   "review",
			Names: map[string][]string{
				"vi": {"duyệt"},
				"zh": {"审核"},
			},
			UsageByLang: map[string]string{
				"vi": "/review on|off",
				"en": "/review on|off",
				"zh": "/review on|off",
			},
			Group:       "writing",
			Usage:       "/review on|off",
			Description: i18n.T("Bật/tắt duyệt từng chương"),
			Run: func(m Model, args []string) (tea.Model, tea.Cmd) {
				if len(args) != 1 || (args[0] != "on" && args[0] != "off") {
					m.applyEvent(host.Event{Time: time.Now(), Category: "ERROR", Summary: i18n.T("Cách dùng: /review on|off"), Level: "error"})
					m.refreshEventViewport()
					return m, nil
				}
				mode := domain.ChapterAdvanceReview
				if args[0] == "off" {
					mode = domain.ChapterAdvanceAuto
				}
				if err := m.runtime.SetAdvanceMode(mode); err != nil {
					m.applyEvent(host.Event{Time: time.Now(), Category: "ERROR", Summary: i18n.T("Đổi chế độ duyệt thất bại: ") + err.Error(), Level: "error"})
					m.refreshEventViewport()
					return m, nil
				}
				return m, fetchSnapshot(m.runtime)
			},
		},
		{
			Name: "next",
			ID:   "next",
			Names: map[string][]string{
				"vi": {"tiếp"},
				"zh": {"继续"},
			},
			UsageByLang: map[string]string{
				"vi": "/next",
				"en": "/next",
				"zh": "/next",
			},
			Group:       "writing",
			Usage:       "/next",
			Description: i18n.T("Duyệt để viết chương tiếp theo"),
			AutoExecute: true,
			NeedsIdle:   true,
			Run: func(m Model, args []string) (tea.Model, tea.Cmd) {
				if len(args) != 0 {
					m.applyEvent(host.Event{Time: time.Now(), Category: "ERROR", Summary: i18n.T("Cách dùng: /next"), Level: "error"})
					m.refreshEventViewport()
					return m, nil
				}
				if err := m.runtime.AdvanceOneChapter(); err != nil {
					m.applyEvent(host.Event{Time: time.Now(), Category: "ERROR", Summary: i18n.T("Cho viết chương tiếp thất bại: ") + err.Error(), Level: "error"})
					m.refreshEventViewport()
					return m, nil
				}
				return m, tea.Batch(fetchSnapshot(m.runtime), listenDone(m.runtime), m.textarea.Focus())
			},
		},
		{
			Name: "start",
			ID:   "start",
			Names: map[string][]string{
				"vi": {"bắtđầu"},
				"zh": {"开始"},
			},
			UsageByLang: map[string]string{
				"vi": "/start <đường-dẫn>",
				"en": "/start <path>",
				"zh": "/start <路径>",
			},
			Group:       "writing",
			Usage:       "/start <path>",
			Description: i18n.T("Tạo truyện mới từ file thiết lập/dàn ý"),
			Run: func(m Model, args []string) (tea.Model, tea.Cmd) {
				if m.mode != modeNew {
					m.applyEvent(host.Event{
						Time: time.Now(), Category: "ERROR", Summary: i18n.T("/start chỉ dùng ở màn hình chào để tạo truyện mới"), Level: "error",
					})
					m.refreshEventViewport()
					return m, nil
				}
				prompt, err := prepareFileStart(args)
				if err != nil {
					m.err = err
					return m, nil
				}
				cmd := m.enterStarting(prompt)
				return m, tea.Batch(startRuntime(m.runtime, prompt), cmd)
			},
		},
		{
			Name: "import",
			ID:   "import",
			Names: map[string][]string{
				"vi": {"nhập"},
				"zh": {"导入"},
			},
			Group:       "writing",
			Usage:       i18n.T("/import <path> [--yes] [--story=open|closed] [--continue] [--guide=<hướng dẫn cắt chương>]"),
			Description: i18n.T("Nhập truyện ngoài vào để viết tiếp (không tham số thì tiếp tục lần nhập dở; --guide chỉnh cách cắt chương bằng ngôn ngữ tự nhiên)"),
			NeedsIdle:   true,
			Run: func(m Model, args []string) (tea.Model, tea.Cmd) {
				m.importSeq++
				state, listenCmd, err := startImport(m.runtime, m.importSeq, args, m.width, m.height)
				if err != nil {
					m.applyEvent(host.Event{
						Time: time.Now(), Category: "ERROR", Summary: i18n.T("Bắt đầu nhập thất bại: ") + err.Error(), Level: "error",
					})
					m.refreshEventViewport()
					return m, nil
				}
				m.importer = state
				m.importHint = "" // Đã vào luồng nhập, gợi ý khôi phục ở màn hình chào xong nhiệm vụ
				m.textarea.Blur()
				return m, listenCmd
			},
		},
		{
			Name: "reopen",
			ID:   "reopen",
			Names: map[string][]string{
				"vi": {"mởlại"},
				"zh": {"重开"},
			},
			Group:       "writing",
			Usage:       i18n.T("/reopen [hướng viết tiếp]"),
			Description: i18n.T("Mở lại truyện đã hoàn thành để viết tiếp"),
			NeedsIdle:   true,
			Run: func(m Model, args []string) (tea.Model, tea.Cmd) {
				if err := m.runtime.Reopen(strings.Join(args, " ")); err != nil {
					m.applyEvent(host.Event{
						Time: time.Now(), Category: "ERROR", Summary: i18n.T("Mở lại thất bại: ") + err.Error(), Level: "error",
					})
					m.refreshEventViewport()
					return m, nil
				}
				return m, tea.Batch(m.textarea.Focus(), resumeBook(m.runtime))
			},
		},
		{
			Name: "cocreate",
			ID:   "cocreate",
			Names: map[string][]string{
				"vi": {"đồngsángtác"},
				"zh": {"协作"},
			},
			UsageByLang: map[string]string{
				"vi": "/cocreate",
				"en": "/cocreate",
				"zh": "/cocreate",
			},
			Aliases:     []string{"plan"},
			Group:       "writing",
			Usage:       "/cocreate",
			Description: i18n.T("Tạm dừng để cùng AI định hướng giai đoạn tiếp"),
			AutoExecute: true,
			Run: func(m Model, _ []string) (tea.Model, tea.Cmd) {
				if m.mode != modeRunning {
					m.applyEvent(host.Event{
						Time: time.Now(), Category: "ERROR", Summary: i18n.T("Đồng sáng tác chỉ dùng khi đang viết"), Level: "error",
					})
					m.refreshEventViewport()
					return m, nil
				}
				if !m.runtime.PauseForCoCreate() {
					m.applyEvent(host.Event{
						Time: time.Now(), Category: "ERROR", Summary: i18n.T("Không vào được đồng sáng tác: truyện đã xong hoặc đang trong đồng sáng tác"), Level: "error",
					})
					m.refreshEventViewport()
					return m, nil
				}
				m.cocreate = newStageCoCreateState()
				m.resizeTextarea()
				m.textarea.Blur()
				return m, m.sendCoCreate()
			},
		},
		{
			Name: "simulate",
			ID:   "simulate",
			Names: map[string][]string{
				"vi": {"môphỏng"},
				"zh": {"模拟"},
			},
			UsageByLang: map[string]string{
				"vi": "/simulate",
				"en": "/simulate",
				"zh": "/simulate",
			},
			Group:       "writing",
			Usage:       "/simulate",
			Description: i18n.T("Đọc ./simulate để tạo/cập nhật hồ sơ văn phong"),
			NeedsIdle:   true,
			Run: func(m Model, args []string) (tea.Model, tea.Cmd) {
				m.simSeq++
				state, listenCmd, err := startSimulate(m.runtime, m.simSeq, args, m.width, m.height)
				if err != nil {
					m.applyEvent(host.Event{
						Time: time.Now(), Category: "ERROR", Summary: i18n.T("Bắt đầu mô phỏng văn phong thất bại: ") + err.Error(), Level: "error",
					})
					m.refreshEventViewport()
					return m, nil
				}
				m.simulator = state
				m.textarea.Blur()
				return m, listenCmd
			},
		},
		{
			Name: "importsim",
			ID:   "importsim",
			Names: map[string][]string{
				"vi": {"nhậpvănphong"},
				"zh": {"导风格"},
			},
			UsageByLang: map[string]string{
				"vi": "/importsim <profile.json>",
				"en": "/importsim <profile.json>",
				"zh": "/importsim <profile.json>",
			},
			Group:       "writing",
			Usage:       "/importsim <profile.json>",
			Description: i18n.T("Nhập hồ sơ văn phong có sẵn từ file json"),
			NeedsIdle:   true,
			Run: func(m Model, args []string) (tea.Model, tea.Cmd) {
				m.simSeq++
				state, listenCmd, err := startImportSimulation(m.runtime, m.simSeq, args, m.width, m.height)
				if err != nil {
					m.applyEvent(host.Event{
						Time: time.Now(), Category: "ERROR", Summary: i18n.T("Nhập hồ sơ văn phong thất bại: ") + err.Error(), Level: "error",
					})
					m.refreshEventViewport()
					return m, nil
				}
				m.simulator = state
				m.textarea.Blur()
				return m, listenCmd
			},
		},
		{
			Name: "sync",
			ID:   "sync",
			Names: map[string][]string{
				"vi": {"đồngbộ"},
				"zh": {"同步"},
			},
			UsageByLang: map[string]string{
				"vi": "/sync [--check]",
				"en": "/sync [--check]",
				"zh": "/sync [--check]",
			},
			Group:       "writing",
			Usage:       "/sync [--check]",
			Description: i18n.T("Kiểm tra/nhận các chương bạn sửa tay"),
			AutoExecute: true,
			NeedsIdle:   true,
			Run: func(m Model, args []string) (tea.Model, tea.Cmd) {
				cmd, checkOnly, err := startRevisionSync(m.runtime, args)
				if err != nil {
					m.applyEvent(host.Event{Time: time.Now(), Category: "ERROR", Summary: i18n.T("Bắt đầu đồng bộ chương thất bại: ") + err.Error(), Level: "error"})
					m.refreshEventViewport()
					return m, nil
				}
				summary := i18n.T("Đang phân tích và nhận các sửa đổi chương...")
				if checkOnly {
					summary = i18n.T("Đang kiểm tra các chương bị sửa từ bên ngoài...")
				}
				m.applyEvent(host.Event{Time: time.Now(), Category: "SYSTEM", Summary: summary, Level: "info"})
				m.refreshEventViewport()
				return m, cmd
			},
		},
		{
			Name: "export",
			ID:   "export",
			Names: map[string][]string{
				"vi": {"xuất"},
				"zh": {"导出"},
			},
			UsageByLang: map[string]string{
				"vi": "/export [đường-dẫn] [from=N] [to=M] [--overwrite]",
				"en": "/export [path] [from=N] [to=M] [--overwrite]",
				"zh": "/export [路径] [from=N] [to=M] [--overwrite]",
			},
			Group:       "writing",
			Usage:       "/export [path] [from=N] [to=M] [--overwrite]",
			Description: i18n.T("Xuất các chương đã xong ra TXT/EPUB"),
			AutoExecute: true,
			Run: func(m Model, args []string) (tea.Model, tea.Cmd) {
				cmd, err := startExport(m.runtime, args)
				if err != nil {
					m.applyEvent(host.Event{
						Time: time.Now(), Category: "ERROR", Summary: i18n.T("Bắt đầu xuất thất bại: ") + err.Error(), Level: "error",
					})
					m.refreshEventViewport()
					return m, nil
				}
				m.applyEvent(host.Event{
					Time: time.Now(), Category: "SYSTEM", Summary: i18n.T("Đang xuất..."), Level: "info",
				})
				m.refreshEventViewport()
				return m, cmd
			},
		},
		{
			Name:    "read",
			Aliases: []string{"doc"},
			ID:      "read",
			UsageByLang: map[string]string{
				"vi": "/read [số chương]",
				"en": "/read [chapter]",
				"zh": "/read [章节]",
			},
			Names:       map[string][]string{"vi": {"đọc"}, "zh": {"阅读"}},
			Group:       "writing",
			Usage:       "/read [số chương]",
			Description: i18n.T("Đọc các chương đã lưu ngay trong TUI"),
			AutoExecute: true,
			Run: func(m Model, args []string) (tea.Model, tea.Cmd) {
				// Đọc không đụng tới Engine nên không cần NeedsIdle: vẫn đọc được
				// trong lúc đang viết. Chỉ mở khung, không khởi động việc gì.
				m.reader = newReaderState(m.runtime, m.width, m.height, strings.Join(args, " "))
				m.textarea.Blur()
				return m, nil
			},
		},
		{
			Name:    "books",
			Aliases: []string{"truyen"},
			ID:      "books",
			UsageByLang: map[string]string{
				"vi": "/books",
				"en": "/books",
				"zh": "/books",
			},
			Names:       map[string][]string{"vi": {"truyện"}, "zh": {"书库"}},
			Group:       "writing",
			Usage:       "/books",
			Description: i18n.T("Xem danh sách truyện, tạo mới hoặc xoá"),
			AutoExecute: true,
			Run: func(m Model, args []string) (tea.Model, tea.Cmd) {
				return openBooks(m, booksList)
			},
		},
		{
			Name: "new",
			ID:   "new",
			Names: map[string][]string{
				"vi": {"mới"},
				"zh": {"新建"},
			},
			UsageByLang: map[string]string{
				"vi": "/new [tên truyện]",
				"en": "/new [title]",
				"zh": "/new [标题]",
			},
			Group:       "writing",
			Usage:       "/new [tên truyện]",
			Description: i18n.T("Tạo thư mục truyện mới trong output/"),
			AutoExecute: true,
			Run: func(m Model, args []string) (tea.Model, tea.Cmd) {
				if len(args) == 0 {
					return openBooks(m, booksNewDraft)
				}
				m.books = newBooksState(m.width, m.height, booksNewDraft)
				m.books.draft = strings.Join(args, " ")
				return m.createBookConfirmed()
			},
		},
		{
			Name:    "delete",
			Aliases: []string{"rm"},
			ID:      "delete",
			Names: map[string][]string{
				"vi": {"xóa"},
				"zh": {"删除"},
			},
			UsageByLang: map[string]string{
				"vi": "/delete [tên truyện]",
				"en": "/delete [title]",
				"zh": "/delete [标题]",
			},
			Group:       "writing",
			Usage:       "/delete [tên truyện]",
			Description: i18n.T("Xoá truyện — có bước xác nhận, không khôi phục được"),
			AutoExecute: true,
			Run: func(m Model, args []string) (tea.Model, tea.Cmd) {
				next, cmd := openBooks(m, booksList)
				mm := next.(Model)
				if len(args) == 0 {
					return mm, cmd
				}
				want := strings.Join(args, " ")
				for _, bk := range mm.books.list {
					if bk.Name == want || filepath.Base(bk.Dir) == want {
						res, err := mm.runtime.InspectBook(bk.Dir)
						if err != nil {
							return renderBooksError(mm, err.Error())
						}
						mm.books.pending = &res
						mm.books.mode = booksDeleteConfirm
						return mm, cmd
					}
				}
				return renderBooksError(mm, i18n.Tf("Không tìm thấy truyện %q.", want))
			},
		},
		{
			Name:    "rename",
			Aliases: []string{"doiten"},
			ID:      "rename",
			Names: map[string][]string{
				"vi": {"đổitên"},
				"zh": {"重命名"},
			},
			UsageByLang: map[string]string{
				"vi": "/rename <tên mới>",
				"en": "/rename <new title>",
				"zh": "/rename <新标题>",
			},
			Group:       "writing",
			Usage:       "/rename <tên mới>",
			Description: i18n.T("Đổi tên hiển thị của truyện đang mở"),
			AutoExecute: true,
			Run: func(m Model, args []string) (tea.Model, tea.Cmd) {
				return m.runRenameBook(args)
			},
		},
		{
			Name:    "language",
			Aliases: []string{"lang", "ngonngu"},
			ID:      "language",
			UsageByLang: map[string]string{
				"vi": "/language [ui|write] [vi|en|zh]",
				"en": "/language [ui|write] [vi|en|zh]",
				"zh": "/language [ui|write] [vi|en|zh]",
			},
			Names:       map[string][]string{"vi": {"ngônngữ"}, "zh": {"语言"}},
			Group:       "system",
			Usage:       "/language [ui|write] [vi|en|zh]",
			Description: i18n.T("Ngôn ngữ giao diện (ui) và ngôn ngữ sáng tác (write) — tách riêng"),
			AutoExecute: true,
			Run:         runLanguageCommand,
		},
		{
			// /sp giữ NGUYÊN mọi locale (C1): nó là identifier/brand name, chỉ
			// localize subcommand (hỏi/ask/问 → ask qua subcommandCatalog).
			Name: "sp",
			ID:   "sp",
			UsageByLang: map[string]string{
				"vi": "/sp [hỏi] <câu hỏi> | /sp soi | /sp gợi ý",
				"en": "/sp [ask] <question> | /sp inspect | /sp suggest",
				"zh": "/sp [问] <问题> | /sp 检查 | /sp 建议",
			},
			Group:       "writing",
			Usage:       "/sp [hỏi] <câu hỏi> | /sp soi | /sp gợi ý",
			Description: i18n.T("Hỏi Story Partner mà không dừng máy đang viết"),
			AutoExecute: true,
			Run:         runSPCommand,
		},
	})
}

// runLanguageCommand đổi ngôn ngữ giao diện ngay và ghi xuống cấu hình.
//
// runLanguageCommand xử lý cả hai ngôn ngữ, vì /language là lối vào duy nhất.
//
// Hai ngôn ngữ này là hai lựa chọn KHÁC NHAU, dù chung một tên khoá:
//   - "ui"   → ngôn ngữ giao diện TUI. Đổi ngay trong phiên.
//   - "write" → ngôn ngữ sáng tác (lớp văn phong + chỉ dẫn đầu ra cho model).
//     Host nạp bộ prompt một lần lúc khởi động, nên đổi chỉ có tác dụng từ
//     lần mở truyện sau.
//
// Gộp chung một tên khoá là cố ý: người quen /language vi không phải học thêm
// lệnh, và không thêm hàng mới vào bảng lệnh.
func runLanguageCommand(m Model, args []string) (tea.Model, tea.Cmd) {
	parts := args
	target := ""
	if len(parts) >= 2 {
		target, parts = strings.ToLower(strings.TrimSpace(parts[0])), parts[1:]
	}
	lang := strings.ToLower(strings.TrimSpace(strings.Join(parts, " ")))

	// Gõ /language hoặc /language <vi|en|zh> không kèm từ khoá: xem cả hai.
	if target == "" && (lang == "" || isLangCode(lang)) {
		if lang == "" {
			return m.languageSummary()
		}
		// /language vi — hiểu là đổi giao diện, vì đó là cái đổi được ngay.
		// Ngôn ngữ sáng tác phải nói rõ tên mới khỏi khoá nhầm truyện đang viết.
		target = "ui"
	}
	if target == "" {
		return m.languageUsage()
	}

	switch target {
	case "ui", "giao-dien", "interface":
		return m.setUILanguage(lang)
	case "write", "viet", "sang-tac", "writing":
		return m.setWriteLanguage(lang)
	default:
		m.applyEvent(host.Event{
			Time: time.Now(), Category: "ERROR", Level: "warn",
			Summary: i18n.Tf("Không hiểu %q. Dùng: /language ui vi|en|zh hoặc /language write vi|en|zh", target),
		})
		m.refreshEventViewport()
		return m, nil
	}
}

func isLangCode(s string) bool {
	switch s {
	case i18n.LangVietnamese, i18n.LangEnglish, i18n.LangChinese:
		return true
	}
	return false
}

// languageSummary hiện trạng hai ngôn ngữ, vì gõ /language không tham số là
// hỏi "đang ở đâu" chứ không phải hỏi "muốn đổi gì".
func (m Model) languageSummary() (tea.Model, tea.Cmd) {
	m.applyEvent(host.Event{
		Time: time.Now(), Category: "SYSTEM", Level: "info",
		Summary: i18n.T("Giao diện: ") + languageLabel(i18n.Language()) +
			i18n.T(" · Ngôn ngữ sáng tác: ") + languageLabel(writeLangOf(m.cfg)) +
			i18n.T(" — đổi bằng /language ui <vi|en|zh> hoặc /language write <vi|en|zh>"),
	})
	m.refreshEventViewport()
	return m, nil
}

func (m Model) languageUsage() (tea.Model, tea.Cmd) {
	m.applyEvent(host.Event{
		Time: time.Now(), Category: "ERROR", Level: "warn",
		Summary: i18n.T("Dùng: /language ui vi|en|zh (giao diện) hoặc /language write vi|en|zh (ngôn ngữ sáng tác). Gõ /language để xem trạng thái."),
	})
	m.refreshEventViewport()
	return m, nil
}

// writeLangOf trả ngôn ngữ sáng tác đang áp dụng. Trống = "vi".
func writeLangOf(cfg bootstrap.Config) string {
	if l := bootstrap.NormalizeLanguage(cfg.Language); l != "" {
		return l
	}
	return i18n.LangVietnamese
}

// setUILanguage đổi giao diện ngay trong phiên và lưu vào ui_language.
// Không đụng Language, nên ngôn ngữ sáng tác giữ nguyên.
func (m Model) setUILanguage(lang string) (tea.Model, tea.Cmd) {
	if !isLangCode(lang) {
		m.applyEvent(host.Event{
			Time: time.Now(), Category: "ERROR", Level: "warn",
			Summary: i18n.T("Ngôn ngữ giao diện phải là vi, en hoặc zh."),
		})
		m.refreshEventViewport()
		return m, nil
	}
	// Đổi trước để thông báo phía dưới sinh ra bằng ngôn ngữ mới.
	i18n.SetLanguage(lang)
	m.retranslate()
	cfg := bootstrap.CloneConfig(m.cfg)
	cfg.UILanguage = lang
	return m.saveLanguageSetting(cfg, lang, "")
}

// setWriteLanguage đổi ngôn ngữ sáng tác cho truyện mới. Chỉ có tác dụng từ lần
// mở truyện sau, vì bộ prompt đã nạp lúc khởi động — nói rõ điều này thay vì
// để người dùng tưởng đã đổi xong.
func (m Model) setWriteLanguage(lang string) (tea.Model, tea.Cmd) {
	if !isLangCode(lang) {
		m.applyEvent(host.Event{
			Time: time.Now(), Category: "ERROR", Level: "warn",
			Summary: i18n.T("Ngôn ngữ sáng tác phải là vi, en hoặc zh."),
		})
		m.refreshEventViewport()
		return m, nil
	}
	cfg := bootstrap.CloneConfig(m.cfg)
	cfg.Language = lang
	// Ghi kèm cảnh báo truyện đang mở giữ ngôn ngữ cũ: truyện này đã khoá
	// ngôn ngữ theo chương, đổi mặc định không kéo truyện đang viết theo.
	return m.saveLanguageSetting(cfg, lang,
		i18n.T(" — truyện đang mở giữ ngôn ngữ của nó; mặc định này dùng cho truyện tạo sau"))
}

func (m Model) saveLanguageSetting(cfg bootstrap.Config, lang, tail string) (tea.Model, tea.Cmd) {
	summary := i18n.T("Đã đổi sang: ") + languageLabel(lang)
	level := "info"
	// Lỗi ghi không chặn việc đổi ngôn ngữ của phiên đang chạy.
	if err := bootstrap.SaveConfig(bootstrap.EffectiveConfigPath(), cfg); err != nil {
		level = "warn"
		summary += i18n.T(" — lưu cấu hình thất bại, lần sau có thể mất lựa chọn: ") + err.Error()
	} else {
		summary += tail
	}
	m.cfg = cfg
	m.applyEvent(host.Event{Time: time.Now(), Category: "SYSTEM", Level: level, Summary: summary})
	m.refreshEventViewport()
	return m, nil
}

// retranslate dựng lại những nhãn đã tính sẵn lúc khởi động, vì chúng đã bị khoá
// theo ngôn ngữ cũ. Placeholder và tiêu đề bảng lệnh là hai chỗ thường bị sót.
func (m *Model) retranslate() {
	m.textarea.Placeholder = donePlaceholder()
	if m.compActive || len(m.compItems) > 0 {
		m.compItems = commandRegistryInstance().PaletteItems()
		if m.compIdx >= len(m.compItems) {
			m.compIdx = 0
		}
	}
	if m.help != nil {
		m.help = newHelpState(m.width, m.height)
	}
}

// languageLabel trả tên ngôn ngữ hiển thị thân thiện. Tên ngôn ngữ giữ nguyên
// dạng gốc, không dịch: người dùng nhận ra tên ngôn ngữ của họ hơn là bản dịch.
func languageLabel(lang string) string {
	switch lang {
	case i18n.LangEnglish:
		return "English"
	case i18n.LangChinese:
		return "中文"
	default:
		return "Tiếng Việt"
	}
}

func commandSpecs() []slashCommandSpec {
	return commandRegistryInstance().Visible()
}

func prepareFileStart(args []string) (string, error) {
	path := strings.TrimSpace(strings.Join(args, " "))
	if len(path) >= 2 && ((path[0] == '"' && path[len(path)-1] == '"') ||
		(path[0] == '\'' && path[len(path)-1] == '\'')) {
		path = path[1 : len(path)-1]
	}
	if path == "" {
		return "", errors.New(i18n.T("Cách dùng: /start <đường dẫn file thiết lập hoặc dàn ý>"))
	}
	prompt, err := startup.LoadPromptFile(path)
	if err != nil {
		return "", err
	}
	return startup.PrepareQuick(prompt)
}

func (m Model) handleSlashCommand(cmd slashCommand) (tea.Model, tea.Cmd) {
	spec, ok := commandRegistryInstance().Find(cmd.name)
	if !ok {
		m.applyEvent(host.Event{
			Time: time.Now(), Category: "ERROR", Summary: i18n.T("Lệnh không rõ: /") + cmd.name, Level: "error",
		})
		m.refreshEventViewport()
		return m, nil
	}
	if spec.NeedsIdle && m.snapshot.IsRunning {
		m.applyEvent(host.Event{
			Time: time.Now(), Category: "ERROR", Summary: i18n.T("Lệnh chỉ chạy khi đang rảnh: /") + spec.Name, Level: "error",
		})
		m.refreshEventViewport()
		return m, nil
	}
	return spec.Run(m, cmd.args)
}
