package rules

import (
	"strings"
	"testing"
)

func TestLint_CleanText(t *testing.T) {
	if vs := Lint("# 第一章 风起\n他迈步向前。\n夜色渐深。"); len(vs) != 0 {
		t.Errorf("clean text should pass: %+v", vs)
	}
}

func TestLint_MarkdownResidue(t *testing.T) {
	text := "# 第一章\n这是**重点**内容。\n## 小标题\n正文。"
	vs := Lint(text)
	bold := findViolation(vs, "markdown_residue", "**")
	if bold == nil || bold.Actual != 2 {
		t.Errorf("expected ** residue x2: %+v", vs)
	}
	heading := findViolation(vs, "markdown_residue", "#")
	if heading == nil || heading.Actual != 1 {
		t.Errorf("expected 1 heading beyond first line: %+v", vs)
	}
}

func TestLint_NonCJKFragments(t *testing.T) {
	text := "# 第一章\n他发现了一个pattern，这个pattern像DNA一样规律。"
	vs := Lint(text)
	var v *Violation
	for i := range vs {
		if vs[i].Rule == "non_cjk_fragments" {
			v = &vs[i]
			break
		}
	}
	if v == nil {
		t.Fatalf("expected non_cjk violation: %+v", vs)
	}
	if v.Actual != 3 {
		t.Errorf("total count: got %v want 3", v.Actual)
	}
	if !strings.Contains(v.Target, "pattern") || !strings.Contains(v.Target, "DNA") {
		t.Errorf("examples should be distinct: %q", v.Target)
	}
	if v.Severity != SeverityWarning {
		t.Errorf("severity: %v", v.Severity)
	}
}

func TestLintForLanguage_Vietnamese(t *testing.T) {
	// Vietnamese text should not trigger non_cjk_fragments
	vnClean := "# Chương 1: Hai giờ bốn mươi bảy\nNgọc đặt bút xuống, nhấc máy.\nBên kia có tiếng gõ."
	vs := LintForLanguage(vnClean, "vi")
	if len(vs) != 0 {
		t.Errorf("clean Vietnamese text should have 0 violations, got: %+v", vs)
	}

	// Vietnamese text with Chinese characters should trigger cjk_residue
	vnWithCJK := "# Chương 1\nCô nhìn thấy chữ 某种程度上 trên tường."
	vsCJK := LintForLanguage(vnWithCJK, "vi")
	var cjkV *Violation
	for i := range vsCJK {
		if vsCJK[i].Rule == "cjk_residue" {
			cjkV = &vsCJK[i]
			break
		}
	}
	if cjkV == nil || cjkV.Actual != 5 {
		t.Errorf("expected cjk_residue violation with count 5, got: %+v", vsCJK)
	}
}
