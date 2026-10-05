package stylestat

import (
	"reflect"
	"sort"
	"sync"
	"testing"
)

func TestTrackerMatchesComputeAcrossUpdates(t *testing.T) {
	allChapters := map[int]string{
		1: chapterWith("夜里，他不是迟疑，而是恐惧。青云山巅风声渐紧。\n此生未能远行，望你替我看看远方的山海。\n他走了。"),
		2: chapterWith("清晨，她沉默了几息，青云山巅云海翻涌。\n此生未能远行，望你替我看看远方的山海。\n天色渐亮。"),
		3: chapterWith("陆九渊站在青云山巅，眼中闪过寒意。\n此生未能远行，望你替我看看远方的山海。\n无人回答。"),
		4: chapterWith("众人望向青云山巅，觉得风雨将至。\n长街尽头传来钟声。\n门开了。"),
		5: chapterWith("仿佛一场旧梦压在青云山巅。\n一种说不出的寒意沿石阶蔓延。\n灯灭了。"),
		6: chapterWith("他心头一紧，却没有回头。\n青云山巅仍旧沉在云里。\n故事仍要继续向前延伸。"),
	}
	titles := []string{"第一章 风起", "云涌", "第3章 雷动", "暗潮", "归途", "山门"}
	stopwords := []string{"陆九渊"}

	tracker := NewTracker()
	chapters := make(map[int]string)
	for chapter := 1; chapter <= 6; chapter++ {
		chapters[chapter] = allChapters[chapter]
		tracker.Upsert(chapter, allChapters[chapter])
		assertTrackerMatchesCompute(t, tracker, chapters, titles, stopwords)
	}

	chapters[2] = chapterWith("黎明时，她心头一沉，宛如旧梦惊醒。\n改写后的长句只在这一章出现，不应成为跨章复读。\n风停了。")
	tracker.Upsert(2, chapters[2])
	assertTrackerMatchesCompute(t, tracker, chapters, titles, stopwords)

	delete(chapters, 4)
	tracker.Remove(4)
	assertTrackerMatchesCompute(t, tracker, chapters, titles, stopwords)
}

func TestTrackerSnapshotReturnsIndependentCopy(t *testing.T) {
	tracker := NewTracker()
	for chapter := 1; chapter <= 5; chapter++ {
		tracker.Upsert(chapter, chapterWith("他不是退缩，而是在等待。"))
	}

	first := tracker.Snapshot(nil, nil)
	if first == nil || len(first.Patterns) == 0 {
		t.Fatalf("unexpected first snapshot: %+v", first)
	}
	first.Patterns[0].Total = 999

	second := tracker.Snapshot(nil, nil)
	if second.Patterns[0].Total == 999 {
		t.Fatal("cached snapshot was mutated through caller result")
	}
}

func TestTrackerConcurrentSnapshotAndUpdate(t *testing.T) {
	tracker := NewTracker()
	for chapter := 1; chapter <= 8; chapter++ {
		tracker.Upsert(chapter, chapterWith("他不是退缩，而是在等待。"))
	}

	var wg sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				if worker%2 == 0 {
					tracker.Upsert(8, chapterWith("他不是退缩，而是在等待。"))
				} else {
					_ = tracker.Snapshot([]string{"第一章"}, []string{"林砚"})
				}
			}
		}(worker)
	}
	wg.Wait()
}

func assertTrackerMatchesCompute(
	t *testing.T,
	tracker *Tracker,
	chapters map[int]string,
	titles, stopwords []string,
) {
	t.Helper()
	ids := make([]int, 0, len(chapters))
	for chapter := range chapters {
		ids = append(ids, chapter)
	}
	sort.Ints(ids)
	texts := make([]string, 0, len(ids))
	for _, chapter := range ids {
		texts = append(texts, chapters[chapter])
	}

	want := Compute(Input{Chapters: texts, Titles: titles, Stopwords: stopwords})
	got := tracker.Snapshot(titles, stopwords)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tracker mismatch\n got: %+v\nwant: %+v", got, want)
	}
}
