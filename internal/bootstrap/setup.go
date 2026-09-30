package bootstrap

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/CTKiet2006/kietnovel/internal/rules"
	"github.com/CTKiet2006/kietnovel/internal/utils"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// exampleConfig là template có comment sẽ ghi vào ~/.kietnovel/config.example.jsonc sau khi dẫn.
// File nhúng phải khớp với config.example.jsonc ở gốc repo, test sẽ chống trôi.
//
//go:embed config.example.jsonc
var exampleConfig string

// NeedsSetup kiểm tra có cần dẫn lần đầu không (kích hoạt khi cả cấu hình toàn cục lẫn project đều chưa có).
func NeedsSetup() bool {
	if p := DefaultConfigPath(); p != "" {
		if _, err := os.Stat(p); err == nil {
			return false
		}
	}
	if _, err := os.Stat(projectConfigPath()); err == nil {
		return false
	}
	return true
}

type setupProvider struct {
	name           string
	label          string
	baseURL        string // base_url điền sẵn
	needType       bool   // Proxy tùy chỉnh cần hỏi thêm type và base_url
	apiKeyOptional bool   // true nghĩa là API Key được phép để trống
}

// ProviderPreset là mục danh mục provider dùng chung cho dẫn lần đầu và /config lúc runtime.
type ProviderPreset struct {
	Name           string
	Label          string
	BaseURL        string
	NeedType       bool
	APIKeyOptional bool
}

var setupProviders = []setupProvider{
	{name: "openrouter", label: "OpenRouter", baseURL: "https://openrouter.ai/api/v1"},
	{name: "anthropic", label: "Anthropic"},
	{name: "gemini", label: "Gemini"},
	{name: "openai", label: "OpenAI"},
	{name: "deepseek", label: "DeepSeek"},
	{name: "qwen", label: "Qwen"},
	{name: "glm", label: "GLM"},
	{name: "grok", label: "Grok"},
	{name: "ollama", label: "Ollama", baseURL: "http://localhost:11434/v1", apiKeyOptional: true},
	{name: "bedrock", label: "Bedrock", apiKeyOptional: true},
	{name: "custom", label: "Custom Proxy", needType: true, apiKeyOptional: true},
}

// ProviderPresets trả về danh sách preset có thể sửa an toàn.
func ProviderPresets() []ProviderPreset {
	out := make([]ProviderPreset, 0, len(setupProviders))
	for _, preset := range setupProviders {
		out = append(out, ProviderPreset{
			Name: preset.name, Label: preset.label, BaseURL: preset.baseURL,
			NeedType: preset.needType, APIKeyOptional: preset.apiKeyOptional,
		})
	}
	return out
}

// RunSetup chạy dẫn lần đầu, trả về cấu hình đã sinh.
func RunSetup() (Config, error) {
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("99")).
		Render("Chưa thấy file cấu hình, bắt đầu thiết lập..."))
	fmt.Fprintf(os.Stderr, "  Đường dẫn file cấu hình: %s\n", lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Render(DefaultConfigPath()))
	fmt.Fprintf(os.Stderr, "  Xong có thể sửa file này để chỉnh các thiết lập nâng cao.\n")
	fmt.Fprintln(os.Stderr)

	// Bước 1: chọn Provider
	sp, err := runProviderSelect()
	if err != nil {
		return Config{}, err
	}

	providerName := sp.name
	var pc ProviderConfig
	printStepDone("Provider", sp.label)

	// Proxy tùy chỉnh: hỏi thêm tên và loại giao thức API
	if sp.needType {
		providerName, err = runTextInput("Tên Provider", "my-proxy")
		if err != nil {
			return Config{}, err
		}
		providerType, err := runTypeSelect()
		if err != nil {
			return Config{}, err
		}
		pc.Type = providerType
	}

	// Bước 2: nhập API Key
	var apiKey string
	if sp.apiKeyOptional {
		apiKey, err = runOptionalTextInput("[2/5] API Key (có thể bỏ trống)", "Bỏ trống nếu không dùng API Key")
	} else {
		apiKey, err = runTextInput("[2/5] API Key", "sk-xxx")
	}
	if err != nil {
		return Config{}, err
	}
	pc.APIKey = apiKey
	if apiKey == "" {
		printStepDone("API Key", "Chưa đặt")
	} else {
		printStepDone("API Key", maskKey(apiKey))
	}

	// Bước 3: Base URL (Enter thẳng để dùng địa chỉ chính thức)
	baseDefault := sp.baseURL
	baseHint := "Bỏ trống để dùng địa chỉ chính thức"
	if baseDefault != "" {
		baseHint = baseDefault
	}
	baseURL, err := runTextInputWithDefault("[3/5] Base URL (Enter dùng mặc định, dùng proxy thì điền địa chỉ proxy)", baseHint, baseDefault)
	if err != nil {
		return Config{}, err
	}
	pc.BaseURL = baseURL
	if baseURL != "" {
		printStepDone("Base URL", baseURL)
	} else {
		printStepDone("Base URL", "Mặc định")
	}

	// Bước 4: tên model (bắt buộc)
	modelName, err := runTextInput("[4/5] Tên model", "Ví dụ: gpt-4o / claude-sonnet-4 / gemini-2.5-pro")
	if err != nil {
		return Config{}, err
	}
	printStepDone("Model", modelName)
	pc.Models = []ModelConfig{{Name: modelName}}

	// Bước 5: Ngôn ngữ sáng tác (mặc định Tiếng Việt)
	lang, langLabel, err := runLanguageSelect()
	if err != nil {
		return Config{}, err
	}
	printStepDone("Ngôn ngữ", langLabel)

	cfg := Config{
		Provider:  providerName,
		ModelName: modelName,
		Language:  lang,
		Providers: map[string]ProviderConfig{providerName: pc},
		Roles:     map[string]RoleConfig{},
		Style:     "default",
	}

	// Lưu
	path := DefaultConfigPath()
	if err := SaveConfig(path, cfg); err != nil {
		return cfg, fmt.Errorf("lưu cấu hình: %w", err)
	}

	// Sinh template có comment
	saveExampleConfig()

	// Thư mục quy tắc toàn cục do luồng khởi động (runWithConfig) tạo thống nhất, ở đây chỉ lấy đường dẫn để nhắc
	rulesDir := rules.DefaultHomeRulesDir()

	fmt.Fprintln(os.Stderr)
	fmt.Fprintf(os.Stderr, "%s Đã lưu cấu hình vào %s\n",
		lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Render("✓"), path)
	fmt.Fprintf(os.Stderr, "  Model mặc định: %s\n", modelName)
	fmt.Fprintln(os.Stderr, "  Muốn mỗi vai trò dùng model khác nhau thì sửa file cấu hình.")
	if rulesDir != "" {
		fmt.Fprintf(os.Stderr, "  Quy tắc viết toàn cục đặt trong file .md ở %s (xem README.txt trong đó)\n", rulesDir)
	}
	fmt.Fprintln(os.Stderr)

	return cfg, nil
}

func saveExampleConfig() {
	dir, err := configDir()
	if err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(dir, "config.example.jsonc"), []byte(exampleConfig), 0o644)
}

// printStepDone in một dòng xác nhận hoàn thành bước.
func printStepDone(label, value string) {
	fmt.Fprintf(os.Stderr, "  %s %s: %s\n",
		lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Render("✓"),
		label,
		lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Render(value))
}

func maskKey(key string) string {
	if len(key) <= 8 {
		return "****"
	}
	return key[:4] + "****" + key[len(key)-4:]
}

// ---------- Thành phần TUI ----------

func runProviderSelect() (setupProvider, error) {
	m := setupSelectModel{
		title: "[1/5] Chọn Provider",
		items: setupProviders,
	}
	p := tea.NewProgram(m, tea.WithOutput(os.Stderr))
	final, err := p.Run()
	if err != nil {
		return setupProvider{}, err
	}
	result := final.(setupSelectModel)
	if result.cancelled {
		return setupProvider{}, fmt.Errorf("đã hủy thiết lập")
	}
	return result.items[result.cursor], nil
}

var apiTypeOptions = []setupProvider{
	{name: "openai", label: "Tương thích OpenAI"},
	{name: "anthropic", label: "Tương thích Anthropic"},
	{name: "gemini", label: "Tương thích Gemini"},
}

func runTypeSelect() (string, error) {
	m := setupSelectModel{
		title: "Loại giao thức API",
		items: apiTypeOptions,
	}
	p := tea.NewProgram(m, tea.WithOutput(os.Stderr))
	final, err := p.Run()
	if err != nil {
		return "", err
	}
	result := final.(setupSelectModel)
	if result.cancelled {
		return "", fmt.Errorf("đã hủy thiết lập")
	}
	return result.items[result.cursor].name, nil
}

// languageOptions là lựa chọn ngôn ngữ ở bước cuối wizard.
// Tái dùng setupSelectModel: name là mã language ("vi"/"en"/"zh").
// Ngôn ngữ này chi phối cả ngôn ngữ giao diện lẫn ngôn ngữ đầu ra của truyện.
var languageOptions = []setupProvider{
	{name: LangVietnamese, label: "Tiếng Việt — giao diện + truyện tiếng Việt (mặc định)"},
	{name: LangEnglish, label: "English — English UI + English stories"},
	{name: LangChinese, label: "中文 — 中文界面与中文小说"},
}

func runLanguageSelect() (lang, label string, err error) {
	m := setupSelectModel{
		title: "[5/5] Ngôn ngữ giao diện & sáng tác truyện",
		items: languageOptions,
	}
	p := tea.NewProgram(m, tea.WithOutput(os.Stderr))
	final, err := p.Run()
	if err != nil {
		return "", "", err
	}
	result := final.(setupSelectModel)
	if result.cancelled {
		return "", "", fmt.Errorf("đã hủy thiết lập")
	}
	picked := result.items[result.cursor]
	return NormalizeLanguage(picked.name), picked.label, nil
}

func runTextInput(label, placeholder string) (string, error) {
	return runTextInputWithDefault(label, placeholder, "")
}

func runOptionalTextInput(label, placeholder string) (string, error) {
	m := setupInputModel{label: label, placeholder: placeholder, allowEmpty: true}
	p := tea.NewProgram(m, tea.WithOutput(os.Stderr))
	final, err := p.Run()
	if err != nil {
		return "", err
	}
	result := final.(setupInputModel)
	if result.cancelled {
		return "", fmt.Errorf("đã hủy thiết lập")
	}
	return utils.CleanInputLine(result.value), nil
}

// runTextInputWithDefault nhập một trường có sẵn giá trị mặc định.
// Ô trống + Enter luôn được chấp nhận: trả về defaultValue nếu có, không thì ""
// — nếu không, người dùng chọn provider không kèm baseURL dựng sẵn sẽ bị kẹt
// vĩnh viễn ở bước này vì Enter không làm gì (issue #125).
func runTextInputWithDefault(label, placeholder, defaultValue string) (string, error) {
	m := setupInputModel{label: label, placeholder: placeholder, defaultValue: defaultValue, allowEmpty: true}
	p := tea.NewProgram(m, tea.WithOutput(os.Stderr))
	final, err := p.Run()
	if err != nil {
		return "", err
	}
	result := final.(setupInputModel)
	if result.cancelled {
		return "", fmt.Errorf("đã hủy thiết lập")
	}
	if utils.CleanInputLine(result.value) == "" && result.defaultValue != "" {
		return result.defaultValue, nil
	}
	return utils.CleanInputLine(result.value), nil
}

// ---------- Bộ chọn ----------

var (
	setupCursorStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("212"))
	setupDimStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	setupHeaderStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("99"))
	setupInputStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("212"))
)

type setupSelectModel struct {
	title     string
	items     []setupProvider
	cursor    int
	cancelled bool
}

func (m setupSelectModel) Init() tea.Cmd { return nil }

func (m setupSelectModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if msg, ok := msg.(tea.KeyMsg); ok {
		switch msg.String() {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.items)-1 {
				m.cursor++
			}
		case "enter":
			return m, tea.Quit
		case "q", "esc", "ctrl+c":
			m.cancelled = true
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m setupSelectModel) View() string {
	var b strings.Builder
	b.WriteString(setupHeaderStyle.Render(m.title))
	b.WriteString("\n\n")
	for i, item := range m.items {
		cursor := "  "
		label := item.label
		if i == m.cursor {
			cursor = setupCursorStyle.Render("❯ ")
			label = setupCursorStyle.Render(label)
		}
		b.WriteString(cursor + label + "\n")
	}
	b.WriteString(setupDimStyle.Render("\n  ↑↓ Chọn · Enter Xác nhận · Esc Hủy"))
	return b.String()
}

// ---------- Nhập văn bản ----------

type setupInputModel struct {
	label        string
	placeholder  string
	defaultValue string // Giá trị mặc định dùng khi Enter thẳng
	allowEmpty   bool   // Cho phép nhập thẳng giá trị trống
	value        string
	cancelled    bool
}

func (m setupInputModel) Init() tea.Cmd { return nil }

func (m setupInputModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if msg, ok := msg.(tea.KeyMsg); ok {
		switch msg.String() {
		case "enter":
			if utils.CleanInputLine(m.value) != "" || m.defaultValue != "" || m.allowEmpty {
				return m, tea.Quit
			}
		case "ctrl+c", "esc":
			m.cancelled = true
			return m, tea.Quit
		case "backspace":
			if len(m.value) > 0 {
				runes := []rune(m.value)
				m.value = string(runes[:len(runes)-1])
			}
		default:
			if msg.Type == tea.KeyRunes {
				m.value += utils.CleanInputRunes(msg.Runes)
			} else if msg.Type == tea.KeySpace {
				m.value += " "
			}
		}
	}
	return m, nil
}

func (m setupInputModel) View() string {
	var b strings.Builder
	b.WriteString(setupHeaderStyle.Render(m.label))
	b.WriteString("\n\n")
	b.WriteString(setupInputStyle.Render("❯ "))
	if m.value == "" {
		b.WriteString(setupCursorStyle.Render("▌"))
		b.WriteString(setupDimStyle.Render(m.placeholder))
	} else {
		b.WriteString(m.value)
		b.WriteString(setupCursorStyle.Render("▌"))
	}
	b.WriteString(setupDimStyle.Render("  (Enter Xác nhận, Esc Hủy)"))
	b.WriteString("\n")
	return b.String()
}
