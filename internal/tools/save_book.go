package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/voocel/agentcore/schema"
	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/errs"
	"github.com/voocel/ainovel-cli/internal/store"
)

// SaveBookTool 保存作品对外信息，Architect 专用。
type SaveBookTool struct{ store *store.Store }

func NewSaveBookTool(store *store.Store) *SaveBookTool { return &SaveBookTool{store: store} }

func (t *SaveBookTool) Name() string { return "save_book" }
func (t *SaveBookTool) Description() string {
	return "保存作品书名和面向读者的无剧透简介。简介应呈现主角、核心冲突和阅读钩子，不得写成内部大纲或结局梗概。"
}
func (t *SaveBookTool) Label() string                          { return "保存作品信息" }
func (t *SaveBookTool) ReadOnly(_ json.RawMessage) bool        { return false }
func (t *SaveBookTool) ConcurrencySafe(_ json.RawMessage) bool { return false }
func (t *SaveBookTool) StrictSchema() bool                     { return true }

func (t *SaveBookTool) Schema() map[string]any {
	return schema.Object(
		schema.Property("title", schema.String("正式书名，不带书名号")).Required(),
		schema.Property("synopsis", schema.String("面向读者的无剧透小说简介")).Required(),
	)
}

func (t *SaveBookTool) Execute(_ context.Context, args json.RawMessage) (json.RawMessage, error) {
	var book domain.BookMetadata
	if err := json.Unmarshal(args, &book); err != nil {
		return nil, fmt.Errorf("invalid args: %w: %w", errs.ErrToolArgs, err)
	}
	if err := book.Validate(); err != nil {
		return nil, fmt.Errorf("invalid book metadata: %w: %w", errs.ErrToolArgs, err)
	}
	if err := t.store.Book.Save(book); err != nil {
		return nil, fmt.Errorf("save book metadata: %w: %w", errs.ErrStoreWrite, err)
	}
	if _, err := t.store.Checkpoints.AppendArtifact(domain.GlobalScope(), "book", "meta/book.json"); err != nil {
		return nil, fmt.Errorf("checkpoint book metadata: %w: %w", errs.ErrStoreWrite, err)
	}
	remaining, err := t.store.FoundationMissing()
	if err != nil {
		return nil, fmt.Errorf("load foundation state: %w: %w", errs.ErrStoreRead, err)
	}
	return json.Marshal(map[string]any{
		"saved":            true,
		"foundation_ready": len(remaining) == 0,
		"remaining":        remaining,
	})
}
