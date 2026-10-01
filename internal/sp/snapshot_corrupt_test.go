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
// Câu cấm ("cấm tự chế ID entity-level kiểu [character:ngoc]") được phép nhắc
// tới nó như ví dụ cấm — test chỉ bắt dạng "dạng [character:..." (ví dụ cho phép).
func TestPromptKhongCheIDKhongTonTai(t *testing.T) {
	sys, _ := RenderPrompt(StorySnapshot{}, "Hỏi?")
	if strings.Contains(sys, "dạng [character:") {
		t.Error("prompt còn ví dụ cho phép cite ID entity-level không tồn tại trong snapshot")
	}
	for _, must := range []string{"[progress]", "[outline:chapter:5]", "[characters]"} {
		if !strings.Contains(sys, must) {
			t.Errorf("prompt thiếu ví dụ ID thật %q", must)
		}
	}
}
