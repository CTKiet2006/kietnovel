package domain

import (
	"slices"
	"strings"
)

// CastEntry 是从章节接纳记录投影出的配角信息。
//
// 与 Character（characters.json，Architect 维护的核心档案）解耦：
//   - CastEntry 由 ChapterRecord 确定性计算，记录"出现过的有名字的次要角色"
//   - Character 由 Architect 显式设计，记录主角和关键配角的人格弧线/特质/tier
//
// 同名时以 Character 为准，避免重复。
type CastEntry struct {
	Name             string `json:"name"`
	BriefRole        string `json:"brief_role,omitempty"` // 一句话定位（首次出场由 Writer 填，可后续补全；不被覆盖）
	FirstSeenChapter int    `json:"first_seen_chapter"`
	LastSeenChapter  int    `json:"last_seen_chapter"`
	// AppearanceCount 派生自 len(AppearanceChapters)。
	AppearanceCount    int   `json:"appearance_count"`
	AppearanceChapters []int `json:"appearance_chapters"`
}

// CastIntro 是 Writer 在 commit_chapter 时对新出场角色的简介声明。
// 投影采用该角色最早的非空简介。
type CastIntro struct {
	Name      string `json:"name"`
	BriefRole string `json:"brief_role"`
}

// ProjectCast 从接纳记录重建配角视图。records 的输入顺序不影响结果。
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

// RecentCast 返回最近活跃的前 limit 位配角，不修改输入。
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
