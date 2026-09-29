package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/CTKiet2006/kietnovel/internal/host"
	"github.com/CTKiet2006/kietnovel/internal/i18n"
)

// newRenderableModel dựng Model đủ để View() vẽ được, không cần host thật.
func newRenderableModel() Model {
	m := NewModel(nil, "test")
	m.width = 100
	m.height = 34
	m.snapshot = host.UISnapshot{
		IsRunning:      true,
		CompletedCount: 7,
		TotalWordCount: 48213,
		Flow:           "auto",
		Phase:          "write",
		RuntimeState:   "running",
		Agents:         []host.AgentSnapshot{},
	}
	m.applyEvent(host.Event{
		Time: time.Now(), Category: "SYSTEM", Level: "info",
		Summary: "Đã lưu chương 7", Detail: "Tạo file 0007.txt",
	})
	m.updateViewportSize()
	m.resizeTextarea()
	return m
}

// stripANSI bỏ mã màu để log dễ đọc. Không dùng lipgloss.SetColorProfile vì đó là
// trạng thái toàn cục: đổi ở đây sẽ làm các test render khác đổi kết quả.
func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b {
			for i < len(s) && s[i] != 'm' {
				i++
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func TestXemBaNgonNgu(t *testing.T) {
	defer i18n.SetLanguage(i18n.LangVietnamese)

	for _, lang := range []string{i18n.LangVietnamese, i18n.LangEnglish, i18n.LangChinese} {
		i18n.SetLanguage(lang)
		m := newRenderableModel()
		t.Logf("\n================= %s =================\n%s", lang, stripANSI(m.View()))
	}
}

// TestKhongConTiengVietKhiSangEn bat trung vieo chuoi tieng Viet con sot lai.
// Kiem tra phan UI thuong va giup kiem tra chung cho ca ban dich.
func TestKhongConTiengVietKhiSangEn(t *testing.T) {
	defer i18n.SetLanguage(i18n.LangVietnamese)

	i18n.SetLanguage(i18n.LangEnglish)
	m := newRenderableModel()
	view := m.View()

	// Cac cum tieng Viet bi loai khoi UI: tinh nang (feature) da duoc dich, con
	// du lieu lai co the do host/dia de ve.
	leaks := []string{"Đã lưu", "chương", "người", "được", "không", "chưa"}
	var found []string
	for _, w := range leaks {
		if strings.Contains(view, w) {
			found = append(found, w)
		}
	}
	if len(found) > 0 {
		t.Errorf("tieng Viet con sot trong UI tieng Anh: %v\n%s", found, view)
	}
}
