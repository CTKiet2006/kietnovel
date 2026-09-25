package model

import (
	"errors"
	"testing"
	"time"
)

func TestAdjudicationValidateAndFindingID(t *testing.T) {
	at := time.Date(2026, 9, 9, 0, 0, 0, 0, time.UTC)
	basis := EvidenceBasis{Documents: []DocumentBasis{{Ref: DocumentRef{Kind: DocumentManuscript, ID: "chapter-1"}, Revision: 2}}}
	if id := FindingID("run:book:1:review:r2", 3); id != "run:book:1:review:r2/3" {
		t.Fatalf("finding id = %s", id)
	}
	operationID, index, err := ParseFindingID("run:book:1:review:r2/3")
	if err != nil || operationID != "run:book:1:review:r2" || index != 3 {
		t.Fatalf("parsed = %s/%d, %v", operationID, index, err)
	}
	for _, bad := range []string{"", "review", "review/", "/1", "review/x", "review/-1"} {
		if _, _, err := ParseFindingID(bad); !errors.Is(err, ErrInvalid) {
			t.Fatalf("ParseFindingID(%q) err = %v", bad, err)
		}
	}
	cases := []struct {
		name   string
		record Adjudication
		valid  bool
	}{
		{"accept", Adjudication{ID: "a1", Finding: "review/0", Basis: basis, Reason: "可以接受", CreatedAt: at}, true},
		{"withdraw", Adjudication{ID: "a2", Withdraws: "a1", Reason: "改主意了", CreatedAt: at}, true},
		{"neither", Adjudication{ID: "a3", Reason: "x", CreatedAt: at}, false},
		{"both", Adjudication{ID: "a4", Finding: "review/0", Basis: basis, Withdraws: "a1", Reason: "x", CreatedAt: at}, false},
		{"accept without basis", Adjudication{ID: "a5", Finding: "review/0", Reason: "x", CreatedAt: at}, false},
		{"withdraw with basis", Adjudication{ID: "a6", Withdraws: "a1", Basis: basis, Reason: "x", CreatedAt: at}, false},
		{"no reason", Adjudication{ID: "a7", Finding: "review/0", Basis: basis, CreatedAt: at}, false},
	}
	for _, tc := range cases {
		err := tc.record.Validate()
		if tc.valid && err != nil {
			t.Fatalf("%s: unexpected err %v", tc.name, err)
		}
		if !tc.valid && !errors.Is(err, ErrInvalid) {
			t.Fatalf("%s: err = %v, want ErrInvalid", tc.name, err)
		}
	}
	accepted := AcceptedFindings([]Adjudication{
		{ID: "a1", Finding: "review/0"}, {ID: "a2", Finding: "review/1"}, {ID: "a3", Withdraws: "a2"},
	})
	if _, ok := accepted["review/0"]; !ok || len(accepted) != 1 {
		t.Fatalf("accepted = %v, want only review/0", accepted)
	}
}

func TestReviewVerdictAdjudicatedClearsItems(t *testing.T) {
	verdict := ReviewVerdict{
		Status: ReviewBlocked, Revision: 3, ChapterIDs: []string{"chapter-1", "chapter-2"}, ReviewKey: "review",
		Checks: []RequirementCheck{
			{ID: "directive:hook", Status: CheckViolated}, {ID: "intent:required:0", Status: CheckViolated},
			{ID: "directive:rain", Status: CheckSatisfied}, {ID: "intent:ending", Status: CheckPending},
		},
		Findings: []ReviewFinding{
			{ChapterID: "chapter-1", Severity: FindingBlocking, Note: "没有钩子", Requirement: "directive:hook"},
			{ChapterID: "chapter-2", Severity: FindingBlocking, Note: "主角没登场", Requirement: "intent:required:0"},
			{ChapterID: "chapter-2", Severity: FindingNote, Note: "节奏略慢"},
		},
	}
	none := verdict.Adjudicated("review", nil)
	if none.Status != ReviewBlocked || len(none.Findings) != 3 {
		t.Fatalf("no adjudication must keep the verdict: %#v", none)
	}
	partial := verdict.Adjudicated("review", map[string]struct{}{"review/0": {}})
	if partial.Status != ReviewBlocked || len(partial.Findings) != 2 ||
		partial.CheckStatus("directive:hook") != CheckSatisfied || partial.CheckStatus("intent:required:0") != CheckViolated {
		t.Fatalf("partial acceptance = %#v", partial)
	}
	full := verdict.Adjudicated("review", map[string]struct{}{"review/0": {}, "review/1": {}, "review/2": {}})
	if full.Status != ReviewPass || len(full.Findings) != 1 || full.Findings[0].Severity != FindingNote ||
		full.CheckStatus("intent:required:0") != CheckSatisfied {
		t.Fatalf("full acceptance = %#v", full)
	}
	// 接受发现只翻被违反的项：pending 表示还没到期，不因裁决变成满足。
	if full.CheckStatus("intent:ending") != CheckPending {
		t.Fatalf("pending check must stay pending, got %q", full.CheckStatus("intent:ending"))
	}
	if verdict.Status != ReviewBlocked || len(verdict.Findings) != 3 || verdict.Checks[0].Status != CheckViolated {
		t.Fatal("original verdict must stay untouched")
	}
}

func TestReviewVerdictRequiresCheckLinks(t *testing.T) {
	operation := Operation{
		Kind: OperationReviewRange, Snapshot: ExecutionSnapshot{BaseRevision: 3},
		Input: []byte(`{"chapter_ids":["chapter-1"],"requirements":[` +
			`{"id":"intent:required:0","text":"主角登场"},{"id":"intent:forbidden:0","text":"不写感情线","settle":true}],` +
			`"basis":{"documents":[{"ref":{"kind":"manuscript","id":"chapter-1"},"revision":2}]}}`),
	}
	basis := EvidenceBasis{Documents: []DocumentBasis{{Ref: DocumentRef{Kind: DocumentManuscript, ID: "chapter-1"}, Revision: 2}}}
	verdict := func(status, required, forbidden string, findings ...ReviewFinding) ReviewVerdict {
		if findings == nil {
			findings = []ReviewFinding{}
		}
		var checks []RequirementCheck
		for _, check := range []RequirementCheck{{ID: "intent:required:0", Status: required}, {ID: "intent:forbidden:0", Status: forbidden}} {
			if check.Status != "" {
				checks = append(checks, check)
			}
		}
		return ReviewVerdict{Status: status, Revision: 3, ChapterIDs: []string{"chapter-1"}, ReviewKey: "review", Basis: basis, Checks: checks, Findings: findings}
	}
	linked := ReviewFinding{ChapterID: "chapter-1", Severity: FindingBlocking, Note: "主角没登场", Requirement: "intent:required:0"}
	plain := ReviewFinding{ChapterID: "chapter-1", Severity: FindingBlocking, Note: "x"}
	cases := []struct {
		name    string
		verdict ReviewVerdict
		wantErr bool
	}{
		{"pending allowed when not settle", verdict(ReviewPass, CheckPending, CheckSatisfied), false},
		{"settle item pending", verdict(ReviewPass, CheckSatisfied, CheckPending), true},
		{"missing check", verdict(ReviewPass, CheckSatisfied, ""), true},
		{"extra check", ReviewVerdict{Status: ReviewPass, Revision: 3, ChapterIDs: []string{"chapter-1"}, ReviewKey: "review", Basis: basis, Findings: []ReviewFinding{},
			Checks: []RequirementCheck{{ID: "intent:required:0", Status: CheckSatisfied}, {ID: "intent:forbidden:0", Status: CheckSatisfied}, {ID: "mood", Status: CheckSatisfied}}}, true},
		{"unknown status", verdict(ReviewPass, "done", CheckSatisfied), true},
		{"violated unlinked", verdict(ReviewBlocked, CheckViolated, CheckSatisfied, plain), true},
		{"violated linked", verdict(ReviewBlocked, CheckViolated, CheckSatisfied, linked), false},
		{"pass with violated", verdict(ReviewPass, CheckViolated, CheckSatisfied), true},
		{"link to pending", verdict(ReviewBlocked, CheckPending, CheckSatisfied, linked), true},
		{"note carries link", verdict(ReviewPass, CheckSatisfied, CheckSatisfied, ReviewFinding{ChapterID: "chapter-1", Severity: FindingNote, Note: "x", Requirement: "intent:required:0"}), true},
		{"all satisfied", verdict(ReviewPass, CheckSatisfied, CheckSatisfied), false},
	}
	for _, tc := range cases {
		err := ValidateReviewVerdictForOperation(operation, tc.verdict)
		if tc.wantErr && !errors.Is(err, ErrInvalid) {
			t.Fatalf("%s: err = %v, want ErrInvalid", tc.name, err)
		}
		if !tc.wantErr && err != nil {
			t.Fatalf("%s: unexpected err = %v", tc.name, err)
		}
	}
}
