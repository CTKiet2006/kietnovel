package host

import (
	"fmt"
	"strings"

	"github.com/CTKiet2006/kietnovel/internal/store"
	"github.com/CTKiet2006/kietnovel/internal/utils"
)

func buildStoryStateSummary(s *store.Store) string {
	return buildStoryStateSummaryFor(s, "zh")
}

func buildStoryStateSummaryFor(s *store.Store, lang string) string {
	if s == nil {
		return ""
	}
	var b strings.Builder
	var warnings []string
	cleanLang := strings.ToLower(strings.TrimSpace(lang))

	warn := func(scope string, err error) {
		if err != nil {
			switch cleanLang {
			case "vi":
				warnings = append(warnings, fmt.Sprintf("Đọc %s thất bại: %v", scope, err))
			case "en":
				warnings = append(warnings, fmt.Sprintf("Failed reading %s: %v", scope, err))
			default:
				warnings = append(warnings, fmt.Sprintf("%s 读取失败: %v", scope, err))
			}
		}
	}

	titleFmt := "- 书名：《%s》\n"
	progFmt1 := "- 进度：已完成 %d 章"
	progFmtLayered := " / 当前已细化 %d 章（后续按弧动态规划）"
	progFmtFlat := " / 规划 %d 章"
	progFmtTail := "，约 %d 字，下一章为第 %d 章\n"
	posFmt := "- 当前位置：第 %d 卷 第 %d 弧\n"
	compassDirFmt := "- 终局方向：%s\n"
	compassScaleFmt := "- 预估规模：%s\n"
	compassThreadsFmt := "- 活跃长线：%s\n"
	recentVolFmt := "- 最近《%s》：%s\n"
	charsFmt := "- 主要人物：%s\n"
	fsFmt := "- 未收伏笔：%s\n"
	warnFmt := "- 数据告警：%s\n"

	switch cleanLang {
	case "vi":
		titleFmt = "- Tên truyện: 《%s》\n"
		progFmt1 = "- Tiến độ: Đã hoàn thành %d chương"
		progFmtLayered = " / Hiện đã chi tiết hóa %d chương (các chương sau quy hoạch động theo arc)"
		progFmtFlat = " / Quy hoạch %d chương"
		progFmtTail = ", khoảng %d từ, chương tiếp theo là chương %d\n"
		posFmt = "- Vị trí hiện tại: Quyển %d Arc %d\n"
		compassDirFmt = "- Hướng đi chung cuộc: %s\n"
		compassScaleFmt = "- Quy mô dự kiến: %s\n"
		compassThreadsFmt = "- Tuyến dài hạn đang mở: %s\n"
		recentVolFmt = "- Quyển gần nhất 《%s》: %s\n"
		charsFmt = "- Nhân vật chính: %s\n"
		fsFmt = "- Phục bút chưa đóng: %s\n"
		warnFmt = "- Cảnh báo dữ liệu: %s\n"
	case "en":
		titleFmt = "- Book title: <%s>\n"
		progFmt1 = "- Progress: %d chapters completed"
		progFmtLayered = " / %d chapters detailed (subsequent chapters planned dynamically per arc)"
		progFmtFlat = " / %d chapters planned"
		progFmtTail = ", ~%d words, next chapter is Chapter %d\n"
		posFmt = "- Current position: Volume %d Arc %d\n"
		compassDirFmt = "- Finale trajectory: %s\n"
		compassScaleFmt = "- Estimated scale: %s\n"
		compassThreadsFmt = "- Active open threads: %s\n"
		recentVolFmt = "- Recent Volume <%s>: %s\n"
		charsFmt = "- Main cast: %s\n"
		fsFmt = "- Open foreshadowing: %s\n"
		warnFmt = "- Data alerts: %s\n"
	}

	if book, err := s.Book.Load(); book != nil {
		fmt.Fprintf(&b, titleFmt, book.Title)
	} else {
		warn("book", err)
	}

	if progress, err := s.Progress.Load(); progress != nil {
		fmt.Fprintf(&b, progFmt1, len(progress.CompletedChapters))
		if progress.Layered {
			outline, outlineErr := s.Outline.LoadOutline()
			if outlineErr != nil {
				warn("outline", outlineErr)
			} else if len(outline) > 0 {
				fmt.Fprintf(&b, progFmtLayered, len(outline))
			}
		} else if progress.TotalChapters > 0 {
			fmt.Fprintf(&b, progFmtFlat, progress.TotalChapters)
		}
		fmt.Fprintf(&b, progFmtTail, progress.TotalWordCount, progress.NextChapter())
		if progress.Layered && progress.CurrentVolume > 0 {
			fmt.Fprintf(&b, posFmt, progress.CurrentVolume, progress.CurrentArc)
		}
	} else {
		warn("progress", err)
	}

	if compass, err := s.Outline.LoadCompass(); compass != nil {
		if dir := strings.TrimSpace(compass.EndingDirection); dir != "" {
			fmt.Fprintf(&b, compassDirFmt, dir)
		}
		if compass.EstimatedScale != "" {
			fmt.Fprintf(&b, compassScaleFmt, compass.EstimatedScale)
		}
		if len(compass.OpenThreads) > 0 {
			delim := "；"
			if cleanLang == "vi" || cleanLang == "en" {
				delim = "; "
			}
			fmt.Fprintf(&b, compassThreadsFmt, strings.Join(compass.OpenThreads, delim))
		}
	} else {
		warn("story_compass", err)
	}

	// The latest volume summary tells the assistant where the story just got to
	if vols, err := s.Summaries.LoadAllVolumeSummaries(); len(vols) > 0 {
		last := vols[len(vols)-1]
		fmt.Fprintf(&b, recentVolFmt, last.Title, utils.TruncateRunes(last.Summary, 200))
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
				line += " (" + role + ")"
			}
			names = append(names, line)
			if len(names) >= 8 {
				break
			}
		}
		if len(names) > 0 {
			delim := "、"
			if cleanLang == "vi" || cleanLang == "en" {
				delim = ", "
			}
			fmt.Fprintf(&b, charsFmt, strings.Join(names, delim))
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
		delim := "；"
		if cleanLang == "vi" || cleanLang == "en" {
			delim = "; "
		}
		fmt.Fprintf(&b, fsFmt, strings.Join(items, delim))
	} else {
		warn("foreshadow", err)
	}

	if len(warnings) > 0 {
		delim := "；"
		if cleanLang == "vi" || cleanLang == "en" {
			delim = "; "
		}
		fmt.Fprintf(&b, warnFmt, strings.Join(warnings, delim))
	}

	return strings.TrimSpace(b.String())
}

// stageSystemPrompt assembles the full system prompt for staged co-create: the stage prompt plus the current story state summary.
// The summary is appended at the end as a data appendix (separated from the format spec by a divider), echoing the "progress is below" instruction in the prompt.
func stageSystemPrompt(s *store.Store) string {
	return stageSystemPromptFor(s, "zh")
}

func stageSystemPromptFor(s *store.Store, lang string) string {
	prompt := stageCoCreateSystemPromptFor(lang)
	cleanLang := strings.ToLower(strings.TrimSpace(lang))
	sectionHeader := "\n\n---\n## 当前故事状态\n（以下是已写内容的客观摘要，供你规划后续时参照，不要在 <draft> 里照抄原文）\n"
	switch cleanLang {
	case "vi":
		sectionHeader = "\n\n---\n## Trạng thái câu chuyện hiện tại\n(Dưới đây là tóm tắt khách quan các nội dung đã viết để bạn tham khảo khi quy hoạch tiếp, không sao chép nguyên văn vào <draft>)\n"
	case "en":
		sectionHeader = "\n\n---\n## Current Story State\n(Below is an objective summary of written content for planning reference; do not copy verbatim into <draft>)\n"
	}
	if summary := buildStoryStateSummaryFor(s, lang); summary != "" {
		prompt += sectionHeader + summary
	}
	return prompt
}
