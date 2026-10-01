package sp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CTKiet2006/kietnovel/internal/domain"
)

// TestSnapshotCorruptionBaoLoi — file hỏng phải trả lỗi, không được im lặng bỏ
// qua rồi đưa snapshot thiếu dữ kiện như thể chưa có.
//
// Trước fix, mọi loader err != nil đều bị nuốt: truyện hỏng characters.json vẫn
// nhận snapshot không có character state, advisor trả lời như thể phần đó đơn
// giản là chưa có — lỗi trust nặng.
func TestSnapshotCorruptionBaoLoi(t *testing.T) {
	st := newTestStore(t)
	startWriting(t, st)
	// Ghi dữ kiện hợp lệ trước để store đầy đủ.
	if err := st.Characters.Save([]domain.Character{{Name: "Ngoc"}}); err != nil {
		t.Fatal(err)
	}

	// Làm hỏng characters.json.
	if err := os.WriteFile(filepath.Join(st.Dir(), "characters.json"), []byte("{ hong"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := BuildSnapshot(st)
	if err == nil {
		t.Fatal("characters.json hỏng phải lỗi")
	}
	if !strings.Contains(err.Error(), "characters") {
		t.Errorf("lỗi phải nêu rõ section hỏng: %v", err)
	}
}

func TestSnapshotOutlineCorruptionBaoLoi(t *testing.T) {
	st := newTestStore(t)
	startWriting(t, st)
	if err := st.Outline.SaveOutline([]domain.OutlineEntry{{Chapter: 1, Title: "Mo dau"}}); err != nil {
		t.Fatal(err)
	}
	dir := st.Dir()
	if err := os.WriteFile(filepath.Join(dir, "outline.json"), []byte("{ hong"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := BuildSnapshot(st)
	if err == nil {
		t.Fatal("outline.json hỏng phải lỗi")
	}
	if !strings.Contains(err.Error(), "dàn ý") {
		t.Errorf("lỗi phải nêu rõ section hỏng: %v", err)
	}
}

// TestSnapshotThieuFileVanQua — file chưa có (truyện mới) thì bỏ qua, không lỗi.
// Phân biệt với corruption ở trên: missing là chưa có, corrupt là hỏng.
func TestSnapshotThieuFileVanQua(t *testing.T) {
	st := newTestStore(t)
	// Store mới init: chưa có characters.json, outline.json, summaries...
	snap, err := BuildSnapshot(st)
	if err != nil {
		t.Fatalf("truyện mới thiếu file phải qua, lỗi: %v", err)
	}
	if snap.Block("progress") == nil {
		t.Error("thiếu block progress")
	}
}

// TestPromptKhongCheIDKhongTonTai — contract chỉ được VÍ DỤ các ID khối có thật.
func TestPromptBaLocaleTagsKhongDoi(t *testing.T) {
	tags := []string{"[FACT]", "[INFERENCE]", "[OPTION]", "[UNKNOWN]"}
	langs := map[string][]string{
		"vi": {"Bạn là Story Partner"},
		"en": {"Answer in English", "You are Story Partner"},
		"zh": {"请使用中文回答", "Story Partner"},
	}
	for lang, musts := range langs {
		sys, _ := RenderPrompt(StorySnapshot{}, "Q?", lang)
		for _, m := range append(tags, musts...) {
			if !strings.Contains(sys, m) {
				t.Errorf("%s thiếu %q", lang, m)
			}
		}
	}
	// Lang lạ/rỗng về vi.
	sys, _ := RenderPrompt(StorySnapshot{}, "Q?", "fr")
	if !strings.Contains(sys, "Bạn là Story Partner") {
		t.Error("lang lạ phải về vi")
	}
	sys, _ = RenderPrompt(StorySnapshot{}, "Q?", "")
	if !strings.Contains(sys, "Bạn là Story Partner") {
		t.Error("lang rỗng phải về vi")
	}
	// Header user localized theo request lang (system đã dịch mà header còn Việt
	// là i18n nửa vời), nhưng block dữ kiện phải giống hệt mọi locale.
	blocks := []ContextBlock{{ID: "progress", Kind: "progress", Content: "x"}}
	_, uVi := RenderPrompt(StorySnapshot{Blocks: blocks}, "Q?", "vi")
	_, uEn := RenderPrompt(StorySnapshot{Blocks: blocks}, "Q?", "en")
	_, uZh := RenderPrompt(StorySnapshot{Blocks: blocks}, "Q?", "zh")
	if !strings.Contains(uVi, "CÂU HỎI CỦA NGƯỜI VIẾT") {
		t.Error("user vi thiếu header Việt")
	}
	if !strings.Contains(uEn, "WRITER'S QUESTION") {
		t.Error("user en thiếu header Anh")
	}
	if !strings.Contains(uZh, "写作者的问题") {
		t.Error("user zh thiếu header Trung")
	}
	for _, u := range []string{uVi, uEn, uZh} {
		if !strings.Contains(u, "[progress]\nx") {
			t.Errorf("block dữ kiện phải giống hệt mọi locale: %q", u)
		}
	}
}

// P4.2: wrapper RenderPrompt cho ask phải byte-identical với RenderPromptForMode
// ask — regression ở đường hỏi chính là không chấp nhận được.
func TestRenderPromptWrapperByteIdenticalAsk(t *testing.T) {
	snap := StorySnapshot{ProgressDigest: "abc", Blocks: []ContextBlock{
		{ID: "progress", Kind: "progress", Content: "x"},
		{ID: "outline:chapter:5", Kind: "outline", Content: "y"},
	}}
	for _, lang := range []string{"vi", "en", "zh", "", "fr"} {
		s1, u1 := RenderPrompt(snap, "Q?", lang)
		s2, u2 := RenderPromptForMode(snap, ModeAsk, "Q?", lang)
		if s1 != s2 || u1 != u2 {
			t.Errorf("lang %q: wrapper khác ForMode(ask)", lang)
		}
	}
}

// P4.2: prompt inspect/suggest giữ tags, có task riêng theo mode, đủ 3 locale.
func TestPromptModeTaskRieng(t *testing.T) {
	snap := StorySnapshot{}
	cases := []struct {
		mode Mode
		lang string
		must []string
		not  []string
	}{
		{ModeInspect, "vi", []string{"[FACT]", "[UNKNOWN]", "CHẨN ĐOÁN"}, []string{"ĐỀ XUẤT HƯỚNG"}},
		{ModeInspect, "en", []string{"[FACT]", "DIAGNOSE"}, []string{"PROPOSE NEXT"}},
		{ModeInspect, "zh", []string{"[FACT]", "诊断"}, []string{"后续方向"}},
		{ModeSuggest, "vi", []string{"[OPTION]", "ĐỀ XUẤT HƯỚNG"}, []string{"CHẨN ĐOÁN"}},
		{ModeSuggest, "en", []string{"[OPTION]", "PROPOSE NEXT"}, []string{"DIAGNOSE"}},
		{ModeSuggest, "zh", []string{"[OPTION]", "后续方向"}, []string{"诊断故事状态"}},
	}
	for _, c := range cases {
		sys, _ := RenderPromptForMode(snap, c.mode, "", c.lang)
		for _, m := range c.must {
			if !strings.Contains(sys, m) {
				t.Errorf("%s/%s thiếu %q", c.mode, c.lang, m)
			}
		}
		for _, n := range c.not {
			if strings.Contains(sys, n) {
				t.Errorf("%s/%s lẫn task mode khác %q", c.mode, c.lang, n)
			}
		}
	}
}

// P4.2: user section render giống hệt mọi mode (question rỗng ở soi/gợi ý OK).
func TestRenderPromptForModeUserGiongNhau(t *testing.T) {
	snap := StorySnapshot{ProgressDigest: "d", Blocks: []ContextBlock{
		{ID: "progress", Kind: "progress", Content: "x"},
	}}
	_, uAsk := RenderPromptForMode(snap, ModeAsk, "", "vi")
	_, uSoi := RenderPromptForMode(snap, ModeInspect, "", "vi")
	_, uGoiY := RenderPromptForMode(snap, ModeSuggest, "", "vi")
	if uAsk != uSoi || uSoi != uGoiY {
		t.Error("user section phải giống hệt mọi mode")
	}
}
func TestPromptKhongCheIDKhongTonTai(t *testing.T) {
	sys, _ := RenderPrompt(StorySnapshot{}, "Hỏi?", "vi")
	if strings.Contains(sys, "dạng [character:") {
		t.Error("prompt còn ví dụ cho phép cite ID entity-level không tồn tại trong snapshot")
	}
	for _, must := range []string{"[progress]", "[outline:chapter:5]", "[characters]"} {
		if !strings.Contains(sys, must) {
			t.Errorf("prompt thiếu ví dụ ID thật %q", must)
		}
	}
}
