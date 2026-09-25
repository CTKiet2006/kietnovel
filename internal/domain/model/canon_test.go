package model

import (
	"encoding/json"
	"testing"
)

func TestCanonFactEffectiveChapterDefaultsToSource(t *testing.T) {
	fact := CanonFact{ID: "mood", Kind: CanonState, SubjectID: "hero", Predicate: "state.mood", Value: json.RawMessage(`"忐忑"`), SourceChapterID: "chapter-3"}
	if fact.EffectiveChapter() != "chapter-3" || fact.IsEvent() {
		t.Fatalf("effective = %q, event = %v", fact.EffectiveChapter(), fact.IsEvent())
	}
	fact.EffectiveChapterID = "chapter-1"
	if fact.EffectiveChapter() != "chapter-1" || fact.Validate() != nil {
		t.Fatalf("flashback effective = %q, err = %v", fact.EffectiveChapter(), fact.Validate())
	}
	fact.EffectiveChapterID = " "
	if err := fact.Validate(); err == nil {
		t.Fatal("blank effective chapter must be rejected")
	}
	if (CanonFact{Kind: CanonEvent}).IsEvent() != true || (CanonFact{}).EffectiveChapter() != "" {
		t.Fatal("event kind and planning-time fact classification")
	}
}

// D61：状态、世界规则与伏笔按主体+谓词确定身份；事件跨章追加、关系对象在值里，不按键归并。
func TestCanonConceptKeyCoversKeyedKindsOnly(t *testing.T) {
	for kind, keyed := range map[CanonFactKind]bool{
		CanonState: true, CanonWorldRule: true, CanonForeshadow: true,
		CanonEvent: false, CanonRelationship: false,
	} {
		key, ok := CanonFact{Kind: kind, SubjectID: "hero", Predicate: "p"}.ConceptKey()
		if ok != keyed || (keyed && key != (CanonKey{SubjectID: "hero", Predicate: "p"})) {
			t.Fatalf("%s: key = %v, keyed = %v", kind, key, ok)
		}
	}
}

func TestCanonResolvedOnlyForForeshadow(t *testing.T) {
	fact := CanonFact{ID: "jade", Kind: CanonForeshadow, SubjectID: "hero", Predicate: "foreshadow.jade_origin", Value: json.RawMessage(`"玉佩来历"`), Resolved: true}
	if err := fact.Validate(); err != nil {
		t.Fatalf("resolved foreshadow: %v", err)
	}
	fact.Kind, fact.Predicate = CanonState, "state.jade_origin"
	if err := fact.Validate(); err == nil {
		t.Fatal("resolved state must be rejected")
	}
}
