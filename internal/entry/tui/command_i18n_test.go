package tui

import (
	"strings"
	"testing"

	"github.com/CTKiet2006/kietnovel/internal/i18n"
)

// Bảng subcommand cho /sp (Phase 3 dùng). Định nghĩa ở đây để chứng minh
// resolveSubcommand xuyên ngôn ngữ trước khi lệnh tồn tại.
var spAskTable = map[string][]string{
	"ask": {"hỏi", "ask", "问"},
}

func TestCommandIDBatBien(t *testing.T) {
	r := commandRegistryInstance()
	for _, spec := range r.specs {
		if spec.CommandID() == "" {
			t.Errorf("spec %+v thiếu ID", spec.Name)
		}
	}
	// read/books/language/new/delete phải có ID tường minh (không rơi về Name).
	for _, want := range []string{"read", "books", "language", "new", "delete"} {
		spec, ok := r.Find(want)
		if !ok {
			t.Fatalf("không tìm thấy %q", want)
		}
		if spec.CommandID() != want {
			t.Errorf("CommandID = %q, mong %q", spec.CommandID(), want)
		}
	}
}

func TestMatchDaNgonNgu(t *testing.T) {
	r := commandRegistryInstance()
	cases := []struct{ input, wantID string }{
		{"read", "read"},
		{"đọc", "read"},
		{"阅读", "read"},
		{"doc", "read"}, // alias cũ không dấu vẫn chạy
		{"books", "books"},
		{"truyện", "books"},
		{"书库", "books"},
		{"truyen", "books"}, // alias cũ vẫn chạy
		{"language", "language"},
		{"ngônngữ", "language"},
		{"语言", "language"},
		{"ngonngu", "language"}, // alias cũ vẫn chạy
		{"lang", "language"},
	}
	for _, c := range cases {
		spec, ok := r.Find(c.input)
		if !ok {
			t.Errorf("Find(%q) không thấy", c.input)
			continue
		}
		if spec.CommandID() != c.wantID {
			t.Errorf("Find(%q) = %q, mong %q", c.input, spec.CommandID(), c.wantID)
		}
	}
}

func TestDisplayNameTheoLocale(t *testing.T) {
	r := commandRegistryInstance()
	spec, _ := r.Find("read")
	if got := spec.DisplayName("vi"); got != "đọc" {
		t.Errorf("vi: %q", got)
	}
	if got := spec.DisplayName("zh"); got != "阅读" {
		t.Errorf("zh: %q", got)
	}
	if got := spec.DisplayName("en"); got != "read" {
		t.Errorf("en phải rơi về Name: %q", got)
	}
	if got := spec.DisplayName("fr"); got != "read" {
		t.Errorf("ngôn ngữ lạ phải rơi về Name: %q", got)
	}
	// Lệnh new giờ có tên locale vi=[mới]: DisplayName(vi) phải là tên đó,
	// không phải Name.
	plain, _ := r.Find("new")
	if got := plain.DisplayName("vi"); got != "mới" {
		t.Errorf("new/vi: %q", got)
	}
}

func TestPaletteHienTenTheoLocale(t *testing.T) {
	r := commandRegistryInstance()
	byName := func(items []commandPaletteItem, want string) commandPaletteItem {
		for _, it := range items {
			if it.Name == want {
				return it
			}
		}
		return commandPaletteItem{}
	}
	vi := byName(r.PaletteItemsIn("vi"), "đọc")
	if vi.Name == "" {
		t.Fatal("palette vi thiếu /đọc")
	}
	// Gõ tên chuẩn vẫn khớp dù palette hiện tên locale.
	found := false
	for _, a := range vi.Aliases {
		if a == "read" {
			found = true
		}
	}
	if !found {
		t.Errorf("palette vi phải giữ tên chuẩn trong Aliases: %v", vi.Aliases)
	}
	zh := byName(r.PaletteItemsIn("zh"), "阅读")
	if zh.Name == "" {
		t.Fatal("palette zh thiếu /阅读")
	}
	en := byName(r.PaletteItemsIn("en"), "read")
	if en.Name == "" {
		t.Fatal("palette en thiếu /read")
	}
}

func TestResolveSubcommandXuyenNgonNgu(t *testing.T) {
	for _, in := range []string{"hỏi", "HỎI", "ask", "ASK", "问"} {
		mode, ok := resolveSubcommand(in, spAskTable)
		if !ok || mode != "ask" {
			t.Errorf("resolveSubcommand(%q) = %q,%v — phải về ask", in, mode, ok)
		}
	}
	// Từ lạ và rỗng không phải mode nào.
	for _, in := range []string{"", "   ", "soi", "gợi ý", "delete"} {
		if mode, ok := resolveSubcommand(in, spAskTable); ok {
			t.Errorf("resolveSubcommand(%q) = %q — phải không khớp", in, mode)
		}
	}
}

func TestResolveSubcommandBangRong(t *testing.T) {
	if _, ok := resolveSubcommand("hỏi", nil); ok {
		t.Error("bảng rỗng phải không khớp gì")
	}
	if _, ok := resolveSubcommand("hỏi", map[string][]string{}); ok {
		t.Error("bảng rỗng phải không khớp gì")
	}
}

// lookupSubcommand đi qua catalog theo commandID — key là ID chuẩn, không phải
// tiếng Việt. /sp hỏi/ask/问 đều về ask của story_partner.
func TestLookupSubcommandTheoCommandID(t *testing.T) {
	for _, in := range []string{"hỏi", "ask", "问"} {
		mode, ok := lookupSubcommand("story_partner", in)
		if !ok || mode != "ask" {
			t.Errorf("lookupSubcommand(story_partner, %q) = %q,%v", in, mode, ok)
		}
	}
	// Command không có trong catalog → không khớp gì, không panic.
	if _, ok := lookupSubcommand("read", "hỏi"); ok {
		t.Error("read không có subcommand catalog")
	}
	if _, ok := lookupSubcommand("khong-co", "hỏi"); ok {
		t.Error("command lạ phải không khớp")
	}
	// Từ lạ → false để caller mặc định ask (người dùng khỏi nhớ từ).
	if _, ok := lookupSubcommand("story_partner", "Ngọc có nên tha bà Bảy không?"); ok {
		t.Error("câu hỏi thẳng phải không khớp subcommand nào")
	}
}

// MatchLocale: EN/en-US về en, zh-CN về zh, lạ về vi.
func TestMatchLocaleChuanHoa(t *testing.T) {
	cases := map[string]string{
		"vi": "vi", "VI": "vi", "": "vi", "xx": "vi",
		"en": "en", "EN": "en", "en-US": "en", "en-GB": "en",
		"zh": "zh", "ZH": "zh", "zh-CN": "zh", "zh-TW": "zh",
	}
	for in, want := range cases {
		if got := i18n.MatchLocale(in); got != want {
			t.Errorf("MatchLocale(%q) = %q, mong %q", in, got, want)
		}
	}
}

// DisplayName chịu được tag thô ("EN") nhờ chuẩn hoá trong.
func TestDisplayNameChiuTagTho(t *testing.T) {
	spec, _ := commandRegistryInstance().Find("read")
	if got := spec.DisplayName("EN"); got != "read" {
		t.Errorf("EN: %q", got)
	}
}

// Collision: trong cùng locale, hai command không được trùng tên hiển thị,
// và alias không được đè lên command/subcommand khác. Trùng thì Find lặng lẽ
// lấy command đầu tiên — sai khó phát hiện.
func TestKhongCollisionTenTheoLocale(t *testing.T) {
	r := commandRegistryInstance()
	for _, lang := range []string{"vi", "en", "zh"} {
		seen := map[string]string{} // tên thường -> command ID
		claim := func(name, id string) {
			key := strings.ToLower(strings.TrimSpace(name))
			if key == "" {
				return
			}
			if owner, ok := seen[key]; ok && owner != id {
				t.Errorf("locale %s: tên %q trùng giữa %q và %q", lang, name, owner, id)
			} else {
				seen[key] = id
			}
		}
		for _, spec := range r.specs {
			id := spec.CommandID()
			claim(spec.DisplayName(lang), id)
			claim(spec.Name, id)
			for _, a := range spec.Aliases {
				claim(a, id)
			}
			if names, ok := spec.Names[lang]; ok {
				for _, n := range names {
					claim(n, id)
				}
			}
		}
	}
}

// ID phải duy nhất toàn registry.
func TestCommandIDuyNhat(t *testing.T) {
	seen := map[string]bool{}
	for _, spec := range commandRegistryInstance().specs {
		id := spec.CommandID()
		if seen[id] {
			t.Errorf("trùng ID %q", id)
		}
		seen[id] = true
	}
}

// Help phải render tên + cách dùng theo đúng locale hiện tại, không phải chuỗi raw.
// Trước fix, Help luôn hiện "/read" và Usage tiếng Việt ở mọi language.
func TestHelpTheoLocale(t *testing.T) {
	defer i18n.SetLanguage(i18n.LangVietnamese)

	i18n.SetLanguage(i18n.LangChinese)
	zh := renderHelpText(100)
	for _, must := range []string{"/阅读", "/书库", "/语言", "/sp [问]"} {
		if !strings.Contains(zh, must) {
			t.Errorf("help zh thiếu %q", must)
		}
	}
	if strings.Contains(zh, "/đọc") {
		t.Error("help zh không được lẫn tên vi")
	}

	i18n.SetLanguage(i18n.LangEnglish)
	en := renderHelpText(100)
	for _, must := range []string{"/read", "/sp [ask] <question>"} {
		if !strings.Contains(en, must) {
			t.Errorf("help en thiếu %q", must)
		}
	}

	i18n.SetLanguage(i18n.LangVietnamese)
	vi := renderHelpText(100)
	for _, must := range []string{"/đọc", "/truyện", "/sp [hỏi] <câu hỏi>"} {
		if !strings.Contains(vi, must) {
			t.Errorf("help vi thiếu %q", must)
		}
	}
}

// UsageText rơi về Usage khi locale không có bản riêng.
func TestUsageTextFallback(t *testing.T) {
	r := commandRegistryInstance()
	spec, _ := r.Find("sp")
	if got := spec.UsageText("fr"); got != "/sp [hỏi] <câu hỏi>" {
		t.Errorf("fr phải về Usage chuẩn: %q", got)
	}
	if got := spec.UsageText("EN"); got != "/sp [ask] <question>" {
		t.Errorf("EN phải chuẩn hoá về en: %q", got)
	}
}

// retranslate() dựng lại compItems từ PaletteItems() (đọc i18n.Language hiện
// tại) nên cơ chế đã đúng — test khóa hành vi đó.
func TestDoiUIThiTenHienThiDoiNgay(t *testing.T) {
	defer i18n.SetLanguage(i18n.LangVietnamese)
	r := commandRegistryInstance()
	spec, _ := r.Find("read")

	i18n.SetLanguage(i18n.LangEnglish)
	if got := spec.DisplayName(i18n.Language()); got != "read" {
		t.Errorf("UI en: %q", got)
	}
	i18n.SetLanguage(i18n.LangChinese)
	if got := spec.DisplayName(i18n.Language()); got != "阅读" {
		t.Errorf("UI zh: %q", got)
	}
	i18n.SetLanguage(i18n.LangVietnamese)
	if got := spec.DisplayName(i18n.Language()); got != "đọc" {
		t.Errorf("UI vi: %q", got)
	}
}

func TestPaletteMacDinhTheoUILang(t *testing.T) {
	defer i18n.SetLanguage(i18n.LangVietnamese)
	i18n.SetLanguage(i18n.LangVietnamese)
	found := false
	for _, it := range commandRegistryInstance().PaletteItems() {
		if it.Name == "đọc" {
			found = true
		}
	}
	if !found {
		t.Error("UI vi mà palette không hiện /đọc")
	}
}
