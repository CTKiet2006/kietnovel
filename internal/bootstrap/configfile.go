package bootstrap

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

const configDirName = ".ainovel"

// DefaultConfigPath trả về đường dẫn file cấu hình toàn cục ~/.ainovel/config.json.
func DefaultConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, configDirName, "config.json")
}

// DefaultConfigDir trả về đường dẫn thư mục ~/.ainovel; khi không lấy được home thì trả chuỗi trống.
// Chỉ dùng để đọc/ghi file không bắt buộc tồn tại (như cache model), không tự tạo thư mục.
func DefaultConfigDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, configDirName)
}

// configDir trả về đường dẫn thư mục ~/.ainovel, chưa có thì tạo.
func configDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("home dir: %w", err)
	}
	dir := filepath.Join(home, configDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create config dir: %w", err)
	}
	return dir, nil
}

// projectConfigPath trả về đường dẫn tương đối của file cấu hình cấp project ./.ainovel/config.json.
// Dotdir cấp project mirror toàn cục ~/.ainovel/, dùng chung configDirName; giải theo cwd.
func projectConfigPath() string {
	return filepath.Join(configDirName, "config.json")
}

// EffectiveConfigPath trả về file cấu hình mà thao tác TUI (/config, /model) nên ghi về:
// thư mục project có ./.ainovel/config.json thì ghi nó — cùng hướng project đè global lúc đọc,
// đảm bảo "sửa đúng bản đang hiệu lực", sửa xong hiệu lực ngay; nếu không thì ghi toàn cục ~/.ainovel/config.json.
// Chỉ sửa cấu hình project đã tồn tại, không tự tạo từ hư không (tạo project overlay là hành động chủ động đặt file của người dùng).
func EffectiveConfigPath() string {
	rel := projectConfigPath()
	if _, err := os.Stat(rel); err == nil {
		if abs, err := filepath.Abs(rel); err == nil {
			return abs
		}
		return rel
	}
	return DefaultConfigPath()
}

// LoadConfig tải và merge cấu hình theo thứ tự ưu tiên:
//  1. ~/.ainovel/config.json (toàn cục)
//  2. ./.ainovel/config.json (project override)
func LoadConfig() (Config, error) {
	var cfg Config

	// 1. Cấu hình toàn cục. Nó là nền ưu tiên thấp nhất, file hỏng thì hạ thành cảnh báo chứ không chặn —
	//    vì có thể bị project override; fail cứng sẽ chặn oan người dùng "global hỏng + project còn tốt".
	//    NHƯNG: nếu không có project override thì nuốt lặng là sai — cấu hình rỗng sẽ đẩy người dùng
	//    đi săn lỗi "thiếu provider" trong khi nguyên nhân thật là JSON sai cú pháp ở tầng trên
	//    (issue #124). Vì vậy phải đợi biết kết quả project rồi mới quyết định nâng cảnh báo thành lỗi.
	var globalErr error
	var globalPath string
	if p := DefaultConfigPath(); p != "" {
		global, found, err := loadOptionalJSON(p)
		globalPath, globalErr = p, err
		switch {
		case err != nil:
			// xử lý sau, khi đã biết có project override cứu được không
			slog.Warn("Giải parse cấu hình toàn cục thất bại, tạm bỏ qua (có thể được project override)", "module", "config", "path", p, "err", err)
		case found:
			cfg = global
		}
	}

	// 2. Project override. File hỏng thì fail to: đây là cấu hình người dùng chủ động đặt ở thư mục hiện tại,
	//    nuốt lặng sẽ khiến "cấu hình mà không hiệu lực" không còn đường dò (issue #37).
	project, found, err := loadOptionalJSON(projectConfigPath())
	if err != nil {
		return cfg, fmt.Errorf("giải parse cấu hình project ./.ainovel/config.json thất bại (hãy kiểm tra cú pháp JSON): %w", err)
	}
	if found {
		cfg = mergeConfig(cfg, project)
	}

	// Không có project override thì cấu hình toàn cục là nguồn duy nhất: hỏng ở đây
	// phải nói thẳng ra thay vì để người dùng đi tìm lỗi ở tầng dưới.
	if globalErr != nil && !found {
		return cfg, fmt.Errorf("giải parse cấu hình toàn cục %s thất bại và không có ./.ainovel/config.json để ghi đè (hãy kiểm tra cú pháp JSON): %w", globalPath, globalErr)
	}

	return cfg, nil
}

// loadOptionalJSON đọc một file cấu hình tùy chọn:
//   - file không tồn tại → (zero, false, nil), bên gọi tự quyết dùng mặc định/giá trị tầng trên
//   - file có nhưng giải parse thất bại → trả lỗi (không nuốt lặng nữa — nếu không cấu hình của người dùng
//     "đặt mà không ăn" lại không đường dò, đúng căn nguyên issue #37)
func loadOptionalJSON(path string) (Config, bool, error) {
	cfg, err := loadJSONFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Config{}, false, nil
		}
		return Config{}, false, err
	}
	return cfg, true, nil
}

// LoadConfigFile đọc một file JSON cấu hình, hỗ trợ comment dòng //.
// Không merge gì, chỉ trả cấu hình của riêng file đó. File không tồn tại thì trả lỗi.
func LoadConfigFile(path string) (Config, error) {
	return loadJSONFile(path)
}

// loadJSONFile đọc file JSON cấu hình, hỗ trợ comment dòng //.
// File không tồn tại thì trả lỗi (bên gọi tự quyết có bỏ qua hay không).
func loadJSONFile(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	cleaned := stripJSONComments(data)
	var cfg Config
	if err := json.Unmarshal(cleaned, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return cfg, nil
}

// mergeConfig merge overlay lên base. Field khác zero thì đè, map thì merge theo key.
func mergeConfig(base, overlay Config) Config {
	if overlay.Provider != "" {
		base.Provider = overlay.Provider
	}
	if overlay.ModelName != "" {
		base.ModelName = overlay.ModelName
	}
	if overlay.ReasoningEffort != "" {
		base.ReasoningEffort = overlay.ReasoningEffort
	}
	// Ngôn ngữ sáng tác: project-level đè global khi khai báo tường minh.
	if overlay.Language != "" {
		base.Language = NormalizeLanguage(overlay.Language)
	}
	if overlay.Style != "" {
		base.Style = overlay.Style
	}
	if overlay.ContextWindow > 0 {
		base.ContextWindow = overlay.ContextWindow
	}

	// Providers: key của overlay đè key cùng tên của base
	if len(overlay.Providers) > 0 {
		if base.Providers == nil {
			base.Providers = make(map[string]ProviderConfig)
		}
		for k, v := range overlay.Providers {
			existing := base.Providers[k]
			if v.Type != "" {
				existing.Type = v.Type
			}
			if v.API != "" {
				existing.API = v.API
			}
			if v.APIKey != "" {
				existing.APIKey = v.APIKey
			}
			if v.BaseURL != "" {
				existing.BaseURL = v.BaseURL
			}
			if len(v.Models) > 0 {
				existing.Models = append([]ModelConfig(nil), v.Models...)
			}
			if len(v.ExtraBody) > 0 {
				existing.ExtraBody = cloneMap(v.ExtraBody)
			}
			if len(v.Extra) > 0 {
				existing.Extra = cloneMap(v.Extra)
			}
			base.Providers[k] = existing
		}
	}

	// Roles: key của overlay đè key cùng tên của base
	if len(overlay.Roles) > 0 {
		if base.Roles == nil {
			base.Roles = make(map[string]RoleConfig)
		}
		for k, v := range overlay.Roles {
			existing := base.Roles[k]
			if v.Provider != "" {
				existing.Provider = v.Provider
			}
			if v.Model != "" {
				existing.Model = v.Model
			}
			if len(v.Fallbacks) > 0 {
				existing.Fallbacks = append([]ModelRef(nil), v.Fallbacks...)
			}
			if v.ReasoningEffort != "" {
				existing.ReasoningEffort = v.ReasoningEffort
			}
			base.Roles[k] = existing
		}
	}

	// Budget / Notify: đè nguyên khối (budget/cảnh báo cấp project là tuyên bố chính sách độc lập, không ghép từng field với global)
	if overlay.Budget != (BudgetConfig{}) {
		base.Budget = overlay.Budget
	}
	if overlay.Notify.Enabled != nil || overlay.Notify.Command != "" || len(overlay.Notify.Events) > 0 {
		base.Notify = overlay.Notify
	}
	// Kiểm tra cập nhật là tùy chọn riêng tư: tầng nào đã tắt tường minh thì tầng cao hơn không được ngầm bật lại.
	if overlay.DisableUpdateCheck {
		base.DisableUpdateCheck = true
	}

	return base
}

func cloneMap(m map[string]any) map[string]any {
	if len(m) == 0 {
		return nil
	}
	c := make(map[string]any, len(m))
	for k, v := range m {
		c[k] = v
	}
	return c
}

// CloneConfig deep-copy các map/slice sẽ bị sửa lúc runtime, tránh cấu hình ứng viên làm bẩn cấu hình hiện tại.
func CloneConfig(cfg Config) Config {
	clone := cfg
	clone.Providers = make(map[string]ProviderConfig, len(cfg.Providers))
	for name, pc := range cfg.Providers {
		pc.Models = append([]ModelConfig(nil), pc.Models...)
		pc.Extra = cloneMap(pc.Extra)
		pc.ExtraBody = cloneMap(pc.ExtraBody)
		clone.Providers[name] = pc
	}
	clone.Roles = make(map[string]RoleConfig, len(cfg.Roles))
	for role, rc := range cfg.Roles {
		rc.Fallbacks = append([]ModelRef(nil), rc.Fallbacks...)
		clone.Roles[role] = rc
	}
	clone.Notify.Events = append([]string(nil), cfg.Notify.Events...)
	return clone
}

// SaveProviderConfig cập nhật patch credentials và kho model của một provider trong tầng cấu hình đích.
// Chỉ đụng đoạn providers, tuyệt đối không chạm chọn lựa provider/model top-level — "đang dùng cái nào" là việc của /model.
// Đích chưa có thì tạo cấu hình tối thiểu; đích hỏng thì từ chối ghi đè.
func SaveProviderConfig(path string, provider string, pc ProviderConfig) error {
	target, found, err := loadOptionalJSON(path)
	if err != nil {
		return err
	}
	if !found {
		target = Config{}
	}
	if target.Providers == nil {
		target.Providers = make(map[string]ProviderConfig)
	}
	target.Providers[provider] = pc
	return SaveConfig(path, target)
}

// stripJSONComments gỡ comment dòng // trong JSON, theo dõi trạng thái trong ngoặc kép để tránh xóa nhầm nội dung chuỗi.
func stripJSONComments(data []byte) []byte {
	out := make([]byte, 0, len(data))
	inString := false
	escaped := false

	for i := 0; i < len(data); i++ {
		b := data[i]

		if escaped {
			out = append(out, b)
			escaped = false
			continue
		}

		if inString {
			out = append(out, b)
			if b == '\\' {
				escaped = true
			} else if b == '"' {
				inString = false
			}
			continue
		}

		// Không trong chuỗi
		if b == '"' {
			inString = true
			out = append(out, b)
			continue
		}

		// Phát hiện comment //
		if b == '/' && i+1 < len(data) && data[i+1] == '/' {
			// Bỏ tới cuối dòng
			for i < len(data) && data[i] != '\n' {
				i++
			}
			if i < len(data) {
				out = append(out, '\n')
			}
			continue
		}

		out = append(out, b)
	}

	return out
}

// WriteStartupError append lỗi chí mạng lúc khởi động vào ~/.ainovel/last-error.log, và trả về
// đường dẫn file đó (best-effort, thất bại thì trả chuỗi trống). Khi double-click khởi động, cửa sổ console
// đóng ngay theo tiến trình thoát, lỗi thoáng qua rồi mất — ghi đĩa là đường truy vết duy nhất của nhóm người dùng này.
func WriteStartupError(msg string) string {
	dir := DefaultConfigDir()
	if dir == "" {
		return ""
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ""
	}
	path := filepath.Join(dir, "last-error.log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return ""
	}
	defer f.Close()
	if _, err := fmt.Fprintf(f, "[%s] %s\n", time.Now().Format(time.RFC3339), msg); err != nil {
		return ""
	}
	return path
}

// SaveConfig ghi cấu hình ra đường dẫn chỉ định (dạng JSON, indent đẹp).
func SaveConfig(path string, cfg Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	committed := false
	defer func() {
		_ = tmp.Close()
		if !committed {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return err
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	committed = true
	return nil
}
