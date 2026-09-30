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
	Name        string
	Aliases     []string
	Group       string
	Usage       string
	Description string
	AutoExecute bool
	Hidden      bool
	NeedsIdle   bool
	Run         func(m Model, args []string) (tea.Model, tea.Cmd)
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
	return false
}

func commandRegistryInstance() commandRegistry {
	return newCommandRegistry([]slashCommandSpec{
		{
			Name:        "help",
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
			Name:        "model",
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
			Name:        "config",
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
			Name:        "diag",
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
			Name:        "review",
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
			Name:        "next",
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
			Name:        "start",
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
			Name:        "import",
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
			Name:        "reopen",
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
			Name:        "cocreate",
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
			Name:        "simulate",
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
			Name:        "importsim",
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
			Name:        "sync",
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
			Name:        "export",
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
			Name:        "read",
			Aliases:     []string{"doc"},
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
			Name:        "books",
			Aliases:     []string{"truyen"},
			Group:       "writing",
			Usage:       "/books",
			Description: i18n.T("Xem danh sách truyện, tạo mới hoặc xoá"),
			AutoExecute: true,
			Run: func(m Model, args []string) (tea.Model, tea.Cmd) {
				return openBooks(m, booksList)
			},
		},
		{
			Name:        "new",
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
			Name:        "delete",
			Aliases:     []string{"rm"},
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
			Name:        "language",
			Aliases:     []string{"lang", "ngonngu"},
			Group:       "system",
			Usage:       "/language [vi|en|zh]",
			Description: i18n.T("Đổi ngôn ngữ giao diện và ngôn ngữ sáng tác"),
			AutoExecute: true,
			Run:         runLanguageCommand,
		},
	})
}

// runLanguageCommand đổi ngôn ngữ giao diện ngay và ghi xuống cấu hình.
//
// Ranh giới quan trọng: ngôn ngữ sáng tác (lớp voice) được host nạp một lần lúc
// khởi động, nên đổi giữa phiên chỉ có tác dụng từ lần mở sau. Không nói quá tay,
// và nói rõ trong thông báo để không gây hiểu nhầm.
func runLanguageCommand(m Model, args []string) (tea.Model, tea.Cmd) {
	lang := strings.ToLower(strings.TrimSpace(strings.Join(args, " ")))
	if lang == "" {
		m.applyEvent(host.Event{
			Time: time.Now(), Category: "SYSTEM", Level: "info",
			Summary: i18n.T("Ngôn ngữ hiện tại: ") + languageLabel(i18n.Language()) +
				i18n.T(" — dùng /language vi|en|zh để đổi"),
		})
		return m, nil
	}

	switch lang {
	case i18n.LangVietnamese, i18n.LangEnglish, i18n.LangChinese:
	default:
		m.applyEvent(host.Event{
			Time: time.Now(), Category: "ERROR", Level: "warn",
			Summary: i18n.T("Không hỗ trợ ngôn ngữ này, chọn: vi, en, zh"),
		})
		return m, nil
	}

	// Đổi giao diện trước để các thông báo phía dưới sinh ra bằng ngôn ngữ mới.
	i18n.SetLanguage(lang)
	m.retranslate()

	// Ghi cấu hình. Lỗi ghi không chặn việc đổi ngôn ngữ của phiên đang chạy.
	path := bootstrap.EffectiveConfigPath()
	cfg := bootstrap.CloneConfig(m.cfg)
	cfg.Language = lang
	summary := i18n.T("Đã đổi sang: ") + languageLabel(lang)
	level := "info"
	if err := bootstrap.SaveConfig(path, cfg); err != nil {
		level = "warn"
		summary += i18n.T(" — lưu cấu hình thất bại, lần sau có thể mất lựa chọn: ") + err.Error()
	} else {
		summary += i18n.T(" — ngôn ngữ sáng tác sẽ đổi từ lần khởi động sau")
	}
	m.applyEvent(host.Event{
		Time: time.Now(), Category: "SYSTEM", Level: level, Summary: summary,
	})
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
