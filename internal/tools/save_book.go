package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/CTKiet2006/kietnovel/internal/domain"
	"github.com/CTKiet2006/kietnovel/internal/errs"
	"github.com/CTKiet2006/kietnovel/internal/store"
	"github.com/voocel/agentcore/schema"
)

// SaveBookTool saves the book's public-facing information; for the Architect only.
type SaveBookTool struct{ store *store.Store }

func NewSaveBookTool(store *store.Store) *SaveBookTool { return &SaveBookTool{store: store} }

func (t *SaveBookTool) Name() string { return "save_book" }
func (t *SaveBookTool) Description() string {
	switch toolLang(t.store) {
	case "vi":
		return "Lưu tên truyện chính thức và lời giới thiệu không tiết lộ nội dung hướng tới độc giả. Lời giới thiệu cần nêu bật nhân vật chính, xung đột cốt lõi và móc câu thu hút, không viết thành dàn ý nội bộ hay tóm tắt kết cục."
	case "en":
		return "Save book title and spoiler-free synopsis for readers. Synopsis should present protagonist, core conflict, and reading hooks, not internal outlines or ending spoilers."
	default:
		return "保存作品书名和面向读者的无剧透简介。简介应呈现主角、核心冲突和阅读钩子，不得写成内部大纲或结局梗概。"
	}
}
func (t *SaveBookTool) Label() string {
	switch toolLang(t.store) {
	case "vi":
		return "Lưu thông tin tác phẩm"
	case "en":
		return "Save book info"
	default:
		return "保存作品信息"
	}
}
func (t *SaveBookTool) ReadOnly(_ json.RawMessage) bool        { return false }
func (t *SaveBookTool) ConcurrencySafe(_ json.RawMessage) bool { return false }
func (t *SaveBookTool) StrictSchema() bool                     { return true }

func (t *SaveBookTool) Schema() map[string]any {
	titleDesc := "正式书名，不带书名号"
	synopsisDesc := "面向读者的无剧透小说简介"
	switch toolLang(t.store) {
	case "vi":
		titleDesc = "Tên truyện chính thức, không kèm dấu ngoặc kép hay ngoặc nhọn"
		synopsisDesc = "Lời giới thiệu tiểu thuyết không spoil nội dung hướng tới độc giả"
	case "en":
		titleDesc = "Official book title, without formatting brackets"
		synopsisDesc = "Spoiler-free novel synopsis for prospective readers"
	}
	return schema.Object(
		schema.Property("title", schema.String(titleDesc)).Required(),
		schema.Property("synopsis", schema.String(synopsisDesc)).Required(),
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
