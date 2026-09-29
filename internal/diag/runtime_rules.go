package diag

import (
	"fmt"
	"strings"

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
			title = "工具反复报同一错误"
			sugg = "近端同一工具反复返回同一错误，多为模型参数不合规或工具契约不符；查 agentcore 工具校验 / prompt 参数约定（参见 #34）。"
		case strings.Contains(r.Sig, "(args invalid)"):
			rule = "ArgsInvalidLoop"
			title = "参数反复无法解析"
			sugg = "模型发来的参数无法解析却不断重试；看 agentcore 是否对该类型做了宽松强转（参见 #34）。"
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
		Title:      "checkpoint 停滞在同一 step",
		Evidence:   fmt.Sprintf("连续停在 `%s` ×%d", rc.StuckStep, rc.StuckCount),
		Suggestion: "同一 step 反复写入而不推进；结合上面的重复签名定位是哪个子代理卡住。",
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
		Title:      "流式中断频发（stream_idle）",
		Evidence:   fmt.Sprintf("stream_idle ×%d", n),
		Suggestion: "上游长时间不吐 token 被 watchdog 误杀；慢思考模型调大 streamIdleTimeout，或排查 provider 连接稳定性（参见 #32）。",
	}}
}
