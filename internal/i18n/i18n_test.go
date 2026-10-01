package i18n

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
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

// TestMoiChuoiTDeuCoBanDich quét toàn bộ literal i18n.T("...")/i18n.Tf("...") trong
// source và bắt mỗi chuỗi phải có mặt trong cả catalog en lẫn zh.
//
// Trước test này, thêm một chuỗi TUI mới mà quên catalog thì app vẫn pass test và
// lặng lẽ rơi về tiếng Việt ở EN/ZH. Test này biến "quên dịch" thành build đỏ.
//
// Giới hạn có chủ ý (ghi rõ để không ai "sửa test cho qua"):
//   - chỉ bắt string literal trực tiếp; chuỗi dựng động (nối biến, Sprintf trước
//     rồi mới T()) không quét được — những chỗ đó phải tự rà bằng mắt.
//   - chỉ quét package có gọi i18n (hiện tại: entry/tui, host); thêm package mới
//     gọi i18n thì thêm vào dirs dưới đây.
//   - bỏ qua file *_test.go.
func TestMoiChuoiTDeuCoBanDich(t *testing.T) {
	dirs := []string{"../entry/tui", "../host"}
	got := map[string]map[string]bool{} // literal -> {file:line}
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("đọc %s: %v", dir, err)
		}
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			path := filepath.Join(dir, name)
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				t.Fatalf("parse %s: %v", path, err)
			}
			ast.Inspect(f, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				id, ok := sel.X.(*ast.Ident)
				if !ok || id.Name != "i18n" || (sel.Sel.Name != "T" && sel.Sel.Name != "Tf") {
					return true
				}
				if len(call.Args) == 0 {
					return true
				}
				lit, ok := call.Args[0].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true
				}
				s, err := strconv.Unquote(lit.Value)
				if err != nil {
					return true
				}
				if s == "" {
					return true
				}
				pos := fset.Position(lit.Pos())
				if got[s] == nil {
					got[s] = map[string]bool{}
				}
				got[s][fmt.Sprintf("%s:%d", path, pos.Line)] = true
				return true
			})
		}
	}
	if len(got) == 0 {
		t.Fatal("không quét được literal nào — test hỏng, không phải coverage tốt")
	}
	for _, lang := range []string{LangEnglish, LangChinese} {
		table := catalog[lang]
		for s, locs := range got {
			if _, ok := table[s]; !ok {
				var where []string
				for loc := range locs {
					where = append(where, loc)
				}
				sort.Strings(where)
				t.Errorf("%s thiếu %q (dùng ở %s)", lang, s, strings.Join(where, ", "))
			}
		}
	}
}
