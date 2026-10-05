package store

import (
	"testing"

	"github.com/voocel/ainovel-cli/internal/domain"
)

func TestInvalidateChapterAggregatesRemovesAffectedArtifacts(t *testing.T) {
	st := NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Outline.SaveLayeredOutline([]domain.VolumeOutline{{
		Index: 1,
		Arcs: []domain.ArcOutline{{
			Index: 1,
			Chapters: []domain.OutlineEntry{
				{Chapter: 1, Title: "第一章"}, {Chapter: 2, Title: "第二章"},
			},
		}},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := st.Summaries.SaveArcSummary(domain.ArcSummary{Volume: 1, Arc: 1, Summary: "旧弧摘要"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Summaries.SaveVolumeSummary(domain.VolumeSummary{Volume: 1, Summary: "旧卷摘要"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Characters.SaveSnapshots(1, 1, []domain.CharacterSnapshot{{Volume: 1, Arc: 1, Name: "林墨"}}); err != nil {
		t.Fatal(err)
	}
	if err := st.World.SaveStyleRules(domain.WritingStyleRules{Volume: 1, Arc: 1, Prose: []string{"旧规则"}}); err != nil {
		t.Fatal(err)
	}
	if err := st.World.SaveReview(domain.ReviewEntry{Chapter: 2, Scope: "arc", Verdict: "accept", Summary: "旧审阅"}); err != nil {
		t.Fatal(err)
	}

	if err := st.InvalidateChapterAggregates(1); err != nil {
		t.Fatal(err)
	}
	if sum, _ := st.Summaries.LoadArcSummary(1, 1); sum != nil {
		t.Fatalf("弧摘要未失效: %+v", sum)
	}
	if sum, _ := st.Summaries.LoadVolumeSummary(1); sum != nil {
		t.Fatalf("卷摘要未失效: %+v", sum)
	}
	if snapshots, _ := st.Characters.LoadSnapshots(1, 1); len(snapshots) != 0 {
		t.Fatalf("角色快照未失效: %+v", snapshots)
	}
	if rules, _ := st.World.LoadStyleRules(); rules != nil {
		t.Fatalf("写作规则未失效: %+v", rules)
	}
	if review, _ := st.World.LoadReview(2); review != nil {
		t.Fatalf("审阅未失效: %+v", review)
	}
}
