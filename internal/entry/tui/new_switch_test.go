package tui

import (
	"os"
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
	m.width, m.height = 120, 40

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
	// Mất kích thước terminal thì View() chỉ vẽ "Đang tải..." — người dùng thấy
	// truyện đã chuyển nhưng UI trống, dễ tưởng /new hỏng.
	if m.width == 0 || m.height == 0 {
		t.Errorf("/new xong mất kích thước terminal: %dx%d", m.width, m.height)
	}
	// Host mới giữ khoá thư mục truyện mới: phải đóng trước khi TempDir xoá,
	// nếu không Windows giữ file .kietnovel.lock và test fail vì cleanup.
	t.Cleanup(m.runtime.Close)
}

// TestStartTaoVaChuyenTrongMotLenh là regression cho yêu cầu: /start phải là
// MỘT lệnh. Trước đây truyện đã có nội dung thì /start báo lỗi "dùng /new",
// bắt người dùng tự gõ /new rồi /start — hai lệnh nối tiếp, dễ quên.
//
// Giờ: /start mở khung tạo truyện mới, tạo xong chuyển truyện VÀ mang theo
// prompt để engine chạy nốt — không cần gõ thêm lệnh nào.
func TestStartTaoVaChuyenTrongMotLenh(t *testing.T) {
	base := t.TempDir()
	rt := newTestHost(t, base)
	oldDir := rt.Dir()

	m := NewModel(rt, "test")
	m.cfg = testCfg(base)
	m.hostOpts = nil
	m.mode = modeRunning
	m.snapshot = host.UISnapshot{Phase: "writing", CompletedCount: 12}

	// Bước 1: /start <file> khi truyện đang mở đã có nội dung → mở khung tạo mới.
	dir := filepath.Join(t.TempDir(), "dan-y")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "outline.md")
	if err := os.WriteFile(path, []byte("Cốt truyện mẫu\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd, ok := parseSlashCommand("/start " + path)
	if !ok {
		t.Fatal("/start phải phân tích được")
	}
	out, _ := m.handleSlashCommand(cmd)
	m = out.(Model)
	if m.books == nil {
		t.Fatal("/start trên truyện cũ phải mở khung tạo truyện mới")
	}
	if m.books.startAfterCreate == "" {
		t.Fatal("phải nhớ prompt để chạy nốt, không thì tạo xong rồi đứng im")
	}

	// Bước 2: gõ tên rồi Enter.
	m.books.draft = "Truyen Tu File"
	out, _ = m.createBookConfirmed()
	m = out.(Model)

	want := filepath.Join(base, "output", "truyen-tu-file")
	if !sameDir(m.runtime.Dir(), want) {
		t.Errorf("/start chưa chuyển sang truyện mới:\n  cũ  = %s\n  mới = %s", m.runtime.Dir(), want)
	}
	if sameDir(m.runtime.Dir(), oldDir) {
		t.Error("runtime vẫn trỏ truyện cũ sau /start")
	}
	// Prompt phải sống sót qua bước dựng Model mới, không bị switchBook bỏ rơi.
	if m.pendingStart == "" {
		t.Error("prompt /start bị mất khi chuyển truyện — sẽ không bao giờ chạy engine")
	}
	t.Cleanup(m.runtime.Close)
}
