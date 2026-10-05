package domain

// FoundationAuditIssue 是 Architect 对已落盘基础设定给出的跨文件一致性问题。
type FoundationAuditIssue struct {
	Artifact    string `json:"artifact"`
	Description string `json:"description"`
	Evidence    string `json:"evidence"`
	Suggestion  string `json:"suggestion,omitempty"`
}

// FoundationAudit 记录一次针对确定版本基础设定的模型审查。
type FoundationAudit struct {
	Fingerprint string                 `json:"fingerprint"`
	Ready       bool                   `json:"ready"`
	Summary     string                 `json:"summary"`
	Issues      []FoundationAuditIssue `json:"issues"`
}
