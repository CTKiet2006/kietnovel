package model

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// OperationOutcome 是 Operation 的统一产出（D30/D47）：需要改变 Authority 时提交
// Proposal，审阅/校验类产出结构化 Verdict，二者互斥；Artifacts 可单独产出，也可
// 伴随 Proposal（附件引用它们）。审阅通过不制造空 Patch。
type OperationOutcome struct {
	Proposal  *Proposal       `json:"proposal,omitempty"`
	Verdict   json.RawMessage `json:"verdict,omitempty"`
	Artifacts []Artifact      `json:"artifacts,omitempty"`
}

func (o OperationOutcome) Validate() error {
	hasProposal := o.Proposal != nil
	hasVerdict := len(o.Verdict) != 0
	if hasProposal && hasVerdict {
		return fmt.Errorf("operation outcome cannot carry both proposal and verdict: %w", ErrInvalid)
	}
	if !hasProposal && !hasVerdict && len(o.Artifacts) == 0 {
		return fmt.Errorf("operation outcome requires a proposal, a verdict or artifacts: %w", ErrInvalid)
	}
	if hasVerdict && !json.Valid(o.Verdict) {
		return fmt.Errorf("operation verdict must be valid JSON: %w", ErrInvalid)
	}
	seen := make(map[string]struct{}, len(o.Artifacts))
	for i, artifact := range o.Artifacts {
		if err := artifact.Validate(); err != nil {
			return fmt.Errorf("artifact %d: %w", i, err)
		}
		if _, ok := seen[artifact.ID]; ok {
			return fmt.Errorf("duplicate artifact %q: %w", artifact.ID, ErrInvalid)
		}
		seen[artifact.ID] = struct{}{}
	}
	return nil
}

// DerivedVerdictKind 是裁定在派生数据中的落点：按 (project, revision, operation)
// 版本化绑定——正文再变化时旧裁定随 Revision 自动失效（§6.4）。
const DerivedVerdictKind = "operation_verdict"

// ReviewArtifactMediaType 标识 verdict 引用的结构化审阅过程记录。
const ReviewArtifactMediaType = "application/vnd.ainovel.review-findings+json"

const (
	ReviewPass    = "pass"
	ReviewBlocked = "blocked"

	FindingBlocking = "blocking"
	FindingNote     = "note"
)

// ReviewFinding 是一条审阅发现。阻塞发现可通过 Requirement 链接到它证明被违反的要求
// （§6.4）：违反项与阻塞发现一一可追溯，用户接受发现即接受该项（D43）。
type ReviewFinding struct {
	ChapterID   string `json:"chapter_id"`
	Severity    string `json:"severity"`
	Note        string `json:"note"`
	Requirement string `json:"requirement,omitempty"`
}

// 要求核验的三态（D62）：窗口只对自己看得到的正文下结论，未到期的要求如实 pending，
// 由作用域末章所在窗口兑现。
const (
	CheckSatisfied = "satisfied"
	CheckViolated  = "violated"
	CheckPending   = "pending"
)

// RequirementCheck 是审阅对任务输入中一项要求的显式核验声明。
type RequirementCheck struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Note   string `json:"note,omitempty"`
}

// ReviewVerdict 是审阅 Operation 的版本化裁定（§6.4）：pass 表示范围内正文没有阻塞级
// 发现、没有被违反的要求。Checks 逐项声明任务输入的要求（D62）。
// Revision 是产出裁定的基线 Revision；有效性按 Basis 判定（D48），不按 Revision 相等。
type ReviewVerdict struct {
	Status     string             `json:"status"`
	Revision   Revision           `json:"revision"`
	ChapterIDs []string           `json:"chapter_ids"`
	ReviewKey  string             `json:"review_key"`
	Basis      EvidenceBasis      `json:"basis"`
	Checks     []RequirementCheck `json:"checks,omitempty"`
	Findings   []ReviewFinding    `json:"findings"`
}

// CheckStatus 返回裁定对某项要求的结论；未声明返回空串。
func (v ReviewVerdict) CheckStatus(id string) string {
	for _, check := range v.Checks {
		if check.ID == id {
			return check.Status
		}
	}
	return ""
}

func (v ReviewVerdict) Validate() error {
	if v.Revision <= InitialRevision || len(v.ChapterIDs) == 0 || strings.TrimSpace(v.ReviewKey) == "" {
		return fmt.Errorf("review verdict revision, chapter range and review key are required: %w", ErrInvalid)
	}
	if v.Findings == nil {
		return fmt.Errorf("review verdict requires an explicit findings array: %w", ErrInvalid)
	}
	if err := validateEvidenceBasis("review verdict", v.Basis); err != nil {
		return err
	}
	if err := validateDistinctStrings("review verdict chapters", v.ChapterIDs); err != nil {
		return err
	}
	checks := make(map[string]struct{}, len(v.Checks))
	for i, check := range v.Checks {
		if strings.TrimSpace(check.ID) == "" {
			return fmt.Errorf("review verdict check %d is empty: %w", i, ErrInvalid)
		}
		if _, exists := checks[check.ID]; exists {
			return fmt.Errorf("review verdict check %q is duplicated: %w", check.ID, ErrInvalid)
		}
		checks[check.ID] = struct{}{}
		switch check.Status {
		case CheckSatisfied, CheckViolated, CheckPending:
		default:
			return fmt.Errorf("review verdict check %q has unknown status %q: %w", check.ID, check.Status, ErrInvalid)
		}
	}
	blocking := 0
	for i, finding := range v.Findings {
		if strings.TrimSpace(finding.ChapterID) == "" || strings.TrimSpace(finding.Note) == "" {
			return fmt.Errorf("review finding %d requires chapter and note: %w", i, ErrInvalid)
		}
		switch finding.Severity {
		case FindingBlocking:
			blocking++
		case FindingNote:
			if finding.Requirement != "" {
				// 工具边界的错误要能让模型当场自纠：指明改哪个字段，不要只说违反了规则。
				return fmt.Errorf(
					"review finding %d has severity %q but links requirement %q; drop requirement, "+
						"or use severity %q if the requirement is actually violated (other conclusions belong in the verdict's checks): %w",
					i, FindingNote, finding.Requirement, FindingBlocking, ErrInvalid)
			}
		default:
			return fmt.Errorf("unknown finding severity %q: %w", finding.Severity, ErrInvalid)
		}
	}
	switch v.Status {
	case ReviewPass:
		if blocking != 0 {
			return fmt.Errorf("passing verdict cannot carry blocking findings: %w", ErrInvalid)
		}
	case ReviewBlocked:
		if blocking == 0 {
			return fmt.Errorf("blocked verdict requires at least one blocking finding: %w", ErrInvalid)
		}
	default:
		return fmt.Errorf("unknown review status %q: %w", v.Status, ErrInvalid)
	}
	return nil
}

// ValidateReviewVerdictForOperation 把模型不能自证的 Revision、审阅范围与要求核验
// 绑定到启动 Operation，由所有 Executor 共用同一确定性校验。
func ValidateReviewVerdictForOperation(operation Operation, verdict ReviewVerdict) error {
	if operation.Kind != OperationReviewRange {
		return fmt.Errorf("%s operation cannot produce a review verdict: %w", operation.Kind, ErrInvalid)
	}
	if err := verdict.Validate(); err != nil {
		return err
	}
	if verdict.Revision != operation.Snapshot.BaseRevision {
		return fmt.Errorf("review verdict revision does not match operation snapshot: %w", ErrInvalid)
	}
	input, err := TaskInputAs[ReviewRangeInput](operation)
	if err != nil {
		return err
	}
	if !verdict.Basis.Equal(input.Basis) {
		return fmt.Errorf("review verdict basis does not match the task basis: %w", ErrInvalid)
	}
	requested := make(map[string]struct{}, len(input.ChapterIDs))
	for _, id := range input.ChapterIDs {
		requested[id] = struct{}{}
	}
	if len(requested) != len(verdict.ChapterIDs) {
		return fmt.Errorf("verdict does not exactly cover the requested chapter range: %w", ErrInvalid)
	}
	for _, id := range verdict.ChapterIDs {
		if _, ok := requested[id]; !ok {
			return fmt.Errorf("verdict chapter %q is outside the requested range: %w", id, ErrInvalid)
		}
	}
	for _, finding := range verdict.Findings {
		if err := input.AdmitsFinding(finding); err != nil {
			return err
		}
	}
	return validateChecks(input.Requirements, verdict)
}

// validateChecks 是要求核验门（D62）：任务输入的每项要求恰好声明一次，settle 项不得
// pending；pass 不得有违反项；每个违反项至少一条阻塞发现链接它，链接只能指向违反项。
func validateChecks(requirements []Requirement, verdict ReviewVerdict) error {
	expected := make(map[string]Requirement, len(requirements))
	ids := make([]string, 0, len(requirements))
	for _, requirement := range requirements {
		expected[requirement.ID] = requirement
		ids = append(ids, requirement.ID)
	}
	if len(expected) != len(verdict.Checks) {
		// 任务没带 requirements 时模型常自拟几条；说清期望集合，不要只说"不匹配"。
		return fmt.Errorf("verdict declares %d checks but the task requested %d %v; declare exactly these (omit the field when the task requests none): %w",
			len(verdict.Checks), len(ids), ids, ErrInvalid)
	}
	linked := make(map[string]bool)
	for _, check := range verdict.Checks {
		requirement, ok := expected[check.ID]
		if !ok {
			return fmt.Errorf("verdict check %q is outside the requested requirements %v: %w", check.ID, ids, ErrInvalid)
		}
		switch {
		case check.Status == CheckPending && requirement.Settle:
			return fmt.Errorf("requirement %q must be settled in this window; declare %q or %q instead of %q: %w",
				check.ID, CheckSatisfied, CheckViolated, CheckPending, ErrInvalid)
		case check.Status == CheckViolated && verdict.Status == ReviewPass:
			return fmt.Errorf("review pass cannot declare requirement %q violated; report it as a blocking finding: %w", check.ID, ErrInvalid)
		case check.Status == CheckViolated:
			linked[check.ID] = false
		}
	}
	for i, finding := range verdict.Findings {
		if finding.Requirement == "" {
			continue
		}
		if _, violated := linked[finding.Requirement]; !violated {
			return fmt.Errorf("finding %d links requirement %q that is not declared %q: %w", i, finding.Requirement, CheckViolated, ErrInvalid)
		}
		linked[finding.Requirement] = true
	}
	for _, check := range verdict.Checks {
		if ok, violated := linked[check.ID]; violated && !ok {
			return fmt.Errorf("violated requirement %q requires a blocking finding linked to it: %w", check.ID, ErrInvalid)
		}
	}
	return nil
}

// Adjudicated 套用用户裁决（D43）得到生效裁定：被接受的阻塞发现移除；违反项在没有
// 剩余阻塞发现链接它时视为满足，pending 不受影响；不再有阻塞发现即生效为 pass。原裁定不变。
func (v ReviewVerdict) Adjudicated(operationID string, accepted map[string]struct{}) ReviewVerdict {
	if len(accepted) == 0 {
		return v
	}
	effective := v
	effective.Findings = make([]ReviewFinding, 0, len(v.Findings))
	linked := make(map[string]struct{})
	removed := false
	for i, finding := range v.Findings {
		if _, ok := accepted[FindingID(operationID, i)]; ok && finding.Severity == FindingBlocking {
			removed = true
			continue
		}
		effective.Findings = append(effective.Findings, finding)
		if finding.Requirement != "" {
			linked[finding.Requirement] = struct{}{}
		}
	}
	if !removed {
		return v
	}
	effective.Checks = slices.Clone(v.Checks)
	for i, check := range effective.Checks {
		if _, still := linked[check.ID]; check.Status == CheckViolated && !still {
			effective.Checks[i].Status = CheckSatisfied
		}
	}
	if len(effective.BlockingChapters()) == 0 {
		effective.Status = ReviewPass
	}
	return effective
}

// BlockingChapters 返回按发现顺序去重的阻塞章节。
func (v ReviewVerdict) BlockingChapters() []string {
	seen := make(map[string]struct{})
	var chapters []string
	for _, finding := range v.Findings {
		if finding.Severity != FindingBlocking {
			continue
		}
		if _, ok := seen[finding.ChapterID]; ok {
			continue
		}
		seen[finding.ChapterID] = struct{}{}
		chapters = append(chapters, finding.ChapterID)
	}
	return chapters
}
