package host

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// cjk bắt ký tự Hán. Dùng để phát hiện chuỗi hiển thị còn sót tiếng Trung.
// Chuỗi đôi, không phải raw string: regexp không hiểu \u trong raw string.
var cjk = regexp.MustCompile("[\u3000-\u303f\u4e00-\u9fff\uff00-\uffef]")

// displayFuncs là các hàm dựng CHUỖI HIỂN THỊ đưa thẳng ra TUI. Chúng nằm ở
// host, không đi qua i18n (chỉ package tui mới import i18n), nên TUI in ra nguyên
// văn. Nếu chuỗi ở đây viết bằng Trung thì người dùng chọn tiếng Việt vẫn thấy
// Hán tự — đúng lỗi đã gặp: 恢复 và 思考中 lọt ra giao diện.
//
// Không quét toàn bộ package: host còn rất nhiều thông điệp lỗi tiếng Trung trả
// về cho tool của LLM, đó là chủ đích, không phải lỗi.
var displayFuncs = map[string]bool{
	"describeResume":         true,
	"describeArcEndLabel":    true,
	"resumeLabel":            true,
	"retryPrefix":            true,
	"handleThinkingProgress": true,
	"handleSubagentDelta":    true,
	"handleContextProgress":  true,
}

// TestChuoiHienThiKhongConHanTu chặn tái phát lỗi "sót chữ Trung trong UI tiếng Việt".
func TestChuoiHienThiKhongConHanTu(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	found := map[string]bool{}

	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || !displayFuncs[fn.Name.Name] {
				continue
			}
			found[fn.Name.Name] = true
			ast.Inspect(fn, func(n ast.Node) bool {
				lit, ok := n.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true
				}
				val, err := strconv.Unquote(lit.Value)
				if err != nil {
					return true
				}
				if cjk.MatchString(val) {
					t.Errorf("%s: hàm %s còn chuỗi hiển thị bằng Trung: %q\n"+
						"  → chuỗi này hiện thẳng ra TUI, tiếng Việt sẽ lộ Hán tự. "+
						"Sửa thành tiếng Việt (nguồn sự thật của i18n).",
						fset.Position(lit.Pos()), fn.Name.Name, val)
				}
				return true
			})
		}
	}

	// Đòi tìm đủ mọi hàm liệt kê: nếu ai đó đổi tên hoặc xoá hàm, canh gác phải
	// báo đỏ chứ không lặng lẽ thu hẹp phạm vi rồi báo "pass".
	for name := range displayFuncs {
		if !found[name] {
			t.Errorf("không tìm thấy hàm %s trong package host — cập nhật displayFuncs "+
				"nếu nó đã bị đổi tên, nếu không canh gác này đang quét thiếu", name)
		}
	}
}
