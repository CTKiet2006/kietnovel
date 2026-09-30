package assets

import (
	"strings"
	"testing"
)

// TestLoadWithLanguageChonDungLopVoice — phần cốt lõi của 3c.
//
// Nếu truyện A viết tiếng Việt rồi chuyển sang truyện B, mà vẫn dùng Bundle cũ,
// thì chương của B được viết bằng giọng của A. Đây là test chặn đúng chỗ đó.
func TestLoadWithLanguageChonDungLopVoice(t *testing.T) {
	vi := LoadWithLanguage("vi", "", LoadOptions{})
	zh := LoadWithLanguage("zh", "", LoadOptions{})
	en := LoadWithLanguage("en", "", LoadOptions{})

	if vi.Voice == zh.Voice {
		t.Error("voice vi == voice zh, chuyen truyen se van viet sai giong")
	}
	if vi.Voice == en.Voice {
		t.Error("voice vi == voice en")
	}
	if vi.Language != "vi" || zh.Language != "zh" || en.Language != "en" {
		t.Errorf("Bundle.Language khong khop: %q %q %q", vi.Language, zh.Language, en.Language)
	}
}

// BuildWriterPrompt phải nhúng voice vào prompt, và mỗi ngôn ngữ ra một prompt
// khác nhau — nếu không, prompt Writer vẫn giọng cũ dù Bundle đã đổi.
func TestBuildWriterPromptPhaiNhungVoice(t *testing.T) {
	tpl := "SYSTEM-HEADER\n{{VOICE}}\nSYSTEM-FOOTER"
	for _, lang := range []string{"vi", "zh", "en"} {
		b := LoadWithLanguage(lang, "", LoadOptions{})
		got := BuildWriterPrompt(tpl, b.Voice, "")
		if strings.Contains(got, "{{VOICE}}") {
			t.Errorf("%s: placeholder {{VOICE}} chua duoc thay", lang)
		}
		if !strings.Contains(got, strings.TrimSpace(b.Voice[:40])) {
			t.Errorf("%s: voice khong co trong prompt", lang)
		}
	}
}

// ApplyLanguage phải bám vào CẢ BA vai trò kể cả Arbiter — đã từng sót Arbiter và
// làm tiền đề tràn ra tiếng Trung khi language=vi.
func TestApplyLanguageBamTatCaVaiTro(t *testing.T) {
	b := LoadWithLanguage("vi", "", LoadOptions{})
	b.ApplyLanguage("vi")
	zh := LoadWithLanguage("zh", "", LoadOptions{})
	zh.ApplyLanguage("zh")

	for _, role := range []string{"architect", "writer", "editor", "arbiter"} {
		a := promptFor(b, role)
		c := promptFor(zh, role)
		if a == "" {
			t.Errorf("%s: prompt rong", role)
			continue
		}
		if a == c {
			t.Errorf("%s: prompt vi == prompt zh, directive ngon ngu khong co tac dung", role)
		}
	}
}

// promptFor lấy prompt theo tên vai trò, dùng chung cho cả Architect và Arbiter vì
// cả hai đều là "can thiệp lúc dàn ý".
func promptFor(b Bundle, role string) string {
	switch role {
	case "architect":
		return b.Prompts.ArchitectLong
	case "writer":
		return BuildWriterPrompt(b.Prompts.Writer, b.Voice, "")
	case "editor":
		return b.Prompts.Editor
	case "arbiter":
		return b.Prompts.ArbiterPlanStart + b.Prompts.ArbiterIntervention
	}
	return ""
}
