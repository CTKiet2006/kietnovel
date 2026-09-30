package store

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// TestListDraftsPhaiThayDungDinhDangTenThat — bug thật: SaveDraft ghi
// drafts/%02d.draft.md, còn ListDrafts trước đây cắt ".md" rồi Atoi("01.draft")
// nên lỗi và bỏ qua mọi bản nháp. Màn đọc vì thế không thấy chương chưa chốt.
func TestListDraftsPhaiThayDungDinhDangTenThat(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	if err := s.Drafts.SaveDraft(1, "chương 1 nháp"); err != nil {
		t.Fatal(err)
	}
	if err := s.Drafts.SaveDraft(3, "chương 3 nháp"); err != nil {
		t.Fatal(err)
	}
	if err := s.Drafts.SaveDraft(2, "chương 2 nháp"); err != nil {
		t.Fatal(err)
	}

	// Xác nhận tên file đúng như SaveDraft sinh ra, để test này không vô nghĩa
	// nếu ai đó đổi định dạng ở SaveDraft.
	entries, err := os.ReadDir(filepath.Join(dir, "drafts"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 || entries[0].Name() != "01.draft.md" {
		t.Fatalf("SaveDraft không ghi %02d.draft.md, thực tế: %v", 1, entries[0].Name())
	}

	got, err := s.Drafts.ListDrafts()
	if err != nil {
		t.Fatal(err)
	}
	if want := []int{1, 2, 3}; !reflect.DeepEqual(got, want) {
		t.Errorf("ListDrafts = %v, mong %v", got, want)
	}
}

func TestListDraftsBoQuaTenKhongPhaiChuong(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"note.md", "abc.draft.md", "0.draft.md", "readme.txt", "x-y.draft.md"} {
		if err := os.WriteFile(filepath.Join(dir, "drafts", name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.Drafts.ListDrafts()
	if err != nil {
		t.Fatal(err)
	}
	// "0.draft" bị loa vì số chương phải > 0; các tên khác không phải số.
	if len(got) != 0 {
		t.Errorf("ListDrafts = %v, mong rong (0.draft.md va ten khong phai so phai bi bo)", got)
	}
}

func TestListDraftsThuMucKhongTonTai(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	got, err := s.Drafts.ListDrafts()
	if err != nil {
		t.Fatalf("thu muc drafts chua co khong duoc la loi: %v", err)
	}
	if got != nil {
		t.Errorf("ListDrafts = %v, mong nil", got)
	}
}
