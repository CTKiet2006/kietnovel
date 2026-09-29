package assets

import (
	"strings"
	"testing"
)

// language=vi: directive tiếng Việt gắn vào 4 prompt vai trò chính.
func TestApplyLanguage_Vietnamese(t *testing.T) {
	b := Load("default", LoadOptions{})
	before := b.Prompts.Writer
	b.ApplyLanguage("vi")
	for name, p := range map[string]string{
		"architect-short": b.Prompts.ArchitectShort,
		"architect-long":  b.Prompts.ArchitectLong,
		"writer":          b.Prompts.Writer,
		"editor":          b.Prompts.Editor,
	} {
		if !strings.Contains(p, "Tiếng Việt") {
			t.Fatalf("%s thiếu chỉ dẫn ngôn ngữ", name)
		}
	}
	if b.Prompts.Writer == before {
		t.Fatal("writer prompt không đổi sau ApplyLanguage(vi)")
	}
	// Placeholder {{VOICE}} của writer template phải còn nguyên để BuildWriterPrompt tiêu thụ.
	if !strings.Contains(b.Prompts.Writer, voicePlaceholder) {
		t.Fatal("ApplyLanguage làm mất {{VOICE}} placeholder")
	}
}

// Chuỗi trống cũng là vi (tương thích cấu hình cũ không có trường language).
func TestApplyLanguage_EmptyIsVietnamese(t *testing.T) {
	b := Load("default", LoadOptions{})
	b.ApplyLanguage("")
	if !strings.Contains(b.Prompts.Editor, "Tiếng Việt") {
		t.Fatal("language trống phải xử như vi")
	}
}

// language=zh: giữ nguyên prompt gốc (vốn tiếng Trung).
func TestApplyLanguage_ChineseKeepsOriginal(t *testing.T) {
	b := Load("default", LoadOptions{})
	snap := b.Prompts
	b.ApplyLanguage("zh")
	if b.Prompts != snap {
		t.Fatal("ApplyLanguage(zh) phải giữ nguyên prompts")
	}
}
