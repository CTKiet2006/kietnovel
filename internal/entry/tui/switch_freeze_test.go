package tui

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/CTKiet2006/kietnovel/assets"
	"github.com/CTKiet2006/kietnovel/internal/host"
)

// TestChuyenTruyenKhongTreo mô phỏng sát thực tế: Host có file log (đúng option app
// thật truyền vào), rồi chuyển truyện — có nút cứu: nếu treo thì dump goroutine
// đang bị chặn, không đoán mò.
func TestChuyenTruyenKhongTreo(t *testing.T) {
	base := t.TempDir()
	cfg := testCfg(base)
	bundle := assets.LoadWithLanguage("vi", "default", assets.DefaultLoadOptions(cfg.OutputDir))
	opts := []host.NewOption{host.WithFileLog("tui.log", false)}

	rt, err := host.New(cfg, bundle, opts...)
	if err != nil {
		t.Fatalf("host.New: %v", err)
	}
	defer rt.Close()

	m := NewModel(rt, "test")
	m.cfg = cfg
	m.hostOpts = opts
	m.width, m.height = 120, 40

	if _, err := rt.NewBookDir("truyen-hai"); err != nil {
		t.Fatalf("NewBookDir: %v", err)
	}
	target := filepath.Join(base, "output", "truyen-hai")

	done := make(chan struct{})
	var switchErr error
	var gotDir string
	var gotView string
	var gotW, gotH int
	go func() {
		defer close(done)
		next, _, err := m.switchBook(target)
		switchErr = err
		if err == nil {
			gotDir = next.runtime.Dir()
			gotW, gotH = next.width, next.height
			gotView = next.View()
			next.runtime.Close()
		}
	}()

	select {
	case <-done:
		if switchErr != nil {
			t.Fatalf("switchBook: %v", switchErr)
		}
		if !sameDir(gotDir, target) {
			t.Errorf("chưa sang truyện mới: %s", gotDir)
		}
		// Chuyển xong phải còn vẽ được UI. NewModel để width/height = 0; nếu
		// không mang sang thì View() rơi vào nhánh chỉ vẽ "Đang tải..." và người
		// dùng thấy mất sạch UI sau mỗi lần chuyển truyện.
		if gotW == 0 || gotH == 0 {
			t.Errorf("chuyển truyện xong mất kích thước terminal: %dx%d", gotW, gotH)
		}
		if strings.Contains(gotView, "Đang tải") {
			t.Errorf("UI sau khi chuyển truyện chỉ hiện 'Đang tải...': %q", gotView)
		}
	case <-time.After(45 * time.Second):
		buf := make([]byte, 1<<20)
		buf = buf[:runtime.Stack(buf, true)]
		var blocked []string
		for _, g := range strings.Split(string(buf), "\n\n") {
			if strings.Contains(g, "chan receive") || strings.Contains(g, "chan send") ||
				strings.Contains(g, "semacquire") || strings.Contains(g, "WaitGroup.Wait") {
				blocked = append(blocked, strings.SplitN(g, "\n", 2)[0])
			}
		}
		t.Fatalf("TREO. Goroutine bị chặn (%d):\n%s", len(blocked), strings.Join(blocked, "\n"))
	}
}
