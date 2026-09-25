package model

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestReviewVerdictRequirementCoverage(t *testing.T) {
	// D62：任务输入携带的每项要求都必须被恰好声明一次，不得漏项、越界或重复。
	operation := Operation{
		Kind: OperationReviewRange, Snapshot: ExecutionSnapshot{BaseRevision: 3},
		Input: json.RawMessage(`{"chapter_ids":["chapter-1"],"requirements":[{"id":"directive:hook","text":"结尾留钩子"},{"id":"directive:rain","text":"要下雨"}],"basis":{"documents":[{"ref":{"kind":"manuscript","id":"chapter-1"},"revision":2}]}}`),
	}
	basis := EvidenceBasis{Documents: []DocumentBasis{{Ref: DocumentRef{Kind: DocumentManuscript, ID: "chapter-1"}, Revision: 2}}}
	base := func(checks ...RequirementCheck) ReviewVerdict {
		return ReviewVerdict{
			Status: ReviewPass, Revision: 3, ChapterIDs: []string{"chapter-1"}, ReviewKey: "review", Basis: basis,
			Checks: checks, Findings: []ReviewFinding{},
		}
	}
	hook := RequirementCheck{ID: "directive:hook", Status: CheckSatisfied}
	rain := RequirementCheck{ID: "directive:rain", Status: CheckPending}
	cases := []struct {
		name    string
		verdict ReviewVerdict
		wantErr bool
	}{
		{"missing all", base(), true},
		{"missing one", base(hook), true},
		{"outside requested", base(hook, RequirementCheck{ID: "directive:other", Status: CheckSatisfied}), true},
		{"duplicated", base(hook, hook), true},
		{"all declared", base(rain, hook), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateReviewVerdictForOperation(operation, tc.verdict)
			if tc.wantErr && !errors.Is(err, ErrInvalid) {
				t.Fatalf("err = %v, want ErrInvalid", err)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected err = %v", err)
			}
		})
	}

	// 任务未携带要求时，裁定不得凭空声明。
	plain := Operation{
		Kind: OperationReviewRange, Snapshot: ExecutionSnapshot{BaseRevision: 3},
		Input: json.RawMessage(`{"chapter_ids":["chapter-1"],"basis":{"documents":[{"ref":{"kind":"manuscript","id":"chapter-1"},"revision":2}]}}`),
	}
	if err := ValidateReviewVerdictForOperation(plain, base(hook)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unrequested check err = %v", err)
	}
	if err := ValidateReviewVerdictForOperation(plain, base()); err != nil {
		t.Fatalf("plain pass err = %v", err)
	}
}

func TestOperationOutcomeCombinations(t *testing.T) {
	proposal := &Proposal{ID: "p"}
	verdict := json.RawMessage(`{"status":"pass"}`)
	artifacts := []Artifact{{
		ID: "op/cover", ProjectID: "book-1", Digest: Digest([]byte("d")), MediaType: "image/png",
		OperationID: "op", Attempt: 1, CreatedAt: time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC),
	}}
	cases := []struct {
		name    string
		outcome OperationOutcome
		valid   bool
	}{
		{"empty", OperationOutcome{}, false},
		{"proposal", OperationOutcome{Proposal: proposal}, true},
		{"verdict", OperationOutcome{Verdict: verdict}, true},
		{"artifacts", OperationOutcome{Artifacts: artifacts}, true},
		{"proposal+verdict", OperationOutcome{Proposal: proposal, Verdict: verdict}, false},
		{"proposal+artifacts", OperationOutcome{Proposal: proposal, Artifacts: artifacts}, true},
		{"verdict+artifacts", OperationOutcome{Verdict: verdict, Artifacts: artifacts}, true},
		{"all", OperationOutcome{Proposal: proposal, Verdict: verdict, Artifacts: artifacts}, false},
	}
	for _, testCase := range cases {
		if err := testCase.outcome.Validate(); (err == nil) != testCase.valid {
			t.Fatalf("%s: err = %v, want valid=%v", testCase.name, err, testCase.valid)
		}
	}
	duplicate := OperationOutcome{Artifacts: append(append([]Artifact(nil), artifacts...), artifacts...)}
	if err := duplicate.Validate(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("duplicate artifacts err = %v", err)
	}
}
