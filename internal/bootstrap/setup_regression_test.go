package bootstrap

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func enterKey() tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyEnter} }

// Trùng issue #125: bước Base URL trong wizard treo vĩnh viễn với provider không
// có baseURL dựng sẵn (8/11 preset: anthropic/gemini/openai/deepseek/qwen/glm/
// grok/bedrock/custom). Enter trên ô trống phải được chấp nhận.
func TestSetupInput_EmptyEnterAlwaysQuits(t *testing.T) {
	cases := []struct {
		name         string
		defaultValue string
	}{
		{"khong co mac dinh (8/11 preset)", ""},
		{"co mac dinh (openrouter/ollama)", "https://openrouter.ai/api/v1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// Đúng cách runTextInputWithDefault khởi tạo model sau khi sửa.
			m := setupInputModel{
				label:        "[3/5] Base URL",
				placeholder:  "Bỏ trống để dùng địa chỉ chính thức",
				defaultValue: c.defaultValue,
				allowEmpty:   true,
			}
			next, cmd := m.Update(enterKey())
			if cmd == nil {
				t.Fatal("Enter trên ô trống phải kết thúc prompt, không được nuốt phím (treo wizard)")
			}
			if res := next.(setupInputModel); res.cancelled {
				t.Fatal("Enter không được hiểu là hủy")
			}
		})
	}
}

// Ô bắt buộc (tên model) vẫn phải chặn Enter khi trống — không nới theo quá tay.
func TestSetupInput_RequiredFieldStillBlocksEmpty(t *testing.T) {
	m := setupInputModel{label: "[4/5] Tên model", placeholder: "Ví dụ: gpt-4o"}
	_, cmd := m.Update(enterKey())
	if cmd != nil {
		t.Fatal("ô bắt buộc không được cho phép Enter khi trống")
	}
}

// Enter khi đã có nội dung thì thoát bình thường.
func TestSetupInput_NonEmptyEnterQuits(t *testing.T) {
	m := setupInputModel{label: "[4/5] Tên model", allowEmpty: true, value: "gpt-4o"}
	_, cmd := m.Update(enterKey())
	if cmd == nil {
		t.Fatal("Enter khi đã nhập phải thoát prompt")
	}
}

// Trùng issue #124: "Ollama"/"Bedrock" viết hoa phải được chấp nhận — tầng LLM
// (llm.IsProviderRegistered) đã ToLower, tầng cấu hình phải khớp.
func TestRequiresAPIKey_CaseInsensitive(t *testing.T) {
	for _, name := range []string{"ollama", "Ollama", "OLLAMA", " ollama "} {
		if (ProviderConfig{}).RequiresAPIKey(name) {
			t.Fatalf("%q phải được miễn api_key", name)
		}
	}
	for _, name := range []string{"bedrock", "BedRock"} {
		if (ProviderConfig{}).RequiresAPIKey(name) {
			t.Fatalf("%q phải được miễn api_key", name)
		}
	}
	if !(ProviderConfig{}).RequiresAPIKey("openrouter") {
		t.Fatal("openrouter vẫn phải yêu cầu api_key")
	}
}

// Trùng issue #124/#125: tên vai trò viết hoa phải hợp lệ vì host cũng ToLower.
func TestValidateBase_RoleNameCaseInsensitive(t *testing.T) {
	cfg := Config{
		Provider:  "openrouter",
		ModelName: "m1",
		Providers: map[string]ProviderConfig{
			"openrouter": {APIKey: "sk-x", Models: []ModelConfig{{Name: "m1"}}},
		},
		Roles: map[string]RoleConfig{
			"Writer": {Provider: "openrouter", Model: "m1"},
		},
	}
	if err := cfg.ValidateBase(); err != nil {
		t.Fatalf(`roles {"Writer": ...} phải hợp lệ: %v`, err)
	}
}

// Trùng issue #124: tên provider viết hoa không được đổi hành vi yêu cầu key.
func TestValidateBase_ProviderNameCaseInsensitiveKeyRule(t *testing.T) {
	cfg := Config{
		Provider:  "Ollama",
		ModelName: "qwen3:14b",
		Providers: map[string]ProviderConfig{
			"Ollama": {Type: "openai", BaseURL: "http://localhost:11434/v1", Models: []ModelConfig{{Name: "qwen3:14b"}}},
		},
	}
	if err := cfg.ValidateBase(); err != nil {
		t.Fatalf(`provider "Ollama" kiểu tùy chỉnh phải qua được validate: %v`, err)
	}
	// Ngược lại: provider không có Type thì vẫn phải đòi key, dù viết hoa.
	cfg2 := Config{
		Provider:  "OpenRouter",
		ModelName: "m1",
		Providers: map[string]ProviderConfig{
			"OpenRouter": {APIKey: "sk-x", Models: []ModelConfig{{Name: "m1"}}},
		},
	}
	if err := cfg2.ValidateBase(); err != nil {
		t.Fatalf("provider có key phải hợp lệ: %v", err)
	}
	cfg2.Providers["OpenRouter"] = ProviderConfig{Models: []ModelConfig{{Name: "m1"}}}
	if err := cfg2.ValidateBase(); err == nil || !strings.Contains(err.Error(), "api_key") {
		t.Fatalf("provider thiếu key phải bị chặn, got %v", err)
	}
}
