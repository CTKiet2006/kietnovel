package diag

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/CTKiet2006/kietnovel/internal/domain"
	"github.com/CTKiet2006/kietnovel/internal/i18n"
	"github.com/CTKiet2006/kietnovel/internal/utils"
)

// InvalidPendingRewrites detects an unfinished chapter that slipped into the rework queue.
func InvalidPendingRewrites(snap *Snapshot) []Finding {
	if snap.Progress == nil || len(snap.Progress.PendingRewrites) == 0 {
		return nil
	}
	p := snap.Progress
	completed := append([]int(nil), p.CompletedChapters...)
	slices.Sort(completed)

	var invalid []int
	for _, ch := range p.PendingRewrites {
		if ch <= 0 || !slices.Contains(completed, ch) {
			invalid = append(invalid, ch)
		}
	}
	if len(invalid) == 0 {
		return nil
	}
	slices.Sort(invalid)
	return []Finding{{
		Rule:       "InvalidPendingRewrites",
		Category:   CatFlow,
		Severity:   SevCritical,
		Confidence: ConfHigh,
		AutoLevel:  AutoSuggest,
		Target:     "meta/progress.json",
		Title:      i18n.Tf("Hàng đợi viết lại chứa chương chưa hoàn thành: [%s]", intsToStr(invalid)),
		Evidence:   fmt.Sprintf("pending_rewrites=[%s], completed_chapters=[%s], flow=%s", intsToStr(p.PendingRewrites), intsToStr(completed), p.Flow),
		Suggestion: i18n.T("Bất biến trạng thái bị hỏng. Hãy dừng chạy rồi sửa meta/progress.json, gỡ các chương chưa hoàn thành khỏi pending_rewrites; nếu hàng đợi rỗng thì đổi flow thành writing và xoá rewrite_reason."),
	}}
}

// RewritePendingPressure detects the existence of chapters pending a rewrite (currently it only detects that the state exists, it does not judge stagnation).
func RewritePendingPressure(snap *Snapshot) []Finding {
	if snap.Progress == nil {
		return nil
	}
	p := snap.Progress
	if len(p.PendingRewrites) == 0 {
		return nil
	}
	if p.Flow != domain.FlowRewriting && p.Flow != domain.FlowPolishing {
		return nil
	}
	chapters := intsToStr(p.PendingRewrites)
	return []Finding{{
		Rule:       "RewritePendingPressure",
		Category:   CatFlow,
		Severity:   SevWarning,
		Confidence: ConfMedium,
		AutoLevel:  AutoNone,
		Target:     "runtime.flow",
		Title:      i18n.Tf("Chương chờ viết lại: [%s]", chapters),
		Evidence:   fmt.Sprintf("flow=%s, pending_rewrites=[%s]", p.Flow, chapters),
		Suggestion: i18n.T("Kiểm tra tiêu chuẩn duyệt của Editor có quá khắt khe không, hoặc prompt viết lại của Writer có hiệu lực không.") +
			i18n.T("Khi một chương thất bại lặp lại, engine sẽ tự gỡ khỏi hàng đợi và tiếp tục sáng tác, không cần dọn thủ công."),
	}}
}

// OrphanedSteer detects a user steering instruction that was never consumed.
func OrphanedSteer(snap *Snapshot) []Finding {
	if snap.RunMeta == nil || snap.RunMeta.PendingSteer == "" {
		return nil
	}
	if snap.Progress != nil && snap.Progress.Flow == domain.FlowSteering {
		return nil // return nil // it is being processed, so it does not count as orphaned
	}
	return []Finding{{
		Rule:       "OrphanedSteer",
		Category:   CatFlow,
		Severity:   SevWarning,
		Confidence: ConfHigh,
		AutoLevel:  AutoSafe,
		Target:     "runtime.recovery",
		Title:      i18n.T("Có chỉ dẫn hướng chưa được dùng"),
		Evidence:   fmt.Sprintf("pending_steer=%q, flow=%s", utils.TruncateRunes(snap.RunMeta.PendingSteer, 60), flowStr(snap.Progress)),
		Suggestion: i18n.T("Chỉ dẫn này đã được lưu nhưng không được bước định đoạn can thiệp dùng đến. Kiểm tra logic khôi phục khi bị gián đoạn, hoặc gửi lại để ghi đè."),
	}}
}

// PhaseFlowMismatch detects a mismatch between the phase and the flow state.
func PhaseFlowMismatch(snap *Snapshot) []Finding {
	if snap.Progress == nil {
		return nil
	}
	p := snap.Progress
	if p.Phase == domain.PhaseWriting || p.Phase == "" {
		return nil
	}
	if p.Flow == "" || p.Flow == domain.FlowWriting {
		return nil
	}
	return []Finding{{
		Rule:       "PhaseFlowMismatch",
		Category:   CatFlow,
		Severity:   SevCritical,
		Confidence: ConfHigh,
		AutoLevel:  AutoSafe,
		Target:     "runtime.flow",
		Title:      i18n.Tf("Giai đoạn/luồng không khớp: phase=%s, flow=%s", p.Phase, p.Flow),
		Evidence:   i18n.Tf("phase=%s không được có flow khác trạng thái ban đầu là %s", p.Phase, p.Flow),
		Suggestion: i18n.T("Máy trạng thái có thể đã hỏng, cần tự kiểm tra các trường phase và flow trong meta/progress.json."),
	}}
}

// ChapterGaps detects a gap in the list of completed chapters.
func ChapterGaps(snap *Snapshot) []Finding {
	if snap.Progress == nil || len(snap.Progress.CompletedChapters) < 2 {
		return nil
	}
	sorted := append([]int(nil), snap.Progress.CompletedChapters...)
	sort.Ints(sorted)

	var gaps []int
	for i := 1; i < len(sorted); i++ {
		for ch := sorted[i-1] + 1; ch < sorted[i]; ch++ {
			gaps = append(gaps, ch)
		}
	}
	if len(gaps) == 0 {
		return nil
	}
	return []Finding{{
		Rule:       "ChapterGaps",
		Category:   CatFlow,
		Severity:   SevWarning,
		Confidence: ConfHigh,
		AutoLevel:  AutoNone,
		Target:     "runtime.flow",
		Title:      i18n.Tf("Chương bị thiếu số: thiếu [%s]", intsToStr(gaps)),
		Evidence:   fmt.Sprintf("completed=[%s]", intsToStr(sorted)),
		Suggestion: i18n.T("commit_chapter có thể đã bị gián đoạn. Kiểm tra meta/pending_commit.json xem có bản ghi chưa hoàn tất không."),
	}}
}

func flowStr(p *domain.Progress) string {
	if p == nil {
		return "<nil>"
	}
	return string(p.Flow)
}

func intsToStr(nums []int) string {
	parts := make([]string, len(nums))
	for i, n := range nums {
		parts[i] = fmt.Sprintf("%d", n)
	}
	return strings.Join(parts, ", ")
}
