package assets

import (
	"strings"
	"testing"
)

// vi (mặc định, kể cả chuỗi rỗng) → gắn chỉ dẫn buộc đầu ra tiếng Việt.
func TestApplyLanguage_Vietnamese(t *testing.T) {
	b := LoadWithLanguage("vi", "default", LoadOptions{})
	before := b.Prompts.Writer
	b.ApplyLanguage("vi")
	for name, p := range map[string]string{
		"architect-short": b.Prompts.ArchitectShort,
		"architect-long":  b.Prompts.ArchitectLong,
		"writer":          b.Prompts.Writer,
		"editor":          b.Prompts.Editor,
	} {
		if !strings.Contains(p, "Tiếng Việt") {
			t.Fatalf("prompt %s thiếu chỉ dẫn ngôn ngữ", name)
		}
	}
	if b.Prompts.Writer == before {
		t.Fatal("writer prompt phải đổi sau ApplyLanguage(vi)")
	}
	// {{VOICE}} phải còn nguyên để BuildWriterPrompt tiêu thụ.
	if !strings.Contains(b.Prompts.Writer, voicePlaceholder) {
		t.Fatal("ApplyLanguage làm mất {{VOICE}} placeholder")
	}
}

// Chuỗi rỗng tương thích cấu hình cũ chưa có trường language.
func TestApplyLanguage_EmptyIsVietnamese(t *testing.T) {
	b := LoadWithLanguage("", "default", LoadOptions{})
	b.ApplyLanguage("")
	if !strings.Contains(b.Prompts.Editor, "Tiếng Việt") {
		t.Fatal("language rỗng phải xử như vi")
	}
}

// zh → không đụng gì, vì protocol gốc vốn đã là tiếng Trung.
func TestApplyLanguage_ChineseKeepsOriginal(t *testing.T) {
	b := LoadWithLanguage("zh", "default", LoadOptions{})
	snap := b.Prompts
	b.ApplyLanguage("zh")
	if b.Prompts != snap {
		t.Fatal("ApplyLanguage(zh) phải giữ nguyên prompts")
	}
}

// Lớp voice theo ngôn ngữ: vi→voice.md, en→voice_en.md, zh→voice_zh.md.
func TestLoadWithLanguage_VoiceSelection(t *testing.T) {
	vi := LoadWithLanguage("vi", "default", LoadOptions{})
	en := LoadWithLanguage("en", "default", LoadOptions{})
	zh := LoadWithLanguage("zh", "default", LoadOptions{})

	if vi.Voice != mustRead(voiceFS, "voice.md") {
		t.Fatal("vi phải nạp voice.md")
	}
	if en.Voice != mustRead(voiceFS, "voice_en.md") {
		t.Fatal("en phải nạp voice_en.md")
	}
	if zh.Voice != mustRead(voiceFS, "voice_zh.md") {
		t.Fatal("zh phải nạp voice_zh.md")
	}
	if vi.Voice == en.Voice || en.Voice == zh.Voice {
		t.Fatal("ba lớp voice phải khác nhau")
	}
	if vi.Language != "vi" || en.Language != "en" || zh.Language != "zh" {
		t.Fatalf("Bundle.Language sai: %q / %q / %q", vi.Language, en.Language, zh.Language)
	}
}

// en: chỉ dẫn đầu ra tiếng Anh.
func TestApplyLanguage_English(t *testing.T) {
	b := LoadWithLanguage("en", "default", LoadOptions{})
	b.ApplyLanguage("en")
	for name, p := range map[string]string{
		"architect-short": b.Prompts.ArchitectShort,
		"architect-long":  b.Prompts.ArchitectLong,
		"writer":          b.Prompts.Writer,
		"editor":          b.Prompts.Editor,
	} {
		if !strings.Contains(p, "MUST be written in") {
			t.Fatalf("prompt %s thiếu chỉ dẫn tiếng Anh", name)
		}
	}
	if !strings.Contains(b.Prompts.Writer, voicePlaceholder) {
		t.Fatal("ApplyLanguage(en) làm mất {{VOICE}} placeholder")
	}
}

// Ba ngôn ngữ phải dùng directive riêng, không lẫn nhau.
func TestApplyLanguage_DirectivesAreDistinct(t *testing.T) {
	vi := LoadWithLanguage("vi", "default", LoadOptions{})
	vi.ApplyLanguage("vi")
	en := LoadWithLanguage("en", "default", LoadOptions{})
	en.ApplyLanguage("en")

	if strings.Contains(vi.Prompts.Writer, "MUST be written in") {
		t.Fatal("directive tiếng Anh lọt vào bản tiếng Việt")
	}
	if strings.Contains(en.Prompts.Writer, "PHẢI viết bằng") {
		t.Fatal("directive tiếng Việt lọt vào bản tiếng Anh")
	}
}

// Load() 2-arg giữ hành vi cũ (mặc định vi) để eval không bị đổi.
func TestLoad_DefaultsToVietnamese(t *testing.T) {
	if got := Load("default", LoadOptions{}).Voice; got != mustRead(voiceFS, "voice.md") {
		t.Fatal("Load() không tham số phải mặc định tiếng Việt")
	}
}
