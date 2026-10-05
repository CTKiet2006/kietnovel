package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/voocel/ainovel-cli/internal/domain"
)

func TestBookStorePersistsCanonicalDataAndReadableProjection(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	if err := s.Book.Save(domain.BookMetadata{Title: " 长夜将明 ", Synopsis: " 少年守住最后一盏灯。 "}); err != nil {
		t.Fatal(err)
	}
	book, err := s.Book.Load()
	if err != nil {
		t.Fatal(err)
	}
	if book.Title != "长夜将明" || book.Synopsis != "少年守住最后一盏灯。" {
		t.Fatalf("unexpected book metadata: %+v", book)
	}
	projection, err := os.ReadFile(filepath.Join(dir, "book.md"))
	if err != nil {
		t.Fatal(err)
	}
	if text := string(projection); !strings.Contains(text, "《长夜将明》") || !strings.Contains(text, "少年守住最后一盏灯。") {
		t.Fatalf("unexpected book projection: %s", text)
	}
	if err := s.Book.Save(domain.BookMetadata{Title: "空简介"}); err == nil {
		t.Fatal("empty synopsis must be rejected")
	}
}
