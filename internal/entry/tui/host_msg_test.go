package tui

import (
	"strings"
	"testing"

	"github.com/CTKiet2006/kietnovel/internal/host"
	"github.com/CTKiet2006/kietnovel/internal/i18n"
	"github.com/charmbracelet/x/ansi"
)

// TestNhanHostDichDungTrenMoiNgonNgu là cái bài toán gốc: host gửi nhãn hiển thị
// qua ranh giới, và nhãn đó phải hiện đúng ở cả vi/en/zh. Trước khi có host.Msg,
// host điền sẵn rồi gửi chuỗi, nên TUI không dịch được — tiếng Việt lọt ra cả
// bản en và zh. Test này khoá lại đúng điều đó.
func TestNhanHostDichDungTrenMoiNgonNgu(t *testing.T) {
	defer i18n.SetLanguage(i18n.LangVietnamese)

	label := host.Msg{Key: "Khôi phục: chương %d bị gián đoạn lúc ghi", Args: []any{3}}

	vi := label.String()
	i18n.SetLanguage(i18n.LangEnglish)
	en := i18n.Tf(label.Key, label.Args...)
	i18n.SetLanguage(i18n.LangChinese)
	zh := i18n.Tf(label.Key, label.Args...)

	if vi == en || en == zh || vi == zh {
		t.Fatalf("nhãn không đổi theo ngôn ngữ: vi=%q en=%q zh=%q", vi, en, zh)
	}
	for name, got := range map[string]string{"vi": vi, "en": en, "zh": zh} {
		if !strings.Contains(got, "3") {
			t.Errorf("%s mất tham số %q: %q", name, "3", got)
		}
	}
	// en và zh phải dịch, không phải rơi về tiếng Việt.
	if strings.Contains(en, "Khôi phục") {
		t.Errorf("en vẫn ra tiếng Việt (thiếu bản dịch): %q", en)
	}
	if !strings.Contains(zh, "恢复") {
		t.Errorf("zh không ra tiếng Trung: %q", zh)
	}
}

// TestRenderEventLineDichTheoNgonNgu: SummaryMsg phải được dịch khi render log,
// không chỉ khi đọc trực tiếp từ snapshot.
func TestRenderEventLineDichTheoNgonNgu(t *testing.T) {
	defer i18n.SetLanguage(i18n.LangVietnamese)

	msg := host.Msg{Key: "Đang suy nghĩ"}
	ev := host.Event{
		Category:   "MODEL",
		Summary:    msg.String(), // host điền sẵn cho log
		SummaryMsg: &msg,         // bản chưa dịch cho UI
	}

	vi := ansi.Strip(renderEventLine(ev, 60, 0))
	i18n.SetLanguage(i18n.LangChinese)
	zh := ansi.Strip(renderEventLine(ev, 60, 0))

	if !strings.Contains(vi, "Đang suy nghĩ") {
		t.Errorf("vi: %q", vi)
	}
	if strings.Contains(zh, "Đang suy nghĩ") {
		t.Errorf("zh chưa dịch, còn tiếng Việt: %q", zh)
	}
}

// TestEventSummaryGhepTienToVaDuLieu: sự kiện retry ghép tiền tố dịch được với
// thông điệp provider không dịch. Nếu chỉ lấy bản dịch rồi bỏ phần đuôi, người
// dùng mất thông tin lỗi thật của provider.
func TestEventSummaryGhepTienToVaDuLieu(t *testing.T) {
	defer i18n.SetLanguage(i18n.LangVietnamese)

	prefix := host.Msg{Key: "Thử lại (%d/%d): ", Args: []any{2, 7}}
	providerMsg := "stream read error: INTERNAL_ERROR"
	ev := host.Event{
		Category:   "TOOL",
		Summary:    prefix.String() + providerMsg,
		SummaryMsg: &prefix,
	}

	vi := eventSummary(ev)
	if !strings.Contains(vi, "Thử lại (2/7)") || !strings.Contains(vi, "INTERNAL_ERROR") {
		t.Fatalf("vi ghép sai: %q", vi)
	}

	i18n.SetLanguage(i18n.LangEnglish)
	en := eventSummary(ev)
	if !strings.Contains(en, "Retry (2/7)") {
		t.Errorf("tiền tố chưa dịch: %q", en)
	}
	if !strings.Contains(en, "INTERNAL_ERROR") {
		t.Errorf("mất thông điệp provider: %q", en)
	}
	i18n.SetLanguage(i18n.LangChinese)
	zh := eventSummary(ev)
	if !strings.Contains(zh, "重试 (2/7)") || !strings.Contains(zh, "INTERNAL_ERROR") {
		t.Errorf("zh ghép sai: %q", zh)
	}
}

// TestEventKhongCoSummaryMsgGiuNguyen: SummaryMsg nil nghĩa là Summary là dữ liệu
// thô (tên tool, thông điệp provider) — không được "dịch" nó, nếu không sẽ hỏng.
func TestEventKhongCoSummaryMsgGiuNguyen(t *testing.T) {
	defer i18n.SetLanguage(i18n.LangVietnamese)

	ev := host.Event{Category: "MODEL", Summary: "read_chapter"}
	i18n.SetLanguage(i18n.LangChinese)
	got := ansi.Strip(renderEventLine(ev, 60, 0))
	if !strings.Contains(got, "read_chapter") {
		t.Fatalf("tên tool bị mất: %q", got)
	}
}

// TestMsgEmptyVaString khoá hợp đồng của kiểu Msg: Empty phải đúng cả khi chỉ có
// khoảng trắng, String phải trả về nguyên Key khi không có Args (tránh %!(EXTRA)).
func TestMsgEmptyVaString(t *testing.T) {
	if !(host.Msg{}).Empty() {
		t.Error("Msg rỗng phải Empty")
	}
	if !(host.Msg{Key: "   "}).Empty() {
		t.Error("Msg toàn khoảng trắng phải Empty")
	}
	if (host.Msg{Key: "Đang suy nghĩ"}).Empty() {
		t.Error("Msg có Key không được Empty")
	}
	if got := (host.Msg{Key: "Đang suy nghĩ"}).String(); got != "Đang suy nghĩ" {
		t.Errorf("String() = %q", got)
	}
	if got := (host.Msg{}).String(); got != "" {
		t.Errorf("Msg rỗng String() = %q, mong rỗng", got)
	}
	// Có Args thì phải điền đúng, không sinh %!(EXTRA).
	got := (host.Msg{Key: "Sinh %s", Args: []any{"read_chapter"}}).String()
	if got != "Sinh read_chapter" {
		t.Errorf("String() = %q", got)
	}
}
