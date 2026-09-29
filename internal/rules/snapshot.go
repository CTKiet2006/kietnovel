package rules

import (
	"fmt"
	"maps"
	"strings"
)

// Snapshot is this book's normalized user-rule snapshot (meta/user_rules.json).
//
// It is the single source of truth at runtime: normalized and merged from every source when a book is created, imported or
// refreshed, after which both novel_context injection and commit_chapter checks read only this one copy instead of re-reading the rules files (avoiding drift and divergence between two readers).
//
// Only Structured + Preferences are injected into the model (see Payload); Version / Status / Sources /
// Uncertain are operational and diagnostic metadata and stay out of working_memory.user_rules.
type Snapshot struct {
	Version     int        `json:"version"`
	Status      Status     `json:"status"`
	Structured  Structured `json:"structured"`
	Preferences string     `json:"preferences"`
	Sources     []string   `json:"sources"`
	Uncertain   []string   `json:"uncertain"`
}

// Status records whether snapshot normalization completed successfully.
type Status string

const (
	// StatusReady means every source normalized successfully.
	StatusReady Status = "ready"
	// StatusDegraded means at least one source failed to normalize and was degraded to raw preferences (see Uncertain / the log).
	StatusDegraded Status = "degraded"
)

// SnapshotVersion is the current snapshot schema version, kept for future migration.
// v2: chapter_words leaves structured (word count is a soft semantic constraint and travels through preferences).
// v1 snapshots load unchanged: unknown fields are ignored during deserialization and converge naturally to v2 on the next overlay save;
// "rebuild on version mismatch" is deliberately not done -- that would throw away the non-reproducible rules AddRuntimeRule appends at runtime.
const SnapshotVersion = 2

// Candidate is the normalized candidate result of a single source.
//
// Sources are ordered by ascending priority and handed to BuildSnapshot for a deterministic merge. The LLM only turns a single source's
// natural language into a candidate Structured/Preferences; priority and field overrides are decided by BuildSnapshot (Go).
type Candidate struct {
	Source      string     // readable source label, lands in Snapshot.Sources (e.g. system_defaults / startup_prompt / global:my.md)
	Structured  Structured // the source's candidate structured fields
	Preferences string     // the source's natural-language preference body
	Uncertain   []string   // items deliberately not promoted to structured, plus the reason (diagnostics)
	Degraded    bool       // the source failed to normalize and was degraded to raw preferences
}

// Payload returns the shape injected into working_memory.user_rules, exposing only structured + preferences.
// It returns a stable structure even when both are empty, so the LLM never sees user_rules=null and takes an exceptional branch.
func (s Snapshot) Payload() map[string]any {
	return map[string]any{
		"structured":  s.Structured,
		"preferences": s.Preferences,
	}
}

// BuildSnapshot deterministically merges candidates ordered by priority (low -> high) into a snapshot.
//
// Merge rules (all deterministic on the Go side, never handed to the LLM):
//   - structured: overridden field by field, higher-priority sources beating lower ones; fatigue_words merged per word
//   - preferences: never overridden, concatenated in source order (higher priority last), each with its source heading
//   - an empty or zero value counts as a missing field and does not override an existing value (sanitizeStructured)
//   - any source Degraded -> snapshot status=degraded
func BuildSnapshot(cands []Candidate) Snapshot {
	snap := Snapshot{
		Version: SnapshotVersion,
		Status:  StatusReady,
		Sources: make([]string, 0, len(cands)),
	}
	var prefs []string
	for _, c := range cands {
		s := sanitizeStructured(c.Structured)
		if s.Genre != "" {
			snap.Structured.Genre = s.Genre
		}
		if len(s.ForbiddenChars) > 0 {
			snap.Structured.ForbiddenChars = s.ForbiddenChars
		}
		if len(s.ForbiddenPhrases) > 0 {
			snap.Structured.ForbiddenPhrases = s.ForbiddenPhrases
		}
		if len(s.FatigueWords) > 0 {
			snap.Structured.FatigueWords = mergeFatigueWords(snap.Structured.FatigueWords, s.FatigueWords)
		}

		if p := strings.TrimSpace(c.Preferences); p != "" {
			if src := strings.TrimSpace(c.Source); src != "" {
				prefs = append(prefs, fmt.Sprintf("## [%s]\n\n%s", src, p))
			} else {
				prefs = append(prefs, p)
			}
		}
		if src := strings.TrimSpace(c.Source); src != "" {
			snap.Sources = append(snap.Sources, src)
		}
		snap.Uncertain = append(snap.Uncertain, c.Uncertain...)
		if c.Degraded {
			snap.Status = StatusDegraded
		}
	}
	snap.Preferences = strings.Join(prefs, "\n\n")
	return snap
}

// OverlaySnapshot overlays a higher-priority candidate onto an existing snapshot (the candidate wins).
//
// It backs the runtime Arbiter rules action: instead of re-normalizing every source it overlays just the new rules onto the current snapshot --
// structured overridden field by field, preferences gets a section appended, sources/uncertain accumulate, and degradation propagates.
func OverlaySnapshot(base Snapshot, cand Candidate) Snapshot {
	out := base
	out.Version = SnapshotVersion
	s := sanitizeStructured(cand.Structured)
	if s.Genre != "" {
		out.Structured.Genre = s.Genre
	}
	if len(s.ForbiddenChars) > 0 {
		out.Structured.ForbiddenChars = s.ForbiddenChars
	}
	if len(s.ForbiddenPhrases) > 0 {
		out.Structured.ForbiddenPhrases = s.ForbiddenPhrases
	}
	if len(s.FatigueWords) > 0 {
		out.Structured.FatigueWords = mergeFatigueWords(cloneFatigue(out.Structured.FatigueWords), s.FatigueWords)
	}
	if p := strings.TrimSpace(cand.Preferences); p != "" {
		section := p
		if src := strings.TrimSpace(cand.Source); src != "" {
			section = fmt.Sprintf("## [%s]\n\n%s", src, p)
		}
		if strings.TrimSpace(out.Preferences) == "" {
			out.Preferences = section
		} else {
			out.Preferences = out.Preferences + "\n\n" + section
		}
	}
	if src := strings.TrimSpace(cand.Source); src != "" {
		out.Sources = append(append([]string{}, out.Sources...), src)
	}
	if len(cand.Uncertain) > 0 {
		out.Uncertain = append(append([]string{}, out.Uncertain...), cand.Uncertain...)
	}
	if cand.Degraded {
		out.Status = StatusDegraded
	}
	return out
}

// mergeFatigueWords merges fatigue-word thresholds per word, with src overriding the same word's threshold in dst (nearest wins).
// That lets a user add only a few fatigue words instead of relisting the whole built-in baseline.
func mergeFatigueWords(dst, src map[string]int) map[string]int {
	if len(src) == 0 {
		return dst
	}
	if dst == nil {
		dst = make(map[string]int, len(src))
	}
	maps.Copy(dst, src)
	return dst
}

func cloneFatigue(m map[string]int) map[string]int {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]int, len(m))
	maps.Copy(out, m)
	return out
}

// SystemDefaults is the mechanical baseline built into the code (the lowest-priority source) and does not go through LLM normalization.
//
// The values were migrated from the front matter of the old assets/rules/default.md, with the threshold reasoning kept:
// the later-stage fatigue words (像一 / 沉默了 / 没有说话 / X息) come from evidence in a 196-chapter long run -- once the
// early-stage cliche table was wiped out, the model used these "beat words" 5-7 times per chapter, so the thresholds are loose enough to tolerate normal use.
func SystemDefaults() Candidate {
	return Candidate{
		Source: "system_defaults",
		Structured: Structured{
			// Fixed-length AI cliches; the checker does literal substring matching, while patterns with a variable slot (not X but Y) belong to the semantic layer.
			ForbiddenPhrases: []string{"某种程度上", "值得注意的是", "不知为何", "五味杂陈"},
			FatigueWords: map[string]int{
				"不禁": 1, "竟然": 1, "仿佛": 2, "此外": 1, "然而": 2,
				"一丝": 2, "一抹": 2, "一缕": 2, "宛如": 1, "不由得": 1,
				"像一": 3, "沉默了": 2, "没有说话": 2, "几息": 3, "一息": 3, "数息": 2,
			},
		},
	}
}

// sanitizeStructured enforces "empty or zero means the field is missing": a normalizer may emit placeholders like genre:""
// (observed in prototype testing); they must be treated as undeclared so they cannot pollute merging or the mechanical checks.
func sanitizeStructured(s Structured) Structured {
	out := Structured{}
	if g := strings.TrimSpace(s.Genre); g != "" {
		out.Genre = g
	}
	out.ForbiddenChars = nonEmptyStrings(s.ForbiddenChars)
	out.ForbiddenPhrases = nonEmptyStrings(s.ForbiddenPhrases)
	out.FatigueWords = sanitizeFatigueWords(s.FatigueWords)
	return out
}

func nonEmptyStrings(in []string) []string {
	var out []string
	for _, s := range in {
		if t := strings.TrimSpace(s); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func sanitizeFatigueWords(m map[string]int) map[string]int {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]int, len(m))
	for w, n := range m {
		if w = strings.TrimSpace(w); w == "" || n <= 0 {
			continue
		}
		out[w] = n
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
