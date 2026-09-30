package main

// Kiểm tra repo không còn sót tên cũ sau khi đổi module path và tên binary.
//
// Sự cố này đã xảy ra nhiều lần: đổi tên xong, .goreleaser.yml vẫn trỏ owner
// cũ, scripts/install.sh vẫn tải từ repo cũ, comment còn nhắc binary cũ. Mỗi
// lần sót là người dùng tải nhầm bản, nên phải chặn bằng test chứ không kiểm
// bằng mắt.
//
// Chạy: go run ./internal/tools/checkself  (hoặc go test ./internal/tools/...)

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// allow là file được phép giữ tên "ainovel-cli" — README và LICENSE ghi nhận
// nguồn gốc, đổi tên ở đó sẽ mất ý nghĩa credit. Chính thư mục checker này cũng
// phải được miễn: nó chứa chính các mẫu tìm kiếm, nếu không sẽ tự báo mình.
var allow = regexp.MustCompile(`^(README(\.\w+)?\.md|LICENSE|NOTICE)$`)

// selfDir là thư mục chứa checker, được bỏ qua khi quét.
var selfDir = "internal" + string(filepath.Separator) + "tools" + string(filepath.Separator) + "checkself"

// stale là các mẫu chắc chắn là sót. Thư mục dữ liệu ".ainovel" cố ý giữ nguyên
// để không phá dữ liệu người dùng cũ, nên không nằm trong đây.
//
// Lưu ý: Go dùng RE2, không hỗ trợ lookbehind, nên mẫu chỉ ghép thẳng
// "ainovel-cli". Nhờ vậy nó cũng bắt được dạng "voocel/ainovel-cli" trong link,
// vốn là trường hợp cần bắt nhất. README/LICENSE đã được allow nên không bị đụng.
var stale = []struct {
	pattern *regexp.Regexp
	why     string
}{
	{regexp.MustCompile(`cmd/ainovel-cli`), "đường dẫn thư mục đã đổi thành cmd/kietnovel"},
	{regexp.MustCompile(`\bAINOVEL_[A-Z_]+`), "biến môi trường đã đổi thành KIETNOVEL_*"},
	{regexp.MustCompile(`ainovel-cli`), "tên binary cũ, phải là kietnovel"},
	{regexp.MustCompile(`voocel/ainovel`), "repo cũ, phải là CTKiet2006/kietnovel"},
}

// exts giới hạn vào file có thể chứa đường dẫn/URL, bỏ file nhị phân.
var exts = map[string]bool{
	".go": true, ".md": true, ".sh": true, ".ps1": true,
	".yml": true, ".yaml": true, ".json": true, ".jsonc": true,
	".txt": true, ".mod": true, ".toml": true,
}

var skipDir = map[string]bool{
	".git": true, "node_modules": true, "output": true,
	"novels": true, "simulate": true, "xuat_ban": true,
}

func main() {
	root := repoRoot()
	bad := scan(root)

	if len(bad) > 0 {
		fmt.Fprintf(os.Stderr, "Tim thay %d cho con ten cu:\n", len(bad))
		for _, f := range bad {
			fmt.Fprintf(os.Stderr, "  %s:%d  [%s]\n      %s\n", f.rel, f.line, f.why, f.text)
		}
		fmt.Fprintln(os.Stderr, "\nSau khi doi module path va ten binary, khong duoc con tro toi ten cu.")
		os.Exit(1)
	}
	fmt.Println("Khong con sot ten repo/binary cu.")
}

type finding struct {
	rel, why, text string
	line           int
}

func scan(root string) []finding {
	var out []finding
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if skipDir[d.Name()] || d.Name() == "checkself" {
				return filepath.SkipDir
			}
			return nil
		}
		if !exts[strings.ToLower(filepath.Ext(d.Name()))] {
			return nil
		}
		if allow.MatchString(d.Name()) {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		for i, line := range strings.Split(string(raw), "\n") {
			for _, s := range stale {
				if s.pattern.MatchString(line) {
					out = append(out, finding{rel: rel, line: i + 1, why: s.why, text: strings.TrimSpace(line)})
				}
			}
		}
		return nil
	})
	sort.Slice(out, func(i, j int) bool {
		if out[i].rel != out[j].rel {
			return out[i].rel < out[j].rel
		}
		return out[i].line < out[j].line
	})
	return out
}

// repoRoot đi lên tới thư mục chứa go.mod, để chạy được từ bất kỳ đâu trong repo.
func repoRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		return "."
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "."
		}
		dir = parent
	}
}
