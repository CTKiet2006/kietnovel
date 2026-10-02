package diag

import (
	"fmt"

	"github.com/CTKiet2006/kietnovel/internal/i18n"
)

// PlanActions generates executable actions from high-confidence findings.
// Only a finding with Confidence==high && AutoLevel==safe produces an Action.
func PlanActions(findings []Finding) []Action {
	var actions []Action
	seen := make(map[string]struct{})

	for _, f := range findings {
		if f.Confidence != ConfHigh || f.AutoLevel != AutoSafe {
			continue
		}
		if _, ok := seen[f.Rule]; ok {
			continue
		}
		seen[f.Rule] = struct{}{}

		actions = append(actions, planRule(f)...)
	}
	return actions
}

func planRule(f Finding) []Action {
	key := findingFingerprint(f)

	switch f.Rule {
	case "PhaseFlowMismatch":
		return []Action{
			{SourceRule: f.Rule, Kind: ActionEmitNotice, Severity: f.Severity, Summary: f.Title, Message: f.Title, Fingerprint: key},
			{SourceRule: f.Rule, Kind: ActionEnqueueFollowUp, Severity: f.Severity, Summary: i18n.T("Sửa lỗi máy trạng thái"), Message: i18n.T("Máy trạng thái bất thường: ") + f.Evidence + i18n.T(". Hãy kiểm tra và sửa phase/flow của progress trước khi chạy tiếp."), Fingerprint: key},
		}
	case "OutlineExhausted":
		return []Action{
			{SourceRule: f.Rule, Kind: ActionEnqueueFollowUp, Severity: f.Severity, Summary: i18n.T("Xử lý dàn ý đã cạn"), Message: i18n.T("Số chương đã xong đạt giới hạn đã lên kế hoạch. Hãy gọi Architect bung cung kế tiếp hoặc thêm tập mới trước khi viết tiếp."), Fingerprint: key},
		}
	case "OrphanedSteer":
		return []Action{
			{SourceRule: f.Rule, Kind: ActionEnqueueFollowUp, Severity: f.Severity, Summary: i18n.T("Xử lý chỉ dẫn người dùng chưa dùng"), Message: i18n.T("Còn chỉ dẫn của người dùng chưa được dùng. Hãy xử lý pending steer trước rồi mới tiếp tục việc đang dở."), Fingerprint: key},
		}
	default:
		return nil
	}
}

func findingFingerprint(f Finding) string {
	return fmt.Sprintf("%s|%s|%s|%s", f.Rule, f.Target, f.Title, f.Evidence)
}
