package tui

import (
	"errors"
	"strings"
	"testing"

	"github.com/CTKiet2006/kietnovel/internal/i18n"
	"github.com/CTKiet2006/kietnovel/internal/sp"
	tea "github.com/charmbracelet/bubbletea"
)

// 9-11. /sp hỏi|ask|问 đều về ask với đúng question.
func TestParseSPBaLocale(t *testing.T) {
	cases := []struct {
		args         []string
		questionWant string
	}{
		{[]string{"hỏi", "Ngọc", "có", "nên?"}, "Ngọc có nên?"},
		{[]string{"ask", "Should", "Ngoc?"}, "Should Ngoc?"},
		{[]string{"问", "Ngọc", "好吗?"}, "Ngọc 好吗?"},
		{[]string{"HỎI", "x?"}, "x?"},
	}
	for _, c := range cases {
		mode, q, ok := parseSPArgs(c.args)
		if !ok || mode != "ask" || q != c.questionWant {
			t.Errorf("parseSPArgs(%v) = %q,%q,%v", c.args, mode, q, ok)
		}
	}
}

// 12. /sp <question> thẳng → ask mặc định, khỏi nhớ từ.
func TestParseSPShortcut(t *testing.T) {
	mode, q, ok := parseSPArgs([]string{"Ngọc", "có", "nên", "tha", "không?"})
	if !ok || mode != "ask" || q != "Ngọc có nên tha không?" {
		t.Errorf("shortcut sai: %q,%q,%v", mode, q, ok)
	}
}

// 13. /sp không question → usage (ok=false), kể cả "/sp hỏi" cụt.
func TestParseSPKhongQuestion(t *testing.T) {
	for _, args := range [][]string{nil, {}, {""}, {"   "}, {"hỏi"}, {"ask"}, {"问"}} {
		if _, _, ok := parseSPArgs(args); ok {
			t.Errorf("parseSPArgs(%v) phải false", args)
		}
	}
}

// runSPCommand với runtime nil → báo lỗi, không panic, không mở modal.
func TestRunSPKhongRuntime(t *testing.T) {
	m := newTestModel(120, 30)
	m.runtime = nil
	out, _ := runSPCommand(m, []string{"hỏi", "x?"})
	got := out.(Model)
	if got.spState != nil {
		t.Error("không runtime mà vẫn mở modal")
	}
}

// 15-17. Modal: loading → result điền answer + metadata + viewport.
func TestSPModalResult(t *testing.T) {
	m := newTestModel(120, 30)
	m.spState = newStoryPartnerState(120, 30, 7, "Hỏi?", "vi")
	if !m.spState.loading {
		t.Error("mới mở phải loading")
	}
	out, _ := m.handleSPResultMsg(spResultMsg{reqID: 7, result: sp.Result{
		Answer: "[FACT progress] ok", SnapshotDigest: "abcdef1234567890",
		Chapter: 3, Provider: "p", Model: "m",
	}})
	got := out.(Model)
	s := got.spState
	if s.loading {
		t.Error("có result phải hết loading")
	}
	if !strings.Contains(s.answer, "[FACT progress]") {
		t.Errorf("answer sai: %q", s.answer)
	}
	if s.digest != "abcdef1234567890" || s.chapter != 3 || s.provider != "p" || s.model != "m" {
		t.Errorf("metadata sai: %+v", s)
	}
	if !strings.Contains(s.viewport.View(), "[FACT progress]") {
		t.Error("viewport phải chứa answer để cuộn")
	}
	// View phải vẽ được metadata.
	view := s.view(120, 30)
	for _, must := range []string{"abcdef123456", "chương 3", "Hỏi: "} {
		if !strings.Contains(view, must) {
			t.Errorf("view thiếu %q", must)
		}
	}
}

// 18. Error state: giữ modal, hiện lỗi, không retry tự động.
func TestSPModalError(t *testing.T) {
	m := newTestModel(120, 30)
	m.spState = newStoryPartnerState(120, 30, 7, "Hỏi?", "vi")
	out, _ := m.handleSPResultMsg(spResultMsg{reqID: 7, err: errors.New("provider boom")})
	got := out.(Model)
	if got.spState == nil {
		t.Error("lỗi phải giữ modal để đọc, không đóng")
	}
	if !strings.Contains(got.spState.errMsg, "provider boom") {
		t.Errorf("errMsg sai: %q", got.spState.errMsg)
	}
	if !strings.Contains(got.spState.view(120, 30), "provider boom") {
		t.Error("view phải hiện lỗi")
	}
}

// 19-20. Esc đóng modal ngay (kể cả đang loading).
func TestSPEscDongNgay(t *testing.T) {
	m := newTestModel(120, 30)
	m.spState = newStoryPartnerState(120, 30, 7, "Hỏi?", "vi")
	out, _, handled := m.handleSPKey(tea.KeyMsg{Type: tea.KeyEsc})
	if !handled {
		t.Fatal("modal phải chặn Esc")
	}
	if out.(Model).spState != nil {
		t.Error("Esc phải đóng modal ngay, không đợi model")
	}
}

// 21. Stale result (reqID khác / modal đã đóng) bị bỏ.
func TestSPStaleResultBo(t *testing.T) {
	m := newTestModel(120, 30)
	m.spState = newStoryPartnerState(120, 30, 9, "Mới?", "vi")
	out, _ := m.handleSPResultMsg(spResultMsg{reqID: 7, result: sp.Result{Answer: "cũ"}})
	got := out.(Model)
	if got.spState.answer != "" || !got.spState.loading {
		t.Error("result cũ phải bị bỏ, không ghi đè request mới")
	}

	// Modal đã đóng (Esc) rồi result mới về → bỏ, không mở lại.
	m2 := newTestModel(120, 30)
	out2, _ := m2.handleSPResultMsg(spResultMsg{reqID: 7, result: sp.Result{Answer: "muộn"}})
	if out2.(Model).spState != nil {
		t.Error("result muộn không được mở lại modal")
	}
}

// 22. Request mới supersede: reqID tăng, modal cũ thay bằng modal mới.
func TestSPRequestMoiSupersede(t *testing.T) {
	m := newTestModel(120, 30)
	m.spSeq = 7
	m.spState = newStoryPartnerState(120, 30, 7, "Cũ?", "vi")
	m.spSeq++
	m.spState = newStoryPartnerState(120, 30, m.spSeq, "Mới?", "vi")
	if m.spState.reqID != 8 || m.spState.question != "Mới?" {
		t.Errorf("modal phải là request mới: %+v", m.spState)
	}
	// Result của 7 về sau → bỏ.
	out, _ := m.handleSPResultMsg(spResultMsg{reqID: 7, result: sp.Result{Answer: "cũ"}})
	if out.(Model).spState.answer != "" {
		t.Error("result cũ không được ghi đè request mới")
	}
}

// 14. Request language capture từ UI hiện tại.
func TestSPCaptureUILang(t *testing.T) {
	defer i18n.SetLanguage(i18n.LangVietnamese)
	i18n.SetLanguage(i18n.LangChinese)
	if i18n.Language() != "zh" {
		t.Fatal("setup UI zh thất bại")
	}
	// Capture điểm gọi: runSPCommand đọc i18n.Language() lúc mở modal.
	// Ở đây kiểm tra state giữ đúng lang được truyền.
	s := newStoryPartnerState(120, 30, 1, "Q?", i18n.Language())
	if s.lang != "zh" {
		t.Errorf("lang = %q, mong zh", s.lang)
	}
}
