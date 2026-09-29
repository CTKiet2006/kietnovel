package diag

// Severity represents the severity of a finding.
type Severity string

const (
	SevCritical Severity = "critical" // SevCritical Severity = "critical" // blocks progress or corrupts data
	SevWarning  Severity = "warning"  // SevWarning  Severity = "warning"  // may lower the quality or waste tokens
	SevInfo     Severity = "info"     // SevInfo     Severity = "info"     // an optimizable point
)

// Category groups the findings by dimension.
type Category string

const (
	CatFlow     Category = "flow"     // CatFlow     Category = "flow"     // flow stalls, state anomalies, recovery problems
	CatQuality  Category = "quality"  // CatQuality  Category = "quality"  // review scores, contract fulfilment, consistency
	CatPlanning Category = "planning" // CatPlanning Category = "planning" // outline gaps, setup drift, an out-of-date compass
	CatContext  Category = "context"  // CatContext  Category = "context"  // character/timeline/relation anomalies
)

// Confidence represents how confident a rule verdict is.
type Confidence string

const (
	ConfHigh   Confidence = "high"   // ConfHigh   Confidence = "high"   // a strong certainty, trustworthy
	ConfMedium Confidence = "medium" // ConfMedium Confidence = "medium" // a heuristic judgement, may be a misjudgement
	ConfLow    Confidence = "low"    // ConfLow    Confidence = "low"    // a rough signal, for reference only
)

// AutoLevel represents whether a Finding may be turned into an automated action.
type AutoLevel string

const (
	AutoNone    AutoLevel = "none"    // AutoNone    AutoLevel = "none"    // report only, nothing automatic
	AutoSuggest AutoLevel = "suggest" // AutoSuggest AutoLevel = "suggest" // suggest an action but a human has to confirm
	AutoSafe    AutoLevel = "safe"    // AutoSafe    AutoLevel = "safe"    // safe to execute automatically
)

// Finding is one actionable diagnostic result.
type Finding struct {
	Rule       string     // Rule       string     // the rule name, e.g. "StaleForeshadow"
	Category   Category   // Category   Category   // the category
	Severity   Severity   // Severity   Severity   // the severity
	Confidence Confidence // Confidence Confidence // the confidence of the verdict
	AutoLevel  AutoLevel  // AutoLevel  AutoLevel  // the automation level
	Target     string     // Target     string     // the suggested target, e.g. "runtime.flow"
	Title      string     // Title      string     // a one-line summary
	Evidence   string     // Evidence   string     // the concrete data evidence
	Suggestion string     // Suggestion string     // the improvement suggestion (pointing at prompt/flow/config)
}

// RuleFunc is the unified signature of a diagnostic rule.
type RuleFunc func(snap *Snapshot) []Finding

// ActionKind represents the type of a diagnostic action.
type ActionKind string

const (
	ActionEmitNotice      ActionKind = "emit_notice"       // ActionEmitNotice      ActionKind = "emit_notice"       // emit a system notice
	ActionEnqueueFollowUp ActionKind = "enqueue_follow_up" // ActionEnqueueFollowUp ActionKind = "enqueue_follow_up" // generate a follow-up handling suggestion
)

// Action is an executable action the Planner generates from a high-confidence Finding.
type Action struct {
	SourceRule  string     // SourceRule  string     // the source rule name
	Kind        ActionKind // Kind        ActionKind // the action type
	Severity    Severity   // Severity    Severity   // inherited from the Finding
	Summary     string     // Summary     string     // a short description
	Message     string     // Message     string     // the message passed to the control flow
	Fingerprint string     // Fingerprint string     // a stable fingerprint of the source Finding, used for runtime dedup
}

// Stats is the overview metric shown alongside the findings.
type Stats struct {
	CompletedChapters int
	TotalChapters     int
	TotalWords        int
	AvgWordsPerCh     int
	Phase             string
	Flow              string
	PlanningTier      string
	ReviewCount       int
	RewriteCount      int
	AvgReviewScore    float64
	ForeshadowOpen    int
	ForeshadowStale   int
}

// Report is the complete output of one diagnosis run.
type Report struct {
	Stats    Stats
	Findings []Finding
	Actions  []Action
}
