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

// Lớp voice theo ngôn ngữ: vi dùng voice.md, zh dùng voice_zh.md.
func TestLoadWithLanguage_VoiceSelection(t *testing.T) {
	vi := LoadWithLanguage("vi", "default", LoadOptions{})
	zh := LoadWithLanguage("zh", "default", LoadOptions{})

	if vi.Voice != mustRead(voiceFS, "voice.md") {
		t.Fatal("vi phải nạp voice.md")
	}
	if zh.Voice != mustRead(voiceFS, "voice_zh.md") {
		t.Fatal("zh phải nạp voice_zh.md")
	}
	if vi.Voice == zh.Voice {
		t.Fatal("voice vi và zh phải khác nhau")
	}
	if vi.Language != "vi" || zh.Language != "zh" {
		t.Fatalf("Bundle.Language sai: %q / %q", vi.Language, zh.Language)
	}
}

// Load() 2-arg giữ hành vi cũ (mặc định vi) để eval không bị đổi.
func TestLoad_DefaultsToVietnamese(t *testing.T) {
	if got := Load("default", LoadOptions{}).Voice; got != mustRead(voiceFS, "voice.md") {
		t.Fatal("Load() không tham số phải mặc định tiếng Việt")
	}
}
