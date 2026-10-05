package rules

import (
	"testing"
)

// findViolation 在结果中按 rule + target 查找第一条违规。
func findViolation(vs []Violation, rule, target string) *Violation {
	for i := range vs {
		if vs[i].Rule == rule && vs[i].Target == target {
			return &vs[i]
		}
	}
	return nil
}

func TestCheck_EmptyStructured(t *testing.T) {
	vs := Check("任何内容", Structured{})
	if vs != nil {
		t.Errorf("empty structured should return nil, got %+v", vs)
	}
}

func TestCheck_ForbiddenChars(t *testing.T) {
	text := "他笑了——又叹了口气——离去。"
	vs := Check(text, Structured{
		ForbiddenChars: []string{"——"},
	})
	v := findViolation(vs, "forbidden_chars", "——")
	if v == nil {
		t.Fatal("expected forbidden_chars violation")
	}
	if v.Severity != SeverityError {
		t.Errorf("severity=%s, want error", v.Severity)
	}
	if v.Actual != 2 {
		t.Errorf("actual=%v, want 2", v.Actual)
	}
}

func TestCheck_ForbiddenCharsNotPresent(t *testing.T) {
	vs := Check("普通文本无违规", Structured{
		ForbiddenChars: []string{"——"},
	})
	if len(vs) != 0 {
		t.Errorf("expected no violations, got %+v", vs)
	}
}

func TestCheck_ForbiddenPhrases(t *testing.T) {
	text := "不是……而是真相被掩盖了。这里探讨核心动机。"
	vs := Check(text, Structured{
		ForbiddenPhrases: []string{"不是……而是", "核心动机"},
	})
	if len(vs) != 2 {
		t.Errorf("expected 2 violations, got %d: %+v", len(vs), vs)
	}
	for _, v := range vs {
		if v.Severity != SeverityError {
			t.Errorf("severity=%s, want error", v.Severity)
		}
	}
}

func TestCheck_FatigueWordsUnderLimit(t *testing.T) {
	text := "他不禁笑了。"
	vs := Check(text, Structured{
		FatigueWords: map[string]int{"不禁": 1},
	})
	if len(vs) != 0 {
		t.Errorf("under limit should not violate, got %+v", vs)
	}
}

func TestCheck_FatigueWordsAtLimit(t *testing.T) {
	// limit=1，actual=1 → 不违规
	text := "他不禁笑了。"
	vs := Check(text, Structured{
		FatigueWords: map[string]int{"不禁": 1},
	})
	if len(vs) != 0 {
		t.Errorf("at limit should not violate (limit 1 actual 1), got %+v", vs)
	}
}

func TestCheck_FatigueWordsOverLimit(t *testing.T) {
	// limit=1，actual=3 → warning
	text := "他不禁笑了，又不禁皱眉，最后不禁离去。"
	vs := Check(text, Structured{
		FatigueWords: map[string]int{"不禁": 1},
	})
	v := findViolation(vs, "fatigue_words", "不禁")
	if v == nil {
		t.Fatal("expected fatigue_words violation")
	}
	if v.Severity != SeverityWarning {
		t.Errorf("severity=%s, want warning", v.Severity)
	}
	if v.Limit != 1 {
		t.Errorf("limit=%v, want 1", v.Limit)
	}
	if v.Actual != 3 {
		t.Errorf("actual=%v, want 3", v.Actual)
	}
}

func TestCheck_MultipleRulesAtOnce(t *testing.T) {
	text := "他不禁——又不禁——离去。"
	s := Structured{
		ForbiddenChars: []string{"——"},
		FatigueWords:   map[string]int{"不禁": 1},
	}
	vs := Check(text, s)

	// 应同时触发两类：forbidden_chars + fatigue_words
	rules := map[string]bool{}
	for _, v := range vs {
		rules[v.Rule] = true
	}
	if !rules["forbidden_chars"] || !rules["fatigue_words"] {
		t.Errorf("expected both rules triggered, got %+v", rules)
	}
}

func TestCheck_FatigueZeroLimitSkipped(t *testing.T) {
	// limit=0 是非法值，应跳过整条规则（parser 也会过滤，这里防御）
	text := "不禁不禁不禁"
	vs := Check(text, Structured{
		FatigueWords: map[string]int{"不禁": 0},
	})
	if len(vs) != 0 {
		t.Errorf("limit=0 should be skipped, got %+v", vs)
	}
}

func TestCheck_EmptyTargetsSkipped(t *testing.T) {
	// 空字符串目标不应导致 false positive
	vs := Check("任何文本", Structured{
		ForbiddenChars:   []string{""},
		ForbiddenPhrases: []string{""},
		FatigueWords:     map[string]int{"": 1},
	})
	if len(vs) != 0 {
		t.Errorf("empty targets should be skipped, got %+v", vs)
	}
}
