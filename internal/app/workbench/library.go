package workbench

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/voocel/ainovel-cli/internal/app/novel"
	"github.com/voocel/ainovel-cli/internal/domain/model"
)

// LibraryEntry 的 Target 是全书章数：用户固定或 AI 收官承诺；0 表示尚未确定（D63）。
type LibraryEntry struct {
	ID, Premise     string
	Written, Target int
	State           model.CreationRunState
	UpdatedAt       time.Time
}

func (s *Query) Library(ctx context.Context) ([]LibraryEntry, error) {
	summaries, err := s.store.ListProjectSummaries(ctx)
	if err != nil {
		return nil, err
	}
	entries := make([]LibraryEntry, 0, len(summaries))
	for _, summary := range summaries {
		var intent model.Intent
		if err := json.Unmarshal(summary.Intent, &intent); err != nil {
			return nil, fmt.Errorf("read library intent %s: %w", summary.ID, err)
		}
		entry := LibraryEntry{ID: summary.ID, Premise: intent.Premise, Written: summary.Written}
		if len(summary.Compass) > 0 {
			var compass model.Compass
			if err := json.Unmarshal(summary.Compass, &compass); err != nil {
				return nil, fmt.Errorf("read library compass %s: %w", summary.ID, err)
			}
			entry.Target = novel.CommittedFinal(&compass, summary.Planned)
		}
		run, hasRun, err := s.runs.LatestCreationRun(ctx, summary.ID)
		if err != nil {
			return nil, err
		}
		if hasRun {
			entry.State, entry.UpdatedAt = run.State, run.UpdatedAt
			if run.Goal.Kind == model.GoalNovel {
				goal, err := model.DecodeNovelGoal(run.Goal)
				if err != nil {
					return nil, err
				}
				if goal.TargetChapters > 0 {
					entry.Target = goal.TargetChapters
				}
			}
		}
		entries = append(entries, entry)
	}
	slices.SortStableFunc(entries, func(a, b LibraryEntry) int { return b.UpdatedAt.Compare(a.UpdatedAt) })
	return entries, nil
}
