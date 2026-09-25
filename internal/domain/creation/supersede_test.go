package creation_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/voocel/ainovel-cli/internal/domain/creation"
	"github.com/voocel/ainovel-cli/internal/domain/model"
)

// awaitingTasks 让新建的工作直接停在待审批，驱动随即落 waiting_user。
type awaitingTasks struct{ creation.Tasks }

func (awaitingTasks) Start(_ context.Context, runID string, work creation.WorkItem, _ time.Time) (model.Operation, error) {
	return model.Operation{ID: work.ID, Kind: work.Kind, RunID: runID, State: model.OperationAwaitingApproval}, nil
}

// TestDriverSupersedesOnlyReplacedAwaitingWork 钉住 D64 的取代口径：待审批的是槽位 write:ch4
// 后继链上的 r2。推导给出同种类的另一项工作时旧稿转 stale；推导仍指向它的槽位、暂时去做
// 别的种类或停下等待时旧稿保留，仍可批准。
func TestDriverSupersedesOnlyReplacedAwaitingWork(t *testing.T) {
	cases := []struct {
		name string
		step creation.Step
		want model.OperationState
	}{
		{"same kind, another slot", creation.Step{Work: &creation.WorkItem{ID: "write:ch5", Kind: model.OperationWriteChapter}}, model.OperationStale},
		{"a prefix is another slot", creation.Step{Work: &creation.WorkItem{ID: "write", Kind: model.OperationWriteChapter}}, model.OperationStale},
		{"its slot", creation.Step{Work: &creation.WorkItem{ID: "write:ch4", Kind: model.OperationWriteChapter}}, model.OperationAwaitingApproval},
		{"another kind first", creation.Step{Work: &creation.WorkItem{ID: "review:1-3", Kind: model.OperationReviewRange}}, model.OperationAwaitingApproval},
		{"wait", creation.Step{Wait: "修订预算已用尽"}, model.OperationAwaitingApproval},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			st, run := fixture(t)
			now := run.CreatedAt
			input := json.RawMessage(`{"chapter_plan_id":"chapter-plan-4","chapter_number":4}`)
			if _, err := st.CreateOperation(ctx, model.Operation{
				ID: "write:ch4:r2", Kind: model.OperationWriteChapter, RunID: run.ID, State: model.OperationQueued,
				Target: model.AuthorityTarget{Kind: model.AuthorityProject, ID: run.ProjectID},
				Snapshot: model.ExecutionSnapshot{
					Executor: "fixture@1", BaseRevision: 1, InputDigest: model.Digest(input),
					ConfigDigest: "fixture", ApprovalPolicy: model.ApprovalManual,
				},
				Input: input, CreatedAt: now, UpdatedAt: now,
			}); err != nil {
				t.Fatal(err)
			}
			claimed, err := st.ClaimNextOperation(ctx, "worker", time.Minute, now)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := st.ConcludeOperation(ctx, claimed.ID, claimed.Attempt, model.OperationAwaitingApproval, "awaits user", now); err != nil {
				t.Fatal(err)
			}
			goal := goalFunc(func(context.Context, model.CreationRun) (creation.Decision, error) {
				return creation.Decision{Revision: 1, Step: tc.step}, nil
			})
			driver := creation.New(st, map[model.GoalKind]creation.Goal{run.Goal.Kind: goal}, func() time.Time { return now.Add(time.Minute) })
			if _, err := driver.Drive(ctx, run, awaitingTasks{}, creation.DriveCommand{WorkerID: "worker", LeaseDuration: time.Minute, CreatedAt: now}); err != nil {
				t.Fatal(err)
			}
			if operation, err := st.GetOperation(ctx, "write:ch4:r2"); err != nil || operation.State != tc.want {
				t.Fatalf("awaiting write = %s, %v; want %s", operation.State, err, tc.want)
			}
		})
	}
}
