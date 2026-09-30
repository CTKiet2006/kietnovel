package bootstrap

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBookLanguageOfChuaKhoa(t *testing.T) {
	dir := t.TempDir()
	got, err := BookLanguageOf(dir)
	if err != nil {
		t.Fatalf("truyen chua khoa khong duoc la loi: %v", err)
	}
	if got != "" {
		t.Errorf("= %q, mong rong", got)
	}
}

func TestBookLanguageOfDaKhoa(t *testing.T) {
	dir := t.TempDir()
	write := func(lang string) {
		if err := os.MkdirAll(filepath.Join(dir, "meta"), 0o755); err != nil {
			t.Fatal(err)
		}
		body := `{"language":"` + lang + `"}`
		if err := os.WriteFile(filepath.Join(dir, "meta", "language.json"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("zh")
	if got, _ := BookLanguageOf(dir); got != "zh" {
		t.Errorf("= %q, mong zh", got)
	}
	// Ghi đè được: đổi ngôn ngữ có chủ ý thì phải đổi được.
	write("en")
	if got, _ := BookLanguageOf(dir); got != "en" {
		t.Errorf("ghi de khong duoc: = %q, mong en", got)
	}
}

// File hỏng KHÔNG được làm hỏng lượt chạy — coi như chưa khoá rồi hỏi lại.
func TestBookLanguageOfFileHongKhongLoi(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "meta"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "meta", "language.json"), []byte("{ hong"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := BookLanguageOf(dir)
	if err != nil {
		t.Fatalf("file hong phai coi nhu chua khoa, khong phai loi: %v", err)
	}
	if got != "" {
		t.Errorf("= %q, mong rong de hoi lai", got)
	}
}

func TestBookLanguageOfDirRong(t *testing.T) {
	if got, err := BookLanguageOf(""); err != nil || got != "" {
		t.Errorf("dir rong phai tra rong, khong loi: %q %v", got, err)
	}
}

// Thông điệp phải chỉ CÁCH sửa, không chỉ báo thiếu gì.
func TestNeedBookLanguageMessageCoCachSua(t *testing.T) {
	msg := NeedBookLanguageMessage("C:/x/output/novel").Error()
	for _, must := range []string{"vi|en|zh", "TUI", "headless"} {
		if !strings.Contains(msg, must) {
			t.Errorf("thông điệp thiếu %q: %q", must, msg)
		}
	}
}
