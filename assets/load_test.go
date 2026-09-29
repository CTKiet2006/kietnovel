package assets

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildWriterPrompt_AssemblesProperly(t *testing.T) {
	protocol := mustRead(promptsFS, "prompts/writer.md")
	voice := mustRead(voiceFS, "voice.md")

	if !strings.Contains(protocol, voicePlaceholder) {
		t.Fatal("writer.md bắt buộc phải chứa voicePlaceholder")
	}

	const style = "## Phong cách kiếm hiệp\n\n- Văn phong cổ điển"
	got := BuildWriterPrompt(WithSimulationGuidance(protocol, "writer", "vi"), voice, style)
	if !strings.Contains(got, voice) {
		t.Fatal("BuildWriterPrompt phải chèn nội dung voice vào đúng vị trí")
	}
	if !strings.Contains(got, style) {
		t.Fatal("BuildWriterPrompt phải nối thêm style ở cuối")
	}
	if strings.Contains(got, voicePlaceholder) {
		t.Fatal("placeholder phải được thay thế hoàn toàn")
	}
}

// TestLoad_NoOverrides 零覆盖时 Voice/AntiAITone 与内置逐字节一致。
func TestLoad_NoOverrides(t *testing.T) {
	b := Load("default", LoadOptions{})
	if b.Voice != mustRead(voiceFS, "voice.md") {
		t.Fatal("Không có ghi đè thì Voice phải khớp với voice.md tích hợp")
	}
	if b.References.AntiAITone != mustRead(referencesFS, "references/anti-ai-tone.md") {
		t.Fatal("Không có ghi đè thì AntiAITone phải khớp với anti-ai-tone.md tích hợp")
	}
	if _, ok := b.Styles["default"]; !ok {
		t.Fatal("Bộ styles mặc định phải chứa default")
	}
}

func TestInterventionPromptsKeepScopeContract(t *testing.T) {
	promptsVI := loadPrompts("vi")
	for _, phrase := range []string{"ngữ cảnh không đồng nghĩa với ủy quyền sửa đổi", "phạm vi tối thiểu đủ dùng", "phạm vi phân tích không đồng nghĩa với phạm vi sửa đổi"} {
		if !strings.Contains(promptsVI.ArbiterIntervention, phrase) {
			t.Fatalf("Arbiter can thiệp tiếng Việt thiếu ràng buộc phạm vi: %q", phrase)
		}
	}

	promptsZH := loadPrompts("zh")
	for _, phrase := range []string{"上下文不等于修改授权", "最小充分范围", "分析范围不等于修改范围"} {
		if !strings.Contains(promptsZH.ArbiterIntervention, phrase) {
			t.Fatalf("Arbiter can thiệp tiếng Trung thiếu ràng buộc phạm vi: %q", phrase)
		}
	}
}

func TestStructuredArbiterPromptsContainOnlySemantics(t *testing.T) {
	prompts := loadPrompts("vi")
	for name, prompt := range map[string]string{
		"plan_start": prompts.ArbiterPlanStart,
		"failure":    prompts.ArbiterFailure,
	} {
		for _, duplicate := range []string{"```json", "đừng dùng Markdown", "xuất ra một đối tượng JSON"} {
			if strings.Contains(prompt, duplicate) {
				t.Fatalf("%s prompt còn lặp lại định dạng output: %q", name, duplicate)
			}
		}
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// TestLoad_ThreeTierAppendAndReplace kiểm tra 3 tầng ưu tiên ghi đè.
func TestLoad_ThreeTierAppendAndReplace(t *testing.T) {
	home := t.TempDir()
	book := t.TempDir()
	opts := LoadOptions{HomeStyleDir: home, BookStyleDir: book}

	writeFile(t, filepath.Join(home, "voice.md"), "Toàn cục: Giảm từ sáo rỗng")
	writeFile(t, filepath.Join(book, "voice.md"), "Cuốn sách: Tăng đối thoại tự nhiên")
	writeFile(t, filepath.Join(book, "anti-ai-tone.md"), "Cuốn sách: Cấm lặp từ")

	writeFile(t, filepath.Join(home, "styles", "fantasy.md"), "Kỳ ảo toàn cục")
	writeFile(t, filepath.Join(book, "styles", "xianxia.md"), "Tiên hiệp tùy chỉnh")
	writeFile(t, filepath.Join(book, "styles", "Bad Name!.md"), "Không hợp lệ")

	writeFile(t, filepath.Join(home, "genres", "fantasy", "style-references.md"), "Tham khảo toàn cục")
	writeFile(t, filepath.Join(book, "genres", "fantasy", "style-references.md"), "Tham khảo cuốn sách")

	b := Load("fantasy", opts)

	builtinVoice := mustRead(voiceFS, "voice.md")
	if !strings.HasPrefix(b.Voice, builtinVoice) {
		t.Fatal("Phần append phải giữ nguyên văn bản gốc làm tiền tố")
	}
	giIdx := strings.Index(b.Voice, "## Người dùng ghi đè văn phong toàn cục")
	bkIdx := strings.Index(b.Voice, "## Ghi đè văn phong cuốn sách này")
	if giIdx < 0 || bkIdx < 0 || giIdx > bkIdx {
		t.Fatalf("Thứ tự các phần ghi đè không đúng: global=%d book=%d", giIdx, bkIdx)
	}
	if !strings.Contains(b.Voice, "Toàn cục: Giảm từ sáo rỗng") || !strings.Contains(b.Voice, "Cuốn sách: Tăng đối thoại tự nhiên") {
		t.Fatal("Thiếu nội dung ghi đè")
	}
	if !strings.Contains(b.References.AntiAITone, "Cuốn sách: Cấm lặp từ") {
		t.Fatal("Thiếu phần ghi đè anti-ai-tone của cuốn sách")
	}

	if b.Styles["fantasy"] != "Kỳ ảo toàn cục" {
		t.Fatal("Style trùng tên phải được thay thế toàn bộ file")
	}
	if b.Styles["xianxia"] != "Tiên hiệp tùy chỉnh" {
		t.Fatal("Style mới phải được nhận diện ngay")
	}
	if _, ok := b.Styles["Bad Name!"]; ok {
		t.Fatal("Tên style không hợp lệ phải bị bỏ qua")
	}

	if b.References.StyleReference != "Tham khảo cuốn sách" {
		t.Fatalf("Tham khảo theo thể loại của cuốn sách phải được ưu tiên, nhận được: %q", b.References.StyleReference)
	}
}

// TestLoad_BookOverridesHomeOnStyles
func TestLoad_BookOverridesHomeOnStyles(t *testing.T) {
	home := t.TempDir()
	book := t.TempDir()
	writeFile(t, filepath.Join(home, "styles", "romance.md"), "Bản toàn cục")
	writeFile(t, filepath.Join(book, "styles", "romance.md"), "Bản của cuốn sách")
	b := Load("default", LoadOptions{HomeStyleDir: home, BookStyleDir: book})
	if b.Styles["romance"] != "Bản của cuốn sách" {
		t.Fatalf("Cuốn sách phải ghi đè toàn cục, nhận được: %q", b.Styles["romance"])
	}
}

// TestOverrideVoice_SharesAssemblyPath
func TestOverrideVoice_SharesAssemblyPath(t *testing.T) {
	b := Load("default", LoadOptions{})
	b.OverrideVoice("## Văn phong thử nghiệm\n\n- Câu ngắn gọn")
	got := BuildWriterPrompt(b.Prompts.Writer, b.Voice, "")
	if !strings.Contains(got, "## Văn phong thử nghiệm") {
		t.Fatal("OverrideVoice chưa có hiệu lực")
	}
	if strings.Contains(got, voicePlaceholder) {
		t.Fatal("Placeholder phải được thay thế")
	}
	if !strings.Contains(got, "## Giao thức thực thi") {
		t.Fatal("Phần giao thức không được bị phá hủy bởi override voice")
	}
}
