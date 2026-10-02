package diag

import (
	"os"
	"strings"
	"testing"
	"unicode"
)

// TestKhongConChuoiTiengTrongMaNguon: báo cáo chẩn đoán hiển thị thẳng cho người
// dùng đọc, nên mọi chuỗi trong package này phải đi qua i18n. Trước đây toàn bộ
// 90 chuỗi viết cứng bằng tiếng Trung, nên truyện tiếng Việt thì báo cáo chẩn
// đoán ra tiếng Trung.
//
// Test này bắt cả trường hợp sửa nửa — dịch được một nửa, sót một nửa, hoặc
// dịch câu Việt mà lỡ tay để lọt ký tự Hán.
func TestKhongConChuoiTiengTrongMaNguon(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(data), "\n") {
			// Bỏ qua dòng chú thích: comment tiếng Trung không hiện ra UI.
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "//") {
				continue
			}
			for _, r := range line {
				if unicode.Is(unicode.Han, r) {
					t.Errorf("%s:%d còn ký tự Hán %q trong chuỗi hiển thị: %s",
						name, i+1, r, trimmed)
					break
				}
			}
		}
	}
}
