package tools

import (
	"fmt"
	"slices"
	"sync"

	"github.com/voocel/ainovel-cli/internal/store"
	"github.com/voocel/ainovel-cli/internal/stylestat"
)

// StyleStatsIndex 把 Store 中的已完成章节同步到增量统计器。
// 首次 Snapshot 全量恢复一次；之后只加载新增章节，重写由 commit_chapter 主动刷新。
type StyleStatsIndex struct {
	store *store.Store

	mu        sync.Mutex
	tracker   *stylestat.Tracker
	completed map[int]struct{}
}

func NewStyleStatsIndex(store *store.Store) *StyleStatsIndex {
	return &StyleStatsIndex{store: store}
}

func (s *StyleStatsIndex) Snapshot(
	completedChapters []int,
	titles, stopwords []string,
) (*stylestat.Stats, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	completed, wanted, err := normalizeCompletedChapters(completedChapters)
	if err != nil {
		return nil, err
	}

	if s.tracker == nil {
		tracker := stylestat.NewTracker()
		for _, chapter := range completed {
			text, err := s.loadChapter(chapter)
			if err != nil {
				return nil, err
			}
			tracker.Upsert(chapter, text)
		}
		s.tracker = tracker
		s.completed = wanted
		return tracker.Snapshot(titles, stopwords), nil
	}

	type chapterText struct {
		chapter int
		text    string
	}
	var additions []chapterText
	for _, chapter := range completed {
		if _, ok := s.completed[chapter]; ok {
			continue
		}
		text, err := s.loadChapter(chapter)
		if err != nil {
			return nil, err
		}
		additions = append(additions, chapterText{chapter: chapter, text: text})
	}

	for chapter := range s.completed {
		if _, ok := wanted[chapter]; ok {
			continue
		}
		s.tracker.Remove(chapter)
	}
	for _, addition := range additions {
		s.tracker.Upsert(addition.chapter, addition.text)
	}
	s.completed = wanted
	return s.tracker.Snapshot(titles, stopwords), nil
}

// ChapterCommitted 在提交 Saga 完整成功后刷新一章。索引尚未初始化时，
// 下一次 Snapshot 会从 Progress 事实一次性恢复。
func (s *StyleStatsIndex) ChapterCommitted(chapter int, text string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.tracker == nil {
		return
	}
	s.tracker.Upsert(chapter, text)
	s.completed[chapter] = struct{}{}
}

func (s *StyleStatsIndex) loadChapter(chapter int) (string, error) {
	text, err := s.store.Drafts.LoadChapterText(chapter)
	if err != nil {
		return "", fmt.Errorf("读取第 %d 章终稿: %w", chapter, err)
	}
	if text == "" {
		return "", fmt.Errorf("第 %d 章已标记完成但终稿不存在", chapter)
	}
	return text, nil
}

func normalizeCompletedChapters(chapters []int) ([]int, map[int]struct{}, error) {
	normalized := slices.Clone(chapters)
	slices.Sort(normalized)
	set := make(map[int]struct{}, len(normalized))
	for _, chapter := range normalized {
		if chapter <= 0 {
			return nil, nil, fmt.Errorf("已完成章节号必须大于 0，实际为 %d", chapter)
		}
		if _, exists := set[chapter]; exists {
			return nil, nil, fmt.Errorf("已完成章节重复：第 %d 章", chapter)
		}
		set[chapter] = struct{}{}
	}
	return normalized, set, nil
}
