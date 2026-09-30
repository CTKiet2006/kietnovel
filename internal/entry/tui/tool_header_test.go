package tui

import (
	"strings"
	"testing"

	"github.com/CTKiet2006/kietnovel/internal/host"
	"github.com/CTKiet2006/kietnovel/internal/i18n"
)

// TestTieuDeKhoiToolDichTheoNgonNgu: host soạn tiêu đề khối (✻ …) bằng tiếng Việt
// làm nguồn, TUI dịch lúc vẽ. Kiểm tra trên chính đường đi thật —
// renderStreamContent — chứ không gọi i18n.T rời rạc.
func TestTieuDeKhoiToolDichTheoNgonNgu(t *testing.T) {
	defer i18n.SetLanguage(i18n.LangVietnamese)

	round := host.StreamToolHeader("plan_chapter")
	if round == "" {
		t.Fatal("tiêu đề khối tool rỗng")
	}

	vi := renderStreamContent([]string{round}, 60, "")
	i18n.SetLanguage(i18n.LangEnglish)
	en := renderStreamContent([]string{round}, 60, "")
	i18n.SetLanguage(i18n.LangChinese)
	zh := renderStreamContent([]string{round}, 60, "")

	if !strings.Contains(vi, "Lên kế hoạch") {
		t.Errorf("vi: %q", vi)
	}
	if strings.Contains(en, "Lên kế hoạch") {
		t.Errorf("en chưa dịch: %q", en)
	}
	if !strings.Contains(en, "Plan") {
		t.Errorf("en thiếu nhãn: %q", en)
	}
	if !strings.Contains(zh, "规划") {
		t.Errorf("zh chưa dịch sang Trung: %q", zh)
	}
	// Ký tự ✻ phải còn, vì renderAgentBlock nhận diện khối theo tiền tố này.
	for name, got := range map[string]string{"vi": vi, "en": en, "zh": zh} {
		if !strings.Contains(got, "✻") {
			t.Errorf("%s mất ký tự ✻ nên không còn nhận diện là khối: %q", name, got)
		}
	}
}

// TestNoiDungVanGiuNguyen: phần thân khối là dữ liệu, không dịch. i18n.T trả
// nguyên bản khi không có bản dịch, nên chỉ cần bảo đảm nội dung chưa dịch không
// bị đổi.
func TestNoiDungVanGiuNguyen(t *testing.T) {
	defer i18n.SetLanguage(i18n.LangVietnamese)

	body := "Câu văn người dùng gõ, không có trong bảng dịch"
	if got := i18n.T(body); got != body {
		t.Fatalf("i18n.T sửa cả nội dung thô: %q → %q", body, got)
	}
}
