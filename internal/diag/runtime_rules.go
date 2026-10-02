package diag

import (
	"fmt"
	"strings"

	"github.com/CTKiet2006/kietnovel/internal/i18n"
	"github.com/CTKiet2006/kietnovel/internal/store"
)

// runtime detection thresholds.
const (
	repeatCritical = 8 // escalating to critical once the near-term repeats reach this count
	streamIdleWarn = 3 // the threshold at which accumulated stream_idle warns
)

// RuntimeRuleFunc is the unified signature of a runtime diagnostic rule (the counterpart of RuleFunc on the writing side).
// It takes the redacted, aggregated RuntimeCapture and produces report-style Findings - all AutoNone,
// diagnosing only and producing no Action (the observer discipline, see architecture.md §2.3).
type RuntimeRuleFunc func(rc *RuntimeCapture) []Finding

var runtimeRules = []RuntimeRuleFunc{
	repeatedErrors,
	stuckStep,
	streamIdleStorm,
}

// runtimeFindings runs every runtime rule.
func runtimeFindings(rc *RuntimeCapture) []Finding {
	var out []Finding
	for _, rule := range runtimeRules {
		out = append(out, rule(rc)...)
	}
	return out
}

// Diagnose is the full diagnosis entry point of /diag: writing diagnosis + runtime signals + runtime detection,
// returning the merged Report and the raw RuntimeCapture (reused by the export to avoid a second capture).
// The runtime Findings are only merged into Findings for display and never change Actions - it stays a pure observer.
func Diagnose(s *store.Store) (Report, RuntimeCapture) {
	rep := Analyze(s)
	rc := CaptureRuntime(s)
	rep.Findings = append(rep.Findings, runtimeFindings(&rc)...)
	sortFindings(rep.Findings)
	return rep, rc
}

// repeatedErrors judges only a "repeatedly occurring near-term error / invalid argument" as a Finding.
// It does not touch ordinary tool repeats - subagent/novel_context/read_chapter are naturally
// high-frequency during a long run and the accumulated count is not a loop signal; the real "repeating without progress" is caught by stuckStep.
func repeatedErrors(rc *RuntimeCapture) []Finding {
	var out []Finding
	for _, r := range rc.Repeats {
		var rule, title, sugg string
		switch {
		case strings.Contains(r.Sig, " · err: "):
			rule = "RepeatedToolError"
			title = i18n.T("Công cụ báo lỗi giống nhau lặp lại")
			sugg = i18n.T("Cùng một công cụ gần đây trả về cùng một lỗi, thường do tham số của model không hợp lệ hoặc hợp đồng công cụ lệch; kiểm tra xác thực tool của agentcore và quy ước tham số trong prompt (xem #34).")
		case strings.Contains(r.Sig, "(args invalid)"):
			rule = "ArgsInvalidLoop"
			title = i18n.T("Tham số lặp lại không phân tích được")
			sugg = i18n.T("Tham số model gửi sang không phân tích được mà vẫn thử lại; xem agentcore có ép kiểu lỏng cho loại đó không (xem #34).")
		default:
			continue // continue // an ordinary tool repeat produces no Finding
		}
		sev := SevWarning
		if r.Count >= repeatCritical {
			sev = SevCritical
		}
		out = append(out, Finding{
			Rule:       rule,
			Category:   CatFlow,
			Severity:   sev,
			Confidence: ConfHigh,
			AutoLevel:  AutoNone,
			Target:     "runtime.flow",
			Title:      title,
			Evidence:   fmt.Sprintf("`%s` ×%d", r.Sig, r.Count),
			Suggestion: sugg,
		})
	}
	return out
}

// stuckStep detects a checkpoint that stays on the same step in a row.
func stuckStep(rc *RuntimeCapture) []Finding {
	if rc.StuckStep == "" {
		return nil
	}
	sev := SevWarning
	if rc.StuckCount >= repeatCritical {
		sev = SevCritical
	}
	return []Finding{{
		Rule:       "StuckStep",
		Category:   CatFlow,
		Severity:   sev,
		Confidence: ConfHigh,
		AutoLevel:  AutoNone,
		Target:     "runtime.flow",
		Title:      i18n.T("Checkpoint đứng ở cùng một step"),
		Evidence:   i18n.Tf("Liên tục dừng ở `%s` ×%d", rc.StuckStep, rc.StuckCount),
		Suggestion: i18n.T("Cùng một step bị ghi lặp mà không tiến; kết hợp với chữ ký lặp ở trên để xác định sub-agent nào bị kẹt."),
	}}
}

// streamIdleStorm detects frequent stream interruptions (#32).
func streamIdleStorm(rc *RuntimeCapture) []Finding {
	n := rc.LogKinds["stream_idle"]
	if n < streamIdleWarn {
		return nil
	}
	return []Finding{{
		Rule:       "StreamIdleStorm",
		Category:   CatFlow,
		Severity:   SevWarning,
		Confidence: ConfHigh,
		AutoLevel:  AutoNone,
		Target:     "runtime.provider",
		Title:      i18n.T("Luồng bị ngắt thường xuyên (stream_idle)"),
		Evidence:   fmt.Sprintf("stream_idle ×%d", n),
		Suggestion: i18n.T("Phía trên lâu không trả token nên watchdog giết nhầm; với model suy nghĩ chậm hãy tăng streamIdleTimeout, hoặc kiểm tra độ ổn định kết nối của provider (xem #32)."),
	}}
}
