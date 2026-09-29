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
)

// Mã ngôn ngữ hỗ trợ.
const (
	LangVietnamese = "vi"
	LangEnglish    = "en"
	LangChinese    = "zh"
)

var (
	mu      sync.RWMutex
	current = LangVietnamese
)

// catalog[lang][nguồn tiếng Việt] = bản dịch.
var catalog = map[string]map[string]string{
	LangEnglish: enCatalog,
	LangChinese: zhCatalog,
}

// SetLanguage đặt ngôn ngữ hiện tại. Mã lạ hoặc rỗng rơi về tiếng Việt.
// Gọi một lần lúc khởi động, trước khi dựng TUI.
func SetLanguage(lang string) {
	mu.Lock()
	defer mu.Unlock()
	switch strings.ToLower(strings.TrimSpace(lang)) {
	case LangEnglish:
		current = LangEnglish
	case LangChinese:
		current = LangChinese
	default:
		current = LangVietnamese
	}
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
