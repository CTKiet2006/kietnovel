package bootstrap

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/CTKiet2006/kietnovel/internal/errs"
)

const validGlobal = `{
  "provider": "openrouter",
  "model": "google/gemini-2.5-flash",
  "providers": { "openrouter": { "api_key": "sk-test-123456" } }
}`

// writeGlobal ghi cấu hình toàn cục dưới HOME cách ly, và trả về HOME đó.
func writeGlobal(t *testing.T, content string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	// os.UserHomeDir của Windows đọc USERPROFILE; không đặt nó sẽ đọc nhầm ~/.kietnovel thật của máy.
	t.Setenv("USERPROFILE", home)
	dir := filepath.Join(home, ".kietnovel")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("tạo thư mục: %v", err)
	}
	if content != "" {
		if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(content), 0o644); err != nil {
			t.Fatalf("ghi global: %v", err)
		}
	}
	return home
}

// writeProjectConfig ghi cấu hình cấp project dưới ./.kietnovel/ của thư mục làm việc hiện tại.
// Gọi trước cần t.Chdir tới thư mục đích.
func writeProjectConfig(t *testing.T, content string) {
	t.Helper()
	if err := os.MkdirAll(".kietnovel", 0o755); err != nil {
		t.Fatalf("tạo thư mục .kietnovel: %v", err)
	}
	if err := os.WriteFile(filepath.Join(".kietnovel", "config.json"), []byte(content), 0o644); err != nil {
		t.Fatalf("ghi project: %v", err)
	}
}

// Căn nguyên 3: project ./.kietnovel/config.json có nhưng là JSON hỏng, bắt buộc phải báo lỗi, không được nuốt lặng rồi rơi về global.
func TestLoadConfig_CorruptProjectFailsLoud(t *testing.T) {
	writeGlobal(t, validGlobal)
	proj := t.TempDir()
	t.Chdir(proj)
	// Chép tay ví dụ thừa dấu phẩy cuối — JSON hỏng thường gặp nhất.
	writeProjectConfig(t, `{ "model": "x", }`)

	if _, err := LoadConfig(); err == nil {
		t.Fatal("config.json project hỏng phải báo lỗi, đằng này lại bị bỏ qua lặng lẽ")
	}
}

// Global là nền ưu tiên thấp nhất: file hỏng không được chặn override project ưu tiên cao hơn (guard hồi quy —
// bản trước lỡ fail-loud cả global, khiến người dùng "global hỏng + project còn tốt" bị file không liên quan chặn).
func TestLoadConfig_CorruptGlobalDoesNotBlockProjectOverride(t *testing.T) {
	writeGlobal(t, `{ not json`)
	proj := t.TempDir()
	t.Chdir(proj)
	writeProjectConfig(t, validGlobal)

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("global hỏng không được chặn cấu hình project còn tốt, nhận được: %v", err)
	}
	if cfg.Provider != "openrouter" {
		t.Errorf("phải dùng giá trị của cấu hình project, nhận được provider=%q", cfg.Provider)
	}
}

// Sửa tại chỗ: thư mục project có ./.kietnovel/config.json thì EffectiveConfigPath trỏ nó (đường dẫn tuyệt đối),
// nếu không thì rơi về global — /config và /model đều căn cứ đó để quyết định chỗ ghi đĩa.
func TestEffectiveConfigPathPrefersProject(t *testing.T) {
	writeGlobal(t, validGlobal)

	t.Chdir(t.TempDir()) // Không có cấu hình project
	if got := EffectiveConfigPath(); got != DefaultConfigPath() {
		t.Fatalf("không có cấu hình project phải rơi về global, got %q want %q", got, DefaultConfigPath())
	}

	proj := t.TempDir()
	t.Chdir(proj)
	writeProjectConfig(t, validGlobal)
	wantAbs, err := filepath.Abs(filepath.Join(".kietnovel", "config.json"))
	if err != nil {
		t.Fatalf("lấy abs: %v", err)
	}
	if got := EffectiveConfigPath(); got != wantAbs {
		t.Fatalf("có cấu hình project phải ghi project, got %q want %q", got, wantAbs)
	}
}

// File không tồn tại là chuyện thường (bản portable/lần đầu), không được báo lỗi.
func TestLoadConfig_MissingFilesNoError(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home) // ~/.kietnovel/config.json không tồn tại
	t.Setenv("USERPROFILE", home)
	t.Chdir(t.TempDir()) // Cũng không có ./.kietnovel/config.json

	if _, err := LoadConfig(); err != nil {
		t.Fatalf("thiếu file cấu hình không được báo lỗi, nhận được: %v", err)
	}
}

// Đường thường: global + project merge có hiệu lực.
func TestLoadConfig_ValidMergeWorks(t *testing.T) {
	writeGlobal(t, validGlobal)
	proj := t.TempDir()
	t.Chdir(proj)
	writeProjectConfig(t, `{
  "model": "google/gemini-2.5-pro",
  "reasoning_effort": "high",
  "disable_update_check": true,
  "roles": {
    "writer": {
      "provider": "openrouter",
      "model": "google/gemini-2.5-flash",
      "reasoning_effort": "low"
    }
  }
}`)

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("cấu hình hợp lệ không được báo lỗi: %v", err)
	}
	if cfg.Provider != "openrouter" {
		t.Errorf("provider phải giữ giá trị global openrouter, nhận được %q", cfg.Provider)
	}
	if cfg.ModelName != "google/gemini-2.5-pro" {
		t.Errorf("model phải bị project override, nhận được %q", cfg.ModelName)
	}
	if cfg.ReasoningEffort != "high" {
		t.Errorf("reasoning_effort phải bị project override, nhận được %q", cfg.ReasoningEffort)
	}
	if got := cfg.Roles["writer"].ReasoningEffort; got != "low" {
		t.Errorf("roles.writer.reasoning_effort phải bị project override, nhận được %q", got)
	}
	if !cfg.DisableUpdateCheck {
		t.Error("project disable_update_check=true phải có hiệu lực")
	}
}

func TestMergeConfig_ProviderExtraFields(t *testing.T) {
	base := Config{
		Provider:  "openrouter",
		ModelName: "google/gemini-2.5-flash",
		Providers: map[string]ProviderConfig{
			"openrouter": {
				API:    "chat",
				APIKey: "sk-test-123456",
				ExtraBody: map[string]any{
					"temperature": 0.8,
				},
				Extra: map[string]any{
					"user_agent": "base-client/1.0",
				},
			},
		},
	}
	overlay := Config{
		Providers: map[string]ProviderConfig{
			"openrouter": {
				API:     "responses",
				BaseURL: "https://proxy.example.com/v1",
				ExtraBody: map[string]any{
					"min_p": 0.05,
				},
				Extra: map[string]any{
					"user_agent": "override-client/1.0",
					"headers": map[string]any{
						"X-Custom-Client": "kietnovel",
					},
				},
			},
		},
	}

	cfg := mergeConfig(base, overlay)
	pc := cfg.Providers["openrouter"]
	if pc.APIKey != "sk-test-123456" {
		t.Fatalf("APIKey = %q, want inherited key", pc.APIKey)
	}
	if pc.API != "responses" {
		t.Fatalf("API = %q, want responses", pc.API)
	}
	if pc.BaseURL != "https://proxy.example.com/v1" {
		t.Fatalf("BaseURL = %q, want overlay URL", pc.BaseURL)
	}
	if _, ok := pc.ExtraBody["temperature"]; ok {
		t.Fatalf("ExtraBody should be replaced by overlay, got %#v", pc.ExtraBody)
	}
	if got := pc.ExtraBody["min_p"]; got != 0.05 {
		t.Fatalf("ExtraBody[min_p] = %#v, want 0.05", got)
	}
	if got := pc.Extra["user_agent"]; got != "override-client/1.0" {
		t.Fatalf("Extra[user_agent] = %#v, want override-client/1.0", got)
	}
	headers, ok := pc.Extra["headers"].(map[string]any)
	if !ok {
		t.Fatalf("Extra[headers] missing or invalid: %#v", pc.Extra["headers"])
	}
	if got := headers["X-Custom-Client"]; got != "kietnovel" {
		t.Fatalf("Extra.headers[X-Custom-Client] = %#v, want kietnovel", got)
	}
}

func TestMergeConfig_DisableUpdateCheck(t *testing.T) {
	cfg := mergeConfig(Config{}, Config{DisableUpdateCheck: true})
	if !cfg.DisableUpdateCheck {
		t.Fatal("项目级 disable_update_check=true 应关闭更新检查")
	}

	// Disabling is a privacy preference, so omitting it at a higher level or writing false must not implicitly re-enable it.
	cfg = mergeConfig(Config{DisableUpdateCheck: true}, Config{})
	if !cfg.DisableUpdateCheck {
		t.Fatal("项目层未声明时应保留全局禁用偏好")
	}
}

// Root cause 2 (the core repro of issue #37): a project-level override sets a provider but declares no matching
// providers credentials, which ValidateBase must report as a config error instead of letting it through and crashing deeper down.
func TestValidateBase_ProviderOverrideWithoutCredentials(t *testing.T) {
	cfg := Config{
		Provider:  "mimo",
		ModelName: "mimo-v2.5-pro",
		Providers: map[string]ProviderConfig{
			"openrouter": {APIKey: "sk-test-123456"},
		},
	}
	cfg.FillDefaults()
	err := cfg.ValidateBase()
	if err == nil {
		t.Fatal("provider 缺凭证应报错")
	}
	if !errors.Is(err, errs.ErrConfig) {
		t.Errorf("应包装 errs.ErrConfig，得到: %v", err)
	}
}

func TestValidateBaseRejectsInvalidProviderAPI(t *testing.T) {
	cfg := Config{
		Provider:  "openai",
		ModelName: "gpt-5.1",
		Providers: map[string]ProviderConfig{
			"openai": {APIKey: "sk-test-123456", API: "legacy"},
		},
	}
	cfg.FillDefaults()
	err := cfg.ValidateBase()
	if err == nil {
		t.Fatal("provider api 非法应报错")
	}
	if !errors.Is(err, errs.ErrConfig) {
		t.Errorf("应包装 errs.ErrConfig，得到: %v", err)
	}
}

func TestValidateBaseRejectsProviderAPIOnNonOpenAIProvider(t *testing.T) {
	cfg := Config{
		Provider:  "anthropic",
		ModelName: "claude-sonnet-4",
		Providers: map[string]ProviderConfig{
			"anthropic": {APIKey: "sk-test-123456", API: "responses"},
		},
	}
	cfg.FillDefaults()
	err := cfg.ValidateBase()
	if err == nil {
		t.Fatal("非 OpenAI provider 配置 api 应报错")
	}
	if !errors.Is(err, errs.ErrConfig) {
		t.Errorf("应包装 errs.ErrConfig，得到: %v", err)
	}
}

// The sample config must be self-consistent: valid JSON once uncommented,
// no dangling top-level provider pointer, and it makes the "pointer" idea concrete -- users copy this template verbatim, so a broken one misleads them.
func TestExampleConfigIsValidAndSelfConsistent(t *testing.T) {
	if exampleConfig == "" {
		t.Fatal("go:embed 未生效，exampleConfig 为空")
	}
	rootExample, err := os.ReadFile(filepath.Join("..", "..", "config.example.jsonc"))
	if err != nil {
		t.Fatalf("读取根目录 config.example.jsonc: %v", err)
	}
	if string(rootExample) != exampleConfig {
		t.Fatal("根目录 config.example.jsonc 与 internal/bootstrap/config.example.jsonc 不一致")
	}
	var cfg Config
	if err := json.Unmarshal(stripJSONComments([]byte(exampleConfig)), &cfg); err != nil {
		t.Fatalf("内置示例去注释后不是合法 JSON（用户照抄即坑）: %v", err)
	}
	if cfg.Provider == "" || cfg.ModelName == "" {
		t.Fatal("示例应给出默认 provider/model")
	}
	if _, ok := cfg.Providers[cfg.Provider]; !ok {
		t.Errorf("示例顶层 provider %q 未指向 providers 中的条目——指针正面样板自己悬空了", cfg.Provider)
	}
	if !contains(exampleConfig, "指针") {
		t.Error("示例应点破“provider 是指针”——别让 #37 的认知陷阱回潮")
	}
}

func TestWriteStartupError(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	path := WriteStartupError("boom: provider not configured")
	if path == "" {
		t.Fatal("应返回落盘路径")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取 last-error.log: %v", err)
	}
	if want := "boom: provider not configured"; !contains(string(data), want) {
		t.Errorf("日志应包含 %q，实际: %s", want, data)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// TestNormalizeRolesVaCham — P1: "Writer" + "writer" cùng lúc là cấu hình mơ hồ
// (entry thắng phụ thuộc thứ tự duyệt map, nondeterministic) nên phải lỗi rõ
// nêu cả hai key, không đoán.
func TestNormalizeRolesVaCham(t *testing.T) {
	cfg := Config{
		Provider: "openrouter", ModelName: "m",
		Providers: map[string]ProviderConfig{"openrouter": {APIKey: "sk-test-123456"}},
		Roles: map[string]RoleConfig{
			"Writer": {Provider: "openrouter", Model: "w1"},
			"writer": {Provider: "openrouter", Model: "w2"},
		},
	}
	err := cfg.NormalizeRoles()
	if err == nil || !errors.Is(err, errs.ErrConfig) {
		t.Fatalf("va chạm key phải ErrConfig, được: %v", err)
	}
}

// TestNormalizeRolesChuanHoa — key lẻ ("Writer") về canonical, reasoning resolve đúng.
func TestNormalizeRolesChuanHoa(t *testing.T) {
	cfg := Config{
		Provider: "openrouter", ModelName: "m",
		Providers: map[string]ProviderConfig{"openrouter": {APIKey: "sk-test-123456"}},
		Roles: map[string]RoleConfig{
			"Writer": {Provider: "openrouter", Model: "w1", ReasoningEffort: "high"},
		},
	}
	if err := cfg.NormalizeRoles(); err != nil {
		t.Fatalf("key lẻ không được lỗi: %v", err)
	}
	if _, ok := cfg.Roles["Writer"]; ok {
		t.Error("key thô 'Writer' phải biến mất sau chuẩn hoá")
	}
	rc, ok := cfg.Roles["writer"]
	if !ok || rc.Model != "w1" {
		t.Fatalf("thiếu entry canonical 'writer': %+v", cfg.Roles)
	}
	if got := cfg.ResolveReasoningEffort("writer"); got != "high" {
		t.Errorf("reasoning writer = %q, mong high", got)
	}
	if got := cfg.ResolveReasoningEffort("WRITER"); got != "high" {
		t.Errorf("reasoning WRITER = %q, mong high (lookup phòng thủ)", got)
	}
}

// TestValidateBaseChuanHoaRoles — ValidateBase là choke point: đi ra map phải canonical.
func TestValidateBaseChuanHoaRoles(t *testing.T) {
	cfg := Config{
		Provider: "openrouter", ModelName: "m",
		Providers: map[string]ProviderConfig{"openrouter": {APIKey: "sk-test-123456"}},
		Roles: map[string]RoleConfig{
			"Advisor": {Provider: "openrouter", Model: "a1"},
		},
	}
	if err := cfg.ValidateBase(); err != nil {
		t.Fatalf("ValidateBase: %v", err)
	}
	if _, ok := cfg.Roles["advisor"]; !ok {
		t.Errorf("sau ValidateBase phải có key canonical: %+v", cfg.Roles)
	}
}
