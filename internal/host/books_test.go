package host

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/CTKiet2006/kietnovel/internal/store"
)

func booksTestHost(t *testing.T) (*Host, string) {
	t.Helper()
	base := t.TempDir()
	// store.Dir() phải là <base>/output/novel đúng như lúc chạy thật, vì
	// outputBase suy ra <base> từ nó. Truyền base thẳng sẽ làm hàm dự ra
	// thư mục cha, và các test sẽ đọc chung một cây output/.
	novelDir := filepath.Join(base, "output", "novel")
	st := store.NewStore(novelDir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init(0); err != nil {
		t.Fatal(err)
	}
	return &Host{store: st}, base
}

func makeBook(t *testing.T, base, name string, chapters int) string {
	t.Helper()
	dir := filepath.Join(base, "output", name)
	if err := os.MkdirAll(filepath.Join(dir, "meta"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "meta", "book.json"),
		[]byte(`{"title":"Tên Sách `+name+`"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "meta", "progress.json"),
		[]byte(`{"total_word_count":1234}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "chapters"), 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= chapters; i++ {
		name := fmt.Sprintf("%02d.md", i)
		if err := os.WriteFile(filepath.Join(dir, "chapters", name),
			[]byte("nội dung"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// TestInspectBookKhongXoaGi: InspectBook chỉ để lấy thông tin hiển thị xác nhận.
// Nếu nó xoá thì bước xác nhận vô nghĩa — người dùng mất truyện trước khi kịp đọc.
func TestInspectBookKhongXoaGi(t *testing.T) {
	h, base := booksTestHost(t)
	dir := makeBook(t, base, "linhdi", 2)

	res, err := h.InspectBook(dir)
	if err != nil {
		t.Fatalf("InspectBook: %v", err)
	}
	if res.Name != "Tên Sách linhdi" {
		t.Errorf("Name = %q", res.Name)
	}
	if res.Chapters != 2 {
		t.Errorf("Chapters = %d, mong 2", res.Chapters)
	}
	if res.Words != 1234 {
		t.Errorf("Words = %d, mong 1234", res.Words)
	}
	if res.SizeBytes == 0 {
		t.Error("SizeBytes = 0, phai > 0 để người dùng thấy mất bao nhiêu")
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("InspectBook đã xoá thư mục: %v", err)
	}
}

// TestDeleteBookXoaThatSauKhiXacNhan: giờ mới xoá.
func TestDeleteBookXoaThatSauKhiXacNhan(t *testing.T) {
	h, base := booksTestHost(t)
	dir := makeBook(t, base, "linhdi", 2)

	if err := h.DeleteBook(dir); err != nil {
		t.Fatalf("DeleteBook: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Error("thư mục vẫn còn sau khi xoá")
	}
}

// TestDeleteBookChanXoaTruyenDangMo là chốt chặn quan trọng nhất: người dùng đang
// viết truyện đó, Engine đang chạy giữa chừng, xoá đi thì mọi ghi sẽ rơi vào
// thư mục biến mất. Phải báo lỗi, không xoá.
func TestDeleteBookChanXoaTruyenDangMo(t *testing.T) {
	_, base := booksTestHost(t)
	dir := makeBook(t, base, "linhdi", 1)

	// Dựng Host đang mở đúng truyện đó.
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	open := &Host{store: st}

	if err := open.DeleteBook(dir); err == nil {
		t.Fatal("xoá truyện đang mở phải bị từ chối")
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatal("thư mục bị xoá dù đang mở")
	}
}

// TestDeleteBookChanXoaNgoiThu muc: chặn xoá nhầm thư mục thường. Nếu không có
// chặn này, /delete với đường dẫn gõ nhầm sẽ xoá nhầm thứ không liên quan.
func TestDeleteBookChanXoaNgoiThuMuc(t *testing.T) {
	h, base := booksTestHost(t)

	// (1) không có meta/ → không phải thư mục truyện
	plain := filepath.Join(t.TempDir(), "Documents")
	if err := os.MkdirAll(plain, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := h.DeleteBook(plain); err == nil {
		t.Error("xoá thư mục thường phải bị từ chối")
	}
	if _, err := os.Stat(plain); err != nil {
		t.Error("thư mục thường đã bị xoá")
	}

	// (2) có meta/ nhưng nằm ngoài <output>/ → vẫn từ chối
	outside := filepath.Join(base, "khac", "truyen")
	if err := os.MkdirAll(filepath.Join(outside, "meta"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := h.DeleteBook(outside); err == nil {
		t.Error("xoá thư mục có meta nhưng ngoài output/ phải bị từ chối")
	}
	if _, err := os.Stat(outside); err != nil {
		t.Error("thư mục ngoài output/ đã bị xoá")
	}
}

// TestNewBookDir: tạo mới phải từ chối tên trùng và tên chứa ký tự nguy hiểm
// cho đường dẫn — nếu không, "/new .." có thể chạy ra khỏi output/.
func TestNewBookDir(t *testing.T) {
	h, _ := booksTestHost(t)

	dir, err := h.NewBookDir("truyen-moi")
	if err != nil {
		t.Fatalf("NewBookDir: %v", err)
	}
	if filepath.Base(dir) != "truyen-moi" {
		t.Errorf("tạo sai thư mục: %s", dir)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("thư mục chưa được tạo: %v", err)
	}

	if _, err := h.NewBookDir("truyen-moi"); err == nil {
		t.Error("tạo trùng tên phải bị từ chối")
	}
	for _, bad := range []string{"a/b", `a\b`, "a:b", "a*b", "a?b", `a"b`, "a<b", "a>b", "a|b", "  "} {
		if _, err := h.NewBookDir(bad); err == nil {
			t.Errorf("NewBookDir(%q) phải bị từ chối", bad)
		}
	}
	// Tên có dấu cách và ký tự tiếng Việt vẫn hợp lệ.
	if _, err := h.NewBookDir("truyen linh di"); err != nil {
		t.Errorf("tên có dấu cách phải hợp lệ: %v", err)
	}
}

// TestBooksLietKeDungTen: danh sách lấy tên từ book.json, không phải tên thư mục,
// để người dùng nhận ra truyện của mình.
func TestBooksLietKeDungTen(t *testing.T) {
	h, base := booksTestHost(t)
	makeBook(t, base, "thu-muc-a", 1)
	makeBook(t, base, "thu-muc-b", 3)

	books, err := h.Books()
	if err != nil {
		t.Fatalf("Books: %v", err)
	}
	// store.Init tự tạo "novel", nên danh sách có 3 mục — kiểm hai truyện ta
	// tạo thay vì đòi đúng số lượng, để test không phụ thuộc chi tiết khởi tạo.
	byName := map[string]Book{}
	for _, b := range books {
		byName[b.Name] = b
	}
	a, okA := byName["Tên Sách thu-muc-a"]
	b, okB := byName["Tên Sách thu-muc-b"]
	if !okA || !okB {
		t.Fatalf("thiếu truyện đã tạo, có: %v", byName)
	}
	if a.Chapters != 1 || b.Chapters != 3 {
		t.Errorf("số chương sai: %d, %d", a.Chapters, b.Chapters)
	}
	if a.Words != 1234 {
		t.Errorf("Words = %d, mong 1234", a.Words)
	}
	// Tên phải lấy từ book.json chứ không phải tên thư mục.
	if filepath.Base(a.Dir) != "thu-muc-a" {
		t.Errorf("Dir = %q, mong kết thúc bằng thu-muc-a", a.Dir)
	}
}
