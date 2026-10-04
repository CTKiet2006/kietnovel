package flow

import (
	"fmt"

	"github.com/CTKiet2006/kietnovel/internal/domain"
	storepkg "github.com/CTKiet2006/kietnovel/internal/store"
)

// LoadState reads every fact Route needs from the Store.
// This is the routing "IO boundary": all reads are concentrated here and Route stays pure.
// Any read failure returns an error; a corrupt artifact and "not generated yet" are two different facts, and the Router must not
// keep dispatching on an incomplete snapshot.
func LoadState(store *storepkg.Store) (State, error) {
	var s State
	missing, err := store.FoundationMissing()
	if err != nil {
		return s, fmt.Errorf("load foundation state: %w", err)
	}
	s.FoundationMissing = missing
	if store.BookLanguage != nil {
		if lang, err := store.BookLanguage.Load(); err == nil && lang != "" {
			s.Language = lang
		}
	}
	// Planning tier: written to RunMeta when save_foundation persists scale, and the completion branch derives the planner from it.
	// A read failure is treated as unknown (empty tier → completion goes to LLM arbitration), consistent with the conservative default of the other facts.
	meta, err := store.RunMeta.Load()
	if err != nil {
		return s, fmt.Errorf("load run meta: %w", err)
	}
	if meta != nil {
		s.PlanningTier = meta.PlanningTier
	}
	progress, err := store.Progress.Load()
	if err != nil {
		return s, fmt.Errorf("load progress: %w", err)
	}
	if progress == nil {
		return s, nil
	}
	s.Progress = progress
	feedback, err := store.Outline.LoadPendingOutlineFeedback()
	if err != nil {
		return s, fmt.Errorf("load outline feedback: %w", err)
	}
	for _, item := range feedback {
		if item.RequiresImmediateReview() {
			s.ImmediateFeedbackCount++
		}
	}

	s.LastCompleted = progress.LatestCompleted()

	// The arc boundary is only computed in layered mode and only when there is a completed chapter
	if progress.Layered && s.LastCompleted > 0 {
		boundaries, err := store.Outline.CompletedArcBoundaries(s.LastCompleted)
		if err != nil {
			return s, fmt.Errorf("load completed arc boundaries: %w", err)
		}
		for i := range boundaries {
			boundary := &boundaries[i]
			hasReview, err := store.World.HasArcReview(boundary.EndChapter)
			if err != nil {
				return s, fmt.Errorf("load arc review: %w", err)
			}
			if !hasReview {
				s.AggregateRefresh = aggregateRefresh(AggregateArcReview, boundary)
				break
			}
			hasArcSummary, err := store.Summaries.HasArcSummary(boundary.Volume, boundary.Arc)
			if err != nil {
				return s, fmt.Errorf("load arc summary: %w", err)
			}
			if !hasArcSummary {
				s.AggregateRefresh = aggregateRefresh(AggregateArcSummary, boundary)
				break
			}
			if boundary.IsVolumeEnd {
				hasVolumeSummary, err := store.Summaries.HasVolumeSummary(boundary.Volume)
				if err != nil {
					return s, fmt.Errorf("load volume summary: %w", err)
				}
				if !hasVolumeSummary {
					s.AggregateRefresh = aggregateRefresh(AggregateVolumeSummary, boundary)
					break
				}
			}
		}

		boundary, err := store.Outline.CheckArcBoundary(s.LastCompleted)
		if err != nil {
			return s, fmt.Errorf("check arc boundary: %w", err)
		}
		if boundary != nil {
			s.ArcBoundary = boundary
			if boundary.IsArcEnd {
				s.HasArcReview, err = store.World.HasArcReview(s.LastCompleted)
				if err != nil {
					return s, fmt.Errorf("load arc review: %w", err)
				}
				s.HasArcSummary, err = store.Summaries.HasArcSummary(boundary.Volume, boundary.Arc)
				if err != nil {
					return s, fmt.Errorf("load arc summary: %w", err)
				}
				if boundary.IsVolumeEnd {
					s.HasVolumeSummary, err = store.Summaries.HasVolumeSummary(boundary.Volume)
					if err != nil {
						return s, fmt.Errorf("load volume summary: %w", err)
					}
				}
			}
		}
	}

	// Non-layered global review fact: only read from disk at the trigger point (Route does not consume this field for the other combinations).
	if !progress.Layered && s.LastCompleted > 0 {
		for completed := domain.ReviewInterval; completed <= len(progress.CompletedChapters); completed += domain.ReviewInterval {
			chapter := progress.CompletedChapters[completed-1]
			hasReview, err := store.World.HasGlobalReview(chapter)
			if err != nil {
				return s, fmt.Errorf("load global review: %w", err)
			}
			if !hasReview {
				s.AggregateRefresh = &AggregateRefresh{Kind: AggregateGlobalReview, EndChapter: chapter}
				break
			}
		}
		if due, _ := domain.ShouldReview(len(progress.CompletedChapters)); due {
			s.HasGlobalReview, err = store.World.HasGlobalReview(s.LastCompleted)
			if err != nil {
				return s, fmt.Errorf("load global review: %w", err)
			}
		}
	}

	return s, nil
}

func aggregateRefresh(kind AggregateKind, boundary *storepkg.ArcBoundary) *AggregateRefresh {
	return &AggregateRefresh{
		Kind: kind, Volume: boundary.Volume, Arc: boundary.Arc,
		StartChapter: boundary.StartChapter, EndChapter: boundary.EndChapter,
	}
}
