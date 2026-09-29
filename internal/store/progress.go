package store

import (
	"fmt"
	"os"
	"slices"

	"github.com/CTKiet2006/kietnovel/internal/domain"
	"github.com/CTKiet2006/kietnovel/internal/errs"
)

// ProgressStore manages the writing-progress state.
type ProgressStore struct{ io *IO }

func NewProgressStore(io *IO) *ProgressStore { return &ProgressStore{io: io} }

// Load reads meta/progress.json. It returns nil when the file does not exist.
func (s *ProgressStore) Load() (*domain.Progress, error) {
	s.io.mu.RLock()
	defer s.io.mu.RUnlock()
	return s.loadUnlocked()
}

func (s *ProgressStore) loadUnlocked() (*domain.Progress, error) {
	var p domain.Progress
	if err := s.io.ReadJSONUnlocked("meta/progress.json", &p); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return &p, nil
}

// Save stores the progress.
func (s *ProgressStore) Save(p *domain.Progress) error {
	s.io.mu.Lock()
	defer s.io.mu.Unlock()
	return s.saveUnlocked(p)
}

func (s *ProgressStore) saveUnlocked(p *domain.Progress) error {
	return s.io.WriteJSONUnlocked("meta/progress.json", p)
}

// Init creates the initial progress.
func (s *ProgressStore) Init(totalChapters int) error {
	return s.Save(&domain.Progress{
		Phase:         domain.PhaseInit,
		TotalChapters: totalChapters,
	})
}

// SetTotalChapters updates the outline capacity: the exact chapter count in non-layered mode, an internal estimate in layered mode.
func (s *ProgressStore) SetTotalChapters(n int) error {
	return s.io.WithWriteLock(func() error {
		p, err := s.loadUnlocked()
		if err != nil {
			return err
		}
		if p == nil {
			p = &domain.Progress{}
		}
		p.TotalChapters = n
		return s.saveUnlocked(p)
	})
}

// UpdatePhase updates the writing phase.
func (s *ProgressStore) UpdatePhase(phase domain.Phase) error {
	return s.io.WithWriteLock(func() error {
		p, err := s.loadUnlocked()
		if err != nil {
			return err
		}
		if p == nil {
			p = &domain.Progress{}
		}
		if err := domain.ValidatePhaseTransition(p.Phase, phase); err != nil {
			return err
		}
		p.Phase = phase
		return s.saveUnlocked(p)
	})
}

// AdvancePhase advances the writing phase at least up to phase; when a later phase has already been reached it stays unchanged.
// Meant for phase artifacts that may be saved repeatedly, so that rewriting an older artifact is never mistaken for a phase rollback.
func (s *ProgressStore) AdvancePhase(phase domain.Phase) error {
	return s.io.WithWriteLock(func() error {
		p, err := s.loadUnlocked()
		if err != nil {
			return err
		}
		if p == nil {
			p = &domain.Progress{}
		}
		if domain.CanTransitionPhase(phase, p.Phase) {
			return nil
		}
		if err := domain.ValidatePhaseTransition(p.Phase, phase); err != nil {
			return err
		}
		p.Phase = phase
		return s.saveUnlocked(p)
	})
}

// StartChapter marks a chapter as being written. It cannot carry the phase-transition duty; the caller must first have
// the foundation/import flow advance Progress explicitly to writing, so a wrong dispatch cannot bypass the planning phase.
func (s *ProgressStore) StartChapter(chapter int) error {
	if chapter <= 0 {
		return fmt.Errorf("chapter must be > 0")
	}
	return s.io.WithWriteLock(func() error {
		p, err := s.loadUnlocked()
		if err != nil {
			return err
		}
		if p == nil {
			return fmt.Errorf("progress 未初始化: %w", errs.ErrToolPrecondition)
		}
		if p.Phase != domain.PhaseWriting {
			return fmt.Errorf("章节写作仅允许在 writing 阶段（当前 phase=%s）: %w", p.Phase, errs.ErrToolPrecondition)
		}
		if p.Flow != domain.FlowRewriting && p.Flow != domain.FlowPolishing {
			p.Flow = domain.FlowWriting
		}
		if p.CurrentChapter < chapter {
			p.CurrentChapter = chapter
		}
		p.InProgressChapter = chapter
		p.CompletedScenes = nil
		return s.saveUnlocked(p)
	})
}

// IsChapterCompleted checks whether a chapter has been committed as complete. A read failure is returned explicitly; a corrupted
// progress must not be treated as "not completed" and then keep overwriting chapters.
func (s *ProgressStore) IsChapterCompleted(chapter int) (bool, error) {
	p, err := s.Load()
	if err != nil {
		return false, err
	}
	if p == nil {
		return false, nil
	}
	return slices.Contains(p.CompletedChapters, chapter), nil
}

// MarkChapterComplete marks a chapter complete and updates the progress atomically.
func (s *ProgressStore) MarkChapterComplete(chapter, wordCount int, hookType, dominantStrand string) error {
	return s.io.WithWriteLock(func() error {
		p, err := s.loadUnlocked()
		if err != nil {
			return err
		}
		if p == nil {
			return fmt.Errorf("progress not initialized, call Init first")
		}
		if p.ChapterWordCounts == nil {
			p.ChapterWordCounts = make(map[int]int)
		}
		if oldWC, ok := p.ChapterWordCounts[chapter]; ok {
			p.TotalWordCount -= oldWC
		}
		p.ChapterWordCounts[chapter] = wordCount
		p.TotalWordCount += wordCount
		if !slices.Contains(p.CompletedChapters, chapter) {
			p.CompletedChapters = append(p.CompletedChapters, chapter)
		}
		if chapter+1 > p.CurrentChapter {
			p.CurrentChapter = chapter + 1
		}
		p.InProgressChapter = 0
		p.CompletedScenes = nil
		if err := domain.ValidatePhaseTransition(p.Phase, domain.PhaseWriting); err != nil {
			return err
		}
		p.Phase = domain.PhaseWriting

		if dominantStrand != "" {
			for len(p.StrandHistory) < chapter-1 {
				p.StrandHistory = append(p.StrandHistory, "")
			}
			if len(p.StrandHistory) < chapter {
				p.StrandHistory = append(p.StrandHistory, dominantStrand)
			} else {
				p.StrandHistory[chapter-1] = dominantStrand
			}
		}
		if hookType != "" {
			for len(p.HookHistory) < chapter-1 {
				p.HookHistory = append(p.HookHistory, "")
			}
			if len(p.HookHistory) < chapter {
				p.HookHistory = append(p.HookHistory, hookType)
			} else {
				p.HookHistory[chapter-1] = hookType
			}
		}

		return s.saveUnlocked(p)
	})
}

// MarkComplete marks the writing of the whole book as finished and clears the reopened-rework flag (a finished book is no longer in the rework state).
func (s *ProgressStore) MarkComplete() error {
	return s.io.WithWriteLock(func() error {
		p, err := s.loadUnlocked()
		if err != nil {
			return err
		}
		if p == nil {
			p = &domain.Progress{}
		}
		if err := domain.ValidatePhaseTransition(p.Phase, domain.PhaseComplete); err != nil {
			return err
		}
		p.Phase = domain.PhaseComplete
		p.ReopenedFromComplete = false
		return s.saveUnlocked(p)
	})
}

// Reopen reopens an already finished book into the rework state: phase complete->writing + the target chapter is queued + flow=rewriting,
// all done atomically inside a single write lock. This is the one exempt exit from the phaseOrder "only moves forward" constraint - deliberately it does not go through
// ValidatePhaseTransition; the legality of the rollback is confined to this method and is protected by the phase=complete precondition,
// so that misuse cannot let the state machine run away. Once the queue has been fixed, commit_chapter automatically finishes the book again.
func (s *ProgressStore) Reopen(chapters []int, reason string) error {
	return s.io.WithWriteLock(func() error {
		p, err := s.loadUnlocked()
		if err != nil {
			return err
		}
		if p == nil {
			return fmt.Errorf("progress 未初始化: %w", errs.ErrToolPrecondition)
		}
		if p.Phase != domain.PhaseComplete {
			return fmt.Errorf("reopen 仅适用于已完结的书（当前 phase=%s）: %w", p.Phase, errs.ErrToolPrecondition)
		}
		normalized, err := normalizePendingRewrites(chapters, p.CompletedChapters)
		if err != nil {
			return err
		}
		p.Phase = domain.PhaseWriting // the only legal rollback, protected by the complete precondition described above
		p.PendingRewrites = normalized
		p.RewriteReason = reason
		p.Flow = domain.FlowRewriting
		p.ReopenedFromComplete = true // finish again with the structure intact after the queue is drained, see the commit_chapter drain block
		return s.saveUnlocked(p)
	})
}

// ReopenContinue reopens an already finished book as continue-writing: only phase complete->writing, no entry into the rework queue,
// and ReopenedFromComplete stays unset (that flag carries the drain semantics of "finish again automatically with the original structure once the rework is drained",
// whereas a continue-writing reopen is precisely meant to extend the structure). Like Reopen it is an exempt exit from the phaseOrder "only moves forward" constraint,
// equally protected by the phase=complete precondition; after reopening, the end-of-volume router dispatches the architect to continue with a new volume.
func (s *ProgressStore) ReopenContinue() error {
	return s.io.WithWriteLock(func() error {
		p, err := s.loadUnlocked()
		if err != nil {
			return err
		}
		if p == nil {
			return fmt.Errorf("progress 未初始化: %w", errs.ErrToolPrecondition)
		}
		if p.Phase != domain.PhaseComplete {
			return fmt.Errorf("重开仅适用于已完结的书（当前 phase=%s）: %w", p.Phase, errs.ErrToolPrecondition)
		}
		p.Phase = domain.PhaseWriting
		p.ReopenCount++ // auditing + it guarantees that the progress digest of the new finish differs from the previous one (see the field comment)
		return s.saveUnlocked(p)
	})
}

// ClearInProgress clears the transient progress state.
func (s *ProgressStore) ClearInProgress() error {
	return s.io.WithWriteLock(func() error {
		p, err := s.loadUnlocked()
		if err != nil {
			return err
		}
		if p == nil {
			return nil
		}
		p.InProgressChapter = 0
		p.CompletedScenes = nil
		return s.saveUnlocked(p)
	})
}

// UpdateVolumeArc updates the current volume/arc position.
func (s *ProgressStore) UpdateVolumeArc(volume, arc int) error {
	return s.io.WithWriteLock(func() error {
		p, err := s.loadUnlocked()
		if err != nil {
			return err
		}
		if p == nil {
			return nil
		}
		p.CurrentVolume = volume
		p.CurrentArc = arc
		return s.saveUnlocked(p)
	})
}

// SetLayered sets the layered-mode flag.
func (s *ProgressStore) SetLayered(layered bool) error {
	return s.io.WithWriteLock(func() error {
		p, err := s.loadUnlocked()
		if err != nil {
			return err
		}
		if p == nil {
			return nil
		}
		p.Layered = layered
		return s.saveUnlocked(p)
	})
}

// SetFlow updates the current flow state.
func (s *ProgressStore) SetFlow(flow domain.FlowState) error {
	return s.io.WithWriteLock(func() error {
		p, err := s.loadUnlocked()
		if err != nil {
			return err
		}
		if p == nil {
			return nil
		}
		if err := domain.ValidateFlowTransition(p.Flow, flow); err != nil {
			return err
		}
		p.Flow = flow
		return s.saveUnlocked(p)
	})
}

// SetPendingRewrites sets the queue of chapters pending a rewrite together with the reason.
// PendingRewrites may only contain completed chapters; an unfinished chapter has no final text yet and must not enter the rewrite/polish queue.
func (s *ProgressStore) SetPendingRewrites(chapters []int, reason string) error {
	return s.io.WithWriteLock(func() error {
		p, err := s.loadUnlocked()
		if err != nil {
			return err
		}
		if p == nil {
			return nil
		}
		normalized, err := normalizePendingRewrites(chapters, p.CompletedChapters)
		if err != nil {
			return err
		}
		p.PendingRewrites = normalized
		p.RewriteReason = reason
		return s.saveUnlocked(p)
	})
}

// ApplyReviewOutcome applies the flow state produced by a review atomically. The review semantics are decided by the layer above; the Store only
// validates the Flow transition and the reworked chapters, and guarantees that Flow, PendingRewrites and RewriteReason never show an intermediate state.
func (s *ProgressStore) ApplyReviewOutcome(flow domain.FlowState, chapters []int, reason string) (*domain.Progress, error) {
	var latest *domain.Progress
	err := s.io.WithWriteLock(func() error {
		p, err := s.loadUnlocked()
		if err != nil {
			return err
		}
		if p == nil {
			return fmt.Errorf("progress 未初始化: %w", errs.ErrToolPrecondition)
		}
		if len(chapters) > 0 {
			if flow == domain.FlowWriting {
				return fmt.Errorf("返工章节非空时 flow 不能为 writing: %w", errs.ErrToolConflict)
			}
			if err := domain.ValidateFlowTransition(p.Flow, flow); err != nil {
				return err
			}
			normalized, err := normalizePendingRewrites(chapters, p.CompletedChapters)
			if err != nil {
				return err
			}
			p.PendingRewrites = normalized
			p.RewriteReason = reason
			p.Flow = flow
		} else if len(p.PendingRewrites) == 0 {
			if err := domain.ValidateFlowTransition(p.Flow, flow); err != nil {
				return err
			}
			p.Flow = flow
		}
		if err := s.saveUnlocked(p); err != nil {
			return err
		}
		latest = p
		return nil
	})
	return latest, err
}

// ValidatePendingRewrites checks whether a chapter list may enter the rework queue; it does not modify any state.
func (s *ProgressStore) ValidatePendingRewrites(chapters []int) error {
	s.io.mu.RLock()
	defer s.io.mu.RUnlock()

	p, err := s.loadUnlocked()
	if err != nil {
		return err
	}
	if p == nil {
		_, err := normalizePendingRewrites(chapters, nil)
		return err
	}
	_, err = normalizePendingRewrites(chapters, p.CompletedChapters)
	return err
}

// CompleteRewrite removes a finished chapter from the pending-rewrite queue.
func (s *ProgressStore) CompleteRewrite(chapter int) error {
	return s.io.WithWriteLock(func() error {
		p, err := s.loadUnlocked()
		if err != nil {
			return err
		}
		if p == nil {
			return nil
		}
		var remaining []int
		for _, ch := range p.PendingRewrites {
			if ch != chapter {
				remaining = append(remaining, ch)
			}
		}
		p.PendingRewrites = remaining
		if len(remaining) == 0 {
			if err := domain.ValidateFlowTransition(p.Flow, domain.FlowWriting); err != nil {
				return err
			}
			p.Flow = domain.FlowWriting
			p.RewriteReason = ""
		}
		return s.saveUnlocked(p)
	})
}

// ClearPendingRewrites forcibly empties the rewrite queue.
func (s *ProgressStore) ClearPendingRewrites() error {
	return s.io.WithWriteLock(func() error {
		p, err := s.loadUnlocked()
		if err != nil {
			return err
		}
		if p == nil {
			return nil
		}
		p.PendingRewrites = nil
		p.RewriteReason = ""
		if err := domain.ValidateFlowTransition(p.Flow, domain.FlowWriting); err != nil {
			return err
		}
		p.Flow = domain.FlowWriting
		return s.saveUnlocked(p)
	})
}

// ValidateChapterWork checks whether the current chapter may be planned or committed.
// The Writer may only work in the writing phase; under the polish/rewrite flow it may only touch chapters that are in
// PendingRewrites. The phase constraint is enforced once more at the Store boundary so that a wrong Arbiter dispatch cannot bypass the Router.
func (s *ProgressStore) ValidateChapterWork(chapter int) error {
	p, err := s.Load()
	if err != nil {
		return err
	}
	if p == nil {
		return fmt.Errorf("progress 未初始化: %w", errs.ErrToolPrecondition)
	}
	if p.Phase != domain.PhaseWriting {
		return fmt.Errorf("章节写作仅允许在 writing 阶段（当前 phase=%s）: %w", p.Phase, errs.ErrToolPrecondition)
	}
	if p.Flow != domain.FlowRewriting && p.Flow != domain.FlowPolishing {
		return nil
	}
	if _, err := normalizePendingRewrites(p.PendingRewrites, p.CompletedChapters); err != nil {
		return err
	}
	if slices.Contains(p.PendingRewrites, chapter) {
		return nil
	}

	verb := "重写"
	if p.Flow == domain.FlowPolishing {
		verb = "打磨"
	}
	return fmt.Errorf("第 %d 章不在待%s队列中，当前队列：%v。请先处理队列内章节，再动新章节: %w", chapter, verb, p.PendingRewrites, errs.ErrToolConflict)
}

func normalizePendingRewrites(chapters, completed []int) ([]int, error) {
	if len(chapters) == 0 {
		return nil, nil
	}
	completedSet := make(map[int]struct{}, len(completed))
	for _, ch := range completed {
		completedSet[ch] = struct{}{}
	}

	seen := make(map[int]struct{}, len(chapters))
	normalized := make([]int, 0, len(chapters))
	var invalid []int
	for _, ch := range chapters {
		if ch <= 0 {
			invalid = append(invalid, ch)
			continue
		}
		if _, ok := completedSet[ch]; !ok {
			invalid = append(invalid, ch)
			continue
		}
		if _, ok := seen[ch]; ok {
			continue
		}
		seen[ch] = struct{}{}
		normalized = append(normalized, ch)
	}
	if len(invalid) > 0 {
		return nil, fmt.Errorf("pending_rewrites 只能包含已完成章节，非法章节：%v，completed_chapters=%v: %w", invalid, completed, errs.ErrToolPrecondition)
	}
	return normalized, nil
}
