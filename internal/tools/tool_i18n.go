package tools

import (
	"strings"

	"github.com/CTKiet2006/kietnovel/internal/store"
)

// nextStepHint returns the post-draft guidance in the book's language.
func nextStepHint(s *store.Store) string {
	switch toolLang(s) {
	case "vi":
		return "Đọc lại bản nháp bằng read_chapter(source=draft), rồi gọi check_consistency, cuối cùng commit_chapter"
	case "en":
		return "Re-read the draft with read_chapter(source=draft), then call check_consistency, finally commit_chapter"
	default:
		return "先 read_chapter(source=draft) 回读草稿，再调用 check_consistency，最后 commit_chapter"
	}
}

// editNextStepHint returns the post-edit guidance in the book's language.
func editNextStepHint(s *store.Store) string {
	switch toolLang(s) {
	case "vi":
		return "Bản sửa đã lưu đĩa. Còn lỗi cứng thì gọi lại edit_chapter; nếu không thì gọi check_consistency rồi commit_chapter"
	case "en":
		return "Edit saved. For remaining flaws call edit_chapter again; otherwise call check_consistency then commit_chapter"
	default:
		return "edit 已落盘。仍有硬伤可再次 edit_chapter；否则 check_consistency 后 commit_chapter"
	}
}

// toolLang returns the normalized book language ("vi", "en", or "zh" as default).
func toolLang(s *store.Store) string {
	if s == nil || s.BookLanguage == nil {
		return "zh"
	}
	l, err := s.BookLanguage.Load()
	if err != nil || l == "" {
		return "zh"
	}
	clean := strings.ToLower(strings.TrimSpace(l))
	if clean == "vi" || clean == "en" {
		return clean
	}
	return "zh"
}
