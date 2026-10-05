package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/llmcontract"
	"github.com/voocel/ainovel-cli/internal/store"
)

func TestSaveBookPersistsMetadata(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	tool := NewSaveBookTool(s)
	if err := llmcontract.ValidateStrictReady(tool.Schema()); err != nil {
		t.Fatalf("save_book schema is not strict-ready: %v", err)
	}
	args, _ := json.Marshal(map[string]any{
		"title": "长夜将明", "synopsis": "失去太阳后，守灯人踏上寻找黎明的旅途。",
	})
	if _, err := tool.Execute(context.Background(), args); err != nil {
		t.Fatal(err)
	}
	book, err := s.Book.Load()
	if err != nil {
		t.Fatal(err)
	}
	if book == nil || book.Title != "长夜将明" || book.Synopsis == "" {
		t.Fatalf("unexpected book metadata: %+v", book)
	}
	if s.Checkpoints.LatestByStep(domain.GlobalScope(), "book") == nil {
		t.Fatal("save_book should append checkpoint")
	}
}
