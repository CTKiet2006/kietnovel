package store

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/voocel/ainovel-cli/internal/domain"
)

const pendingRevisionPath = "meta/pending_revision.json"

type RevisionStore struct{ io *IO }

func NewRevisionStore(io *IO) *RevisionStore { return &RevisionStore{io: io} }

func (s *RevisionStore) LoadPending() (*domain.PendingRevision, error) {
	var pending domain.PendingRevision
	if err := s.io.ReadJSON(pendingRevisionPath, &pending); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return &pending, nil
}

func (s *RevisionStore) SavePending(pending domain.PendingRevision) error {
	return s.io.WriteJSON(pendingRevisionPath, pending)
}

func (s *RevisionStore) ClearPending() error {
	return s.io.RemoveFile(pendingRevisionPath)
}

// InvalidateChapterAggregates 删除输入范围包含修订章节的模型派生工件。
// 章节级投影由 revision.Projector 单独重建。
func (s *Store) InvalidateChapterAggregates(fromChapter int) error {
	if fromChapter <= 0 {
		return fmt.Errorf("from chapter must be > 0")
	}
	volumes, err := s.Outline.LoadLayeredOutline()
	if err != nil {
		return fmt.Errorf("读取分层大纲: %w", err)
	}
	chapter := 1
	arcEnds := make(map[[2]int]int)
	for _, volume := range volumes {
		volumeEnd := chapter - 1
		for _, arc := range volume.Arcs {
			if len(arc.Chapters) == 0 {
				continue
			}
			end := chapter + len(arc.Chapters) - 1
			arcEnds[[2]int{volume.Index, arc.Index}] = end
			if end >= fromChapter {
				if err := s.Summaries.io.RemoveFile(fmt.Sprintf("summaries/arc-v%02da%02d.json", volume.Index, arc.Index)); err != nil {
					return err
				}
				if err := s.Characters.io.RemoveFile(fmt.Sprintf("meta/snapshots/v%02da%02d.json", volume.Index, arc.Index)); err != nil {
					return err
				}
			}
			chapter = end + 1
			volumeEnd = end
		}
		if volumeEnd >= fromChapter {
			if err := s.Summaries.io.RemoveFile(fmt.Sprintf("summaries/vol-v%02d.json", volume.Index)); err != nil {
				return err
			}
		}
	}
	if style, err := s.World.LoadStyleRules(); err != nil {
		return fmt.Errorf("读取写作规则: %w", err)
	} else if style != nil {
		end, ok := arcEnds[[2]int{style.Volume, style.Arc}]
		if !ok {
			return fmt.Errorf("写作规则引用未知弧 V%dA%d", style.Volume, style.Arc)
		}
		if end >= fromChapter {
			if err := s.World.io.RemoveFile("meta/style_rules.json"); err != nil {
				return err
			}
		}
	}
	return s.invalidateReviewsFrom(fromChapter)
}

func (s *Store) invalidateReviewsFrom(fromChapter int) error {
	entries, err := os.ReadDir(s.World.io.path("reviews"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		rel := "reviews/" + entry.Name()
		data, err := s.World.io.ReadFile(rel)
		if err != nil {
			return err
		}
		var review domain.ReviewEntry
		if err := json.Unmarshal(data, &review); err != nil {
			return fmt.Errorf("parse %s: %w", rel, err)
		}
		if review.Chapter >= fromChapter {
			if err := s.World.io.RemoveFile(rel); err != nil {
				return err
			}
		}
	}
	return nil
}
