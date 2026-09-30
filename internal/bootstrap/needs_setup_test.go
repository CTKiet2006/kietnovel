package bootstrap

import (
	"os"
	"path/filepath"
	"testing"
)

// TestNeedsSetupKhongDungTrenConfigRong — lỗi thật đã gặp.
//
// NeedsSetup trước đây chỉ os.Stat, nên config.json rỗng vẫn bị coi là "đã
// thiết lập", rồi chết ở "thiếu provider (bắt buộc)" mà không có Setup Wizard và
// không có đường thoát. Người dùng mới cài lần đầu dính đúng, và bất kỳ ai bị
// hỏng cấu hình giữa chừng cũng dính.
func TestNeedsSetupKhongDungTrenConfigRong(t *testing.T) {
	home := t.TempDir()
	t.Setenv("KIETNOVEL_CONFIG_HOME", home)
	dir := filepath.Join(home, configDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// File tồn tại nhưng rỗng — đúng trạng thái test trước đó để lại trên máy
	// người dùng.
	if err := os.WriteFile(filepath.Join(dir, "config.json"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if !NeedsSetup() {
		t.Error("config rong phai bat buoc chay Setup Wizard, khong phai chet o loi provider")
	}
}

func TestNeedsSetupChayKhiConfigThieuProvider(t *testing.T) {
	home := t.TempDir()
	t.Setenv("KIETNOVEL_CONFIG_HOME", home)
	dir := filepath.Join(home, configDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Có provider nhưng thiếu model.
	body := `{"provider":"openrouter","providers":{"openrouter":{"api_key":"x"}}}`
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if !NeedsSetup() {
		t.Error("config thieu model phai chay Setup Wizard")
	}
}

func TestNeedsSetupChayKhiConfigHongJSON(t *testing.T) {
	home := t.TempDir()
	t.Setenv("KIETNOVEL_CONFIG_HOME", home)
	dir := filepath.Join(home, configDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte("{ khong phai json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !NeedsSetup() {
		t.Error("config hong JSON phai chay Setup Wizard thay vi chet khong huong dan")
	}
}

// Config đầy đủ thì KHÔNG chạy wizard — phải giữ nguyên hành vi cũ, không phải
// cứ khởi động là hỏi lại.
func TestNeedsSetupKhongChayKhiConfigDayDu(t *testing.T) {
	home := t.TempDir()
	t.Setenv("KIETNOVEL_CONFIG_HOME", home)
	dir := filepath.Join(home, configDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"provider":"openrouter","model":"stealth/space-bunny-alpha",
	          "providers":{"openrouter":{"api_key":"x","base_url":"https://openrouter.ai/api/v1"}}}`
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if NeedsSetup() {
		t.Error("config day du khong duoc bat Setup Wizard")
	}
}

func TestNeedsSetupChayKhiChuaCoFile(t *testing.T) {
	t.Setenv("KIETNOVEL_CONFIG_HOME", t.TempDir())
	if !NeedsSetup() {
		t.Error("chua co config phai chay Setup Wizard")
	}
}
