package assets

import (
	"strings"
	"testing"
)

// TestApplyLanguage_GanCaoArbiter là hồi quy cho lỗi tiền đề ra tiếng Trung dù
// config đã đặt language=vi.
//
// Chuỗi nhân quả: Arbiter sinh câu nhiệm vụ → Architect nhận nhiệm vụ đó làm việc.
// Directive gắn riêng vào Architect là không đủ, vì nhiệm vụ truyền xuống vẫn
// tiếng Trung. Log thật đã lộ: nhiệm vụ "补齐基础设定与作品信息缺项" do Arbiter sinh,
// và tiền đề ra tiếng Trung theo sau.
func TestApplyLanguage_GanCaoArbiter(t *testing.T) {
	markers := []struct {
		name string
		p    func(Prompts) string
	}{
		{"ArchitectLong", func(p Prompts) string { return p.ArchitectLong }},
		{"Writer", func(p Prompts) string { return p.Writer }},
		{"Editor", func(p Prompts) string { return p.Editor }},
		{"ArbiterPlanStart", func(p Prompts) string { return p.ArbiterPlanStart }},
		{"ArbiterIntervention", func(p Prompts) string { return p.ArbiterIntervention }},
		{"ArbiterFailure", func(p Prompts) string { return p.ArbiterFailure }},
	}
	for _, m := range markers {
		t.Run(m.name, func(t *testing.T) {
			b := LoadWithLanguage("vi", "default", LoadOptions{})
			before := m.p(b.Prompts)
			b.ApplyLanguage("vi")
			after := m.p(b.Prompts)

			if !strings.HasSuffix(after, "giữ nguyên không dịch.") {
				t.Fatalf("%s chưa nhận directive tiếng Việt", m.name)
			}
			if len(after) <= len(before) {
				t.Errorf("%s không dài thêm", m.name)
			}
		})
	}
}

// TestApplyLanguage_EnglishCungGanArbiter: en cũng phải tới Arbiter, không chỉ vi.
// Nếu chỉ sửa nhánh "vi" thì người dùng chọn English lại gặp lỗi y hệt.
func TestApplyLanguage_EnglishCungGanArbiter(t *testing.T) {
	b := LoadWithLanguage("en", "default", LoadOptions{})
	b.ApplyLanguage("en")
	for name, p := range map[string]string{
		"ArbiterPlanStart":    b.Prompts.ArbiterPlanStart,
		"ArbiterIntervention": b.Prompts.ArbiterIntervention,
		"ArbiterFailure":      b.Prompts.ArbiterFailure,
	} {
		if !strings.Contains(p, "## Writing Language") {
			t.Errorf("%s không nhận directive tiếng Anh", name)
		}
	}
}

// TestApplyLanguage_ZhKhongGanArbiter: với zh thì protocol vốn đã tiếng Trung, nên
// phải giữ nguyên — nếu gắn directive tiếng Việt vào thì sẽ hỏng người viết Trung.
func TestApplyLanguage_ZhKhongGanArbiter(t *testing.T) {
	b := LoadWithLanguage("zh", "default", LoadOptions{})
	snap := b.Prompts
	b.ApplyLanguage("zh")
	if b.Prompts.ArbiterPlanStart != snap.ArbiterPlanStart {
		t.Error("ApplyLanguage(zh) phải giữ nguyên ArbiterPlanStart")
	}
}
