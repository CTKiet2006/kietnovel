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

	"github.com/CTKiet2006/kietnovel/internal/domain"
	"github.com/CTKiet2006/kietnovel/internal/i18n"
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
	"startModelResponse":     true,
	"finishModelResponse":    true,
	"updateModelState":       true,
}

// toolDisplayKeys là mọi tool có tiêu đề khối hiển thị (✻ …). Nhãn này hiện thẳng
// ra khối trong bảng hoạt động, nên phải là nguồn tiếng Việt và phải có bản dịch.
var toolDisplayKeys = []string{
	"✻ Lên kế hoạch", "✻ Trau chuốt", "✻ Ghi chương", "✻ Duyệt",
	"✻ Tóm tắt cung", "✻ Tóm tắt tập", "✻ Thiết lập", "✻ Sửa dàn ý",
	"✻ Đọc chương", "✻ Kiểm tra nhất quán", "✻ Truy vấn ngữ cảnh",
}

// TestKhoaNhanHienThiDaDichHet kiểm nghịch: một nhãn hiển thị mà không có bản
// dịch ở en và zh thì TUI sẽ in ra tiếng Việt. Đó chính là lớp lỗi mà Msg +
// i18n.Tf sinh ra để chặn, nên ở đây phải bắt trước khi nó chạy.
//
// Nhãn lấy từ hằng số/hàm thật nên không thể trôi khỏi bảng dịch một cách âm
// thầm như khi ai đó tự gõ chuỗi mới.
func TestKhoaNhanHienThiDaDichHet(t *testing.T) {
	defer i18n.SetLanguage(i18n.LangVietnamese)

	// Key của các nhãn host gửi ra. Lấy từ chính hàm dựng nhãn để không lệch.
	progress := &domain.Progress{Phase: domain.PhaseWriting, InProgressChapter: 3}
	msgs := []Msg{
		{Key: "Khôi phục: giai đoạn kế hoạch (%s)", Args: []any{progress.Phase}},
		{Key: "Khôi phục: chương %d bị gián đoạn lúc ghi", Args: []any{3}},
		{Key: "%s khôi phục: %d chương chờ xử lý", Args: []any{"Viết lại", 2}},
		{Key: "Khôi phục: gián đoạn lúc duyệt"},
		{Key: "Khôi phục: chương %d đang viết dở", Args: []any{3}},
		{Key: "Khôi phục: tiếp tục từ chương %d", Args: []any{4}},
		{Key: "Khôi phục"},
		{Key: "Khôi phục: chờ duyệt cuối cung (V%d A%d)", Args: []any{1, 2}},
		{Key: "Khôi phục: chờ tạo tóm tắt cung (V%d A%d)", Args: []any{1, 2}},
		{Key: "Khôi phục: chờ tạo tóm tắt tập (V%d)", Args: []any{1}},
		{Key: "Khôi phục: chờ bung cung kế (V%d A%d)", Args: []any{2, 1}},
		{Key: "Khôi phục: chờ quyết định tập kế (V%d cuối)", Args: []any{1}},
		{Key: "Khôi phục việc viết: %s", Args: []any{"Khôi phục"}},
		modelStateWaiting, {Key: "Đang suy nghĩ"}, modelStateReply,
		{Key: "Sinh %s", Args: []any{"read_chapter"}}, modelStateDone,
		{Key: "Thử lại (lần %d): ", Args: []any{2}},
		{Key: "Thử lại (lần %d, %s nữa): ", Args: []any{2, "2s"}},
		{Key: "Thử lại (%d/%d): ", Args: []any{2, 7}},
		{Key: "Thử lại (%d/%d, %s nữa): ", Args: []any{2, 7, "2s"}},
		{Key: "%s ngữ cảnh %.0f%% (%d/%d) chiến lược: %s",
			Args: []any{"writer", 42.0, 1000, 24000, "gần"}},
	}
	// Tiêu đề khối tool: không có Args, dịch thẳng theo chuỗi.
	for _, k := range toolDisplayKeys {
		msgs = append(msgs, Msg{Key: k})
	}

	for _, m := range msgs {
		vi := m.String()
		i18n.SetLanguage(i18n.LangEnglish)
		en := i18n.Tf(m.Key, m.Args...)
		i18n.SetLanguage(i18n.LangChinese)
		zh := i18n.Tf(m.Key, m.Args...)

		if en == vi {
			t.Errorf("thiếu bản dịch en: %q → vẫn ra %q", m.Key, en)
		}
		if zh == vi {
			t.Errorf("thiếu bản dịch zh: %q → vẫn ra %q", m.Key, zh)
		}
		if en == zh {
			t.Errorf("en và zh giống nhau: %q → %q", m.Key, en)
		}
	}
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
