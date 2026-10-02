// Package i18n cung cấp tra cứu chuỗi hiển thị theo ngôn ngữ cho TUI.
//
// Thiết kế: chuỗi tiếng Việt viết thẳng trong code làm "nguồn gốc" (nguồn sự
// thật), lớp này dịch sang ngôn ngữ đang chọn khi có bản dịch, nếu không thì trả
// nguyên bản. Nhờ vậy:
//
//   - code vẫn đọc được bằng mắt thường (chuỗi nằm ngay tại chỗ dùng);
//   - thiếu bản dịch không làm hỏng chương trình, chỉ rơi về tiếng Việt;
//   - thêm ngôn ngữ mới = thêm một bảng dịch, không phải sửa lại 22 file TUI.
//
// Ngôn ngữ được đặt một lần lúc khởi động qua SetLanguage.
package i18n

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"golang.org/x/text/language"
)

// Mã ngôn ngữ hỗ trợ.
const (
	LangVietnamese = "vi"
	LangEnglish    = "en"
	LangChinese    = "zh"
)

// supportedLocale là thứ tự ưu tiên cho Matcher: vi trước vì là fallback mặc định.
var supportedLocale = []language.Tag{
	language.Vietnamese,
	language.English,
	language.Chinese,
}

var localeMatcher = language.NewMatcher(supportedLocale)

// tagToCode đổi language.Tag về mã nội bộ. So base language chứ không so tag
// nguyên văn: Matcher trả tag mở rộng vùng ("en-u-rg-gbzzzz") chứ không trả đúng
// "en", nên so nguyên tag sẽ rơi default oan.
func tagToCode(t language.Tag) string {
	base, _ := t.Base()
	switch base.String() {
	case "en":
		return LangEnglish
	case "zh":
		return LangChinese
	default:
		return LangVietnamese
	}
}

// MatchLocale chuẩn hoá bất kỳ tag nào (en, EN, en-US, zh-CN, ...) về mã nội bộ.
// Thay switch thủ công: "en-US" trước đây rơi về vi oan, giờ về en đúng.
// Tag hỏng hoặc ngôn ngữ không hỗ trợ ("xx", "fr") thì về vi — an toàn cho
// SetLanguage và config vì luôn có giá trị hợp lệ.
func MatchLocale(tag string) string {
	tag = strings.TrimSpace(tag)
	if tag == "" {
		return LangVietnamese
	}
	parsed, err := language.Parse(tag)
	if err != nil {
		// Tag hỏng hoàn toàn ("xx", "123") thì rơi về mặc định, không lỗi.
		return LangVietnamese
	}
	matched, _, _ := localeMatcher.Match(parsed)
	return tagToCode(matched)
}

// SupportedCode trả mã nội bộ chỉ khi base language được hỗ trợ (vi/en/zh).
// Khác MatchLocale: tag lạ ("fr", "ja") trả ("", false) thay vì rơi về vi.
// Dùng cho tra cứu theo locale (tên lệnh hiển thị): locale không hỗ trợ thì
// không có tên, phải rơi về tên chuẩn chứ không lấy tên tiếng Việt oan.
func SupportedCode(tag string) (string, bool) {
	parsed, err := language.Parse(strings.TrimSpace(tag))
	if err != nil {
		return "", false
	}
	base, _ := parsed.Base()
	switch base.String() {
	case "vi":
		return LangVietnamese, true
	case "en":
		return LangEnglish, true
	case "zh":
		return LangChinese, true
	}
	return "", false
}

var (
	mu      sync.RWMutex
	current = LangVietnamese
)

// catalog[lang][nguồn tiếng Việt] = bản dịch.
//
// diagCatalogEn/Zh được trộn vào đây thay vì khai báo riêng, để mọi nơi đọc
// catalog (T(), Has(), test) thấy một bảng duy nhất — tách riêng chỉ là cách
// chia file cho dễ đọc, không phải hai nguồn sự thật.
var catalog = map[string]map[string]string{
	LangEnglish: mergeCatalog(enCatalog, diagCatalogEn),
	LangChinese: mergeCatalog(zhCatalog, diagCatalogZh),
}

// mergeCatalog gộp nhiều bảng thành một. Khoá trùng thì bảng sau ghi đè bảng trước,
// nên gọi theo thứ tự từ chung đến chuyên biệt.
func mergeCatalog(parts ...map[string]string) map[string]string {
	out := make(map[string]string)
	for _, p := range parts {
		for k, v := range p {
			out[k] = v
		}
	}
	return out
}

// SetLanguage đặt ngôn ngữ hiện tại. Chuẩn hoá qua Matcher nên "EN", "en-US",
// "zh-CN" đều về đúng mã; mã lạ hoặc rỗng rơi về tiếng Việt.
// Gọi một lần lúc khởi động, trước khi dựng TUI.
func SetLanguage(lang string) {
	mu.Lock()
	defer mu.Unlock()
	current = MatchLocale(lang)
}

// Language trả về mã ngôn ngữ đang dùng.
func Language() string {
	mu.RLock()
	defer mu.RUnlock()
	return current
}

// T dịch một chuỗi nguồn tiếng Việt sang ngôn ngữ đang chọn.
// Thiếu bản dịch (hoặc đang ở chính tiếng Việt) thì trả nguyên chuỗi.
//
// Nếu chuỗi chứa động từ định dạng (%s, %d, %q, %v...) thì bản dịch chỉ được
// dùng khi giữ nguyên động từ nguồn. Lý do: rất nhiều chuỗi ở TUI được đưa thẳng
// làm format string cho fmt.Sprintf/Errorf, mà bản dịch là dữ liệu lúc chạy. Lệch
// động từ là in ra %!s(MISSING) hoặc lệch tham số, nên rơi về chuỗi nguồn cho chắc.
func T(s string) string {
	mu.RLock()
	lang, table := current, catalog[current]
	mu.RUnlock()
	if lang == LangVietnamese || table == nil {
		return s
	}
	v, ok := table[s]
	if !ok || v == "" {
		return s
	}
	if verbs(v) != verbs(s) {
		return s
	}
	return v
}

// Tf là biến thể có tham số: dịch rồi mới định dạng.
func Tf(format string, args ...any) string {
	return fmt.Sprintf(T(format), args...)
}

// Errf là Tf nhưng trả về error, dùng cho thông báo lỗi đã dịch.
func Errf(format string, args ...any) error {
	return errors.New(Tf(format, args...))
}

// verbPat khớp một động từ định dạng, ví dụ %s, %-10.2f, %q.
var verbPat = regexp.MustCompile(`%[-+# 0]*[0-9]*(?:\.[0-9]+)?[a-zA-Z%]`)

// verbs gom các động từ định dạng theo thứ tự, bỏ qua %% (ký tự trơn).
func verbs(s string) string {
	var b strings.Builder
	for _, m := range verbPat.FindAllString(s, -1) {
		if !strings.HasSuffix(m, "%") {
			b.WriteString(m)
		}
	}
	return b.String()
}

// Has cho biết chuỗi nguồn đã có bản dịch sang ngôn ngữ đang chọn hay chưa.
// Dùng trong test để phát hiện bản dịch thiếu.
func Has(s string) bool {
	mu.RLock()
	defer mu.RUnlock()
	_, ok := catalog[current][s]
	return ok
}

// Available là danh sách ngôn ngữ có thể chọn, theo thứ tự hiển thị.
var Available = []string{LangVietnamese, LangEnglish, LangChinese}
