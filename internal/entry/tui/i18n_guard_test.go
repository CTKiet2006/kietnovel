package tui

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CTKiet2006/kietnovel/internal/i18n"
)

// TestKhongCoBienPackageGoiI18nT chặn một lớp bug rất dễ tái diễn: đặt i18n.T
// trong biến mức package. Biến package được khởi tạo lúc import, tức là TRƯỚC
// SetLanguage, nên nhãn sẽ bị khoá cứng tiếng Việt và /language không bao giờ
// đổi được. Bắt buộc dùng hàm dựng lại lúc render.
func TestKhongCoBienPackageGoiI18nT(t *testing.T) {
	dir := "."
	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		t.Fatal(err)
	}

	fset := token.NewFileSet()
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		for _, decl := range f.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.VAR {
				continue
			}
			for _, spec := range gd.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				if callsI18nT(vs.Values) {
					name := "?"
					if len(vs.Names) > 0 {
						name = vs.Names[0].Name
					}
					t.Errorf("%s: bien package %s chua i18n.T — se bi khoa tieng Viet "+
						"vi bien duoc khoi tao truoc SetLanguage. Hay doi sang ham.",
						fset.Position(vs.Pos()), name)
				}
			}
		}
	}
}

// callsI18nT báo true nếu trong các biểu thức khởi tạo có lệnh gọi i18n.T hoặc i18n.Tf.
func callsI18nT(exprs []ast.Expr) bool {
	found := false
	ast.Inspect(ast.NewIdent(""), func(ast.Node) bool { return true }) // no-op giữ cho rõ ý định
	for _, e := range exprs {
		ast.Inspect(e, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok || pkg.Name != "i18n" {
				return true
			}
			switch sel.Sel.Name {
			case "T", "Tf", "Errf":
				found = true
			}
			return true
		})
	}
	return found
}

// TestCacNhanDaDichTheoNgonNgu khoá lại hành vi quan trọng nhất: đổi ngôn ngữ
// phải đổi cả nhãn nằm trong bảng, không chỉ chuỗi rời.
func TestCacNhanDaDichTheoNgonNgu(t *testing.T) {
	defer i18n.SetLanguage(i18n.LangVietnamese)

	i18n.SetLanguage(i18n.LangVietnamese)
	vi := statusDisplay()["READY"].label
	i18n.SetLanguage(i18n.LangEnglish)
	en := statusDisplay()["READY"].label
	i18n.SetLanguage(i18n.LangChinese)
	zh := statusDisplay()["READY"].label

	if vi == en || en == zh || vi == zh {
		t.Fatalf("nhan READY khong doi theo ngon ngu: vi=%q en=%q zh=%q", vi, en, zh)
	}
	if len(modelRoleOptions()) == 0 || len(allThinkingOptions()) == 0 {
		t.Fatal("bang lua chon rong")
	}
}
