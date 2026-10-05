package store

import (
	"testing"

	"github.com/voocel/ainovel-cli/internal/domain"
)

func TestBuildCastFromChapterRecords(t *testing.T) {
	s := NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	if err := s.Characters.Save([]domain.Character{{Name: "林墨", Aliases: []string{"阿墨"}}}); err != nil {
		t.Fatal(err)
	}

	records := []struct {
		chapter int
		facts   domain.ChapterFacts
	}{
		{2, domain.ChapterFacts{Characters: []string{"林墨", "老周"}}},
		{5, domain.ChapterFacts{
			Characters: []string{"阿墨", "老周", "阿云", "阿云"},
			CastIntros: []domain.CastIntro{
				{Name: "老周", BriefRole: "守门人"},
				{Name: "阿云", BriefRole: "药铺学徒"},
			},
		}},
	}
	for _, record := range records {
		if _, err := s.ChapterRecords.Accept(record.chapter, domain.ChapterOriginGenerated, "正文", record.facts, domain.StyleDelta{}); err != nil {
			t.Fatal(err)
		}
	}

	cast, err := s.BuildCast([]int{5, 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(cast) != 2 {
		t.Fatalf("cast = %+v", cast)
	}
	if cast[0].Name != "老周" || cast[0].BriefRole != "守门人" || cast[0].AppearanceCount != 2 {
		t.Fatalf("old cast entry = %+v", cast[0])
	}
	if cast[1].Name != "阿云" || cast[1].BriefRole != "药铺学徒" || cast[1].AppearanceCount != 1 {
		t.Fatalf("new cast entry = %+v", cast[1])
	}
	if recent := domain.RecentCast(cast, 1); len(recent) != 1 || recent[0].Name != "老周" {
		t.Fatalf("recent cast = %+v", recent)
	}
}

func TestBuildCastRequiresCompleteRecordSet(t *testing.T) {
	s := NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BuildCast([]int{1}); err == nil {
		t.Fatal("缺少已完成章节记录时应报错")
	}
}
