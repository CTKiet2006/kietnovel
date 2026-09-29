package domain

import (
	"slices"
	"strings"
)

// CastEntry is the supporting-cast information projected from the chapter acceptance records.
//
// It is decoupled from Character (characters.json, the core profile maintained by the Architect):
//   - CastEntry is computed deterministically from the ChapterRecord and records "the named minor characters that appeared"
//   - Character is explicitly designed by the Architect and records the personality arc/traits/tier of the protagonist and the key supporting characters
//
// On a name clash Character wins, so there is no duplicate.
type CastEntry struct {
	Name             string `json:"name"`
	BriefRole        string `json:"brief_role,omitempty"` // one-line positioning (filled in by the Writer on the first appearance, may be completed later; never overwritten)
	FirstSeenChapter int    `json:"first_seen_chapter"`
	LastSeenChapter  int    `json:"last_seen_chapter"`
	// AppearanceCount is derived from len(AppearanceChapters).
	AppearanceCount    int   `json:"appearance_count"`
	AppearanceChapters []int `json:"appearance_chapters"`
}

// CastIntro is the Writer's one-paragraph introduction of a newly appearing character, declared at commit_chapter time.
// The projection takes the earliest non-empty introduction of that character.
type CastIntro struct {
	Name      string `json:"name"`
	BriefRole string `json:"brief_role"`
}

// ProjectCast rebuilds the supporting-cast view from the acceptance records. The input order of records does not affect the result.
func ProjectCast(records []ChapterRecord, characters []Character) []CastEntry {
	records = slices.Clone(records)
	slices.SortFunc(records, func(a, b ChapterRecord) int { return a.Chapter - b.Chapter })

	core := make(map[string]bool)
	for _, character := range characters {
		core[character.Name] = true
		for _, alias := range character.Aliases {
			core[alias] = true
		}
	}

	entries := make(map[string]*CastEntry)
	for _, record := range records {
		intros := make(map[string]string)
		for _, intro := range record.Facts.CastIntros {
			intros[intro.Name] = intro.BriefRole
		}
		seen := make(map[string]bool)
		for _, name := range record.Facts.Characters {
			if name == "" || core[name] || seen[name] {
				continue
			}
			seen[name] = true
			entry := entries[name]
			if entry == nil {
				entry = &CastEntry{Name: name, BriefRole: intros[name], FirstSeenChapter: record.Chapter}
				entries[name] = entry
			} else if entry.BriefRole == "" {
				entry.BriefRole = intros[name]
			}
			entry.LastSeenChapter = record.Chapter
			entry.AppearanceChapters = append(entry.AppearanceChapters, record.Chapter)
			entry.AppearanceCount = len(entry.AppearanceChapters)
		}
	}

	out := make([]CastEntry, 0, len(entries))
	for _, entry := range entries {
		out = append(out, *entry)
	}
	slices.SortFunc(out, func(a, b CastEntry) int {
		if a.FirstSeenChapter != b.FirstSeenChapter {
			return a.FirstSeenChapter - b.FirstSeenChapter
		}
		return strings.Compare(a.Name, b.Name)
	})
	return out
}

// RecentCast returns the first limit supporting characters by recent activity and does not modify the input.
func RecentCast(entries []CastEntry, limit int) []CastEntry {
	if limit <= 0 {
		return nil
	}
	entries = slices.Clone(entries)
	slices.SortFunc(entries, func(a, b CastEntry) int {
		if a.LastSeenChapter != b.LastSeenChapter {
			return b.LastSeenChapter - a.LastSeenChapter
		}
		if a.AppearanceCount != b.AppearanceCount {
			return b.AppearanceCount - a.AppearanceCount
		}
		return strings.Compare(a.Name, b.Name)
	})
	return entries[:min(limit, len(entries))]
}
