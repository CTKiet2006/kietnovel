package diag

import (
	"fmt"
	"strings"

	"github.com/CTKiet2006/kietnovel/internal/i18n"
)

// GhostCharacter detects a core/important role that has not appeared for a long time.
func GhostCharacter(snap *Snapshot) []Finding {
	if snap.Progress == nil || len(snap.Characters) == 0 || len(snap.Summaries) == 0 {
		return nil
	}
	completed := snap.CompletedCount()
	if completed < 5 {
		return nil
	}

	// compute the chapter number of each role's last appearance
	lastSeen := make(map[string]int)
	for ch, s := range snap.Summaries {
		for _, name := range s.Characters {
			if ch > lastSeen[name] {
				lastSeen[name] = ch
			}
		}
	}

	threshold := completed / 3
	if threshold < 5 {
		threshold = 5
	}
	latest := snap.LatestCompleted()

	var ghosts []string
	for _, c := range snap.Characters {
		if c.Tier != "core" && c.Tier != "important" {
			continue
		}
		seen, ok := lastSeen[c.Name]
		if !ok {
			// also check the aliases
			for _, alias := range c.Aliases {
				if s, exists := lastSeen[alias]; exists && s > seen {
					seen = s
					ok = true
				}
			}
		}
		gap := latest - seen
		if !ok {
			ghosts = append(ghosts, i18n.Tf("%s(chưa từng xuất hiện trong tóm tắt)", c.Name))
		} else if gap > threshold {
			ghosts = append(ghosts, i18n.Tf("%s(xuất hiện lần cuối ở ch%d, đã vắng %d chương)", c.Name, seen, gap))
		}
	}
	if len(ghosts) == 0 {
		return nil
	}
	return []Finding{{
		Rule:       "GhostCharacter",
		Category:   CatContext,
		Severity:   SevInfo,
		Confidence: ConfMedium,
		AutoLevel:  AutoNone,
		Target:     "context.characters",
		Title:      i18n.Tf("Nhân vật biến mất: %d nhân vật chính vắng mặt lâu", len(ghosts)),
		Evidence:   strings.Join(ghosts, "; "),
		Suggestion: i18n.T("Có thể Writer đã mất dấu nhân vật này. Cân nhắc gửi chỉ dẫn can thiệp trực tiếp ở ô nhập để đưa nhân vật trở lại, hoặc hạ tier của họ trong characters.json."),
	}}
}

// TimelineGaps detects a completed chapter that is missing a timeline event.
func TimelineGaps(snap *Snapshot) []Finding {
	if snap.Progress == nil || len(snap.Progress.CompletedChapters) == 0 {
		return nil
	}
	if len(snap.Timeline) == 0 && snap.CompletedCount() > 0 {
		return []Finding{{
			Rule:       "TimelineGaps",
			Category:   CatContext,
			Severity:   SevInfo,
			Confidence: ConfMedium,
			AutoLevel:  AutoNone,
			Target:     "context.timeline",
			Title:      i18n.T("Dòng thời gian trống"),
			Evidence:   fmt.Sprintf("completed=%d, timeline_events=0", snap.CompletedCount()),
			Suggestion: i18n.T("Việc trích xuất dòng thời gian trong commit_chapter có thể chưa chạy. Kiểm tra output của Writer có trường timeline không."),
		}}
	}

	// build the chapter -> event mapping
	chaptersWithEvents := make(map[int]bool)
	for _, e := range snap.Timeline {
		chaptersWithEvents[e.Chapter] = true
	}

	var missing []int
	for _, ch := range snap.Progress.CompletedChapters {
		if !chaptersWithEvents[ch] {
			missing = append(missing, ch)
		}
	}
	// a few missing events are allowed (some transitional chapters may indeed have no major event)
	if len(missing) == 0 || float64(len(missing))/float64(snap.CompletedCount()) < ThresholdTimelineGapRate {
		return nil
	}
	return []Finding{{
		Rule:       "TimelineGaps",
		Category:   CatContext,
		Severity:   SevInfo,
		Confidence: ConfMedium,
		AutoLevel:  AutoNone,
		Target:     "context.timeline",
		Title:      i18n.Tf("Thiếu dòng thời gian: %d chương không có sự kiện", len(missing)),
		Evidence:   fmt.Sprintf("missing=[%s]", intsToStr(missing)),
		Suggestion: i18n.T("Việc trích xuất dòng thời gian trong commit_chapter có thể hỏng một phần. Kiểm tra định dạng trường timeline trong output của Writer."),
	}}
}

// RelationshipStagnation detects relation data that has stopped being updated.
func RelationshipStagnation(snap *Snapshot) []Finding {
	if snap.Progress == nil || len(snap.Relationships) == 0 {
		return nil
	}
	completed := snap.CompletedCount()
	if completed < 6 {
		return nil
	}

	// find the newest chapter of the relation data
	latestRelCh := 0
	for _, r := range snap.Relationships {
		if r.Chapter > latestRelCh {
			latestRelCh = r.Chapter
		}
	}

	// if the newest relation data is in the first third, judge it as stagnant
	cutoff := snap.LatestCompleted() - completed/3
	if latestRelCh >= cutoff {
		return nil
	}
	return []Finding{{
		Rule:       "RelationshipStagnation",
		Category:   CatContext,
		Severity:   SevInfo,
		Confidence: ConfLow,
		AutoLevel:  AutoNone,
		Target:     "context.relationships",
		Title:      i18n.Tf("Dữ liệu quan hệ đứng yên: lần cập nhật gần nhất ở chương %d", latestRelCh),
		Evidence:   fmt.Sprintf("relationship_entries=%d, latest_update=ch%d, latest_completed=ch%d", len(snap.Relationships), latestRelCh, snap.LatestCompleted()),
		Suggestion: i18n.T("Việc cập nhật quan hệ trong commit_chapter có thể đã ngừng, hoặc quan hệ trong truyện thực sự không thay đổi. Kiểm tra trường relationships trong output của Writer."),
	}}
}
