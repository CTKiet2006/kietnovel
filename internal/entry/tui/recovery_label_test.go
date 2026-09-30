package tui

import (
	"strings"
	"testing"

	"github.com/CTKiet2006/kietnovel/internal/host"
	"github.com/CTKiet2006/kietnovel/internal/i18n"
	"github.com/charmbracelet/x/ansi"
)

// renderRecoveryLabel dựng đúng cái TUI hiển thị cho RecoveryLabel, để test
// kiểm tra chuỗi thật thay vì chỉ kiểm tra hàm dịch rời rạc.
func renderRecoveryLabel(msg host.Msg, contentW int) string {
	if msg.Empty() {
		return ""
	}
	return ansi.Strip(truncate(i18n.Tf(msg.Key, msg.Args...), contentW))
}

// TestRecoveryLabelDichDungTrenMoiNgonNgu là bài toán gốc: nhãn khôi phục do host
// gửi qua ranh giới, phải hiện đúng ở cả vi/en/zh.
//
// Trước khi có host.Msg, host điền sẵn rồi gửi chuỗi — TUI không còn gì để dịch,
// nên tiếng Việt lọt ra cả bản en và zh. Đây là lớp lỗi mà Msg + i18n.Tf chặn.
func TestRecoveryLabelDichDungTrenMoiNgonNgu(t *testing.T) {
	defer i18n.SetLanguage(i18n.LangVietnamese)

	label := host.Msg{Key: "Khôi phục: chương %d bị gián đoạn lúc ghi", Args: []any{3}}

	vi := renderRecoveryLabel(label, 40)
	i18n.SetLanguage(i18n.LangEnglish)
	en := renderRecoveryLabel(label, 40)
	i18n.SetLanguage(i18n.LangChinese)
	zh := renderRecoveryLabel(label, 40)

	if vi == en || en == zh || vi == zh {
		t.Fatalf("nhãn không đổi theo ngôn ngữ: vi=%q en=%q zh=%q", vi, en, zh)
	}
	for name, got := range map[string]string{"vi": vi, "en": en, "zh": zh} {
		if !strings.Contains(got, "3") {
			t.Errorf("%s mất tham số chương: %q", name, got)
		}
	}
	if strings.ContainsAny(en, "ôđá") || !strings.Contains(en, "Resume") {
		t.Errorf("en chưa dịch, còn tiếng Việt: %q", en)
	}
	if !strings.Contains(zh, "恢复") {
		t.Errorf("zh chưa dịch sang Trung: %q", zh)
	}
}

// TestRecoveryLabelRongKhongVeDong: Msg rỗng phải không vẽ gì, để sidebar không
// có một dòng trống lạ lõm ở trên "Tổng quan".
func TestRecoveryLabelRongKhongVeDong(t *testing.T) {
	if got := renderRecoveryLabel(host.Msg{}, 40); got != "" {
		t.Errorf("Msg rỗng mà vẫn ra %q", got)
	}
}
