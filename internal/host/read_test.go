package host

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/CTKiet2006/kietnovel/internal/store"
)

// newReadTestHost dựng Host tối thiểu chỉ đủ đọc chương, không cần Engine.
func newReadTestHost(t *testing.T) *Host {
	t.Helper()
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init(0); err != nil {
		t.Fatal(err)
	}
	return &Host{store: st}
}

func writeChapter(t *testing.T, h *Host, n int, body string) {
	t.Helper()
	if err := h.store.Drafts.SaveFinalChapter(n, body); err != nil {
		t.Fatal(err)
	}
}

func TestReadChapterDocBanChot(t *testing.T) {
	h := newReadTestHost(t)
	writeChapter(t, h, 3, "Chương ba. Nội dung.")

	got, err := h.ReadChapter(3)
	if err != nil {
		t.Fatalf("đọc chương 3: %v", err)
	}
	if !got.Exists {
		t.Fatal("Exists = false dù đã ghi chương")
	}
	if got.Body != "Chương ba. Nội dung." {
		t.Fatalf("Body = %q", got.Body)
	}
	if got.Chapter != 3 {
		t.Fatalf("Chapter = %d, mong 3", got.Chapter)
	}
	if got.WordCount == 0 {
		t.Fatal("WordCount = 0 dù có nội dung")
	}
}

// TestReadChapterChuaVietKhongPhaiLoi: chương chưa có là tình trạng bình thường lúc
// mới tạo sách, không phải lỗi. Nếu trả lỗi, người đọc sẽ thấy thông báo đỏ mỗi
// lần mở khung ở chương chưa viết.
func TestReadChapterChuaVietKhongPhaiLoi(t *testing.T) {
	h := newReadTestHost(t)

	got, err := h.ReadChapter(7)
	if err != nil {
		t.Fatalf("chương chưa viết mà báo lỗi: %v", err)
	}
	if got.Exists {
		t.Fatalf("Exists = true dù chưa viết: %+v", got)
	}
	if got.Body != "" {
		t.Fatalf("Body phải rỗng, được %q", got.Body)
	}
}

// TestReadChapterSoKhongHopLe: số chương <= 0 là sai cú pháp thật, phải báo lỗi
// chứ không âm thầm đọc rỗng.
func TestReadChapterSoKhongHopLe(t *testing.T) {
	h := newReadTestHost(t)
	for _, n := range []int{0, -1, -100} {
		if _, err := h.ReadChapter(n); err == nil {
			t.Errorf("ReadChapter(%d) phải báo lỗi", n)
		}
	}
}

// TestReadChapterVanDocDuocKhiDanYHong: dàn ý hỏng không nên chặn việc đọc truyện —
// người đọc còn cần đọc nội dung, chỉ là mất tiêu đề.
func TestReadChapterVanDocDuocKhiDanYHong(t *testing.T) {
	h := newReadTestHost(t)
	writeChapter(t, h, 1, "Nội dung vẫn đọc được.")
	if err := os.WriteFile(filepath.Join(h.store.Dir(), "meta", "outline.json"),
		[]byte("{không phải json"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := h.ReadChapter(1)
	if err != nil {
		t.Fatalf("dàn ý hỏng mà chặn đọc: %v", err)
	}
	if got.Body != "Nội dung vẫn đọc được." {
		t.Fatalf("Body = %q", got.Body)
	}
	if got.Title != "" {
		t.Fatalf("dàn ý hỏng thì Title phải rỗng, được %q", got.Title)
	}
}

// TestCompletedChaptersPhanBietRongVaCo: chưa chốt chương nào thì trả rỗng, để
// khung đọc hiện "chưa có chương nào" thay vì crash. Sau khi chốt chương thì
// danh sách phải khớp với progress.
func TestCompletedChaptersPhanBietRongVaCo(t *testing.T) {
	h := newReadTestHost(t)

	list, err := h.CompletedChapters()
	if err != nil {
		t.Fatalf("chưa chốt chương nào mà báo lỗi: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("mong rỗng, được %v", list)
	}

	if err := h.store.Progress.MarkChapterComplete(1, 100, "", ""); err != nil {
		t.Fatal(err)
	}
	if err := h.store.Progress.MarkChapterComplete(2, 200, "", ""); err != nil {
		t.Fatal(err)
	}
	list, err = h.CompletedChapters()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0] != 1 || list[1] != 2 {
		t.Fatalf("CompletedChapters = %v, mong [1 2]", list)
	}
}
