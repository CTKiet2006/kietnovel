package tools

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/voocel/ainovel-cli/internal/store"
	"github.com/voocel/ainovel-cli/internal/stylestat"
)

func TestStyleStatsIndexAppendRewriteAndRemove(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}

	chapters := map[int]string{
		1: "# 风起\n夜里，他不是迟疑，而是恐惧。\n此生未能远行，望你替我看看远方的山海。\n他走了。",
		2: "# 云涌\n清晨，她沉默了几息。\n此生未能远行，望你替我看看远方的山海。\n天亮了。",
		3: "# 雷动\n陆九渊眼中闪过寒意。\n此生未能远行，望你替我看看远方的山海。\n无人回答。",
		4: "# 暗潮\n众人觉得风雨将至。\n长街尽头传来钟声。\n门开了。",
		5: "# 归途\n仿佛一场旧梦压在山巅。\n一种说不出的寒意蔓延。\n灯灭了。",
		6: "# 山门\n他心头一紧，却没有回头。\n故事仍要继续向前延伸。",
	}
	for chapter, text := range chapters {
		if err := st.Drafts.SaveFinalChapter(chapter, text); err != nil {
			t.Fatal(err)
		}
	}

	titles := []string{"第一章 风起", "云涌", "第3章 雷动", "暗潮", "归途", "山门"}
	stopwords := []string{"陆九渊"}
	index := NewStyleStatsIndex(st)
	completed := []int{1, 2, 3, 4, 5, 6}
	assertStyleStatsIndexMatchesCompute(t, index, chapters, completed, titles, stopwords)

	chapters[7] = "# 雾散\n宛如旧梦惊醒，他没有说话。\n风停了。"
	if err := st.Drafts.SaveFinalChapter(7, chapters[7]); err != nil {
		t.Fatal(err)
	}
	index.ChapterCommitted(7, chapters[7])
	completed = append(completed, 7)
	assertStyleStatsIndexMatchesCompute(t, index, chapters, completed, titles, stopwords)

	chapters[2] = "# 云涌\n黎明时，她心头一沉。\n改写后的长句只在这一章出现，不应成为跨章复读。\n风停了。"
	if err := st.Drafts.SaveFinalChapter(2, chapters[2]); err != nil {
		t.Fatal(err)
	}
	index.ChapterCommitted(2, chapters[2])
	assertStyleStatsIndexMatchesCompute(t, index, chapters, completed, titles, stopwords)

	delete(chapters, 4)
	completed = []int{1, 2, 3, 5, 6, 7}
	assertStyleStatsIndexMatchesCompute(t, index, chapters, completed, titles, stopwords)
}

func TestStyleStatsIndexSurfacesMissingCompletedChapter(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	_, err := NewStyleStatsIndex(st).Snapshot([]int{1}, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "第 1 章已标记完成但终稿不存在") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestStyleStatsDependencyIsRequired(t *testing.T) {
	st := store.NewStore(t.TempDir())
	tests := []struct {
		name string
		new  func()
	}{
		{
			name: "context",
			new: func() {
				NewContextTool(st, References{}, "default", nil)
			},
		},
		{
			name: "commit",
			new: func() {
				NewCommitChapterTool(st, nil)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("nil StyleStatsIndex must fail immediately")
				}
			}()
			tt.new()
		})
	}
}

func assertStyleStatsIndexMatchesCompute(
	t *testing.T,
	index *StyleStatsIndex,
	chapters map[int]string,
	completed []int,
	titles, stopwords []string,
) {
	t.Helper()
	ids := append([]int(nil), completed...)
	sort.Ints(ids)
	texts := make([]string, 0, len(ids))
	for _, chapter := range ids {
		texts = append(texts, chapters[chapter])
	}
	want := stylestat.Compute(stylestat.Input{
		Chapters:  texts,
		Titles:    titles,
		Stopwords: stopwords,
	})
	got, err := index.Snapshot(completed, titles, stopwords)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("index mismatch\n got: %+v\nwant: %+v", got, want)
	}
}
