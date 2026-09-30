package bootstrap

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// NeedBookLanguageMessage là câu chữ cho trường hợp headless gặp truyện chưa khoá
// ngôn ngữ sáng tác.
//
// Phải nêu rõ CÁCH chỉ định, không chỉ báo lỗi: headless chạy trên VPS/CI thì
// không có ai đọc màn hình, và "thiếu ngôn ngữ" không kèm cách sửa thì người dùng
// chỉ biết dừng. Ở đây vì main.go gọi trước khi dựng Host, không import host được.
func NeedBookLanguageMessage(bookDir string) error {
	return fmt.Errorf(
		"truyện %s chưa khoá ngôn ngữ sáng tác và chế độ headless không thể hỏi. "+
			"Hãy chạy kietnovel một lần trong TUI để chọn, hoặc thêm \"language\": \"vi|en|zh\" vào cấu hình",
		bookDir)
}

// BookLanguageOf đọc ngôn ngữ sáng tác đã khoá của một thư mục truyện.
//
// Trả ("", nil) khi chưa khoá — trường hợp bình thường với truyện tạo trước tính
// năng này, KHÔNG phải lỗi. File hỏng cũng coi như chưa khoá, để còn hỏi lại
// được chứ không chết.
//
// Đọc bằng đường dẫn thay vì qua Store vì lúc này chưa có Store: main.go cần
// quyết ngôn ngữ TRƯỚC khi dựng Host.
func BookLanguageOf(bookDir string) (string, error) {
	if bookDir == "" {
		return "", nil
	}
	data, err := os.ReadFile(filepath.Join(bookDir, "meta", "language.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", nil
	}
	var f struct {
		Language string `json:"language"`
	}
	if err := json.Unmarshal(data, &f); err != nil {
		return "", nil
	}
	return f.Language, nil
}
