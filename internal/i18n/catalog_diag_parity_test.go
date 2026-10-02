package i18n

import "testing"

// TestCatalogDoiKhopNhau: khoá của catalog en và catalog zh phải giống hệt nhau
// và không được rỗng.
//
// Vì sao cần: i18n.T coi giá trị RỖNG y như khoá không tồn tại và rơi về tiếng
// Việt — nghĩa là một mục rỗng là một bản dịch chết lặng lẽ. Test i18n gốc chỉ
// kiểm khoá CÓ mặt, nên mục rỗng vẫn xanh. Test này chặn cả hai kiểu sai:
// khoá lệch giữa hai bảng, và khoá có nhưng dịch rỗng.
func TestCatalogDoiKhopNhau(t *testing.T) {
	for lang, table := range map[string]map[string]string{
		LangEnglish: diagCatalogEn,
		LangChinese: diagCatalogZh,
	} {
		if len(table) == 0 {
			t.Fatalf("catalog %s rỗng, có thể file chưa được nạp", lang)
		}
		for k, v := range table {
			if v == "" {
				t.Errorf("catalog %s: khoá %q có giá trị rỗng — sẽ rơi về tiếng Việt mà không ai báo", lang, k)
			}
		}
	}
	for k := range diagCatalogEn {
		if _, ok := diagCatalogZh[k]; !ok {
			t.Errorf("catalog zh thiếu khoá có trong en: %q", k)
		}
	}
	for k := range diagCatalogZh {
		if _, ok := diagCatalogEn[k]; !ok {
			t.Errorf("catalog en thiếu khoá có trong zh: %q", k)
		}
	}
}
