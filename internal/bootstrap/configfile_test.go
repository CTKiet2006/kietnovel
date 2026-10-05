package bootstrap

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/voocel/ainovel-cli/internal/errs"
)

const validGlobal = `{
  "provider": "openrouter",
  "model": "google/gemini-2.5-flash",
  "providers": { "openrouter": { "api_key": "sk-test-123456" } }
}`

// writeGlobal 在隔离的 HOME 下写入全局配置，并返回该 HOME。
func writeGlobal(t *testing.T, content string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	// Windows 的 os.UserHomeDir 读 USERPROFILE；不设它会读到本机真实 ~/.ainovel。
	t.Setenv("USERPROFILE", home)
	dir := filepath.Join(home, ".ainovel")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if content != "" {
		if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(content), 0o644); err != nil {
			t.Fatalf("write global: %v", err)
		}
	}
	return home
}

// writeProjectConfig 在当前工作目录的 ./.ainovel/ 下写入项目级配置。
// 调用前需先 t.Chdir 到目标目录。
func writeProjectConfig(t *testing.T, content string) {
	t.Helper()
	if err := os.MkdirAll(".ainovel", 0o755); err != nil {
		t.Fatalf("mkdir .ainovel: %v", err)
	}
	if err := os.WriteFile(filepath.Join(".ainovel", "config.json"), []byte(content), 0o644); err != nil {
		t.Fatalf("write project: %v", err)
	}
}

// 根因 3：项目级 ./.ainovel/config.json 存在但是坏 JSON，必须报错，不能静默吞掉退回全局。
func TestLoadConfig_CorruptProjectFailsLoud(t *testing.T) {
	writeGlobal(t, validGlobal)
	proj := t.TempDir()
	t.Chdir(proj)
	// 手抄示例多了个尾逗号——最常见的坏 JSON。
	writeProjectConfig(t, `{ "model": "x", }`)

	if _, err := LoadConfig(); err == nil {
		t.Fatal("坏的 ./.ainovel/config.json 应当报错，却被静默忽略了")
	}
}

// 全局是最低优先级基底：坏文件不得阻断更高优先级的项目级覆盖（回归守卫——
// 上一版误把全局也 fail-loud，导致"坏全局 + 有效项目配置"的用户被无关文件挡住）。
func TestLoadConfig_CorruptGlobalDoesNotBlockProjectOverride(t *testing.T) {
	writeGlobal(t, `{ not json`)
	proj := t.TempDir()
	t.Chdir(proj)
	writeProjectConfig(t, validGlobal)

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("坏全局不应阻断有效项目级配置，得到: %v", err)
	}
	if cfg.Provider != "openrouter" {
		t.Errorf("应使用项目级配置的值，得到 provider=%q", cfg.Provider)
	}
}

// 就近编辑：项目目录有 ./.ainovel/config.json 时 EffectiveConfigPath 指向它（绝对路径），
// 否则回落全局——/config 与 /model 都据此决定写盘位置。
func TestEffectiveConfigPathPrefersProject(t *testing.T) {
	writeGlobal(t, validGlobal)

	t.Chdir(t.TempDir()) // 无项目配置
	if got := EffectiveConfigPath(); got != DefaultConfigPath() {
		t.Fatalf("无项目配置应回落全局，got %q want %q", got, DefaultConfigPath())
	}

	proj := t.TempDir()
	t.Chdir(proj)
	writeProjectConfig(t, validGlobal)
	wantAbs, err := filepath.Abs(filepath.Join(".ainovel", "config.json"))
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	if got := EffectiveConfigPath(); got != wantAbs {
		t.Fatalf("有项目配置应写项目，got %q want %q", got, wantAbs)
	}
}

// 文件不存在是正常情况（便携/首次），不能报错。
func TestLoadConfig_MissingFilesNoError(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home) // ~/.ainovel/config.json 不存在
	t.Setenv("USERPROFILE", home)
	t.Chdir(t.TempDir()) // 也没有 ./.ainovel/config.json

	if _, err := LoadConfig(); err != nil {
		t.Fatalf("缺失配置文件不应报错，得到: %v", err)
	}
}

// 正常路径：全局 + 项目级合并生效。
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
		t.Fatalf("有效配置不应报错: %v", err)
	}
	if cfg.Provider != "openrouter" {
		t.Errorf("provider 应保留全局值 openrouter，得到 %q", cfg.Provider)
	}
	if cfg.ModelName != "google/gemini-2.5-pro" {
		t.Errorf("model 应被项目级覆盖，得到 %q", cfg.ModelName)
	}
	if cfg.ReasoningEffort != "high" {
		t.Errorf("reasoning_effort 应被项目级覆盖，得到 %q", cfg.ReasoningEffort)
	}
	if got := cfg.Roles["writer"].ReasoningEffort; got != "low" {
		t.Errorf("roles.writer.reasoning_effort 应被项目级覆盖，得到 %q", got)
	}
	if !cfg.DisableUpdateCheck {
		t.Error("项目级 disable_update_check=true 应生效")
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
						"X-Custom-Client": "ainovel",
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
	if got := headers["X-Custom-Client"]; got != "ainovel" {
		t.Fatalf("Extra.headers[X-Custom-Client] = %#v, want ainovel", got)
	}
}

func TestMergeConfig_DisableUpdateCheck(t *testing.T) {
	cfg := mergeConfig(Config{}, Config{DisableUpdateCheck: true})
	if !cfg.DisableUpdateCheck {
		t.Fatal("项目级 disable_update_check=true 应关闭更新检查")
	}

	// 禁用属于隐私偏好，较高层省略或写 false 都不应隐式重新开启。
	cfg = mergeConfig(Config{DisableUpdateCheck: true}, Config{})
	if !cfg.DisableUpdateCheck {
		t.Fatal("项目层未声明时应保留全局禁用偏好")
	}
}

// 根因 2（issue #37 核心复现）：项目级覆盖 provider 但没声明对应 providers 凭证，
// ValidateBase 必须报 config 错误（而非放行后在更深处崩溃）。
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

// 示例配置必须自洽：去注释后是合法 JSON、
// 顶层 provider 指针不悬空、且点破了“指针”心智——它是用户照抄的样板，自己坏了就坑人。
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
