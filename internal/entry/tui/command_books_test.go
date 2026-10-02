package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// resolveNewBookName: Enter trống -> tên ngẫu nhiên, không còn lỗi chặn.
func TestResolveNewBookName(t *testing.T) {
	slug, title, auto := resolveNewBookName("   ")
	if !auto || slug == "" || title != "" {
		t.Errorf("trống phải trả slug ngẫu nhiên + auto=true, được slug=%q title=%q auto=%v", slug, title, auto)
	}
	slug, title, auto = resolveNewBookName("Tu Tiên Ký")
	if auto || slug != "tu-tien-ky" || title != "Tu Tiên Ký" {
		t.Errorf("tên thường bị đổi sai: slug=%q title=%q auto=%v", slug, title, auto)
	}
	if slug, _, _ := resolveNewBookName("!!!"); slug != "truyen-moi" {
		t.Errorf("tên rác phải rơi về slug mặc định truyen-moi, được %q", slug)
	}
}

// randomBookSlug: đúng định dạng truyen-xxxx, không trùng trong 200 lần.
func TestRandomBookSlug(t *testing.T) {
	re := regexp.MustCompile(`^truyen-[a-z0-9]{4}$`)
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		s := randomBookSlug()
		if !re.MatchString(s) {
			t.Fatalf("slug sai định dạng: %q", s)
		}
		if seen[s] {
			t.Fatalf("slug trùng: %q", s)
		}
		seen[s] = true
	}
}

// writeBookTitle: ghi roundtrip + giữ nguyên field khác trong book.json.
func TestWriteBookTitleGiuFieldKhac(t *testing.T) {
	dir := t.TempDir()
	meta := filepath.Join(dir, "meta")
	if err := os.MkdirAll(meta, 0o755); err != nil {
		t.Fatal(err)
	}
	existing := map[string]any{"slug": "cu", "title": "Cũ", "language": "vi", "created": "x"}
	raw, _ := json.Marshal(existing)
	if err := os.WriteFile(filepath.Join(meta, "book.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeBookTitle(dir, "Mới Đẹp"); err != nil {
		t.Fatal(err)
	}
	back, err := os.ReadFile(filepath.Join(meta, "book.json"))
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(back, &got); err != nil {
		t.Fatal(err)
	}
	if got["title"] != "Mới Đẹp" || got["language"] != "vi" || got["created"] != "x" {
		t.Errorf("ghi đè mất field khác: %v", got)
	}
}

// Registry: /rename + alias doiten/đổitên đều trỏ đúng command ID rename.
func TestLenhRenameDaDangKy(t *testing.T) {
	r := commandRegistryInstance()
	for _, name := range []string{"rename", "doiten", "đổitên"} {
		spec, ok := r.Find(name)
		if !ok {
			t.Errorf("không tìm thấy %q trong registry", name)
			continue
		}
		if spec.CommandID() != "rename" {
			t.Errorf("%q trỏ nhầm sang %q", name, spec.CommandID())
		}
	}
}

// createBookConfirmed + runRenameBook với runtime nil: báo lỗi, không panic.
func TestBooksNilRuntimeKhongPanic(t *testing.T) {
	m := newTestModel(100, 30)
	m.books = newBooksState(100, 30, booksNewDraft)
	// Hai lệnh đều phải chạy qua mà không panic khi runtime nil.
	m.createBookConfirmed()
	m.runRenameBook([]string{"Tên Mới"})
}
