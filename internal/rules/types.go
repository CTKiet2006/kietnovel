// Package rules implements the input layer of user preferences (Policy): it normalizes the writing rules of every source
// and merges them into this book's snapshot (see snapshot.go), injected at runtime by novel_context and mechanically checked by commit_chapter.
//
// Rule is the fourth kind of fact, alongside Progress / Checkpoint / Artifact, but of the opposite nature:
// the first three are system output, whereas Rule is a persistent input of user intent.
//
// Design constraints (non-negotiable):
//   - the tool returns facts, not instructions (a Violation is a fact; the editor decides whether to trigger a rewrite)
//   - no new verdict path is introduced (PendingRewrites is reused)
//   - no strictness field is introduced (severity is a fixed mapping from rule type; the editor makes the semantic call)
//   - the Flow Router is left alone (a rule takes no part in routing)
package rules

// SourceKind marks where a rules file came from and is only used to build the source label (e.g. global:my-style.md).
type SourceKind int

const (
	// SourceGlobal -- the user's global preferences (every .md under ~/.kietnovel/rules/, merged in filename lexicographic order), reused across books.
	SourceGlobal SourceKind = iota
	// SourceProject -- this book's rules (every .md under ./.kietnovel/rules/, merged in filename lexicographic order), the highest priority.
	SourceProject
)

// String returns the source's readable name, used as the source-label prefix.
func (k SourceKind) String() string {
	switch k {
	case SourceGlobal:
		return "global"
	case SourceProject:
		return "project"
	default:
		return "unknown"
	}
}

// Structured holds the mechanically checkable structured rule fields (the candidate/merged result after each source is normalized).
// Chapter word count is deliberately not among them: how long a chapter should be is a matter of narrative completeness, hence a
// semantic judgement (writer/editor); freezing it into a mechanical hard line would push the model to pad just to cross it -- the intent travels through the natural-language preferences channel.
type Structured struct {
	Genre            string         `json:"genre,omitempty"`
	ForbiddenChars   []string       `json:"forbidden_chars,omitempty"`
	ForbiddenPhrases []string       `json:"forbidden_phrases,omitempty"`
	FatigueWords     map[string]int `json:"fatigue_words,omitempty"`
}

// IsEmpty reports whether there are no structured rules at all, letting the checker skip.
func (s Structured) IsEmpty() bool {
	return s.Genre == "" &&
		len(s.ForbiddenChars) == 0 &&
		len(s.ForbiddenPhrases) == 0 &&
		len(s.FatigueWords) == 0
}

// Severity marks how serious a Violation is.
// Fixed mapping (not user-configurable):
//
//	forbidden_chars occurs            -> Error
//	forbidden_phrases occurs          -> Error
//	fatigue_words over threshold      -> Warning
type Severity string

const (
	SeverityWarning Severity = "warning"
	SeverityError   Severity = "error"
)

// Violation is the checker's output: the factual statement that this chapter broke a mechanical rule.
//
// Note: commit_chapter passes violations through into the returned JSON and does not block the commit;
// the editor maps these facts onto the existing seven dimensions (aesthetic/pacing/character/consistency) when reviewing,
// and the LLM freely decides whether to escalate the verdict to trigger polish/rewrite.
type Violation struct {
	Rule     string   `json:"rule"`             // forbidden_chars / forbidden_phrases / fatigue_words
	Target   string   `json:"target,omitempty"` // the concrete offender (which word/character)
	Limit    any      `json:"limit,omitempty"`  // threshold; fatigue_words=int / forbidden_*=empty
	Actual   any      `json:"actual"`           // the actual value: occurrence count
	Severity Severity `json:"severity"`         // error / warning
}
