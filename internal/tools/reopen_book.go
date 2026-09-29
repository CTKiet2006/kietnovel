package tools

import (
	"fmt"
	"slices"

	"github.com/CTKiet2006/kietnovel/internal/domain"
	"github.com/CTKiet2006/kietnovel/internal/errs"
	"github.com/CTKiet2006/kietnovel/internal/store"
)

// ReopenBook reopens an already finished book into the rework state; the Engine calls it at intervention action boundaries.
// Once the book is complete, completePhaseGate hard-blocks every subagent dispatch, so the user cannot rework already written chapters.
// This function does not go through a subagent and is callable during the complete phase: it atomically switches phase back to writing and puts the target chapter into
// PendingRewrites with flow=rewriting; afterwards the Flow Router dispatches the writer chapter by chapter over the existing rework queue,
// and once the queue drains, commit_chapter automatically re-wraps and completes the book again. None of the Gate / Router / edit / commit core logic needs to change.
func ReopenBook(s *store.Store, chapters []int, reason string) error {
	if len(chapters) == 0 {
		return fmt.Errorf("chapters 不能为空，需指明要返工的章节: %w", errs.ErrToolArgs)
	}

	progress, err := s.Progress.Load()
	if err != nil {
		return fmt.Errorf("load progress: %w: %w", errs.ErrStoreRead, err)
	}
	if progress == nil {
		return fmt.Errorf("progress 未初始化: %w", errs.ErrToolPrecondition)
	}
	// Only already written chapters may be reworked; a chapter number outside the completed set is a continuation / out-of-range write, so it is explicitly rejected and the user is steered towards adjusting the length instead.
	var invalid []int
	for _, ch := range chapters {
		if !slices.Contains(progress.CompletedChapters, ch) {
			invalid = append(invalid, ch)
		}
	}
	if len(invalid) > 0 {
		return fmt.Errorf("第 %v 章尚未写完，reopen 只能返工已完成章节（新增/扩展剧情请走篇幅调整）: %w", invalid, errs.ErrToolPrecondition)
	}

	// The phase precondition is backed up inside store.Reopen (callable only in complete).
	if err := s.Progress.Reopen(chapters, reason); err != nil {
		return fmt.Errorf("reopen: %w: %w", errs.ErrStoreWrite, err)
	}

	// checkpoint: symmetric with complete_book (GlobalScope + meta/progress.json).
	if _, err := s.Checkpoints.AppendArtifact(domain.GlobalScope(), "reopen", "meta/progress.json"); err != nil {
		return fmt.Errorf("checkpoint reopen: %w: %w", errs.ErrStoreWrite, err)
	}
	return nil
}
