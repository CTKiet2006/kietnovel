package arbiter

import (
	"os"
	"path/filepath"
	"testing"

	storepkg "github.com/CTKiet2006/kietnovel/internal/store"
)

// TestCollectFactsTrenBookJsonThucTe dùng đúng hình dạng file mà /new ghi ra:
// {"title": "..."} — không có synopsis, kể cả dấu tiếng Việt bị hỏng encoding
// vẫn phải đọc được và không chặn can thiệp.
func TestCollectFactsTrenBookJsonThucTe(t *testing.T) {
	dir := t.TempDir()
	st := storepkg.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init(10); err != nil {
		t.Fatal(err)
	}
	raw := `{
  "title": "tuất"
}`
	if err := os.WriteFile(filepath.Join(dir, "meta", "book.json"), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	facts, err := CollectInterventionFacts(st)
	if err != nil {
		t.Fatalf("book.json kiểu /new không được chặn can thiệp: %v", err)
	}
	if facts.Title == "" {
		t.Error("phải đọc được title")
	}
}
