package stylestat

import (
	"slices"
	"sort"
	"sync"

	"github.com/voocel/ainovel-cli/internal/utils"
)

// Tracker 按章节维护全书风格统计。首次载入每章一次；新增或重写时只分析变化章节。
// Snapshot 会缓存派生结果，同一书状态下 Writer/Editor 的重复读取不再重算。
type Tracker struct {
	mu sync.Mutex

	chapters      map[int]chapterStats
	patternTotals []int
	sentences     map[string]sentenceAggregate
	openingHits   int
	version       uint64

	cacheVersion   uint64
	cacheTitles    []string
	cacheStopwords []string
	cacheReady     bool
	cache          *Stats
}

type chapterStats struct {
	text        string
	patterns    []int
	sentences   map[string]int
	endingRunes int
	hasEnding   bool
	openingTime bool
}

type sentenceAggregate struct {
	count    int
	chapters int
}

func NewTracker() *Tracker {
	return &Tracker{
		chapters:      make(map[int]chapterStats),
		patternTotals: make([]int, len(patternDefs)),
		sentences:     make(map[string]sentenceAggregate),
	}
}

// Upsert 新增或替换一章。正文未变化时不推进版本。
func (t *Tracker) Upsert(chapter int, text string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.ensure()

	if previous, ok := t.chapters[chapter]; ok {
		if previous.text == text {
			return
		}
		t.subtract(previous)
	}
	current := analyzeChapter(text)
	t.add(current)
	t.chapters[chapter] = current
	t.version++
	t.cacheReady = false
}

// Remove 删除一章；不存在时无操作。
func (t *Tracker) Remove(chapter int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.ensure()

	previous, ok := t.chapters[chapter]
	if !ok {
		return
	}
	t.subtract(previous)
	delete(t.chapters, chapter)
	t.version++
	t.cacheReady = false
}

// Snapshot 返回与 Compute 等价的当前统计快照。
func (t *Tracker) Snapshot(titles, stopwords []string) *Stats {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.ensure()

	if t.cacheReady &&
		t.cacheVersion == t.version &&
		slices.Equal(t.cacheTitles, titles) &&
		slices.Equal(t.cacheStopwords, stopwords) {
		return cloneStats(t.cache)
	}

	stats := t.snapshot(titles, stopwords)
	t.cache = stats
	t.cacheVersion = t.version
	t.cacheTitles = append(t.cacheTitles[:0], titles...)
	t.cacheStopwords = append(t.cacheStopwords[:0], stopwords...)
	t.cacheReady = true
	return cloneStats(stats)
}

func (t *Tracker) ensure() {
	if t.chapters == nil {
		t.chapters = make(map[int]chapterStats)
	}
	if len(t.patternTotals) != len(patternDefs) {
		t.patternTotals = make([]int, len(patternDefs))
	}
	if t.sentences == nil {
		t.sentences = make(map[string]sentenceAggregate)
	}
}

func (t *Tracker) add(chapter chapterStats) {
	for i, count := range chapter.patterns {
		t.patternTotals[i] += count
	}
	for sentence, count := range chapter.sentences {
		aggregate := t.sentences[sentence]
		aggregate.count += count
		aggregate.chapters++
		t.sentences[sentence] = aggregate
	}
	if chapter.openingTime {
		t.openingHits++
	}
}

func (t *Tracker) subtract(chapter chapterStats) {
	for i, count := range chapter.patterns {
		t.patternTotals[i] -= count
	}
	for sentence, count := range chapter.sentences {
		aggregate := t.sentences[sentence]
		aggregate.count -= count
		aggregate.chapters--
		if aggregate.count == 0 {
			delete(t.sentences, sentence)
			continue
		}
		t.sentences[sentence] = aggregate
	}
	if chapter.openingTime {
		t.openingHits--
	}
}

func (t *Tracker) snapshot(titles, stopwords []string) *Stats {
	n := len(t.chapters)
	if n < minChapters {
		return nil
	}

	ids := make([]int, 0, n)
	for chapter := range t.chapters {
		ids = append(ids, chapter)
	}
	sort.Ints(ids)

	stats := &Stats{Chapters: n}
	for i, total := range t.patternTotals {
		if total == 0 {
			continue
		}
		stats.Patterns = append(stats.Patterns, PatternStat{
			Name:       patternDefs[i].name,
			Total:      total,
			PerChapter: round1(float64(total) / float64(n)),
		})
	}

	recentStart := max(0, len(ids)-phraseWindow)
	recent := make([]string, 0, len(ids)-recentStart)
	for _, chapter := range ids[recentStart:] {
		recent = append(recent, t.chapters[chapter].text)
	}
	stats.TopPhrases = minePhrases(recent, stopwords)

	for sentence, aggregate := range t.sentences {
		if aggregate.chapters < 3 {
			continue
		}
		stats.RepeatedSentences = append(stats.RepeatedSentences, SentenceStat{
			Text:     utils.TruncateRunes(sentence, 40),
			Chapters: aggregate.chapters,
			Count:    aggregate.count,
		})
	}
	sort.Slice(stats.RepeatedSentences, func(i, j int) bool {
		if stats.RepeatedSentences[i].Count != stats.RepeatedSentences[j].Count {
			return stats.RepeatedSentences[i].Count > stats.RepeatedSentences[j].Count
		}
		return stats.RepeatedSentences[i].Text < stats.RepeatedSentences[j].Text
	})
	if len(stats.RepeatedSentences) > 5 {
		stats.RepeatedSentences = stats.RepeatedSentences[:5]
	}

	var endingLengths []int
	shortEndings := 0
	for _, chapter := range t.chapters {
		if !chapter.hasEnding {
			continue
		}
		endingLengths = append(endingLengths, chapter.endingRunes)
		if chapter.endingRunes <= shortEndingRunes {
			shortEndings++
		}
	}
	if len(endingLengths) > 0 {
		sort.Ints(endingLengths)
		stats.Ending = EndingStat{
			ShortRatio:  round2(float64(shortEndings) / float64(len(endingLengths))),
			MedianRunes: endingLengths[len(endingLengths)/2],
		}
	}

	stats.OpeningTimeRate = round2(float64(t.openingHits) / float64(n))
	stats.TitleFormats = titleFormats(titles)
	return stats
}

func analyzeChapter(text string) chapterStats {
	stats := chapterStats{
		text:      text,
		patterns:  make([]int, len(patternDefs)),
		sentences: chapterSentenceCounts(text),
	}
	for i, def := range patternDefs {
		stats.patterns[i] = len(def.re.FindAllStringIndex(text, -1))
	}
	if ending := lastNonEmptyLine(text); ending != "" {
		stats.endingRunes = len([]rune(ending))
		stats.hasEnding = true
	}
	stats.openingTime = openingTimeRe.MatchString(firstParagraph(text))
	return stats
}

func chapterSentenceCounts(text string) map[string]int {
	counts := make(map[string]int)
	for _, sentence := range sentenceSplit.Split(text, -1) {
		sentence = trimWrappedQuotes(sentence)
		if len([]rune(sentence)) < 12 {
			continue
		}
		counts[sentence]++
	}
	return counts
}

func cloneStats(stats *Stats) *Stats {
	if stats == nil {
		return nil
	}
	clone := *stats
	clone.Patterns = append([]PatternStat(nil), stats.Patterns...)
	clone.TopPhrases = append([]PhraseStat(nil), stats.TopPhrases...)
	clone.RepeatedSentences = append([]SentenceStat(nil), stats.RepeatedSentences...)
	if stats.TitleFormats != nil {
		titleFormats := *stats.TitleFormats
		clone.TitleFormats = &titleFormats
	}
	return &clone
}
