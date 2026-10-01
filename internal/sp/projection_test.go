package sp

import (
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

// Snapshot test với đủ loại block + content dài để ép truncation.
func bigTestSnapshot() StorySnapshot {
	mk := func(id, kind string, n int) ContextBlock {
		return ContextBlock{ID: id, Kind: kind, Content: strings.Repeat("nội dung "+id+" ", n)}
	}
	return StorySnapshot{
		Chapter:        5,
		ProgressDigest: "abc",
		Blocks: []ContextBlock{
			{ID: "progress", Kind: "progress", Content: "phase viết"},
			{ID: "book", Kind: "book", Content: "sách"},
			{ID: "premise", Kind: "premise", Content: "tiền đề"},
			mk("outline:chapter:4", "outline", 200),
			mk("outline:chapter:5", "outline", 200),
			mk("outline:chapter:6", "outline", 200),
			mk("summary:chapter:3", "summary", 200),
			mk("summary:chapter:4", "summary", 200),
			{ID: "characters", Kind: "character", Content: "nhân vật"},
			{ID: "world:rules", Kind: "world", Content: "quy tắc"},
			{ID: "foreshadow:active", Kind: "foreshadow", Content: "phục bút"},
			{ID: "timeline:recent", Kind: "timeline", Content: "dòng thời gian"},
			{ID: "review:chapter:4", Kind: "review", Content: "nhận xét"},
		},
	}
}

func projIDs(p Projection) []string {
	var out []string
	for _, b := range p.Blocks {
		out = append(out, b.ID)
	}
	return out
}

// 1. Deterministic: cùng input → cùng output byte-level.
func TestProjectionDeterministic(t *testing.T) {
	snap := bigTestSnapshot()
	a, err := ProjectSnapshot(snap, ModeInspect, 3000)
	if err != nil {
		t.Fatal(err)
	}
	b, err := ProjectSnapshot(snap, ModeInspect, 3000)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Error("cùng input phải ra cùng output")
	}
}

// 2. Output không vượt maxChars (đếm rune, không phải byte).
func TestProjectionKhongVuotBudget(t *testing.T) {
	snap := bigTestSnapshot()
	for _, mode := range []Mode{ModeAsk, ModeInspect, ModeSuggest} {
		p, err := ProjectSnapshot(snap, mode, 3000)
		if err != nil {
			t.Fatal(err)
		}
		if p.Chars > 3000 {
			t.Errorf("%s: chars=%d vượt 3000", mode, p.Chars)
		}
		if !p.Truncated {
			t.Errorf("%s: phải truncated khi budget nhỏ", mode)
		}
		if len(p.Omitted) == 0 {
			t.Errorf("%s: phải có omitted", mode)
		}
	}
}

// 3. Priority thấp bị loại trước priority cao (inspect: progress giữ, outline cũ rớt).
func TestProjectionUuTienCaoGiuTruoc(t *testing.T) {
	snap := bigTestSnapshot()
	p, err := ProjectSnapshot(snap, ModeInspect, 3000)
	if err != nil {
		t.Fatal(err)
	}
	ids := projIDs(p)
	has := func(id string) bool {
		for _, x := range ids {
			if x == id {
				return true
			}
		}
		return false
	}
	if !has("progress") {
		t.Error("inspect phải giữ progress (rank cao nhất)")
	}
	if has("outline:chapter:4") {
		t.Error("inspect phải loại outline cũ trước")
	}
	for _, o := range p.Omitted {
		if o == "progress" || o == "book" {
			t.Errorf("omitted chứa nhầm block rank cao: %q", o)
		}
	}
}

// 4. Không mutate snapshot gốc.
func TestProjectionKhongMutateSnapshot(t *testing.T) {
	snap := bigTestSnapshot()
	before := append([]ContextBlock(nil), snap.Blocks...)
	p, err := ProjectSnapshot(snap, ModeInspect, 3000)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(snap.Blocks, before) {
		t.Error("snapshot gốc bị thay đổi")
	}
	// Sửa kết quả cũng không ảnh hưởng gốc (struct copy, không share).
	if len(p.Blocks) > 0 {
		p.Blocks[0].Content = "đã sửa"
		if snap.Block("outline:chapter:5") != nil && snap.Blocks[4].Content == "đã sửa" {
			t.Error("sửa projection lọt vào snapshot gốc")
		}
	}
}

// 5. Marker/source ID vẫn tồn tại trong text render.
func TestProjectionMarkerTonTai(t *testing.T) {
	snap := bigTestSnapshot()
	text, proj, err := RenderContext(snap, ModeInspect, 3000)
	if err != nil {
		t.Fatal(err)
	}
	if !proj.Truncated {
		t.Fatal("phải truncated")
	}
	if !strings.Contains(text, "[context:truncated]") {
		t.Error("thiếu marker truncated")
	}
	for _, b := range proj.Blocks {
		if !strings.Contains(text, "["+b.ID+"]") {
			t.Errorf("thiếu header [%s]", b.ID)
		}
	}
}

// 6. Truncated không tạo dữ liệu mới: content giữ lại là prefix của gốc.
func TestProjectionKhongBiaDuLieu(t *testing.T) {
	snap := bigTestSnapshot()
	orig := map[string]string{}
	for _, b := range snap.Blocks {
		orig[b.ID] = strings.TrimSpace(b.Content)
	}
	p, err := ProjectSnapshot(snap, ModeInspect, 3000)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range p.Blocks {
		full, ok := orig[b.ID]
		if !ok {
			t.Errorf("block lạ %q không có trong snapshot gốc", b.ID)
			continue
		}
		if !strings.HasPrefix(full, strings.TrimSpace(b.Content)) {
			t.Errorf("block %q không phải prefix của gốc (bịa?)", b.ID)
		}
	}
	// Text render chỉ chứa nội dung từ snapshot + marker cố định.
	text, _, err := RenderContext(snap, ModeInspect, 3000)
	if err != nil {
		t.Fatal(err)
	}
	if !utf8.ValidString(text) {
		t.Error("text render vỡ UTF-8 (cắt giữa rune)")
	}
}

// 7. Mode khác nhau tạo projection khác nhau khi budget ép phải chọn.
func TestProjectionModeKhacNhau(t *testing.T) {
	snap := bigTestSnapshot()
	a, err := ProjectSnapshot(snap, ModeAsk, 3000)
	if err != nil {
		t.Fatal(err)
	}
	s, err := ProjectSnapshot(snap, ModeSuggest, 3000)
	if err != nil {
		t.Fatal(err)
	}
	if reflect.DeepEqual(projIDs(a), projIDs(s)) {
		t.Error("ask và suggest phải khác nhau khi budget ép chọn")
	}
	// ask giữ outline:chapter:5 (current) đầu tiên.
	if len(a.Blocks) == 0 || a.Blocks[0].ID != "outline:chapter:5" {
		t.Errorf("ask phải giữ outline current đầu, được %v", projIDs(a))
	}
}

// 8. Empty/very-small budget không panic.
func TestProjectionBudgetNhoKhongPanic(t *testing.T) {
	snap := bigTestSnapshot()
	if _, err := ProjectSnapshot(snap, ModeAsk, 0); err == nil {
		t.Error("budget 0 phải lỗi rõ, không im lặng")
	}
	if _, err := ProjectSnapshot(snap, ModeAsk, -5); err == nil {
		t.Error("budget âm phải lỗi rõ")
	}
	// Budget tí hon (nhỏ hơn cả header): không panic, truncated.
	p, err := ProjectSnapshot(snap, ModeAsk, 10)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Truncated {
		t.Error("budget tí hon phải truncated")
	}
	text, _, err := RenderContext(snap, ModeAsk, 10)
	if err != nil {
		t.Fatal(err)
	}
	if !utf8.ValidString(text) {
		t.Error("vỡ UTF-8")
	}
}

// Budget đủ lớn → giữ hết, không truncated, digest audit khớp.
func TestProjectionDuBudgetGiuHet(t *testing.T) {
	snap := bigTestSnapshot()
	p, err := ProjectSnapshot(snap, ModeAsk, BudgetForMode(ModeAsk))
	if err != nil {
		t.Fatal(err)
	}
	if p.Truncated || len(p.Omitted) != 0 {
		t.Errorf("budget đủ phải giữ hết: omitted=%v", p.Omitted)
	}
	if len(p.Blocks) != len(snap.Blocks) {
		t.Errorf("giữ %d/%d blocks", len(p.Blocks), len(snap.Blocks))
	}
}

// Budget consts theo spec: ask 32k, inspect 28k, suggest 20k.
func TestBudgetTheoMode(t *testing.T) {
	if BudgetForMode(ModeAsk) != 32*1024 {
		t.Errorf("ask=%d", BudgetForMode(ModeAsk))
	}
	if BudgetForMode(ModeInspect) != 28*1024 {
		t.Errorf("inspect=%d", BudgetForMode(ModeInspect))
	}
	if BudgetForMode(ModeSuggest) != 20*1024 {
		t.Errorf("suggest=%d", BudgetForMode(ModeSuggest))
	}
}

// EstimateTokens ghi rõ là ước thô, không âm.
func TestEstimateTokens(t *testing.T) {
	if EstimateTokens(0) != 0 || EstimateTokens(-1) != 0 {
		t.Error("0/âm phải về 0")
	}
	if got := EstimateTokens(400); got != 100 {
		t.Errorf("400 chars → %d tokens", got)
	}
}
