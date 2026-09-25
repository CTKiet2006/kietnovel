package capability

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/voocel/ainovel-cli/internal/domain/model"
	"github.com/voocel/ainovel-cli/internal/infra/store"
)

func TestCanonMaterializationUsesFrozenValuesAndStrictAssertions(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "canon.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now().UTC()
	target := model.AuthorityTarget{Kind: model.AuthorityProject, ID: "book"}
	seedRuntimeProject(t, ctx, s, target, now)
	commit := func(proposal model.Proposal) {
		t.Helper()
		pending := proposal
		pending.ApprovalState, pending.DecidedBy, pending.DecidedAt = model.ApprovalPending, nil, nil
		if _, err := s.SaveProposal(ctx, pending); err != nil {
			t.Fatal(err)
		}
		if _, err := s.CommitProposal(ctx, proposal); err != nil {
			t.Fatal(err)
		}
	}
	ref := model.DocumentRef{Kind: model.DocumentCanon, ID: "hero-origin"}
	fact := model.CanonFact{ID: ref.ID, Kind: model.CanonState, SubjectID: "hero", Predicate: "state.origin", PreviousValue: json.RawMessage(`"农家子"`), Value: json.RawMessage(`"农家，少年"`)}
	content, _ := json.Marshal(fact)
	commit(approvedRuntimeProposal("punctuation", target, 1, now, model.Patch{Document: ref, Operation: model.PatchPut, Content: content}))
	r := NewRuntime(s)
	op := model.Operation{ID: "rewrite", Target: target, Snapshot: model.ExecutionSnapshot{BaseRevision: 2}}
	assertFact := func(patches []model.Patch, old, value string) {
		t.Helper()
		var got model.CanonFact
		if len(patches) != 1 {
			t.Fatalf("patches: %+v", patches)
		}
		if err := json.Unmarshal(patches[0].Content, &got); err != nil {
			t.Fatal(err)
		}
		if string(got.PreviousValue) != old || string(got.Value) != value {
			t.Fatalf("values changed: %s", patches[0].Content)
		}
	}
	confirmed, err := r.materializeCanon(ctx, op, nil, []string{ref.ID})
	if err != nil {
		t.Fatal(err)
	}
	assertFact(confirmed, `"农家，少年"`, `"农家，少年"`)
	fact.PreviousValue, fact.Value = nil, json.RawMessage(`"已入山门"`)
	content, _ = json.Marshal(fact)
	patch := model.Patch{Document: ref, Operation: model.PatchPut, Content: content}
	updated, err := r.materializeCanon(ctx, op, []model.Patch{patch}, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertFact(updated, `"农家，少年"`, `"已入山门"`)
	initial := op
	initial.Snapshot.BaseRevision = model.InitialRevision
	newFacts, err := r.materializeCanon(ctx, initial, []model.Patch{patch}, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertFact(newFacts, "", `"已入山门"`)
	proposal := model.Proposal{ID: "candidate", Target: target, BaseRevision: 2, Author: model.Author{Kind: model.AuthorAI, ID: "writer"}, Reason: "更新事实", Patches: updated, ApprovalState: model.ApprovalPending, CreatedAt: now}
	if err := r.changes.Validate(ctx, proposal); err != nil {
		t.Fatal(err)
	}
	// An explicitly supplied wrong punctuation assertion must never be overwritten.
	fact.PreviousValue = json.RawMessage(`"农家,少年"`)
	patch.Content, _ = json.Marshal(fact)
	wrong, err := r.materializeCanon(ctx, op, []model.Patch{patch}, nil)
	if err != nil {
		t.Fatal(err)
	}
	proposal.Patches = wrong
	if err := r.changes.Validate(ctx, proposal); !errors.Is(err, model.ErrStructuralConflict) {
		t.Fatalf("wrong explicit assertion accepted: %v", err)
	}
	// A later user revision must not silently become the source of old_value.
	fact.PreviousValue, fact.Value = json.RawMessage(`"农家，少年"`), json.RawMessage(`"用户改了出身"`)
	content, _ = json.Marshal(fact)
	commit(approvedRuntimeProposal("user-edit", target, 2, now, model.Patch{Document: ref, Operation: model.PatchPut, Content: content}))
	confirmed, err = r.materializeCanon(ctx, op, nil, []string{ref.ID})
	if err != nil {
		t.Fatal(err)
	}
	assertFact(confirmed, `"农家，少年"`, `"农家，少年"`)
	for _, ids := range [][]string{{ref.ID, ref.ID}, {"missing"}} {
		if _, err := r.materializeCanon(ctx, op, nil, ids); err == nil {
			t.Fatalf("invalid confirmations accepted: %v", ids)
		}
	}
	if _, err := r.materializeCanon(ctx, op, []model.Patch{patch}, []string{ref.ID}); err == nil || !strings.Contains(err.Error(), "overlapping") {
		t.Fatalf("overlap accepted: %v", err)
	}
}

// D61：同一主体+谓词就是同一事实。新 ID 按键并入已有节点并补 old_value；一次提交
// 不得两次动同一节点；基线里同键多份（旧脏数据）时无法判断更新哪条，显式报错。
func TestCanonMaterializationMergesByConceptKey(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "canon.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now().UTC()
	target := model.AuthorityTarget{Kind: model.AuthorityProject, ID: "book"}
	seedRuntimeProject(t, ctx, s, target, now)
	r := NewRuntime(s)
	op := model.Operation{ID: "write", Target: target, Snapshot: model.ExecutionSnapshot{BaseRevision: 1}}
	put := func(id, predicate string) model.Patch {
		content, _ := json.Marshal(model.CanonFact{ID: id, Kind: model.CanonState, SubjectID: "hero", Predicate: predicate, Value: json.RawMessage(`"孤儿"`)})
		return model.Patch{Document: model.DocumentRef{Kind: model.DocumentCanon, ID: id}, Operation: model.PatchPut, Content: content}
	}

	patches, err := r.materializeCanon(ctx, op, []model.Patch{put("origin-v2", "state.origin"), put("hero-talent", "state.talent")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var merged, fresh model.CanonFact
	_ = json.Unmarshal(patches[0].Content, &merged)
	_ = json.Unmarshal(patches[1].Content, &fresh)
	if patches[0].Document.ID != "hero-origin" || merged.ID != "hero-origin" || string(merged.PreviousValue) != `"农家子"` {
		t.Fatalf("merged = %s %s", patches[0].Document.ID, patches[0].Content)
	}
	if patches[1].Document.ID != "hero-talent" || len(fresh.PreviousValue) != 0 {
		t.Fatalf("a new key must stay a new fact: %s", patches[1].Content)
	}
	if _, err := r.materializeCanon(ctx, op, []model.Patch{put("origin-v2", "state.origin")}, []string{"hero-origin"}); err == nil || !strings.Contains(err.Error(), "overlapping") {
		t.Fatalf("merge overlapping a confirmation accepted: %v", err)
	}
	if _, err := r.materializeCanon(ctx, op, []model.Patch{put("hero-origin", "state.origin"), put("origin-v2", "state.origin")}, nil); err == nil || !strings.Contains(err.Error(), "same subject+predicate") {
		t.Fatalf("two actions on one fact accepted: %v", err)
	}

	pending := approvedRuntimeProposal("dirty", target, 1, now, put("origin-dup", "state.origin"))
	pending.ApprovalState, pending.DecidedBy, pending.DecidedAt = model.ApprovalPending, nil, nil
	if _, err := s.SaveProposal(ctx, pending); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CommitProposal(ctx, approvedRuntimeProposal("dirty", target, 1, now, put("origin-dup", "state.origin"))); err != nil {
		t.Fatal(err)
	}
	op.Snapshot.BaseRevision = 2
	if _, err := r.materializeCanon(ctx, op, []model.Patch{put("origin-v3", "state.origin")}, nil); err == nil || !strings.Contains(err.Error(), "cannot tell which one") {
		t.Fatalf("ambiguous key merged: %v", err)
	}
}
