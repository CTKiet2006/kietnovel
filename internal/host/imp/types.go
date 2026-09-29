// Package imp implements the staged semantic import pipeline for external novels (docs/import-pipeline.md).
//
// The model handles open-ended semantics; the code handles coordinates, coverage, types,
// hashing, ordering and idempotency. Every semantic artifact reaches the official book state
// only after validating in the isolated workspace (meta/import/). The next action is derived from artifacts alone (NextAction): no drift-prone stage enum is stored, and recovery does not depend on from=N.
package imp

import "time"

// Options controls one import run. On recovery its fields may be empty and are derived directly from the active workspace and the saved Intent.
type Options struct {
	SourcePath      string // mandatory for a new import; may be empty when resuming
	AutoConfirm     bool   // --yes: accept the segmentation automatically once the override validation passes
	StoryResolution string // --story=open|closed: preselected only when synthesis returns uncertain
	ContinueAfter   bool   // --continue: do not create the import-complete Hold
	Guidance        string // --guide: natural-language segmentation guidance; once it lands in the workspace it naturally makes the old segmentation mismatch and be re-recognised
	// AcceptSegmentation: explicit human confirmation after the TUI preview (y). Releases the
	// current segmentation once, writing no intent. Unlike --yes -- a blind go-ahead that never views the preview and refuses a segmentation carrying tolerance notes (Notes) -- y is a verdict made after reading the preview.
	AcceptSegmentation bool
}

// intent extracts from Options the user authorizations that must be persisted.
func (o Options) intent() Intent {
	return Intent{
		Version:             workspaceSchemaVersion,
		AutoConfirm:         o.AutoConfirm,
		StoryResolution:     o.StoryResolution,
		ContinueAfterImport: o.ContinueAfter,
	}
}

// Stage is the import pipeline's current stage, used only for UI display; it is not the source of truth for recovery (RFC §14.1).
type Stage string

const (
	StageIngesting            Stage = "ingesting"
	StageSegmenting           Stage = "segmenting"
	StageAwaitingConfirmation Stage = "awaiting_confirmation"
	StageAnalyzing            Stage = "analyzing"
	StageSynthesizing         Stage = "synthesizing"
	StageAwaitingStoryStatus  Stage = "awaiting_story_status"
	StageValidating           Stage = "validating"
	StagePublishing           Stage = "publishing"
	StageDone                 Stage = "done"
	StageError                Stage = "error"
)

// Event is a progress event emitted by the import pipeline. Events are projections and take no part in recovery.
type Event struct {
	Time      time.Time
	Stage     Stage
	Current   int       // chapter/range progress
	Total     int       // the total
	Message   string    // a human-readable description
	Level     string    // ""=ordinary progress; "warn"=a warning state such as a backoff retry or a re-ask after validation failed
	Key       string    // when non-empty the UI updates consecutive events with the same Key in place (e.g. 7 backoffs change on one row), matching the ID mechanism of the event panel
	RetryAt   time.Time // non-zero = the deadline of the next retry; the UI renders a second-by-second countdown from it and clears at zero (the request is already in flight)
	Err       error     // carried when StageError
	Continued bool      // set by the Host on StageDone: whether the Engine was auto-started in relay (--continue x auto)
}
