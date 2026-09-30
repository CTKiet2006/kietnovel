package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestKhongConTenCu là cổng chặn cho tên repo và tên binary cũ.
//
// Sự cố đã xảy ra nhiều lần trong lịch sử repo: đổi tên xong rồi .goreleaser.yml
// vẫn trỏ owner cũ, scripts/install.sh vẫn tải từ repo cũ, comment còn nhắc tên
// binary cũ. Mỗi lần sót là người dùng tải nhầm hoặc bị hướng dẫn sai, nên phải
// chặn bằng test thay vì soi bằng mắt.
func TestKhongConTenCu(t *testing.T) {
	root := repoRoot()
	bad := scan(root)
	if len(bad) == 0 {
		return
	}
	for _, f := range bad {
		t.Errorf("%s:%d  [%s]\n    %s", f.rel, f.line, f.why, f.text)
	}
	t.Errorf("Tìm thấy %d chỗ còn trỏ tới tên cũ. Sau khi đổi tên repo hoặc binary thì phải sửa hết.", len(bad))
}

// TestREADMEDuocPhepGiuTenNguonGoc bảo đảm allowlist không nuốt mất credit:
// README và LICENSE cố ý nhắc ainovel-cli vì đó là nguồn gốc.
func TestREADMEDuocPhepGiuTenNguonGoc(t *testing.T) {
	root := repoRoot()
	for _, name := range []string{"README.md", "README.en.md", "README.zh.md", "LICENSE"} {
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			t.Fatalf("thiếu %s: %v", name, err)
		}
	}
	if !allow.MatchString("README.md") {
		t.Error("README.md phải được allow để giữ phần ghi nhận nguồn gốc")
	}
}

// TestKhongTuBaoMinh bảo đảm checker không tự quét thư mục của chính nó. Nếu quét,
// nó sẽ tìm thấy chính các mẫu tìm kiếm trong mã nguồn mình và luôn báo đỏ.
func TestKhongTuBaoMinh(t *testing.T) {
	root := repoRoot()
	bad := scan(filepath.Join(root, selfDir))
	if len(bad) != 0 {
		t.Errorf("checker quét cả thư mục của chính nó: %d kết quả", len(bad))
	}
}

// TestKhongQuetFileNhiPhanGiup file nhị phân hoặc thư mục sinh ra không được
// quét, nếu không lần chạy sau sẽ báo nhầm.
func TestKhongQuetFileNhiPhanGiup(t *testing.T) {
	for _, name := range []string{"kietnovel.exe", "cover.png", "data.zip"} {
		if exts[filepath.Ext(name)] {
			t.Errorf("%s không nên nằm trong danh sách quét", name)
		}
	}
	for _, d := range []string{".git", "output", "novels"} {
		if !skipDir[d] {
			t.Errorf("%s phải nằm trong skipDir", d)
		}
	}
}
