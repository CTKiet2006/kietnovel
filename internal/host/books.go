package host

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Book là một thư mục truyện trong cây đầu ra.
type Book struct {
	// Dir là đường dẫn tuyệt đối tới thư mục truyện.
	Dir string
	// Name là tên lấy từ meta/book.json, rơi về tên thư mục khi chưa có sách.
	Name string
	// Chapters là số chương đã chốt.
	Chapters int
	// Words là tổng số chữ đã viết.
	Words int
	// HasOutline cho biết đã có tiền đề/dàn ý chưa.
	HasOutline bool
}

// listBooks liệt kê các truyện trong <base>/output, mỗi thư mục con là một truyện.
func listBooks(base string) ([]Book, error) {
	out := filepath.Join(base, "output")
	entries, err := os.ReadDir(out)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var books []Book
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(out, e.Name())
		books = append(books, readBook(dir, e.Name()))
	}
	sort.Slice(books, func(i, j int) bool { return books[i].Name < books[j].Name })
	return books, nil
}

func readBook(dir, fallbackName string) Book {
	b := Book{Dir: dir, Name: fallbackName}
	var meta struct {
		Title string `json:"title"`
	}
	if data, err := os.ReadFile(filepath.Join(dir, "meta", "book.json")); err == nil {
		if err := json.Unmarshal(data, &meta); err == nil && meta.Title != "" {
			b.Name = meta.Title
		}
	}
	if entries, err := os.ReadDir(filepath.Join(dir, "chapters")); err == nil {
		for _, f := range entries {
			if !f.IsDir() && strings.HasSuffix(f.Name(), ".md") {
				b.Chapters++
			}
		}
	}
	var prog struct {
		TotalWordCount int `json:"total_word_count"`
	}
	if data, err := os.ReadFile(filepath.Join(dir, "meta", "progress.json")); err == nil {
		_ = json.Unmarshal(data, &prog)
	}
	b.Words = prog.TotalWordCount
	_, errPremise := os.Stat(filepath.Join(dir, "meta", "premise.md"))
	_, errOutline := os.Stat(filepath.Join(dir, "meta", "outline.md"))
	b.HasOutline = errPremise == nil || errOutline == nil
	return b
}

// Books liệt kê các truyện đang tồn tại trong cây đầu ra. base là thư mục gốc
// (thường là nơi kietnovel chạy, hoặc thư mục cha của cây output).
func (h *Host) Books() ([]Book, error) { return listBooks(outputBase(h)) }

// outputBase suy ra thư mục gốc chứa output/ từ thư mục truyện hiện tại.
// <base>/output/novel → <base>.
func outputBase(h *Host) string {
	dir := h.store.Dir()
	return filepath.Dir(filepath.Dir(dir))
}

// DeleteBookResult báo kết quả xoá, đủ để hiển thị xác nhận trước khi xoá.
type DeleteBookResult struct {
	Dir       string
	Name      string
	Chapters  int
	Words     int
	SizeBytes int64
}

// InspectBook dựng thông tin thư mục sẽ bị xoá, KHÔNG xoá gì.
// Dùng cho bước xác nhận: người dùng phải thấy rõ đang xoá cái gì, bao nhiêu
// chương, trước khi đồng ý.
func (h *Host) InspectBook(dir string) (DeleteBookResult, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return DeleteBookResult{}, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return DeleteBookResult{}, fmt.Errorf("không tìm thấy truyện: %w", err)
	}
	if !info.IsDir() {
		return DeleteBookResult{}, fmt.Errorf("%s không phải thư mục truyện", abs)
	}
	// Chặn xoá nhầm: phải là thư mục con trực tiếp của <base>/output, có meta/
	// bên trong. Không có meta/ thì đây là thư mục thường, không phải truyện.
	if _, err := os.Stat(filepath.Join(abs, "meta")); err != nil {
		return DeleteBookResult{}, fmt.Errorf("%s không phải thư mục truyện (thiếu meta/)", abs)
	}
	if filepath.Base(filepath.Dir(abs)) != "output" {
		return DeleteBookResult{}, fmt.Errorf("%s không nằm trong <output>/", abs)
	}

	b := readBook(abs, filepath.Base(abs))
	res := DeleteBookResult{Dir: abs, Name: b.Name, Chapters: b.Chapters, Words: b.Words}
	_ = filepath.Walk(abs, func(_ string, fi os.FileInfo, err error) error {
		if err == nil && fi != nil && !fi.IsDir() {
			res.SizeBytes += fi.Size()
		}
		return nil
	})
	return res, nil
}

// DeleteBook xoá vĩnh viễn thư mục truyện. Không có tham số "đổi ý" — vì vậy
// phía TUI bắt buộc phải gọi InspectBook trước để lấy thông tin xác nhận, rồi mới
// gọi hàm này.
//
// Chặn xoá thư mục đang mở: người dùng đang viết truyện đó, xoá đi thì Engine
// đang chạy giữa chừng sẽ ghi vào thư mục biến mất.
func (h *Host) DeleteBook(dir string) error {
	res, err := h.InspectBook(dir)
	if err != nil {
		return err
	}
	if cur, err := filepath.Abs(h.store.Dir()); err == nil && cur == res.Dir {
		return fmt.Errorf("không xoá được truyện đang mở — hãy /new hoặc chuyển sang truyện khác trước")
	}
	return os.RemoveAll(res.Dir)
}

// NewBookDir tạo thư mục truyện mới trong <base>/output/<name> và trả về đường dẫn.
// Không tạo sẵn meta/: store sẽ khởi tạo đúng cấu trúc khi mở.
func (h *Host) NewBookDir(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("cần tên truyện")
	}
	// Chặn ký tự không an toàn cho đường dẫn và cho tên thư mục.
	for _, r := range name {
		if r == '/' || r == '\\' || r == ':' || r == '*' || r == '?' ||
			r == '"' || r == '<' || r == '>' || r == '|' {
			return "", fmt.Errorf("tên truyện chứa ký tự không hợp lệ: %q", r)
		}
	}
	dir := filepath.Join(outputBase(h), "output", name)
	if _, err := os.Stat(dir); err == nil {
		return "", fmt.Errorf("đã có truyện tên %q trong output/", name)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}
