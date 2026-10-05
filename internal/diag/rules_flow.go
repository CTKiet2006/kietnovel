package diag

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/utils"
)

// InvalidPendingRewrites 检测返工队列里混入未完成章节。
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
		Title:      fmt.Sprintf("返工队列包含未完成章节: [%s]", intsToStr(invalid)),
		Evidence:   fmt.Sprintf("pending_rewrites=[%s], completed_chapters=[%s], flow=%s", intsToStr(p.PendingRewrites), intsToStr(completed), p.Flow),
		Suggestion: "这是状态不变量损坏。请停止运行后编辑 meta/progress.json，移除 pending_rewrites 中未完成章节；若队列为空，将 flow 改为 writing 并清空 rewrite_reason。",
	}}
}

// RewritePendingPressure 检测存在待改写章节（当前仅检测状态存在，不判定停滞）。
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
		Title:      fmt.Sprintf("待改写章节: [%s]", chapters),
		Evidence:   fmt.Sprintf("flow=%s, pending_rewrites=[%s]", p.Flow, chapters),
		Suggestion: "检查 Editor 评审标准是否过严，或 Writer 改写 prompt 是否有效。" +
			"某章返工反复失败时引擎会自动将其移出队列并继续后续创作，无需人工清理。",
	}}
}

// OrphanedSteer 检测未消费的用户转向指令。
func OrphanedSteer(snap *Snapshot) []Finding {
	if snap.RunMeta == nil || snap.RunMeta.PendingSteer == "" {
		return nil
	}
	if snap.Progress != nil && snap.Progress.Flow == domain.FlowSteering {
		return nil // 正在处理中，不算孤立
	}
	return []Finding{{
		Rule:       "OrphanedSteer",
		Category:   CatFlow,
		Severity:   SevWarning,
		Confidence: ConfHigh,
		AutoLevel:  AutoSafe,
		Target:     "runtime.recovery",
		Title:      "存在未消费的转向指令",
		Evidence:   fmt.Sprintf("pending_steer=%q, flow=%s", utils.TruncateRunes(snap.RunMeta.PendingSteer, 60), flowStr(snap.Progress)),
		Suggestion: "该 steer 被持久化但未被干预裁定流程消费。检查中断恢复逻辑，或通过重新提交覆盖。",
	}}
}

// PhaseFlowMismatch 检测阶段与流程状态不匹配。
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
		Title:      fmt.Sprintf("阶段/流程状态不匹配: phase=%s, flow=%s", p.Phase, p.Flow),
		Evidence:   fmt.Sprintf("phase=%s 不应出现非初始 flow=%s", p.Phase, p.Flow),
		Suggestion: "状态机可能损坏，需手动检查 meta/progress.json 的 phase 和 flow 字段。",
	}}
}

// ChapterGaps 检测已完成章节列表中的跳号。
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
		Title:      fmt.Sprintf("章节跳号: 缺少 [%s]", intsToStr(gaps)),
		Evidence:   fmt.Sprintf("completed=[%s]", intsToStr(sorted)),
		Suggestion: "commit_chapter 可能中途中断。检查 meta/pending_commit.json 是否存在未完成提交。",
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
