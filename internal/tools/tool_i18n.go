package tools

import (
	"strings"

	"github.com/CTKiet2006/kietnovel/internal/store"
)

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
