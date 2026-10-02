package diag

import (
	"fmt"
	"strings"

	"github.com/CTKiet2006/kietnovel/internal/domain"
	"github.com/CTKiet2006/kietnovel/internal/i18n"
)

// StaleForeshadow detects a setup that has not been advanced for a long time.
func StaleForeshadow(snap *Snapshot) []Finding {
	if snap.Progress == nil || len(snap.Foreshadow) == 0 {
		return nil
	}
	latest := snap.LatestCompleted()
	threshold := staleForeshadowThreshold(snap.CompletedCount())

	var stale []string
	for _, f := range snap.Foreshadow {
		if f.Status != "planted" {
			continue
		}
		gap := latest - f.PlantedAt
		if gap > threshold {
			stale = append(stale, fmt.Sprintf("%s(ch%d đã gieo, đã qua %d chương)", f.ID, f.PlantedAt, gap))
		}
	}
	if len(stale) == 0 {
		return nil
	}
	return []Finding{{
		Rule:       "StaleForeshadow",
		Category:   CatPlanning,
		Severity:   SevWarning,
		Confidence: ConfMedium,
		AutoLevel:  AutoNone,
		Target:     "context.foreshadow",
		Title:      i18n.Tf("Câu tình tiết đứng yên: %d mục quá %d chương chưa được đẩy tiếp", len(stale), threshold),
		Evidence:   strings.Join(stale, "; "),
		Suggestion: i18n.T("Nhắc tình tiết trong novel_context có thể chưa nạp được, hoặc prompt Writer thiếu chỉ dẫn đẩy tiếp. Kiểm tra foreshadow_ledger và logic nạp ngữ cảnh."),
	}}
}

// CompassDrift detects a compass that has not been updated for a long time.
func CompassDrift(snap *Snapshot) []Finding {
	if snap.Progress == nil || !snap.Progress.Layered {
		return nil
	}
	if snap.Compass == nil {
		if snap.CompletedCount() > 5 {
			return []Finding{{
				Rule:       "CompassDrift",
				Category:   CatPlanning,
				Severity:   SevWarning,
				Confidence: ConfMedium,
				AutoLevel:  AutoNone,
				Target:     "prompt.architect",
				Title:      i18n.T("Chế độ dài hạn thiếu la bàn"),
				Evidence:   fmt.Sprintf("layered=true, completed=%d, compass=nil", snap.CompletedCount()),
				Suggestion: i18n.T("Architect nên tạo compass lúc lập kế hoạch ban đầu. Kiểm tra architect-long.md có lệnh tạo compass không."),
			}}
		}
		return nil
	}

	gap := snap.LatestCompleted() - snap.Compass.LastUpdated
	if gap <= ThresholdCompassDrift {
		return nil
	}
	return []Finding{{
		Rule:       "CompassDrift",
		Category:   CatPlanning,
		Severity:   SevInfo,
		Confidence: ConfLow,
		AutoLevel:  AutoNone,
		Target:     "prompt.architect",
		Title:      i18n.Tf("La bàn đã %d chương chưa cập nhật", gap),
		Evidence:   fmt.Sprintf("last_updated=ch%d, latest=ch%d, open_threads=%d", snap.Compass.LastUpdated, snap.LatestCompleted(), len(snap.Compass.OpenThreads)),
		Suggestion: i18n.T("Architect nên cập nhật compass tại ranh giới cung/tập. Kiểm tra architect-long.md có lệnh cập nhật compass không."),
	}}
}

// OutlineExhausted detects an exhausted outline while the novel is not finished.
func OutlineExhausted(snap *Snapshot) []Finding {
	if snap.Progress == nil {
		return nil
	}
	p := snap.Progress
	if p.Phase == domain.PhaseComplete || p.Phase == domain.PhaseInit {
		return nil
	}

	completed := snap.CompletedCount()
	if completed == 0 {
		return nil
	}

	outlinedCount := p.TotalChapters
	if outlinedCount <= 0 {
		outlinedCount = len(snap.Outline)
	}
	if outlinedCount <= 0 {
		return nil
	}

	if completed < outlinedCount {
		return nil
	}

	return []Finding{{
		Rule:       "OutlineExhausted",
		Category:   CatPlanning,
		Severity:   SevCritical,
		Confidence: ConfHigh,
		AutoLevel:  AutoSafe,
		Target:     "runtime.recovery",
		Title:      i18n.Tf("Dàn ý đã cạn: đã xong %d chương >= đã lên kế hoạch %d chương", completed, outlinedCount),
		Evidence:   fmt.Sprintf("phase=%s, completed=%d, outlined=%d", p.Phase, completed, outlinedCount),
		Suggestion: i18n.T("Tín hiệu bung cung/mở tập mới có thể chưa kích hoạt. Kiểm tra chiến lược chốt phía Host và logic khôi phục, xác nhận dò biên cung, expand_next_arc hoặc append_volume có chạy đúng không."),
	}}
}

// MissingSummaries detects a completed chapter that is missing a summary.
func MissingSummaries(snap *Snapshot) []Finding {
	if snap.Progress == nil || len(snap.Progress.CompletedChapters) == 0 {
		return nil
	}

	var missing []int
	for _, ch := range snap.Progress.CompletedChapters {
		if _, ok := snap.Summaries[ch]; !ok {
			missing = append(missing, ch)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return []Finding{{
		Rule:       "MissingSummaries",
		Category:   CatPlanning,
		Severity:   SevWarning,
		Confidence: ConfHigh,
		AutoLevel:  AutoNone,
		Target:     "runtime.flow",
		Title:      i18n.Tf("Thiếu tóm tắt: %d chương không có tóm tắt", len(missing)),
		Evidence:   fmt.Sprintf("missing=[%s]", intsToStr(missing)),
		Suggestion: i18n.T("Tóm tắt là mấu chốt của tính liên tục ngữ cảnh. Kiểm tra logic ghi tóm tắt trong commit_chapter có chạy đúng không."),
	}}
}
