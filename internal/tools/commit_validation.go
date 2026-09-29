package tools

import (
	"fmt"

	"github.com/CTKiet2006/kietnovel/internal/chapterfacts"
	"github.com/CTKiet2006/kietnovel/internal/errs"
)

// validateCommitArgs validates the model's full semantic payload before the PendingCommit is created.
// Errors go straight back to the model to fix; no half-baked state is produced and no missing value is guessed.
func (t *CommitChapterTool) validateCommitArgs(a commitArgs) error {
	if err := chapterfacts.Validate(a.ChapterFacts); err != nil {
		return fmt.Errorf("%v: %w", err, errs.ErrToolArgs)
	}

	if len(a.ForeshadowUpdates) > 0 {
		ledger, err := t.store.World.LoadForeshadowLedger()
		if err != nil {
			return fmt.Errorf("load foreshadow ledger: %w: %w", errs.ErrStoreRead, err)
		}
		// The ledger is a whole-book projection, while the Projector replays chapter records in chapter order. When an early chapter is
		// is rewritten, the ledger still holds foreshadowing that only later chapters planted -- letting those through makes the pre-commit validation
		// contradict the replay outcome, the model has no way to correct it, and the rework queue then deadlocks. So "visible in this chapter" is always the yardstick.
		plantedAt := make(map[string]int, len(ledger))
		for _, entry := range ledger {
			plantedAt[entry.ID] = entry.PlantedAt
		}
		declared := make(map[string]struct{}, len(a.ForeshadowUpdates))
		for i, update := range a.ForeshadowUpdates {
			switch update.Action {
			case "plant":
				declared[update.ID] = struct{}{}
			case "advance", "resolve":
				if _, ok := declared[update.ID]; ok {
					continue
				}
				at, known := plantedAt[update.ID]
				if !known {
					return fmt.Errorf("foreshadow_updates[%d] references unknown id %q: %w", i, update.ID, errs.ErrToolPrecondition)
				}
				if at > a.Chapter {
					return fmt.Errorf("foreshadow_updates[%d] 伏笔 %q 种植于第 %d 章，不能在第 %d 章推进或回收: %w",
						i, update.ID, at, a.Chapter, errs.ErrToolPrecondition)
				}
			}
		}
	}
	return nil
}
