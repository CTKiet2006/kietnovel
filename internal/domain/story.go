package domain

import (
	"fmt"
	"strings"
)

// BookMetadata is the work information aimed at readers and publications.
// The creative settings belong to Foundation and the writing progress to Progress, so neither of them carries this data.
type BookMetadata struct {
	Title    string `json:"title"`
	Synopsis string `json:"synopsis"`
}

// Normalized returns the canonical value that can be persisted and compared.
func (b BookMetadata) Normalized() BookMetadata {
	b.Title = strings.TrimSpace(b.Title)
	b.Synopsis = strings.TrimSpace(b.Synopsis)
	return b
}

// Validate checks the required fields of the work information.
func (b BookMetadata) Validate() error {
	b = b.Normalized()
	if b.Title == "" {
		return fmt.Errorf("book title is required")
	}
	if b.Synopsis == "" {
		return fmt.Errorf("book synopsis is required")
	}
	return nil
}

// OutlineEntry is an outline entry, corresponding to one chapter.
type OutlineEntry struct {
	Chapter   int      `json:"chapter"`
	Title     string   `json:"title"`
	CoreEvent string   `json:"core_event"`
	Hook      string   `json:"hook"`
	Scenes    []string `json:"scenes"`
}

// Character is a character profile.
type Character struct {
	Name        string   `json:"name"`
	Aliases     []string `json:"aliases,omitempty"` // alias/title/nickname (e.g. "the useless boy", "Brother Yan")
	Role        string   `json:"role"`
	Description string   `json:"description"`
	Arc         string   `json:"arc"`
	Traits      []string `json:"traits"`
	Tier        string   `json:"tier,omitempty"` // core / important / secondary / decorative (default important)
}

// VolumeOutline is the volume-level outline (long-form layered mode).
type VolumeOutline struct {
	Index int          `json:"index"`
	Title string       `json:"title"`
	Theme string       `json:"theme"`           // the core conflict/theme of this volume
	Final bool         `json:"final,omitempty"` // the closing volume: the whole book converges in this volume (declared by the architect on append_volume)
	Arcs  []ArcOutline `json:"arcs"`
}

// IsExpanded reports whether the volume has been expanded (it has an arc-level structure).
func (v *VolumeOutline) IsExpanded() bool { return len(v.Arcs) > 0 }

// FinaleVolume returns the number of the declared closing volume, or 0 when none is declared.
// The finale fact = "the last volume carries the Final flag": once declared the whole book enters the closing state (planning converges, and the final
// volume finishes as soon as it is written); if an unmarked new volume is appended afterwards, that volume becomes the last one and the closing state is naturally lifted -
// so no undo tool is needed and the state can always be derived from the outline data.
func FinaleVolume(volumes []VolumeOutline) int {
	if n := len(volumes); n > 0 && volumes[n-1].Final {
		return volumes[n-1].Index
	}
	return 0
}

// StoryCompass is the endgame-direction compass, which replaces a fixed skeleton volume list.
// The Architect may update it at every volume boundary, which lets the story direction evolve with the writing.
type StoryCompass struct {
	EndingDirection string   `json:"ending_direction"`          // endgame direction (a thematic description)
	OpenThreads     []string `json:"open_threads,omitempty"`    // active long threads (they must be closed off before the ending)
	EstimatedScale  string   `json:"estimated_scale,omitempty"` // rough scale (e.g. "estimated 4-6 volumes")
	LastUpdated     int      `json:"last_updated,omitempty"`    // the number of completed chapters at the time of the update
}

// ArcOutline is the arc-level outline.
type ArcOutline struct {
	Index             int            `json:"index"` // the arc number within the volume
	Title             string         `json:"title"`
	Goal              string         `json:"goal"`                         // the arc goal (setup, development, turn and conclusion)
	EstimatedChapters int            `json:"estimated_chapters,omitempty"` // the estimated chapter count of a skeleton arc (cleared to zero after expansion)
	Chapters          []OutlineEntry `json:"chapters"`
}

// IsExpanded reports whether the arc has been expanded (it has detailed chapters).
func (a *ArcOutline) IsExpanded() bool { return len(a.Chapters) > 0 }

// ArcExpansion is the complete plan the Architect makes for a not-yet-written arc at a structural boundary.
// Title/Goal is not a mechanical copy of the skeleton: the model may revise the not-yet-happened plan based on the finished body text.
type ArcExpansion struct {
	Title    string         `json:"title"`
	Goal     string         `json:"goal"`
	Chapters []OutlineEntry `json:"chapters"`
}

// EstimatedChapterCapacity computes the internal capacity estimate of the layered outline: an expanded arc uses its real chapter count,
// a skeleton arc uses EstimatedChapters. It is only used by the context policy, not as the total chapter count of the book; the chapters that are genuinely
// detailed and writable always come from FlattenOutline, so this value must never be exposed to the user or the model.
func EstimatedChapterCapacity(volumes []VolumeOutline) int {
	n := 0
	for _, v := range volumes {
		for _, a := range v.Arcs {
			if a.IsExpanded() {
				n += len(a.Chapters)
			} else {
				n += a.EstimatedChapters
			}
		}
	}
	return n
}

// FlattenOutline expands the layered outline into a flat chapter list, keeping the global chapter numbers continuous.
func FlattenOutline(volumes []VolumeOutline) []OutlineEntry {
	var result []OutlineEntry
	ch := 1
	for _, v := range volumes {
		for _, a := range v.Arcs {
			for _, e := range a.Chapters {
				e.Chapter = ch
				result = append(result, e)
				ch++
			}
		}
	}
	return result
}

// WorldRule is a worldbuilding rule entry.
type WorldRule struct {
	Category string `json:"category"` // magic / technology / geography / society / other
	Rule     string `json:"rule"`     // the rule description
	Boundary string `json:"boundary"` // a boundary that must not be violated
}
