package i18n

import (
	"strings"
	"testing"
)

// TestCatalogDoiDayBu covers every language against the Vietnamese source keys.
// The catalogs are map literals, so a missing key is a runtime fallback, not a
// compile error. This test is what turns "silently showed Vietnamese" into a
// failing build.
func TestCatalogDoiDayBu(t *testing.T) {
	// Nguồn chân lý: lấy từ chính catalog tiếng Anh rồi đối chiếu tiếng Trung,
	// hai ngôn ngữ phải phủ đúng cùng một tập khoá.
	en := enCatalog
	zh := zhCatalog

	for k := range en {
		if _, ok := zh[k]; !ok {
			t.Errorf("catalog_zh thieu khoa %q", k)
		}
	}
	for k := range zh {
		if _, ok := en[k]; !ok {
			t.Errorf("catalog_en thieu khoa %q", k)
		}
	}

	for lang, table := range map[string]map[string]string{LangEnglish: en, LangChinese: zh} {
		if len(table) == 0 {
			t.Fatalf("catalog %s rong", lang)
		}
		for k, v := range table {
			if strings.TrimSpace(v) == "" {
				t.Errorf("catalog %s: %q -> ban dich rong", lang, k)
			}
		}
	}
}

// TestDongTuKhop là test quan trọng nhất. Chuỗi TUI đi thẳng làm format string
// cho fmt.Sprintf/Errorf, nên bản dịch phải giữ nguyên động từ định dạng (%s, %q,
// %d, %v, %w...). Lệch là lúc chạy sẽ in ra %!s(MISSING) hoặc lệch tham số.
func TestDongTuKhop(t *testing.T) {
	for _, lang := range []string{LangEnglish, LangChinese} {
		table := catalog[lang]
		for k, v := range table {
			got, want := verbs(v), verbs(k)
			if got != want {
				t.Errorf("%s: %q\n  nguon  co %q\n  dich  co %q", lang, k, want, got)
			}
		}
	}
}

// TestTFallbackKhiLechDongTu chứng minh chốt an toàn có tác dụng: dịch sai động từ
// thì phải rơi về chuỗi nguồn chứ không in chuỗi hỏng.
func TestTFallbackKhiLechDongTu(t *testing.T) {
	SetLanguage(LangEnglish)
	defer SetLanguage(LangVietnamese)

	// "Số lượng" -> tiếng Anh trong catalog là "Count", không có %d. Dựng một cặp
	// giả bằng cách gọi trực tiếp lớp trợ giúp trên bản dịch thực tế.
	src := "provider %q chưa có model nào"
	if got := verbs(T(src)); got != verbs(src) {
		t.Errorf("chuoi dang chay co %q phai giu nguyen %q, doc duoc %q",
			verbs(src), verbs(src), got)
	}
	if got := Tf("danh %s rong", "x"); got != "danh x rong" {
		t.Errorf("Tf fallback = %q, muon %q", got, "danh x rong")
	}
}

func TestSetLanguageVaFallback(t *testing.T) {
	defer SetLanguage(LangVietnamese)

	cases := map[string]string{
		LangEnglish:    LangEnglish,
		"EN":           LangEnglish, // hoa chuoi
		"  en  ":       LangEnglish, // khoang trang
		LangChinese:    LangChinese,
		"ZH":           LangChinese,
		LangVietnamese: LangVietnamese,
		"":             LangVietnamese, // rong -> tieng Viet
		"fr":           LangVietnamese, // khong ho tro -> tieng Viet
	}
	for in, want := range cases {
		SetLanguage(in)
		if got := Language(); got != want {
			t.Errorf("SetLanguage(%q) = %q, muon %q", in, got, want)
		}
	}
}

func TestTTrVeNguyenChuoiKhiThieuBanDich(t *testing.T) {
	SetLanguage(LangEnglish)
	defer SetLanguage(LangVietnamese)

	const s = "chuỗi này cố tình không có trong catalog"
	if got := T(s); got != s {
		t.Errorf("T(chua co ban dich) = %q, muon %q", got, s)
	}
}

func TestAvailable(t *testing.T) {
	want := []string{LangVietnamese, LangEnglish, LangChinese}
	if len(Available) != len(want) {
		t.Fatalf("Available = %v, muon %v", Available, want)
	}
	for i := range want {
		if Available[i] != want[i] {
			t.Fatalf("Available = %v, muon %v", Available, want)
		}
	}
}
