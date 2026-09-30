package bootstrap

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/CTKiet2006/kietnovel/internal/errs"
	"github.com/CTKiet2006/kietnovel/internal/models"
	"github.com/CTKiet2006/kietnovel/internal/notify"
	"github.com/CTKiet2006/kietnovel/internal/utils"
	"github.com/voocel/agentcore/llm"
)

// DefaultContextWindow là kích thước cửa sổ dự phòng khi model chưa được đăng ký trong registry.
const DefaultContextWindow = 200000

// CompactRatio là ngưỡng tương đối kích hoạt nén ngữ cảnh: nén khi tokens >= window * CompactRatio.
// 0.85 là giá trị kinh nghiệm, chừa 15% phần đầu cho "prompt vòng tiếp theo + kết quả tool lớn",
// đồng thời để model cửa sổ lớn cũng chủ động nén ở 85%, tránh ăn hết danh nghĩa 1M mới nén
// (vùng suy giảm attention).
//
// Tỉ lệ nén không cho người dùng cấu hình; người dùng chỉ cấu hình context_window thực của từng model.
const CompactRatio = 0.85

// MinCompactReserve là giới hạn dưới của ReserveTokens. Với model cửa sổ nhỏ (ví dụ qwen3:8b
// 32k chạy local), tính theo tỉ lệ 0.15 thì reserve chỉ 4800, một response commit_chapter
// đã có thể chiếm 5-8k, một chương chính văn 8-15k — sẽ rơi vào cảnh "vừa nén xong lại vượt".
// Mốc chặn 8000 đảm bảo trong kịch bản xấu nhất vẫn còn đệm nửa vòng.
const MinCompactReserve = 8000

// CompactReserveTokens tính ngược ReserveTokens từ CompactRatio và áp mốc sàn MinCompactReserve:
//
//	threshold = window - reserve = window * CompactRatio
//	reserve   = max(MinCompactReserve, window * (1 - CompactRatio))
//
// Dùng cho EngineConfig.ReserveTokens của agentcore.context.Engine.
func CompactReserveTokens(window int) int {
	if window <= 0 {
		return 0
	}
	reserve := window - int(float64(window)*CompactRatio)
	if reserve < MinCompactReserve {
		return MinCompactReserve
	}
	return reserve
}

// ProviderConfig định nghĩa credentials của một LLM provider.
type ProviderConfig struct {
	Type    string        `json:"type,omitempty"`     // Loại giao thức API (openai/anthropic/gemini), chỉ định khi dùng proxy tùy chỉnh
	API     string        `json:"api,omitempty"`      // Endpoint theo giao thức OpenAI: chat (mặc định) / responses
	APIKey  string        `json:"api_key,omitempty"`  // API Key
	BaseURL string        `json:"base_url,omitempty"` // API Base URL
	Models  []ModelConfig `json:"models,omitempty"`   // Danh sách model tùy chọn, để TUI hiển thị khi chuyển đổi
	// ExtraBody truyền thẳng vào mỗi request của provider này các tham số bổ sung (như temperature/top_p/min_p/
	// presence_penalty, hoặc key đặc thù hãng như chat_template_kwargs để bật think của nvidia).
	// Với đầu OpenAI-compatible thì merge nguyên văn vào request body (theo ước lệ extra_body); giá trị do người dùng tự chịu trách nhiệm.
	ExtraBody map[string]any `json:"extra_body,omitempty"`
	// Extra truyền thẳng cho cấu hình cấp provider (litellm.ProviderConfig.Extra), dùng cho các tùy chọn
	// client/tầng truyền tải như HTTP headers, user_agent, anthropic_beta.
	Extra map[string]any `json:"extra,omitempty"`
	// StreamIdleTimeout là watchdog nhàn rỗi của stream: quá thời gian này không nhận được chunk nào thì ngắt stream
	// (chuỗi Go duration, ví dụ "900s" / "15m"). Để trống thì mặc định 5m — chặn trên hợp lý cho dịch vụ cloud;
	// LocalAI/ollama và các suy luận tự host chậm, chunk đầu có thể vượt xa 5 phút, cứ nới riêng theo provider,
	// để không làm chậm phát hiện treo của các kênh khác (#79).
	StreamIdleTimeout string `json:"stream_idle_timeout,omitempty"`
}

// ModelConfig mô tả model có thể chuyển đổi của một provider cùng cửa sổ ngữ cảnh tùy chọn.
// Để tương thích cấu hình cũ, vừa đọc được từ chuỗi JSON ("model-name") vừa đọc được từ object;
// khi ghi về luôn chuẩn hóa thành dạng object.
type ModelConfig struct {
	Name          string `json:"name"`
	ContextWindow int    `json:"context_window,omitempty"`
	// JSONSchema là khai báo ba trạng thái của structured output gốc (response_format json_schema):
	// không cấu hình = xét theo năng lực cấp model của provider adapter; true = người dùng khai báo endpoint/model
	// này hỗ trợ (request bị từ chối thì lộ nguyên văn, không lặng lẽ downgrade); false = ép đi theo prompt contract.
	// Năng lực của proxy tùy chỉnh và gateway tổng hợp lấy theo khai báo của người dùng, chương trình không dò.
	JSONSchema *bool `json:"json_schema,omitempty"`
}

func (m *ModelConfig) UnmarshalJSON(data []byte) error {
	var legacy string
	if err := json.Unmarshal(data, &legacy); err == nil {
		m.Name = legacy
		m.ContextWindow = 0
		m.JSONSchema = nil
		return nil
	}
	type modelConfigAlias ModelConfig
	var decoded modelConfigAlias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return fmt.Errorf("cấu hình model phải là chuỗi hoặc object: %w", err)
	}
	*m = ModelConfig(decoded)
	return nil
}

// ModelConfig trả về cấu hình tường minh của model được chỉ định.
func (pc ProviderConfig) ModelConfig(name string) (ModelConfig, bool) {
	name = strings.TrimSpace(name)
	for _, model := range pc.Models {
		if strings.TrimSpace(model.Name) == name {
			return model, true
		}
	}
	return ModelConfig{}, false
}

// ModelJSONSchema trả về khai báo json_schema ba trạng thái của model; khi chưa liệt kê
// trong models hoặc chưa cấu hình thì trả về nil (xét theo năng lực adapter).
func (c Config) ModelJSONSchema(provider, model string) *bool {
	if pc, ok := c.Providers[provider]; ok {
		if mc, ok := pc.ModelConfig(model); ok {
			return mc.JSONSchema
		}
	}
	return nil
}

// defaultStreamIdleTimeout: trong cảnh output dài + ctx dài, provider reasoning-aware
// (mimo / deepseek-r1...) nếu phía server không stream reasoning delta trong giai đoạn suy nghĩ,
// cả đoạn SSE sẽ im lặng. Watchdog mặc định của litellm là 2 phút, với chương 8000 chữ thường
// bị giết nhầm; 5 phút phủ được tuyệt đại đa số case thực đo (xem thống kê thời gian suy nghĩ
// plan→draft trong tasks/todo.md).
const defaultStreamIdleTimeout = 5 * time.Minute

// StreamIdleTimeoutValue parse timeout nhàn rỗi của stream của provider này; để trống thì dùng mặc định.
func (pc ProviderConfig) StreamIdleTimeoutValue() (time.Duration, error) {
	s := strings.TrimSpace(pc.StreamIdleTimeout)
	if s == "" {
		return defaultStreamIdleTimeout, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("duration %q không hợp lệ (dùng Go duration như \"900s\" / \"15m\")", s)
	}
	if d <= 0 {
		return 0, fmt.Errorf("phải là số dương, nhận được %q", s)
	}
	return d, nil
}

// RequiresAPIKey trả về provider này có bắt buộc cấu hình api_key tường minh hay không.
// Ước lệ:
// 1. ollama / bedrock cho phép không key;
// 2. cấu hình có Type tường minh được coi là proxy tùy chỉnh, cho phép không key;
// 3. provider còn lại mặc định yêu cầu key, giữ kiểm tra thận trọng cho interface host chính thức.
//
// So sánh tên provider không phân biệt hoa thường: llm.IsProviderRegistered và
// tầng litellm đều ToLower trước khi tra registry, nên viết "Ollama" trong cấu hình
// phải cho cùng kết quả — không thì bị chặn ở tầng cấu hình dù chạy được ở tầng LLM.
func (pc ProviderConfig) RequiresAPIKey(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "ollama", "bedrock":
		return false
	}
	return pc.Type == ""
}

// ProviderType trả về loại giao thức API hợp lệ.
// Ưu tiên Type tường minh; nếu không thì yêu cầu tên provider phải có trong registry litellm.
func (pc ProviderConfig) ProviderType(name string) (string, error) {
	if pc.Type != "" {
		return pc.Type, nil
	}
	if llm.IsProviderRegistered(name) {
		return name, nil
	}
	return "", fmt.Errorf("provider %q thiếu type và không nằm trong danh sách provider litellm đã biết: %w", name, errs.ErrConfig)
}

// ModelRef biểu diễn một cặp provider/model.
type ModelRef struct {
	Provider string `json:"provider"` // Tên provider (key trong map Providers)
	Model    string `json:"model"`    // Tên model (truyền nguyên văn, không parse gì)
}

// RoleConfig định nghĩa model override của một vai trò.
type RoleConfig struct {
	Provider  string     `json:"provider"`            // Provider chính (key trong map Providers)
	Model     string     `json:"model"`               // Model chính (truyền nguyên văn, không parse gì)
	Fallbacks []ModelRef `json:"fallbacks,omitempty"` // Danh sách provider/model dự phòng tường minh
	// ReasoningEffort là cường độ suy luận của vai trò này (off/low/medium/high/xhigh/max), trống = kế thừa mặc định top-level.
	// Do agents.ParseThinkingLevel kiểm tra rồi mới áp; giá trị vượt cấp coi như trống.
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
}

// knownRoles là tên vai trò được phép cấu hình. Arbiter hiện không mở cấu hình cấp vai trò,
// thống nhất dùng model mặc định top-level (host.arbiterModel dùng models.Default).
// import_* là núm chỉnh nấc model của hàm ngữ nghĩa import (docs/import-pipeline.md §13.1):
// chưa cấu hình thì rơi về architect, cấu hình rồi có thể chỉ hàm mang tính máy móc hơn sang nấc rẻ hơn.
var knownRoles = map[string]bool{
	"architect":         true,
	"writer":            true,
	"editor":            true,
	"import_segment":    true,
	"import_analyze":    true,
	"import_synthesize": true,
}

// NovelDirEnv là biến môi trường chọn bộ truyện đang viết.
// Mỗi thư mục là một bộ truyện độc lập, ví dụ:
//
//	NOVEL_DIR=./novels/tien-hiep-ky kietnovel
//
// Khi đặt, thư mục đầu ra của truyện là <NOVEL_DIR>/output/novel
// (văn phong <outputDir>/style/ và checkpoint đi theo từng truyện).
// Không đặt = hành vi cũ: ./output/novel theo thư mục làm việc.
const NovelDirEnv = "NOVEL_DIR"

// NovelDirBase trả về thư mục gốc bộ truyện từ NOVEL_DIR ("" nếu không đặt).
func NovelDirBase() string {
	return strings.TrimSpace(os.Getenv(NovelDirEnv))
}

// ResolveOutputDir tính thư mục đầu ra của truyện:
// có NOVEL_DIR → <NOVEL_DIR tuyệt đối>/output/novel, không có → output/novel.
func ResolveOutputDir() string {
	base := NovelDirBase()
	if base == "" {
		return filepath.Join("output", "novel")
	}
	if abs, err := filepath.Abs(base); err == nil {
		base = abs
	}
	return filepath.Join(base, "output", "novel")
}

// Ngôn ngữ sáng tác truyện. Mã này điều khiển đồng thời ngôn ngữ đầu ra của
// truyện VÀ ngôn ngữ hiển thị của TUI.
const (
	LangVietnamese = "vi"
	LangEnglish    = "en"
	LangChinese    = "zh"
)

// ValidLanguage trả về mã ngôn ngữ hợp lệ, hoặc "" nếu không hợp lệ.
// So sánh không phân biệt hoa thường và chấp nhận khoảng trắng thừa.
func ValidLanguage(lang string) string {
	switch strings.ToLower(strings.TrimSpace(lang)) {
	case LangVietnamese:
		return LangVietnamese
	case LangEnglish:
		return LangEnglish
	case LangChinese:
		return LangChinese
	default:
		return ""
	}
}

// NormalizeLanguage chuẩn hóa mã ngôn ngữ về "vi"/"en"/"zh".
// Chuỗi trống hoặc mã lạ (cấu hình cũ chưa có trường language) rơi về "vi".
func NormalizeLanguage(lang string) string {
	if v := ValidLanguage(lang); v != "" {
		return v
	}
	return LangVietnamese
}

// Config là cấu hình ứng dụng tiểu thuyết.
type Config struct {
	// Trường runtime (không serialize ra JSON)
	OutputDir string `json:"-"` // Thư mục gốc đầu ra

	// Ngôn ngữ sáng tác: "vi" (mặc định) hoặc "zh". Trống = "vi" (tương thích cấu hình cũ).
	Language string `json:"language,omitempty"`

	// Cấu hình LLM mặc định
	Provider  string `json:"provider"` // Provider mặc định (key trong map Providers)
	ModelName string `json:"model"`    // Tên model mặc định
	// ReasoningEffort là cường độ suy luận mặc định top-level (off/low/medium/high/xhigh/max), trống = không override (giữ mặc định của model/provider).
	// Vai trò chưa cấu hình reasoning_effort riêng thì rơi về giá trị này.
	ReasoningEffort string `json:"reasoning_effort,omitempty"`

	// Kho credentials Provider
	Providers map[string]ProviderConfig `json:"providers,omitempty"`

	// Override model cấp vai trò
	Roles map[string]RoleConfig `json:"roles,omitempty"`

	// Tham số sáng tác
	Style string `json:"style,omitempty"`

	// ContextWindow là cửa sổ ngữ cảnh toàn cục bản cũ, giữ lại làm
	// fallback tương thích sau thời model có context_window riêng.
	// Chỉ ảnh hưởng ngưỡng nén, không đổi độ dài request thực của LLM API.
	ContextWindow int `json:"context_window,omitempty"`

	// Budget là chính sách ngân sách chi phí cho một cuốn sách; book_usd > 0 mới bật.
	Budget BudgetConfig `json:"budget,omitzero"`

	// Notify là cấu hình cảnh báo không người trực; mặc định bật (kênh system đỡ đầu).
	Notify NotifyConfig `json:"notify,omitzero"`

	// DisableUpdateCheck tắt nhắc kiểm tra phiên bản mới lúc khởi động (mặc định bật). Kiểm tra chỉ đọc
	// interface Releases công khai của GitHub, kết quả cache ở thư mục cấu hình local, không báo cáo dữ liệu gì.
	DisableUpdateCheck bool `json:"disable_update_check,omitempty"`
}

// BudgetConfig là tuyên bố chính sách của người dùng cho ví của một cuốn sách. Dừng máy khi vượt tuyến
// tương đương người dùng bấm Abort thủ công ở thời điểm đó — Host chỉ thay mặt thực thi, không đánh giá hành vi model (ranh giới hợp hiến §10 kiến trúc).
type BudgetConfig struct {
	BookUSD   float64 `json:"book_usd,omitempty"`   // Bắt buộc mới bật; 0/thiếu = không giới hạn
	WarnRatio float64 `json:"warn_ratio,omitempty"` // Mốc nước cảnh báo, mặc định 0.8
	HardStop  bool    `json:"hard_stop,omitempty"`  // true=vượt tuyến dừng ngay; mặc định đợi subtask agent hiện tại xong
}

// Enabled trả về chính sách ngân sách có bật hay không.
func (b BudgetConfig) Enabled() bool { return b.BookUSD > 0 }

// NotifyConfig là cấu hình kênh cảnh báo không người trực.
type NotifyConfig struct {
	Enabled *bool    `json:"enabled,omitempty"` // Mặc định true (kênh system dùng được không cần cấu hình)
	Command string   `json:"command,omitempty"` // Tùy chọn, cấu hình rồi thì thay kênh system (đẩy về điện thoại đi đường này)
	Events  []string `json:"events,omitempty"`  // Tùy chọn, lọc theo notify.Kinds; mặc định mở hết
}

// IsEnabled trả về cảnh báo có bật hay không (mặc định true).
func (n NotifyConfig) IsEnabled() bool { return n.Enabled == nil || *n.Enabled }

// ValidateBase kiểm tra cấu hình cơ bản.
func (c *Config) ValidateBase() error {
	if err := validateConfigText("provider", c.Provider); err != nil {
		return err
	}
	if err := validateConfigText("model", c.ModelName); err != nil {
		return err
	}

	if c.Provider == "" {
		return fmt.Errorf("thiếu provider (bắt buộc): %w", errs.ErrConfig)
	}
	if c.ModelName == "" {
		return fmt.Errorf("thiếu model (bắt buộc): %w", errs.ErrConfig)
	}

	// Ngôn ngữ sáng tác chỉ chấp nhận "vi" hoặc "zh" (trống = "vi").
	if c.Language != "" && ValidLanguage(c.Language) == "" {
		return fmt.Errorf("language phải là %q, %q hoặc %q (nhận được %q): %w",
			LangVietnamese, LangEnglish, LangChinese, c.Language, errs.ErrConfig)
	}

	// Provider mặc định phải có credentials
	pc, ok := c.Providers[c.Provider]
	if !ok {
		return fmt.Errorf("provider %q chưa cấu hình credentials trong providers; nếu đã override provider trong ./.ainovel/config.json thì phải khai báo đồng thời providers.%s (gồm api_key/base_url), không được chỉ đổi provider top-level: %w", c.Provider, c.Provider, errs.ErrConfig)
	}
	if pc.RequiresAPIKey(c.Provider) && pc.APIKey == "" {
		return fmt.Errorf("provider %q chưa cấu hình api_key: %w", c.Provider, errs.ErrConfig)
	}
	if err := validateProviderConfigText(c.Provider, pc); err != nil {
		return err
	}
	if err := c.validateProviderAPI("default", c.Provider, pc); err != nil {
		return err
	}
	for name, provider := range c.Providers {
		if err := validateConfigText("provider name", name); err != nil {
			return err
		}
		if err := validateProviderConfigText(name, provider); err != nil {
			return err
		}
		if err := c.validateProviderAPI(fmt.Sprintf("provider %q", name), name, provider); err != nil {
			return err
		}
	}

	// Kiểm tra override vai trò
	for role, rc := range c.Roles {
		if err := validateConfigText("role name", role); err != nil {
			return err
		}
		if err := validateConfigText(fmt.Sprintf("role %q provider", role), rc.Provider); err != nil {
			return err
		}
		if err := validateConfigText(fmt.Sprintf("role %q model", role), rc.Model); err != nil {
			return err
		}
		// host áp vai trò sau khi ToLower+TrimSpace (host.go), nên validate cũng vậy
		// để "Writer" trong cấu hình không bị từ chối oan.
		if !knownRoles[strings.ToLower(strings.TrimSpace(role))] {
			return fmt.Errorf("role %q không xác định trong cấu hình roles (hợp lệ: architect/writer/editor/import_segment/import_analyze/import_synthesize): %w", role, errs.ErrConfig)
		}
		if rc.Provider == "" || rc.Model == "" {
			return fmt.Errorf("role %q phải có đủ provider và model: %w", role, errs.ErrConfig)
		}
		if err := c.validateModelRef(
			fmt.Sprintf("role %q", role),
			ModelRef{Provider: rc.Provider, Model: rc.Model},
		); err != nil {
			return err
		}
		for i, fallback := range rc.Fallbacks {
			if err := validateConfigText(fmt.Sprintf("role %q fallback[%d] provider", role, i), fallback.Provider); err != nil {
				return err
			}
			if err := validateConfigText(fmt.Sprintf("role %q fallback[%d] model", role, i), fallback.Model); err != nil {
				return err
			}
			if err := c.validateModelRef(
				fmt.Sprintf("role %q fallback[%d]", role, i),
				fallback,
			); err != nil {
				return err
			}
		}
	}

	// Kiểm tra chính sách ngân sách
	if c.Budget.BookUSD < 0 {
		return fmt.Errorf("budget.book_usd phải >= 0: %w", errs.ErrConfig)
	}
	if c.Budget.Enabled() && (c.Budget.WarnRatio <= 0 || c.Budget.WarnRatio >= 1) {
		return fmt.Errorf("budget.warn_ratio phải nằm trong (0, 1): %w", errs.ErrConfig)
	}

	// Kiểm tra cấu hình cảnh báo
	if err := validateConfigText("notify.command", c.Notify.Command); err != nil {
		return err
	}
	for _, ev := range c.Notify.Events {
		if !notify.IsKnownKind(ev) {
			return fmt.Errorf("sự kiện notify %q không xác định (hợp lệ: %s): %w", ev, strings.Join(notify.Kinds(), "/"), errs.ErrConfig)
		}
	}

	return nil
}

func validateProviderConfigText(name string, pc ProviderConfig) error {
	fields := []struct {
		label string
		value string
	}{
		{label: fmt.Sprintf("provider %q type", name), value: pc.Type},
		{label: fmt.Sprintf("provider %q api", name), value: pc.API},
		{label: fmt.Sprintf("provider %q api_key", name), value: pc.APIKey},
		{label: fmt.Sprintf("provider %q base_url", name), value: pc.BaseURL},
	}
	for _, field := range fields {
		if err := validateConfigText(field.label, field.value); err != nil {
			return err
		}
	}
	seenModels := make(map[string]bool, len(pc.Models))
	for i, model := range pc.Models {
		modelName := strings.TrimSpace(model.Name)
		if err := validateConfigText(fmt.Sprintf("provider %q models[%d].name", name, i), model.Name); err != nil {
			return err
		}
		if modelName == "" {
			return fmt.Errorf("bắt buộc có provider %q models[%d].name: %w", name, i, errs.ErrConfig)
		}
		if seenModels[modelName] {
			return fmt.Errorf("provider %q bị trùng model %q: %w", name, modelName, errs.ErrConfig)
		}
		seenModels[modelName] = true
		if model.ContextWindow < 0 {
			return fmt.Errorf("provider %q model %q context_window phải >= 0: %w", name, modelName, errs.ErrConfig)
		}
	}
	switch pc.API {
	case "", "chat", "responses":
	default:
		return fmt.Errorf("provider %q api phải là chat hoặc responses: %w", name, errs.ErrConfig)
	}
	if _, err := pc.StreamIdleTimeoutValue(); err != nil {
		return fmt.Errorf("provider %q stream_idle_timeout: %w: %w", name, err, errs.ErrConfig)
	}
	return nil
}

func validateConfigText(name, value string) error {
	if utils.ContainsControl(value) {
		return fmt.Errorf("%s chứa ký tự điều khiển: %w", name, errs.ErrConfig)
	}
	return nil
}

// DefaultProviderConfig trả về cấu hình credentials của provider mặc định.
func (c *Config) DefaultProviderConfig() ProviderConfig {
	if c.Providers == nil {
		return ProviderConfig{}
	}
	return c.Providers[c.Provider]
}

// FillDefaults điền giá trị mặc định.
func (c *Config) FillDefaults() {
	if c.OutputDir == "" {
		// NOVEL_DIR (nếu có) quyết định truyện nào; cấu hình file
		// vẫn đọc theo cwd như cũ, chỉ thư mục đầu ra đi theo truyện.
		c.OutputDir = ResolveOutputDir()
	}
	// Ngôn ngữ sáng tác mặc định là Tiếng Việt; cấu hình cũ không có
	// trường language vẫn chạy như trước mà không cần sửa file.
	c.Language = NormalizeLanguage(c.Language)
	if c.Providers == nil {
		c.Providers = make(map[string]ProviderConfig)
	}
	if c.Roles == nil {
		c.Roles = make(map[string]RoleConfig)
	}
	if c.Style == "" {
		c.Style = "default"
	}
	if c.Budget.Enabled() && c.Budget.WarnRatio == 0 {
		c.Budget.WarnRatio = 0.8
	}
}

// ContextWindowSource đánh dấu nguồn lấy giá trị cửa sổ, để dùng cho log/chẩn đoán.
type ContextWindowSource string

const (
	CtxWindowModelConfig ContextWindowSource = "model_config" // Model khai báo tường minh trong provider
	CtxWindowConfig      ContextWindowSource = "config"       // context_window top-level cũ khai báo tường minh
	CtxWindowRegistry    ContextWindowSource = "registry"     // Khớp baseline OpenRouter
	CtxWindowDefault     ContextWindowSource = "default"      // Dự phòng (proxy tùy chỉnh/model lạ)
)

// ResolveContextWindow giải cửa sổ hiệu lực dùng cho nén ngữ cảnh, theo thứ tự ưu tiên:
//  1. providers.<provider>.models[].context_window
//  2. ContextWindow top-level cũ (tương thích cấu hình hiện có)
//  3. models.DefaultRegistry tra theo tên model (baseline OpenRouter + refresh 24h)
//  4. Dự phòng DefaultContextWindow (proxy tùy chỉnh / model lạ)
//
// Lưu ý: giá trị trả về chỉ dùng tính ngưỡng nén, không thu hẹp độ dài request thực của LLM API.
func (c Config) ResolveContextWindow(provider, modelName string) (int, ContextWindowSource) {
	if pc, ok := c.Providers[strings.TrimSpace(provider)]; ok {
		if model, found := pc.ModelConfig(modelName); found && model.ContextWindow > 0 {
			return model.ContextWindow, CtxWindowModelConfig
		}
	}
	if c.ContextWindow > 0 {
		return c.ContextWindow, CtxWindowConfig
	}
	if rw := models.DefaultRegistry().ResolveContextWindow(modelName); rw > 0 {
		return rw, CtxWindowRegistry
	}
	return DefaultContextWindow, CtxWindowDefault
}

// ResolveReasoningEffort trả về chuỗi cường độ suy luận hiệu lực của một vai trò (off/low/medium/high/xhigh/max hoặc trống).
// Ưu tiên: Roles[role].ReasoningEffort cấp vai trò → ReasoningEffort mặc định top-level → "" (không override, giữ mặc định của model/provider).
// role trống hoặc "default" thì lấy thẳng mặc định top-level. Tính hợp lệ của giá trị do agents.ParseThinkingLevel gác.
func (c Config) ResolveReasoningEffort(role string) string {
	if role != "" && role != "default" {
		if rc, ok := c.Roles[role]; ok && rc.ReasoningEffort != "" {
			return rc.ReasoningEffort
		}
	}
	return c.ReasoningEffort
}

// LogContextWindowChoice in quyết định cửa sổ của một vai trò. Khi source=default thì bắn Warn nhắc
// model này chưa khớp registry (OpenRouter cũng không thu thập), nén ngữ cảnh sau này sẽ kích theo cửa sổ
// dự phòng — nếu cửa sổ thực của model lớn hơn, nên chỉ định tường minh bằng context_window trong file
// cấu hình để tránh bị nén sớm, mất lịch sử.
func LogContextWindowChoice(role, model string, window int, source ContextWindowSource) {
	attrs := []any{"module", "context", "role", role, "model", model, "window", window, "source", source}
	switch source {
	case CtxWindowModelConfig:
		slog.Info("Cửa sổ ngữ cảnh (từ cấu hình model của provider)", attrs...)
	case CtxWindowDefault:
		slog.Warn("Model không nhận diện được, dùng cửa sổ dự phòng (có thể chỉ định tường minh trong providers.<name>.models[].context_window)", attrs...)
	case CtxWindowConfig:
		slog.Info("Cửa sổ ngữ cảnh (từ context_window trong file cấu hình)", attrs...)
	default:
		slog.Info("Cửa sổ ngữ cảnh", attrs...)
	}
}

// CandidateModels trả về danh sách model có thể chuyển của một provider.
// Ưu tiên models do provider khai báo tường minh; đồng thời bổ sung model của provider này đã xuất hiện trong cấu hình hiện tại.
func (c Config) CandidateModels(provider string) []string {
	if provider == "" {
		return nil
	}

	seen := make(map[string]bool)
	models := make([]string, 0, 4)
	add := func(model string) {
		model = strings.TrimSpace(model)
		if model == "" || seen[model] {
			return
		}
		seen[model] = true
		models = append(models, model)
	}

	if pc, ok := c.Providers[provider]; ok {
		for _, model := range pc.Models {
			add(model.Name)
		}
	}
	if c.Provider == provider {
		add(c.ModelName)
	}
	for _, rc := range c.Roles {
		if rc.Provider == provider {
			add(rc.Model)
		}
		for _, fallback := range rc.Fallbacks {
			if fallback.Provider == provider {
				add(fallback.Model)
			}
		}
	}
	return models
}

func (c Config) validateModelRef(owner string, ref ModelRef) error {
	if ref.Provider == "" || ref.Model == "" {
		return fmt.Errorf("%s phải có đủ provider và model: %w", owner, errs.ErrConfig)
	}

	pc, ok := c.Providers[ref.Provider]
	if !ok {
		return fmt.Errorf("%s trỏ tới provider %q chưa được cấu hình: %w", owner, ref.Provider, errs.ErrConfig)
	}
	if pc.RequiresAPIKey(ref.Provider) && pc.APIKey == "" {
		return fmt.Errorf("%s trỏ tới provider %q chưa có api_key: %w", owner, ref.Provider, errs.ErrConfig)
	}
	if err := c.validateProviderAPI(owner, ref.Provider, pc); err != nil {
		return err
	}
	return nil
}

func (c Config) validateProviderAPI(owner, providerName string, pc ProviderConfig) error {
	if pc.API == "" {
		return nil
	}
	providerType, err := pc.ProviderType(providerName)
	if err != nil {
		return fmt.Errorf("%s không giải được loại giao thức từ cấu hình api của provider %q: %w", owner, providerName, err)
	}
	if strings.ToLower(strings.TrimSpace(providerType)) != "openai" {
		return fmt.Errorf("%s api của provider %q chỉ hỗ trợ provider giao thức OpenAI: %w", owner, providerName, errs.ErrConfig)
	}
	return nil
}
