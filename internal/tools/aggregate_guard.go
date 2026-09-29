package tools

import (
	"fmt"

	"github.com/CTKiet2006/kietnovel/internal/errs"
	"github.com/CTKiet2006/kietnovel/internal/flow"
	"github.com/CTKiet2006/kietnovel/internal/store"
)

// requireAggregateTarget binds the Editor's new aggregate write to the only artifact the Router currently has outstanding.
// The target is derived purely from persisted facts: it does not rely on the task wording and does not trust chapter/volume/arc numbers the model filled in itself;
// an idempotent wrap-up that would write content already on disk is recognised by each tool before it calls this function.
func requireAggregateTarget(st *store.Store, kind flow.AggregateKind, volume, arc, endChapter int) error {
	state, err := flow.LoadState(st)
	if err != nil {
		return fmt.Errorf("load aggregate state: %w: %w", errs.ErrStoreRead, err)
	}
	due := state.AggregateRefresh
	if due == nil {
		return fmt.Errorf("当前没有待处理的 %s 工件: %w", kind, errs.ErrToolPrecondition)
	}
	targetMismatch := due.Kind != kind
	switch kind {
	case flow.AggregateArcReview, flow.AggregateArcSummary:
		targetMismatch = targetMismatch || due.Volume != volume || due.Arc != arc
	case flow.AggregateVolumeSummary:
		targetMismatch = targetMismatch || due.Volume != volume
	case flow.AggregateGlobalReview:
		// A global review has no volume/arc coordinates; it is located only by kind and the end chapter.
	}
	endMismatch := endChapter > 0 && due.EndChapter != endChapter
	if targetMismatch || endMismatch {
		return fmt.Errorf(
			"聚合写入目标不匹配：当前应处理 kind=%s volume=%d arc=%d end_chapter=%d，收到 kind=%s volume=%d arc=%d end_chapter=%d: %w",
			due.Kind, due.Volume, due.Arc, due.EndChapter,
			kind, volume, arc, endChapter, errs.ErrToolConflict,
		)
	}
	return nil
}
