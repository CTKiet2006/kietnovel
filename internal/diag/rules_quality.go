package diag

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/CTKiet2006/kietnovel/internal/i18n"
)

// ChronicLowDimension detects a review dimension that stays low across many chapters.
func ChronicLowDimension(snap *Snapshot) []Finding {
	if len(snap.Reviews) < 2 {
		return nil
	}

	dimSums := make(map[string]float64)
	dimCounts := make(map[string]int)
	for _, r := range snap.Reviews {
		for _, d := range r.Dimensions {
			dimSums[d.Dimension] += float64(d.Score)
			dimCounts[d.Dimension]++
		}
	}

	var findings []Finding
	for name, sum := range dimSums {
		count := dimCounts[name]
		if count < 2 {
			continue
		}
		avg := sum / float64(count)
		if avg >= ThresholdDimScoreLow {
			continue
		}
		findings = append(findings, Finding{
			Rule:       "ChronicLowDimension",
			Category:   CatQuality,
			Severity:   SevWarning,
			Confidence: ConfMedium,
			AutoLevel:  AutoNone,
			Target:     "prompt.writer",
			Title:      i18n.Tf("Chiều [%s] điểm thấp kéo dài (trung bình %.0f)", name, avg),
			Evidence:   i18n.Tf("Tổng %d lần đánh giá, điểm trung bình %.1f", count, avg),
			Suggestion: i18n.Tf("Kiểm tra hướng dẫn về %s trong prompt Writer có rõ không, hoặc tiêu chuẩn chấm %s trong prompt Editor có hợp lý không.", name, name),
		})
	}
	return findings
}

// ContractMissPattern detects a contract fulfilment rate that is too low.
func ContractMissPattern(snap *Snapshot) []Finding {
	if len(snap.Reviews) == 0 {
		return nil
	}

	var total, missed int
	var missedChapters []string
	for ch, r := range snap.Reviews {
		total++
		if r.ContractStatus == "partial" || r.ContractStatus == "missed" {
			missed++
			missedChapters = append(missedChapters, fmt.Sprintf("ch%d", ch))
		}
	}
	if total == 0 {
		return nil
	}
	rate := float64(missed) / float64(total)
	if rate <= ThresholdContractMissRate {
		return nil
	}
	return []Finding{{
		Rule:       "ContractMissPattern",
		Category:   CatQuality,
		Severity:   SevWarning,
		Confidence: ConfMedium,
		AutoLevel:  AutoNone,
		Target:     "prompt.writer",
		Title:      i18n.Tf("Tỉ lệ thực hiện hợp đồng thấp (%.0f%% chưa đạt)", rate*100),
		Evidence:   i18n.Tf("Chưa đạt: [%s], tổng %d/%d", strings.Join(missedChapters, ", "), missed, total),
		Suggestion: i18n.T("Có thể Writer chưa đọc contract, hoặc contract.required_beats quá khắt khe. Kiểm tra sự phối hợp giữa plan_chapter và writer.md."),
	}}
}

// HookWeakChain detects chapter hook scores that stay weak in a row.
func HookWeakChain(snap *Snapshot) []Finding {
	if len(snap.Reviews) < ThresholdHookWeakChain {
		return nil
	}

	chapters := sortedChapterReviews(snap)
	var weakChain []int
	for _, ch := range chapters {
		review := snap.Reviews[ch]
		if review == nil || review.Scope != "chapter" {
			continue
		}
		hook := review.Dimension("hook")
		if hook == nil || hook.Score >= ThresholdHookWeakScore {
			if len(weakChain) >= ThresholdHookWeakChain {
				break
			}
			weakChain = weakChain[:0]
			continue
		}
		weakChain = append(weakChain, ch)
	}
	if len(weakChain) < ThresholdHookWeakChain {
		return nil
	}

	var parts []string
	for _, ch := range weakChain {
		if hook := snap.Reviews[ch].Dimension("hook"); hook != nil {
			parts = append(parts, fmt.Sprintf("ch%d(%d)", ch, hook.Score))
		}
	}
	return []Finding{{
		Rule:       "HookWeakChain",
		Category:   CatQuality,
		Severity:   SevWarning,
		Confidence: ConfMedium,
		AutoLevel:  AutoNone,
		Target:     "prompt.writer",
		Title:      i18n.Tf("Móc cuối chương liên tục yếu (liên tiếp %d chương)", len(weakChain)),
		Evidence:   strings.Join(parts, ", "),
		Suggestion: i18n.T("Kiểm tra việc thực hiện hook_goal trong writer.md có rõ không, cần thì nêu rõ ham muốn đọc tiếp của chương trong plan_chapter, và hiệu chỉnh tiêu chuẩn nêu bằng chứng cho hook của Editor."),
	}}
}

// PayoffMissPattern detects a chapter with payoff_points that is not fulfilled for a long time.
func PayoffMissPattern(snap *Snapshot) []Finding {
	var total, missed int
	var details []string
	for ch, plan := range snap.Plans {
		if plan == nil || len(plan.Contract.PayoffPoints) == 0 {
			continue
		}
		review := snap.Reviews[ch]
		if review == nil {
			continue
		}
		total++
		if review.ContractStatus == "partial" || review.ContractStatus == "missed" {
			missed++
			details = append(details, i18n.Tf("ch%d(%d payoff)", ch, len(plan.Contract.PayoffPoints)))
		}
	}
	if total < 2 {
		return nil
	}
	rate := float64(missed) / float64(total)
	if rate <= ThresholdPayoffMissRate {
		return nil
	}
	sort.Strings(details)
	return []Finding{{
		Rule:       "PayoffMissPattern",
		Category:   CatQuality,
		Severity:   SevWarning,
		Confidence: ConfMedium,
		AutoLevel:  AutoNone,
		Target:     "prompt.writer",
		Title:      i18n.Tf("Tỉ lệ hoàn thành điểm tình tiết hơi thấp (%.0f%% chưa đạt)", rate*100),
		Evidence:   i18n.Tf("Chưa hoàn thành ở các chương: [%s], tổng %d/%d", strings.Join(details, ", "), missed, total),
		Suggestion: i18n.T("Kiểm tra payoff_points của plan_chapter có quá nhiều hoặc quá rỗng không, đảm bảo Writer hoàn thành rõ ràng trong văn bản chứ không chỉ bày đặt."),
	}}
}

// ExcessiveRewrites detects a rewrite rate that is too high.
func ExcessiveRewrites(snap *Snapshot) []Finding {
	if len(snap.Reviews) < 2 {
		return nil
	}

	var total, rewrites int
	for _, r := range snap.Reviews {
		total++
		if r.Verdict == "rewrite" {
			rewrites++
		}
	}
	if total == 0 {
		return nil
	}
	rate := float64(rewrites) / float64(total)
	if rate <= ThresholdRewriteRate {
		return nil
	}
	return []Finding{{
		Rule:       "ExcessiveRewrites",
		Category:   CatQuality,
		Severity:   SevWarning,
		Confidence: ConfMedium,
		AutoLevel:  AutoNone,
		Target:     "prompt.editor",
		Title:      i18n.Tf("Tỉ lệ viết lại quá cao (%d/%d = %.0f%%)", rewrites, total, rate*100),
		Evidence:   i18n.Tf("Tổng %d lần đánh giá, %d lần rewrite", total, rewrites),
		Suggestion: i18n.T("Writer liên tục cho ra nội dung dưới ngưỡng của Editor. Kiểm tra tiêu chuẩn chất lượng trong prompt Writer đã khớp với tiêu chuẩn duyệt của Editor chưa."),
	}}
}

// WordCountAnomaly detects an abnormal chapter word count.
func WordCountAnomaly(snap *Snapshot) []Finding {
	if snap.Progress == nil || len(snap.Progress.ChapterWordCounts) < 3 {
		return nil
	}
	wc := snap.Progress.ChapterWordCounts

	var sum float64
	for _, w := range wc {
		sum += float64(w)
	}
	avg := sum / float64(len(wc))
	if avg == 0 {
		return nil
	}

	var anomalies []string
	for ch, w := range wc {
		ratio := float64(w) / avg
		if ratio < ThresholdWordShortRatio {
			anomalies = append(anomalies, i18n.Tf("ch%d(%d chữ, %.0f%%)", ch, w, ratio*100))
		} else if ratio > ThresholdWordLongRatio {
			anomalies = append(anomalies, i18n.Tf("ch%d(%d chữ, %.0f%%)", ch, w, ratio*100))
		}
	}
	if len(anomalies) == 0 {
		return nil
	}
	return []Finding{{
		Rule:       "WordCountAnomaly",
		Category:   CatQuality,
		Severity:   SevInfo,
		Confidence: ConfLow,
		AutoLevel:  AutoNone,
		Target:     "context.window",
		Title:      i18n.Tf("Số chữ của chương bất thường (trung bình %d chữ)", int(math.Round(avg))),
		Evidence:   strings.Join(anomalies, "; "),
		Suggestion: i18n.T("Chương quá ngắn có thể là output bị cắt (giới hạn token), chương quá dài có thể nuốt cửa sổ ngữ cảnh. Kiểm tra cấu hình max_tokens của model."),
	}}
}

func sortedChapterReviews(snap *Snapshot) []int {
	chapters := make([]int, 0, len(snap.Reviews))
	for ch := range snap.Reviews {
		chapters = append(chapters, ch)
	}
	sort.Ints(chapters)
	return chapters
}
