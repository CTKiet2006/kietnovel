package store

import (
	"fmt"

	"github.com/CTKiet2006/kietnovel/internal/domain"
)

// BuildCast builds the current supporting-cast view from the acceptance records.
func (s *Store) BuildCast(chapters []int) ([]domain.CastEntry, error) {
	records, err := s.ChapterRecords.LoadCompleted(chapters)
	if err != nil {
		return nil, fmt.Errorf("读取章节记录: %w", err)
	}
	characters, err := s.Characters.Load()
	if err != nil {
		return nil, fmt.Errorf("读取核心角色: %w", err)
	}
	return domain.ProjectCast(records, characters), nil
}
