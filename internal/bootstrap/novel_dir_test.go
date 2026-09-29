package bootstrap

import (
	"os"
	"path/filepath"
	"testing"
)

// Không đặt NOVEL_DIR: hành vi cũ, output/novel theo cwd.
func TestResolveOutputDir_Default(t *testing.T) {
	t.Setenv(NovelDirEnv, "")
	if got := ResolveOutputDir(); got != filepath.Join("output", "novel") {
		t.Fatalf("got %q", got)
	}
}

// NOVEL_DIR tương đối: resolve về tuyệt đối + output/novel.
func TestResolveOutputDir_NovelDirRelative(t *testing.T) {
	t.Setenv(NovelDirEnv, filepath.Join(".", "novels", "tien-hiep-ky"))
	abs, err := filepath.Abs(filepath.Join(".", "novels", "tien-hiep-ky"))
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(abs, "output", "novel")
	if got := ResolveOutputDir(); got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

// NOVEL_DIR tuyệt đối: dùng nguyên + output/novel.
func TestResolveOutputDir_NovelDirAbsolute(t *testing.T) {
	dir := filepath.Join(string(os.PathSeparator), "tmp", "truyen-a")
	if os.PathSeparator == '\\' {
		dir = `D:\novels\truyen-a`
	}
	t.Setenv(NovelDirEnv, dir)
	want := filepath.Join(dir, "output", "novel")
	if got := ResolveOutputDir(); got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

// OutputDir khai báo tường minh trong Config luôn thắng NOVEL_DIR.
func TestFillDefaults_ExplicitOutputDirWins(t *testing.T) {
	t.Setenv(NovelDirEnv, filepath.Join(".", "novels", "truyen-b"))
	c := Config{OutputDir: filepath.Join("custom", "dir")}
	c.FillDefaults()
	if c.OutputDir != filepath.Join("custom", "dir") {
		t.Fatalf("got %q", c.OutputDir)
	}
}

// FillDefaults không có gì: chạy theo NOVEL_DIR.
func TestFillDefaults_FollowsNovelDir(t *testing.T) {
	t.Setenv(NovelDirEnv, filepath.Join(".", "novels", "truyen-c"))
	var c Config
	c.FillDefaults()
	if c.OutputDir != ResolveOutputDir() {
		t.Fatalf("got %q want %q", c.OutputDir, ResolveOutputDir())
	}
}
