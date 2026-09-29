package host

import (
	"fmt"
	"strings"

	"github.com/CTKiet2006/kietnovel/internal/store"
	"github.com/CTKiet2006/kietnovel/internal/utils"
)

// buildStoryStateSummary assembles a compact summary of the current story state so the staged co-create
// assistant knows "what has already been written". It reuses the store access points and takes only the
// high-level facts the planning direction needs (progress / compass / latest volume / main characters / active
// foreshadowing); it does not pull the prose and does not feed the full novel_context JSON - co-create is a dialogue, it wants a readable overview, not a writing context. Any missing item is skipped (best-effort), and an empty string means no usable progress yet.
func buildStoryStateSummary(s *store.Store) string {
	if s == nil {
		return ""
	}
	var b strings.Builder
	var warnings []string
	warn := func(scope string, err error) {
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("%s 读取失败: %v", scope, err))
		}
	}

	if book, err := s.Book.Load(); book != nil {
		fmt.Fprintf(&b, "- 书名：《%s》\n", book.Title)
	} else {
		warn("book", err)
	}

	if progress, err := s.Progress.Load(); progress != nil {
		fmt.Fprintf(&b, "- 进度：已完成 %d 章", len(progress.CompletedChapters))
		if progress.Layered {
			outline, outlineErr := s.Outline.LoadOutline()
			if outlineErr != nil {
				warn("outline", outlineErr)
			} else if len(outline) > 0 {
				fmt.Fprintf(&b, " / 当前已细化 %d 章（后续按弧动态规划）", len(outline))
			}
		} else if progress.TotalChapters > 0 {
			fmt.Fprintf(&b, " / 规划 %d 章", progress.TotalChapters)
		}
		fmt.Fprintf(&b, "，约 %d 字，下一章为第 %d 章\n", progress.TotalWordCount, progress.NextChapter())
		if progress.Layered && progress.CurrentVolume > 0 {
			fmt.Fprintf(&b, "- 当前位置：第 %d 卷 第 %d 弧\n", progress.CurrentVolume, progress.CurrentArc)
		}
	} else {
		warn("progress", err)
	}

	if compass, err := s.Outline.LoadCompass(); compass != nil {
		if dir := strings.TrimSpace(compass.EndingDirection); dir != "" {
			fmt.Fprintf(&b, "- 终局方向：%s\n", dir)
		}
		if compass.EstimatedScale != "" {
			fmt.Fprintf(&b, "- 预估规模：%s\n", compass.EstimatedScale)
		}
		if len(compass.OpenThreads) > 0 {
			fmt.Fprintf(&b, "- 活跃长线：%s\n", strings.Join(compass.OpenThreads, "；"))
		}
	} else {
		warn("story_compass", err)
	}

	// The latest volume summary tells the assistant where the story just got to
	if vols, err := s.Summaries.LoadAllVolumeSummaries(); len(vols) > 0 {
		last := vols[len(vols)-1]
		fmt.Fprintf(&b, "- 最近《%s》：%s\n", last.Title, utils.TruncateRunes(last.Summary, 200))
	} else {
		warn("volume_summaries", err)
	}

	// Main characters (core/important), at most 8
	if chars, err := s.Characters.Load(); len(chars) > 0 {
		var names []string
		for _, c := range chars {
			if c.Tier == "secondary" || c.Tier == "decorative" {
				continue
			}
			line := c.Name
			if role := strings.TrimSpace(c.Role); role != "" {
				line += "（" + role + "）"
			}
			names = append(names, line)
			if len(names) >= 8 {
				break
			}
		}
		if len(names) > 0 {
			fmt.Fprintf(&b, "- 主要人物：%s\n", strings.Join(names, "、"))
		}
	} else {
		warn("characters", err)
	}

	// Unresolved foreshadowing, at most 6
	if fs, err := s.World.LoadActiveForeshadow(); len(fs) > 0 {
		var items []string
		for _, f := range fs {
			items = append(items, utils.TruncateRunes(f.Description, 40))
			if len(items) >= 6 {
				break
			}
		}
		fmt.Fprintf(&b, "- 未收伏笔：%s\n", strings.Join(items, "；"))
	} else {
		warn("foreshadow", err)
	}

	if len(warnings) > 0 {
		fmt.Fprintf(&b, "- 数据告警：%s\n", strings.Join(warnings, "；"))
	}

	return strings.TrimSpace(b.String())
}

// stageSystemPrompt assembles the full system prompt for staged co-create: the stage prompt plus the current story state summary.
// The summary is appended at the end as a data appendix (separated from the format spec by a divider), echoing the "progress is below" instruction in the prompt.
func stageSystemPrompt(s *store.Store) string {
	prompt := stageCoCreateSystemPrompt
	if summary := buildStoryStateSummary(s); summary != "" {
		prompt += "\n\n---\n## 当前故事状态\n（以下是已写内容的客观摘要，供你规划后续时参照，不要在 <draft> 里照抄原文）\n" + summary
	}
	return prompt
}
