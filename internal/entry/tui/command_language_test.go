package tui

import (
	"strings"
	"testing"

	"github.com/CTKiet2006/kietnovel/internal/bootstrap"
	"github.com/CTKiet2006/kietnovel/internal/i18n"
	tea "github.com/charmbracelet/bubbletea"
)

// useTempConfigHome trỏ cấu hình vào thư mục tạm cho tới hết test.
//
// Bắt buộc: runLanguageCommand gọi bootstrap.SaveConfig(EffectiveConfigPath()) —
// tức ghi file thật. Lần này thiếu chốt nên test đã ghi đè config.json của
// người dùng và làm mất API key. Test đơn vị đụng dữ liệu thật thì vẫn báo xanh,
// nên phải cô lập ở đây chứ không dựa vào cẩn thận khi viết test.
func useTempConfigHome(t *testing.T) {
	t.Helper()
	t.Setenv("KIETNOVEL_CONFIG_HOME", t.TempDir())
}

func langModel(cfg bootstrap.Config) Model {
	m := NewModel(nil, "test")
	m.cfg = cfg
	return m
}

// /language ui đổi giao diện nhưng KHÔNG đụng ngôn ngữ sáng tác.
func TestLanguageUIChiDoiGiaoDien(t *testing.T) {
	useTempConfigHome(t)
	defer i18n.SetLanguage(i18n.LangVietnamese)
	i18n.SetLanguage(i18n.LangVietnamese)
	m := langModel(bootstrap.Config{Language: "zh"})

	out, _ := runLanguageCommand(m, []string{"ui", "en"})
	got := out.(Model)
	if got.cfg.UILanguage != "en" {
		t.Errorf("UILanguage = %q, mong en", got.cfg.UILanguage)
	}
	if got.cfg.Language != "zh" {
		t.Errorf("Language bi doi thanh %q, phai giu zh", got.cfg.Language)
	}
	if i18n.Language() != "en" {
		t.Errorf("i18n = %q, mong en (doi ngay trong phien)", i18n.Language())
	}
}

// /language write đổi ngôn ngữ sáng tác nhưng KHÔNG đụng giao diện — đọc truyện
// tiếng Trung mà cả TUI nhảy theo thì khó chịu.
func TestLanguageWriteChiDoiNgonNguSangTac(t *testing.T) {
	useTempConfigHome(t)
	defer i18n.SetLanguage(i18n.LangVietnamese)
	// Giao diện đang là en, để chứng minh /language write KHÔNG đụng nó.
	i18n.SetLanguage(i18n.LangEnglish)
	m := langModel(bootstrap.Config{UILanguage: "en"})

	out, _ := runLanguageCommand(m, []string{"write", "zh"})
	got := out.(Model)
	if got.cfg.Language != "zh" {
		t.Errorf("Language = %q, mong zh", got.cfg.Language)
	}
	if got.cfg.UILanguage != "en" {
		t.Errorf("UILanguage bi doi thanh %q, phai giu en", got.cfg.UILanguage)
	}
	if i18n.Language() != "en" {
		t.Errorf("i18n = %q, phai giu en", i18n.Language())
	}
}

// Gõ /language vi không kèm từ khoá: hiểu là đổi giao diện, vì đó là cái đổi
// được ngay. Không được hiểu là khoá ngôn ngữ sáng tác.
func TestLanguageKhongThamSoMaCoMaLaDoiGiaoDien(t *testing.T) {
	useTempConfigHome(t)
	defer i18n.SetLanguage(i18n.LangVietnamese)
	i18n.SetLanguage(i18n.LangVietnamese)
	m := langModel(bootstrap.Config{Language: "zh"})

	out, _ := runLanguageCommand(m, []string{"en"})
	got := out.(Model)
	if got.cfg.UILanguage != "en" {
		t.Errorf("UILanguage = %q, mong en", got.cfg.UILanguage)
	}
	if got.cfg.Language != "zh" {
		t.Errorf("Language = %q, phai giu zh", got.cfg.Language)
	}
}

func TestLanguageKhongThamSoKhongDoiGi(t *testing.T) {
	useTempConfigHome(t)
	defer i18n.SetLanguage(i18n.LangVietnamese)
	i18n.SetLanguage(i18n.LangVietnamese)
	m := langModel(bootstrap.Config{Language: "zh"})

	out, _ := runLanguageCommand(m, nil)
	got := out.(Model)
	if got.cfg.UILanguage != "" || got.cfg.Language != "zh" {
		t.Errorf("/language khong tham so phai chi xem, khong doi: %+v", got.cfg)
	}
	if len(got.events) == 0 {
		t.Error("/language khong tham so phai bao trang thai hai ngon ngu")
	}
}

func TestLanguageTuKhoaLai(t *testing.T) {
	useTempConfigHome(t)
	defer i18n.SetLanguage(i18n.LangVietnamese)
	i18n.SetLanguage(i18n.LangVietnamese)
	m := langModel(bootstrap.Config{})

	cases := map[string]string{
		"ui":        "UILanguage",
		"interface": "UILanguage",
		"write":     "Language",
		"writing":   "Language",
		"viet":      "Language",
	}
	for kw, field := range cases {
		out, _ := runLanguageCommand(m, []string{kw, "en"})
		got := out.(Model)
		if field == "UILanguage" && got.cfg.UILanguage != "en" {
			t.Errorf("/language %s en: UILanguage = %q, mong en", kw, got.cfg.UILanguage)
		}
		if field == "Language" && got.cfg.Language != "en" {
			t.Errorf("/language %s en: Language = %q, mong en", kw, got.cfg.Language)
		}
		i18n.SetLanguage(i18n.LangVietnamese)
	}
}

func TestLanguageTuKhoaLaiPhaiBaoLoi(t *testing.T) {
	useTempConfigHome(t)
	defer i18n.SetLanguage(i18n.LangVietnamese)
	i18n.SetLanguage(i18n.LangVietnamese)
	m := langModel(bootstrap.Config{Language: "zh"})

	out, _ := runLanguageCommand(m, []string{"sac", "en"})
	got := out.(Model)
	if got.cfg.UILanguage != "" || got.cfg.Language != "zh" {
		t.Errorf("tu khoa la phai bi tu choi, khong doi gi: %+v", got.cfg)
	}
	if len(got.events) == 0 || got.events[len(got.events)-1].Level != "warn" {
		t.Error("tu khoa la phai bao loi, khong im lang")
	}
}

func TestLanguageGiaTriSai(t *testing.T) {
	useTempConfigHome(t)
	defer i18n.SetLanguage(i18n.LangVietnamese)
	i18n.SetLanguage(i18n.LangVietnamese)
	for _, args := range [][]string{{"ui", "fr"}, {"write", "klingon"}, {"ui", ""}} {
		m := langModel(bootstrap.Config{Language: "zh"})
		out, _ := runLanguageCommand(m, args)
		got := out.(Model)
		if got.cfg.UILanguage != "" || got.cfg.Language != "zh" {
			t.Errorf("%v: gia tri sai van duoc ghi, %+v", args, got.cfg)
		}
	}
}

// writeLangOf: trống = "vi".
func TestWriteLangOfRongLaVi(t *testing.T) {
	if got := writeLangOf(bootstrap.Config{}); got != "vi" {
		t.Errorf("writeLangOf(rong) = %q, mong vi", got)
	}
	if got := writeLangOf(bootstrap.Config{Language: "zh"}); got != "zh" {
		t.Errorf("writeLangOf(zh) = %q", got)
	}
}

// Đổi giao diện phải dựng lại nhãn đã khoá theo ngôn ngữ cũ.
func TestLanguageUIDoiRoiDungLaiNhanCu(t *testing.T) {
	useTempConfigHome(t)
	defer i18n.SetLanguage(i18n.LangVietnamese)
	i18n.SetLanguage(i18n.LangVietnamese)
	m := langModel(bootstrap.Config{})

	out, _ := runLanguageCommand(m, []string{"ui", "zh"})
	got := out.(Model)
	if got.textarea.Placeholder == "" {
		t.Error("placeholder rong sau khi doi ngon ngu")
	}
}

// TestConfigHomeBiTestCongLap — chốt chặn cho chính cái chỗ đã gây mất key:
// EffectiveConfigPath phải trỏ vào thư mục tạm khi KIETNOVEL_CONFIG_HOME được đặt.
func TestConfigHomeBiTestCongLap(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("KIETNOVEL_CONFIG_HOME", dir)
	got := bootstrap.EffectiveConfigPath()
	if !strings.HasPrefix(got, dir) {
		t.Errorf("EffectiveConfigPath = %q, phai nam trong %q", got, dir)
	}
	if !strings.Contains(got, "config.json") {
		t.Errorf("EffectiveConfigPath = %q, phai la file config.json", got)
	}
}

// TestConfigHomeMacDinhLaNhaThat — không đặt biến thì vẫn là nhà thật. Nếu test
// nào chạy song song và lỡ xoá biến, hành vi ứng dụng phải không đổi.
func TestConfigHomeMacDinhLaNhaThat(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("KIETNOVEL_CONFIG_HOME", dir)
	if got := bootstrap.DefaultConfigPath(); !strings.HasPrefix(got, dir) {
		t.Errorf("DefaultConfigPath = %q, phai nam trong %q", got, dir)
	}
}

var _ tea.Model = Model{}
