package rules

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEnsureRulesDirAt verifies the prepared directory plus README.txt: the guidance is written and always overwritten with the
// latest template, and README.txt (not .md) is never scanned and taken for a rule.
func TestEnsureRulesDirAt(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "rules")
	if err := ensureRulesDirAt(dir); err != nil {
		t.Fatal(err)
	}
	readme := filepath.Join(dir, "README.txt")
	data, err := os.ReadFile(readme)
	if err != nil {
		t.Fatalf("README.txt should be written: %v", err)
	}
	// After dropping YAML the guidance now teaches "plain language + automatic normalization" and no longer front matter.
	if !strings.Contains(string(data), "归一化") {
		t.Errorf("README.txt 应说明自然语言会被归一化，got %q", data)
	}
	if strings.Contains(string(data), "front matter") {
		t.Errorf("README.txt 不应再教 YAML front matter，got %q", data)
	}

	// Always overwritten with the latest template: stale text written by an older version is refreshed on the next ensure
	if err := os.WriteFile(readme, []byte("旧版本写的过时文案"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ensureRulesDirAt(dir); err != nil {
		t.Fatal(err)
	}
	if again, _ := os.ReadFile(readme); string(again) != homeRulesReadme {
		t.Errorf("README.txt should be refreshed to latest template, got %q", again)
	}

	// README.txt is not taken for a rule (the scan only recognises .md)
	if srcs := RawFileSources(LoadOptions{HomeRulesDir: dir}); len(srcs) != 0 {
		t.Errorf("README.txt must not be scanned as a rule, got %d sources", len(srcs))
	}
}

// TestDefaultProjectRulesDir pins the project-level rules directory as the mirror of the global one: ./.kietnovel/rules/.
func TestDefaultProjectRulesDir(t *testing.T) {
	proj := filepath.Join("/tmp", "demo-book")
	want := filepath.Join(proj, ".kietnovel", "rules")
	if got := DefaultProjectRulesDir(proj); got != want {
		t.Errorf("DefaultProjectRulesDir=%q, want %q", got, want)
	}
	if got := DefaultProjectRulesDir(""); got != "" {
		t.Errorf("空项目根应返回空串，得到 %q", got)
	}
}

// TestDefaultOptions_ScansProjectRulesFromDotAinovel verifies end to end:
// DefaultOptions plugs the ./.kietnovel/rules/ under cwd into the SourceProject source.
func TestDefaultOptions_ScansProjectRulesFromDotAinovel(t *testing.T) {
	proj := t.TempDir()
	t.Chdir(proj)
	rulesDir := filepath.Join(proj, ".kietnovel", "rules")
	if err := os.MkdirAll(rulesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rulesDir, "book.md"), []byte("# 本书偏好\n每章 4000 字"), 0o644); err != nil {
		t.Fatal(err)
	}

	srcs := RawFileSources(DefaultOptions())
	var got *RawSource
	for i := range srcs {
		if srcs[i].Kind == SourceProject {
			got = &srcs[i]
		}
	}
	if got == nil {
		t.Fatalf("应从 ./.kietnovel/rules/ 扫到项目规则来源，得到 %+v", srcs)
	}
	if !strings.Contains(got.Text, "本书偏好") {
		t.Errorf("项目规则原文应被原样返回，得到 %q", got.Text)
	}
	if got.Label != "project:book.md" {
		t.Errorf("来源标签应为 project:book.md，得到 %q", got.Label)
	}
}
