package host

import (
	"errors"
	"fmt"

	"github.com/CTKiet2006/kietnovel/assets"
	"github.com/CTKiet2006/kietnovel/internal/bootstrap"
	"github.com/CTKiet2006/kietnovel/internal/i18n"
)

// ErrNeedBookLanguage báo truyện chưa khoá ngôn ngữ sáng tác và không thể hỏi
// (headless). Tách riêng để lớp trên biết đây là "cần người dùng chỉ định",
// không phải lỗi hệ thống — nên thông báo phải khác, và phải nêu cách chỉ định.
var ErrNeedBookLanguage = errors.New("book language not set")

// BookLanguage trả ngôn ngữ sáng tác đang khoá cho truyện này.
// Trả ("", nil) khi truyện chưa khoá: đó là trường hợp bình thường với truyện tạo
// trước tính năng này, không phải lỗi.
func (h *Host) BookLanguage() (string, error) {
	return h.store.BookLanguage.Load()
}

// SetBookLanguage khoá ngôn ngữ sáng tác cho truyện này.
func (h *Host) SetBookLanguage(lang string) error {
	if bootstrap.ValidLanguage(lang) == "" {
		return fmt.Errorf("ngôn ngữ không hợp lệ: %q", lang)
	}
	return h.store.BookLanguage.Save(bootstrap.NormalizeLanguage(lang))
}

// ErrNeedBookLanguageMessage là câu chữ cho trường hợp headless gặp truyện chưa
// khoá ngôn ngữ. Thân câu chữ nằm ở bootstrap vì main.go cần gọi trước khi dựng
// Host, không import host được.
func ErrNeedBookLanguageMessage(bookDir string) error {
	return bootstrap.NeedBookLanguageMessage(bookDir)
}

// resolveWriteLanguage quyết định ngôn ngữ sáng tác sẽ dùng cho lượt ghi hiện tại.
//  1. truyện đã khoá → dùng, không hỏi lại (đây là mục tiêu: chương sau không lệch
//     giọa với chương trước)
//  2. truyện chưa khoá + không hỏi được (headless) → ErrNeedBookLanguage
//  3. truyện chưa khoá → trả về ngôn ngữ mặc định VÀ một cờ "cần hỏi", để lớp
//     trên hỏi người dùng rồi khoá lại.
//
// Không bao giờ tự ghi mặc định vào truyện chưa khoá: nếu người dùng đã đổi ngôn
// ngữ giữa chừng, ghi bừa sẽ khoá nhầm — và sai này không sửa được sau.
func (h *Host) resolveWriteLanguage() (lang string, needConfirm bool, err error) {
	locked, err := h.BookLanguage()
	if err != nil {
		return "", false, err
	}
	if locked != "" {
		return locked, false, nil
	}
	return defaultWriteLanguage(h.cfg), true, nil
}

func defaultWriteLanguage(cfg bootstrap.Config) string {
	if l := bootstrap.NormalizeLanguage(cfg.Language); l != "" {
		return l
	}
	return i18n.LangVietnamese
}

// LoadBundleFor dựng bộ tài nguyên theo ngôn ngữ đã quyết.
//
// Tách hàm để dùng chung cho lúc khởi động và lúc chuyển truyện: nếu hai chỗ tự
// dựng khác nhau thì một chỗ sẽ quên ApplyLanguage — đúng loại lỗi từng xảy ra với
// Arbiter. Truyền LANG ĐÃ QUYẾT vào, không tự suy ra, để người gọi là nơi duy
// nhất quyết định (khoá theo truyện hay mặc định cấu hình).
func LoadBundleFor(cfg bootstrap.Config, outDir, lang string) assets.Bundle {
	if bootstrap.NormalizeLanguage(lang) == "" {
		lang = defaultWriteLanguage(cfg)
	}
	b := assets.LoadWithLanguage(lang, cfg.Style, assets.DefaultLoadOptions(outDir))
	b.ApplyLanguage(lang)
	return b
}
