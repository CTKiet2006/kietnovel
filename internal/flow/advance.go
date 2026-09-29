package flow

import (
	"fmt"

	"github.com/CTKiet2006/kietnovel/internal/domain"
)

// StartsForwardChapter reports whether an instruction will start a new forward chapter that is not yet finished.
// It only reads facts and does not decide whether to pass; the Task/Reason wording plays no part in the decision.
func StartsForwardChapter(inst *Instruction, progress *domain.Progress, pending *domain.PendingCommit) bool {
	if inst == nil || inst.Agent != "writer" || progress == nil || progress.Phase != domain.PhaseWriting {
		return false
	}
	if pending != nil || len(progress.PendingRewrites) > 0 || progress.InProgressChapter > 0 {
		return false
	}
	target := inst.Chapter
	if target == 0 {
		target = progress.NextChapter()
	}
	return target > 0 && target == progress.NextChapter()
}

// AdvanceHoldResolution is the outcome of resolving a one-shot pause against the current facts.
type AdvanceHoldResolution int

const (
	AdvanceHoldKeep AdvanceHoldResolution = iota
	AdvanceHoldConsume
	AdvanceHoldConsumeAndStop
)

// ResolveAdvanceHold is a pure function that resolves a one-shot pause. An unknown condition or a missing fact is an
// explicit error; silently degrading to "keep running" is not allowed.
func ResolveAdvanceHold(hold *domain.AdvanceHold, progress *domain.Progress) (AdvanceHoldResolution, error) {
	if hold == nil {
		return AdvanceHoldKeep, nil
	}
	if err := hold.Validate(); err != nil {
		return AdvanceHoldKeep, err
	}
	if progress == nil {
		return AdvanceHoldKeep, fmt.Errorf("缺少 Progress，无法解析一次性暂停")
	}
	if progress.Phase == domain.PhaseComplete {
		return AdvanceHoldConsume, nil
	}
	if progress.Phase != domain.PhaseWriting {
		return AdvanceHoldKeep, fmt.Errorf("一次性暂停仅适用于 writing/complete 阶段（当前 %s）", progress.Phase)
	}
	switch hold.After {
	case domain.AdvanceHoldAtBoundary:
		return AdvanceHoldConsumeAndStop, nil
	case domain.AdvanceHoldAfterRewritesDrained:
		if len(progress.PendingRewrites) > 0 {
			return AdvanceHoldKeep, nil
		}
		return AdvanceHoldConsumeAndStop, nil
	case domain.AdvanceHoldAtChapter:
		if progress.LatestCompleted() < hold.TargetChapter {
			return AdvanceHoldKeep, nil
		}
		return AdvanceHoldConsumeAndStop, nil
	default:
		return AdvanceHoldKeep, fmt.Errorf("不支持的一次性暂停条件 %q", hold.After)
	}
}
