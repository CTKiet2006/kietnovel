package store

import (
	"fmt"
	"os"

	"github.com/voocel/ainovel-cli/internal/domain"
)

// BookStore 管理作品对外信息，meta/book.json 是唯一事实源，book.md 是可读投影。
type BookStore struct{ io *IO }

func NewBookStore(io *IO) *BookStore { return &BookStore{io: io} }

// Load 读取作品信息；尚未生成时返回 nil。
func (s *BookStore) Load() (*domain.BookMetadata, error) {
	var book domain.BookMetadata
	if err := s.io.ReadJSON("meta/book.json", &book); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	book = book.Normalized()
	if err := book.Validate(); err != nil {
		return nil, err
	}
	return &book, nil
}

// Save 保存规范化的作品信息及其可读投影。
func (s *BookStore) Save(book domain.BookMetadata) error {
	book = book.Normalized()
	if err := book.Validate(); err != nil {
		return err
	}
	return s.io.WithWriteLock(func() error {
		if err := s.io.WriteJSONUnlocked("meta/book.json", book); err != nil {
			return err
		}
		return s.io.WriteMarkdownUnlocked("book.md", renderBook(book))
	})
}

func renderBook(book domain.BookMetadata) string {
	return fmt.Sprintf("# 《%s》\n\n## 简介\n\n%s\n", book.Title, book.Synopsis)
}
