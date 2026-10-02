package tui

import (
	"path/filepath"
	"testing"

	"github.com/CTKiet2006/kietnovel/assets"
	"github.com/CTKiet2006/kietnovel/internal/bootstrap"
	"github.com/CTKiet2006/kietnovel/internal/host"
)

// newTestHost dựng Host thật trên thư mục tạm để test luồng chuyển truyện
// end-to-end. Cấu hình tối thiểu: provider proxy + 1 model, không cần mạng vì
// không gọi LLM.
func testCfg(base string) bootstrap.Config {
	pc := bootstrap.ProviderConfig{
		Type: "openai", APIKey: "test-key", BaseURL: "https://example.invalid/v1",
		Models: []bootstrap.ModelConfig{{Name: "test-model", ContextWindow: 128000}},
	}
	return bootstrap.Config{
		Provider:  "proxy",
		ModelName: "test-model",
		Providers: map[string]bootstrap.ProviderConfig{"proxy": pc},
		OutputDir: filepath.Join(base, "output", "novel"),
	}
}

// newTestHost dựng Host thật trên thư mục tạm để test luồng chuyển truyện
// end-to-end. Cấu hình tối thiểu: provider proxy + 1 model, không cần mạng vì
// không gọi LLM.
func newTestHost(t *testing.T, base string) *host.Host {
	t.Helper()
	rt, err := host.New(testCfg(base), assets.LoadWithLanguage("vi", "default", assets.DefaultLoadOptions(testCfg(base).OutputDir)))
	if err != nil {
		t.Fatalf("host.New: %v", err)
	}
	t.Cleanup(rt.Close)
	return rt
}

// TestNewChuyenSangTruyenMoi là regression cho lỗi người dùng báo: /new tạo
// xong thư mục nhưng TUI vẫn bám truyện cũ, không sang truyện vừa tạo.
// Kiểm tra cả tên thư mục lẫn runtime.Dir() sau khi chuyển.
func TestNewChuyenSangTruyenMoi(t *testing.T) {
	base := t.TempDir()
	rt := newTestHost(t, base)
	oldDir := rt.Dir()

	m := NewModel(rt, "test")
	// m.cfg phải giữ nguyên cấu hình đầy đủ như lúc khởi động app: switchBook
	// dựng Host mới TỪ m.cfg, thiếu providers thì host.New fail và /new im lặng
	// không chuyển.
	m.cfg = testCfg(base)
	m.hostOpts = nil

	// Gõ /new <tên> rồi Enter: đi đúng đường người dùng gặp.
	m.books = newBooksState(100, 30, booksNewDraft)
	m.books.draft = "Truyen Moi"
	out, _ := m.createBookConfirmed()
	m = out.(Model)

	if m.books != nil {
		t.Fatalf("/new xong vẫn còn modal, mode=%v err=%q", m.books.mode, m.booksErr)
	}
	want := filepath.Join(base, "output", "truyen-moi")
	if m.runtime == nil {
		t.Fatal("sau /new phải còn runtime")
	}
	if !sameDir(m.runtime.Dir(), want) {
		t.Errorf("/new chưa chuyển sang truyện mới:\n  cũ  = %s\n  mới = %s", m.runtime.Dir(), want)
	}
	if sameDir(m.runtime.Dir(), oldDir) {
		t.Error("runtime vẫn trỏ truyện cũ sau /new")
	}
	// Host mới giữ khoá thư mục truyện mới: phải đóng trước khi TempDir xoá,
	// nếu không Windows giữ file .kietnovel.lock và test fail vì cleanup.
	t.Cleanup(m.runtime.Close)
}
