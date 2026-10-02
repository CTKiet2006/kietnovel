package diag

import (
	"sort"

	"github.com/CTKiet2006/kietnovel/internal/i18n"
	"github.com/CTKiet2006/kietnovel/internal/store"
)

// ── Diagnostic thresholds ─────────────────────────────────────────────

const (
	ThresholdDimScoreLow      = 70  // ChronicLowDimension: warn when the average dimension score falls below this value
	ThresholdContractMissRate = 0.3 // ContractMissPattern: upper bound of the contract-miss rate
	ThresholdRewriteRate      = 0.5 // ExcessiveRewrites: upper bound of the rewrite rate
	ThresholdWordShortRatio   = 0.4 // WordCountAnomaly: a word count below this ratio of the average counts as an anomaly
	ThresholdWordLongRatio    = 2.5 // WordCountAnomaly: a word count above this ratio of the average counts as an anomaly
	ThresholdHookWeakScore    = 75  // HookWeakChain: a hook below this score counts as weak
	ThresholdHookWeakChain    = 3   // HookWeakChain: threshold of consecutive weak chapters
	ThresholdPayoffMissRate   = 0.4 // PayoffMissPattern: upper bound of the unfulfilled-payoff rate
	ThresholdCompassDrift     = 15  // CompassDrift: upper bound of the chapters without a compass update
	ThresholdTimelineGapRate  = 0.3 // TimelineGaps: tolerated upper bound of the missing rate
	ThresholdForeshadowMin    = 8   // StaleForeshadow: minimum number of chapters a setup may stall
)

// allRules is ordered flow -> quality -> planning -> context.
var allRules = []RuleFunc{
	// Flow
	InvalidPendingRewrites,
	RewritePendingPressure,
	OrphanedSteer,
	PhaseFlowMismatch,
	ChapterGaps,
	// Quality
	ChronicLowDimension,
	ContractMissPattern,
	HookWeakChain,
	PayoffMissPattern,
	ExcessiveRewrites,
	WordCountAnomaly,
	// Planning
	StaleForeshadow,
	CompassDrift,
	OutlineExhausted,
	MissingSummaries,
	// Context
	GhostCharacter,
	TimelineGaps,
	RelationshipStagnation,
}

// Analyze is the single entry point of the diagnostic system.
func Analyze(s *store.Store) Report {
	snap := Load(s)

	var findings []Finding
	for _, e := range snap.LoadErrors {
		findings = append(findings, Finding{
			Rule:       "LoadError",
			Category:   CatFlow,
			Severity:   SevWarning,
			Confidence: ConfHigh,
			AutoLevel:  AutoNone,
			Target:     "runtime.flow",
			Title:      i18n.Tf("Nạp dữ liệu thất bại: %s", e),
			Suggestion: i18n.T("File có thể hỏng hoặc thiếu quyền, nên kết quả của các quy tắc chẩn đoán liên quan có thể không đầy đủ."),
		})
	}
	for _, rule := range allRules {
		findings = append(findings, rule(&snap)...)
	}
	sortFindings(findings)

	return Report{
		Stats:    buildStats(&snap),
		Findings: findings,
		Actions:  PlanActions(findings),
	}
}

func buildStats(snap *Snapshot) Stats {
	st := Stats{}
	if snap.Progress == nil {
		return st
	}
	p := snap.Progress
	st.CompletedChapters = len(p.CompletedChapters)
	st.TotalChapters = p.TotalChapters
	st.TotalWords = p.TotalWordCount
	st.Phase = string(p.Phase)
	st.Flow = string(p.Flow)

	if st.CompletedChapters > 0 {
		st.AvgWordsPerCh = st.TotalWords / st.CompletedChapters
	}

	if snap.RunMeta != nil {
		st.PlanningTier = string(snap.RunMeta.PlanningTier)
	}

	// review statistics
	st.ReviewCount = len(snap.Reviews)
	var totalScore float64
	var dimCount int
	for _, r := range snap.Reviews {
		if r.Verdict == "rewrite" {
			st.RewriteCount++
		}
		for _, d := range r.Dimensions {
			totalScore += float64(d.Score)
			dimCount++
		}
	}
	if dimCount > 0 {
		st.AvgReviewScore = totalScore / float64(dimCount)
	}

	// foreshadow statistics
	latest := snap.LatestCompleted()
	for _, f := range snap.Foreshadow {
		if f.Status == "planted" || f.Status == "advanced" {
			st.ForeshadowOpen++
			if f.Status == "planted" && latest-f.PlantedAt > staleForeshadowThreshold(st.CompletedChapters) {
				st.ForeshadowStale++
			}
		}
	}
	return st
}

// sortFindings orders the findings by severity: critical > warning > info.
func sortFindings(findings []Finding) {
	order := map[Severity]int{SevCritical: 0, SevWarning: 1, SevInfo: 2}
	sort.SliceStable(findings, func(i, j int) bool {
		return order[findings[i].Severity] < order[findings[j].Severity]
	})
}

// staleForeshadowThreshold computes the setup-stall threshold from the total chapter count.
func staleForeshadowThreshold(completedChapters int) int {
	t := completedChapters / 3
	if t < ThresholdForeshadowMin {
		return ThresholdForeshadowMin
	}
	return t
}
