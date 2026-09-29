package store

import (
	"fmt"
	"os"

	"github.com/CTKiet2006/kietnovel/internal/domain"
)

// BookStore manages the book's public information; meta/book.json is the single source of truth and book.md is the readable projection.
type BookStore struct{ io *IO }

func NewBookStore(io *IO) *BookStore { return &BookStore{io: io} }

// Load reads the book information; it returns nil when none has been produced yet.
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

// Save stores the canonical book information together with its readable projection.
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
