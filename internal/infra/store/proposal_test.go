package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/voocel/ainovel-cli/internal/domain/model"
)

func TestPendingProposalSurvivesReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "ainovel.db")
	proposal := pendingTestProposal(testChange("persisted-proposal",
		model.AuthorityTarget{Kind: model.AuthorityProject, ID: "book-1"}, 0,
		model.Patch{
			Document:  model.DocumentRef{Kind: model.DocumentIntent, ID: "root"},
			Operation: model.PatchPut,
			Content:   []byte(`{"premise":"凡人修仙"}`),
		},
	))

	s, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if _, err := s.SaveProposal(ctx, proposal); err != nil {
		t.Fatalf("save proposal: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	s, err = Open(ctx, path)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer s.Close()
	stored, err := s.GetProposal(ctx, proposal.ID)
	if err != nil {
		t.Fatalf("get proposal: %v", err)
	}
	if stored.ApprovalState != model.ApprovalPending || stored.Target != proposal.Target {
		t.Fatalf("stored proposal = %#v", stored)
	}
}

func TestRejectedProposalCannotCommit(t *testing.T) {
	ctx := context.Background()
	s := openOperationStore(t)
	target := model.AuthorityTarget{Kind: model.AuthorityProject, ID: "book-1"}
	approved := testChange("rejected-proposal", target, 0, model.Patch{
		Document:  model.DocumentRef{Kind: model.DocumentIntent, ID: "root"},
		Operation: model.PatchPut,
		Content:   []byte(`{"premise":"凡人修仙"}`),
	})
	if _, err := s.SaveProposal(ctx, pendingTestProposal(approved)); err != nil {
		t.Fatalf("save proposal: %v", err)
	}
	rejected := approved
	rejected.ApprovalState = model.ApprovalRejected
	if _, err := s.RejectProposal(ctx, rejected); err != nil {
		t.Fatalf("reject proposal: %v", err)
	}
	if _, err := s.CommitProposal(ctx, approved); !errors.Is(err, model.ErrStateConflict) {
		t.Fatalf("commit rejected proposal error = %v, want model.ErrStateConflict", err)
	}
	if _, err := s.CurrentRevision(ctx, target); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("current revision error = %v, want model.ErrNotFound", err)
	}
}

// TestExecutionCommitIsFencedInsideTheTransaction 守护 D42 的提交边界：执行侧提交在同一
// 事务内确认任务仍在运行且 attempt 未被接替；取消先于提交时 ChangeSet 不得落库。用户
// 裁决路径不受执行围栏限制，但任务提案只在任务等待审批时可批准，任务同事务转为
// succeeded（D64）：取消后的旧稿不能入账。
func TestExecutionCommitIsFencedInsideTheTransaction(t *testing.T) {
	ctx := context.Background()
	s := openOperationStore(t)
	start := operationTime()
	target := model.AuthorityTarget{Kind: model.AuthorityProject, ID: "book-1"}

	if _, err := s.CreateOperation(ctx, testOperation("op", 1, start)); err != nil {
		t.Fatalf("create operation: %v", err)
	}
	claimed, err := s.ClaimNextOperation(ctx, "worker-1", time.Minute, start)
	if err != nil || claimed.Attempt != 1 {
		t.Fatalf("claim = %#v, %v", claimed, err)
	}
	approved := testChange("op-proposal", target, 0, model.Patch{
		Document:  model.DocumentRef{Kind: model.DocumentIntent, ID: "root"},
		Operation: model.PatchPut,
		Content:   []byte(`{"premise":"凡人修仙"}`),
	})
	approved.OperationID = claimed.ID
	if _, err := s.SaveProposal(ctx, pendingTestProposal(approved)); err != nil {
		t.Fatalf("save proposal: %v", err)
	}

	if _, err := s.CommitExecutionProposal(ctx, approved, claimed.Attempt+1); !errors.Is(err, model.ErrStateConflict) {
		t.Fatalf("foreign attempt commit error = %v, want model.ErrStateConflict", err)
	}
	if _, err := s.TransitionOperation(ctx, claimed.ID, model.OperationRunning, model.OperationCancelled, "cancelled by user", start.Add(time.Minute)); err != nil {
		t.Fatalf("user cancel: %v", err)
	}
	if _, err := s.CommitExecutionProposal(ctx, approved, claimed.Attempt); !errors.Is(err, model.ErrStateConflict) {
		t.Fatalf("commit after cancel error = %v, want model.ErrStateConflict", err)
	}
	if _, err := s.CurrentRevision(ctx, target); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("cancelled execution must not create a revision, got %v", err)
	}

	if _, err := s.CommitProposal(ctx, approved); !errors.Is(err, model.ErrStateConflict) {
		t.Fatalf("user commit of a cancelled task's proposal error = %v, want model.ErrStateConflict", err)
	}

	if _, err := s.CreateOperation(ctx, testOperation("awaiting", 1, start)); err != nil {
		t.Fatalf("create awaiting operation: %v", err)
	}
	running, err := s.ClaimNextOperation(ctx, "worker-1", time.Minute, start.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("claim awaiting operation: %v", err)
	}
	if _, err := s.ConcludeOperation(ctx, running.ID, running.Attempt, model.OperationAwaitingApproval, "awaits user", start.Add(2*time.Minute)); err != nil {
		t.Fatalf("await approval: %v", err)
	}
	awaiting := testChange("awaiting-proposal", target, 0, approved.Patches...)
	decidedAt := start.Add(3 * time.Minute)
	awaiting.OperationID, awaiting.DecidedAt = running.ID, &decidedAt
	if _, err := s.SaveProposal(ctx, pendingTestProposal(awaiting)); err != nil {
		t.Fatalf("save awaiting proposal: %v", err)
	}
	committed, err := s.CommitProposal(ctx, awaiting)
	if err != nil || committed.NewRevision != 1 {
		t.Fatalf("user commit = %#v, %v", committed, err)
	}
	if succeeded, err := s.GetOperation(ctx, running.ID); err != nil || succeeded.State != model.OperationSucceeded {
		t.Fatalf("approved operation = %+v, %v", succeeded, err)
	}
}

func pendingTestProposal(proposal model.Proposal) model.Proposal {
	proposal.ApprovalState = model.ApprovalPending
	proposal.DecidedBy = nil
	proposal.DecidedAt = nil
	return proposal
}

// TestUpdatePendingProposalKeepsTheSubmission：只改 pending 行，受执行归属围栏（D42）
// 保护；只允许前移基线与刷新影响，改动提交内容或回退基线被拒；改后的提案按新摘要提交。
func TestUpdatePendingProposalKeepsTheSubmission(t *testing.T) {
	ctx := context.Background()
	s := openOperationStore(t)
	start := operationTime()
	target := model.AuthorityTarget{Kind: model.AuthorityProject, ID: "book-1"}
	intent := func(premise string) model.Patch {
		return model.Patch{
			Document: model.DocumentRef{Kind: model.DocumentIntent, ID: "root"}, Operation: model.PatchPut,
			Content: []byte(`{"premise":"` + premise + `"}`),
		}
	}
	seed := testChange("seed", target, 0, intent("凡人修仙"))
	if _, err := s.SaveProposal(ctx, pendingTestProposal(seed)); err != nil {
		t.Fatalf("save seed: %v", err)
	}
	if _, err := s.CommitProposal(ctx, seed); err != nil {
		t.Fatalf("commit seed: %v", err)
	}
	if _, err := s.CreateOperation(ctx, testOperation("op", 1, start)); err != nil {
		t.Fatalf("create operation: %v", err)
	}
	claimed, err := s.ClaimNextOperation(ctx, "worker-1", time.Minute, start)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	approved := testChange("op-proposal", target, 1, intent("凡人修仙，逆天改命"))
	approved.OperationID = claimed.ID
	relocated := pendingTestProposal(approved)
	original := relocated
	original.BaseRevision = 0
	if _, err := s.SaveProposal(ctx, original); err != nil {
		t.Fatalf("save proposal: %v", err)
	}
	if err := s.UpdatePendingProposal(ctx, relocated, claimed.Attempt+1, start); !errors.Is(err, model.ErrStateConflict) {
		t.Fatalf("foreign attempt relocation err = %v, want model.ErrStateConflict", err)
	}
	tampered := relocated
	tampered.Patches = []model.Patch{intent("凡人修仙，另起炉灶")}
	if err := s.UpdatePendingProposal(ctx, tampered, claimed.Attempt, start); !errors.Is(err, model.ErrIdempotencyConflict) {
		t.Fatalf("patch change err = %v, want model.ErrIdempotencyConflict", err)
	}
	if err := s.UpdatePendingProposal(ctx, relocated, claimed.Attempt, start); err != nil {
		t.Fatalf("relocate: %v", err)
	}
	if err := s.UpdatePendingProposal(ctx, original, claimed.Attempt, start); !errors.Is(err, model.ErrIdempotencyConflict) {
		t.Fatalf("base rewind err = %v, want model.ErrIdempotencyConflict", err)
	}
	if stored, err := s.GetProposal(ctx, relocated.ID); err != nil || stored.BaseRevision != 1 {
		t.Fatalf("stored = %#v, %v", stored, err)
	}
	missing := relocated
	missing.ID = "missing"
	if err := s.UpdatePendingProposal(ctx, missing, 0, start); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("missing proposal err = %v", err)
	}
	committed, err := s.CommitExecutionProposal(ctx, approved, claimed.Attempt)
	if err != nil || committed.BaseRevision != 1 || committed.NewRevision != 2 {
		t.Fatalf("commit relocated = %#v, %v", committed, err)
	}
	if err := s.UpdatePendingProposal(ctx, relocated, 0, start); !errors.Is(err, model.ErrStateConflict) {
		t.Fatalf("relocating a decided proposal err = %v", err)
	}
}
