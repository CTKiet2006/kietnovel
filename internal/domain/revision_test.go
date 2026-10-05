package domain

import "testing"

func TestMergeStyleDeltaPreservesEarlierEvidence(t *testing.T) {
	merged := MergeStyleDelta(
		StyleDelta{
			Prose:    []string{"减少解释"},
			Dialogue: []CharacterVoice{{Name: "林墨", Rules: []string{"少用感叹句"}}},
		},
		StyleDelta{
			Prose:    []string{"减少解释", "动作更直接"},
			Dialogue: []CharacterVoice{{Name: "林墨", Rules: []string{"短句"}}},
		},
	)
	if len(merged.Prose) != 2 || len(merged.Dialogue) != 1 || len(merged.Dialogue[0].Rules) != 2 {
		t.Fatalf("style delta merge = %+v", merged)
	}
}
