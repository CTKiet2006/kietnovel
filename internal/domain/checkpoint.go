package domain

import (
	"fmt"
	"time"
)

// ScopeKind identifies the scope type of a checkpoint.
type ScopeKind string

const (
	ScopeChapter ScopeKind = "chapter"
	ScopeArc     ScopeKind = "arc"
	ScopeVolume  ScopeKind = "volume"
	ScopeGlobal  ScopeKind = "global"
)

// Scope locates the writing scope a checkpoint belongs to.
type Scope struct {
	Kind    ScopeKind `json:"kind"`
	Chapter int       `json:"chapter,omitempty"`
	Volume  int       `json:"volume,omitempty"`
	Arc     int       `json:"arc,omitempty"`
}

// ChapterScope builds a chapter-level Scope.
func ChapterScope(chapter int) Scope {
	return Scope{Kind: ScopeChapter, Chapter: chapter}
}

// ArcScope builds an arc-level Scope.
func ArcScope(volume, arc int) Scope {
	return Scope{Kind: ScopeArc, Volume: volume, Arc: arc}
}

// VolumeScope builds a volume-level Scope.
func VolumeScope(volume int) Scope {
	return Scope{Kind: ScopeVolume, Volume: volume}
}

// GlobalScope builds a global Scope.
func GlobalScope() Scope {
	return Scope{Kind: ScopeGlobal}
}

func (s Scope) String() string {
	switch s.Kind {
	case ScopeChapter:
		return fmt.Sprintf("chapter:%d", s.Chapter)
	case ScopeArc:
		return fmt.Sprintf("arc:v%da%d", s.Volume, s.Arc)
	case ScopeVolume:
		return fmt.Sprintf("volume:%d", s.Volume)
	default:
		return "global"
	}
}

// Matches reports whether two Scopes are the same.
func (s Scope) Matches(other Scope) bool {
	if s.Kind != other.Kind {
		return false
	}
	switch s.Kind {
	case ScopeChapter:
		return s.Chapter == other.Chapter
	case ScopeArc:
		return s.Volume == other.Volume && s.Arc == other.Arc
	case ScopeVolume:
		return s.Volume == other.Volume
	default:
		return true
	}
}

// Checkpoint records the fact that a certain step completed successfully.
// A tool appends it to the JSONL after an atomic write; it is the single source of truth for recovery and observation.
type Checkpoint struct {
	Seq        int64     `json:"seq"`
	Scope      Scope     `json:"scope"`
	Step       string    `json:"step"`
	Artifact   string    `json:"artifact,omitempty"`
	Digest     string    `json:"digest,omitempty"`
	OccurredAt time.Time `json:"occurred_at"`
}
