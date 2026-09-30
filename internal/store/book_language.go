package store

import (
	"encoding/json"
	"fmt"
	"os"
)

// BookLanguageStore lưu ngôn ngữ sáng tác RIÊNG cho từng truyện.
//
// Vì sao không nhét vào meta/book.json: đó là struct domain.BookMetadata nghiêm
// ngặt, Validate() bắt buộc phải có title và synopsis, và nó được nạp lại ở mọi
// nơi. Thêm trường vào đó là mọi chỗ đọc book.json phải biết tới ngôn ngữ, rồi
// truyện cũ (không có trường) sẽ đi qua Validate lỗi. Tách file riêng: truyện cũ
// không có file thì coi như chưa khoá, và file rỗng không làm hỏng book.json.
//
// File: meta/language.json
type BookLanguageStore struct{ io *IO }

func NewBookLanguageStore(io *IO) *BookLanguageStore { return &BookLanguageStore{io: io} }

type bookLanguageFile struct {
	Language string `json:"language"`
}

// Load trả ngôn ngữ đã khoá của truyện.
// Trả ("", nil) khi truyện chưa khoá — đó KHÔNG phải lỗi, mà là trường hợp
// cần hỏi người dùng, đặc biệt với truyện tạo trước khi có tính năng này.
func (s *BookLanguageStore) Load() (string, error) {
	data, err := s.io.ReadFile("meta/language.json")
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	var f bookLanguageFile
	if err := json.Unmarshal(data, &f); err != nil {
		// File hỏng KHÔNG được coi là lỗi chết: coi như chưa khoá rồi hỏi lại.
		// Hỏi còn hơn là không cho vào truyện.
		return "", nil
	}
	return f.Language, nil
}

// Save khoá ngôn ngữ cho truyện. Ghi đè an toàn: không đụng book.json.
func (s *BookLanguageStore) Save(lang string) error {
	if lang == "" {
		return fmt.Errorf("ngôn ngữ rỗng")
	}
	return s.io.WriteJSON("meta/language.json", bookLanguageFile{Language: lang})
}
