package domain

// TimelineEvent is a timeline event.
type TimelineEvent struct {
	Chapter    int      `json:"chapter"`
	Time       string   `json:"time"`
	Event      string   `json:"event"`
	Characters []string `json:"characters,omitempty"`
}

// ForeshadowEntry is a foreshadowing entry.
type ForeshadowEntry struct {
	ID          string `json:"id"`
	Description string `json:"description"`
	PlantedAt   int    `json:"planted_at"`
	Status      string `json:"status"` // planted / advanced / resolved
	ResolvedAt  int    `json:"resolved_at,omitempty"`
}

// ForeshadowUpdate is an incremental foreshadowing operation.
type ForeshadowUpdate struct {
	ID          string `json:"id"`
	Action      string `json:"action"` // plant / advance / resolve
	Description string `json:"description,omitempty"`
}

// RestoreOwnPlants puts the plant operations back at the head of the queue for the setups this chapter planted in the old record but no longer declares in the new record.
// Which setups a chapter planted is a historical fact of that chapter itself and rewriting the body text does not change it; dropping it makes the
// advance/resolve of this chapter and the later ones fail to find their prerequisite plant when the chapter records are fully replayed, and the whole chain errors out.
func RestoreOwnPlants(prev, next []ForeshadowUpdate) []ForeshadowUpdate {
	declared := make(map[string]struct{}, len(next))
	for _, u := range next {
		if u.Action == "plant" {
			declared[u.ID] = struct{}{}
		}
	}
	var restored []ForeshadowUpdate
	for _, u := range prev {
		if u.Action != "plant" {
			continue
		}
		if _, ok := declared[u.ID]; ok {
			continue
		}
		declared[u.ID] = struct{}{}
		restored = append(restored, u)
	}
	if len(restored) == 0 {
		return next
	}
	// plant must be ordered before the advance/resolve of the same chapter, so the replay can build the entry first.
	return append(restored, next...)
}

// RelationshipEntry is a character-relation entry.
type RelationshipEntry struct {
	CharacterA string `json:"character_a"`
	CharacterB string `json:"character_b"`
	Relation   string `json:"relation"`
	Chapter    int    `json:"chapter"`
}

// ConsistencyIssue is a consistency problem.
type ConsistencyIssue struct {
	Type           string `json:"type"`     // the concrete problem dimension the model derived from the rubric
	Severity       string `json:"severity"` // critical / error / warning
	Description    string `json:"description"`
	Evidence       string `json:"evidence,omitempty"` // evidence: an excerpt of the original text, a concrete plot point or state data
	Suggestion     string `json:"suggestion,omitempty"`
	Chapters       []int  `json:"chapters,omitempty"` // the chapters the evidence actually falls in
	RequiresChange bool   `json:"requires_change"`    // whether it should enter the rework queue immediately, decided semantically by the Editor
}

// DimensionScore is a single-dimension review score.
type DimensionScore struct {
	Dimension string `json:"dimension"`         // defined by the review rubric, extendable per task
	Score     int    `json:"score"`             // 0-100
	Verdict   string `json:"verdict,omitempty"` // kept for compatibility with old reviews; the runtime no longer overrides the model judgement with a threshold
	Comment   string `json:"comment,omitempty"` // the brief conclusion for this dimension
}

// ReviewEntry is an Editor review entry.
type ReviewEntry struct {
	Chapter          int                `json:"chapter"`
	Scope            string             `json:"scope"` // chapter / global / arc
	Issues           []ConsistencyIssue `json:"issues"`
	Dimensions       []DimensionScore   `json:"dimensions,omitempty"`      // per-dimension scores
	ContractStatus   string             `json:"contract_status,omitempty"` // met / partial / missed
	ContractMisses   []string           `json:"contract_misses,omitempty"` // contract entries that were not met
	ContractNotes    string             `json:"contract_notes,omitempty"`  // a brief description of how well the contract was honoured
	Verdict          string             `json:"verdict"`                   // accept / polish / rewrite
	Summary          string             `json:"summary"`
	AffectedChapters []int              `json:"affected_chapters,omitempty"` // the chapter numbers that need a rewrite/polish
}

// CriticalCount returns the number of critical-level problems.
func (r *ReviewEntry) CriticalCount() int {
	n := 0
	for _, issue := range r.Issues {
		if issue.Severity == "critical" {
			n++
		}
	}
	return n
}

// ErrorCount returns the number of error-level problems.
func (r *ReviewEntry) ErrorCount() int {
	n := 0
	for _, issue := range r.Issues {
		if issue.Severity == "error" {
			n++
		}
	}
	return n
}

// Dimension returns the score of a given dimension, or nil when it does not exist.
func (r *ReviewEntry) Dimension(name string) *DimensionScore {
	if r == nil {
		return nil
	}
	for i := range r.Dimensions {
		if r.Dimensions[i].Dimension == name {
			return &r.Dimensions[i]
		}
	}
	return nil
}
