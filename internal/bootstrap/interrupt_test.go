package bootstrap_test

import (
	"context"
	"errors"
	"testing"
	"time"

	novelapp "github.com/voocel/ainovel-cli/internal/app/novel"
	"github.com/voocel/ainovel-cli/internal/domain/model"
)

// interruptingExecutor 在第一次写章时取消上下文并等它生效，相当于用户在写章途中退出进程。
type interruptingExecutor struct {
	*scriptedQuickExecutor
	cancel      context.CancelFunc
	interrupted string
}

func (e *interruptingExecutor) Execute(ctx context.Context, operation model.Operation) (model.OperationOutcome, error) {
	if operation.Kind == model.OperationWriteChapter && e.interrupted == "" {
		e.interrupted = operation.ID
		e.cancel()
		<-ctx.Done()
		return model.OperationOutcome{}, ctx.Err()
	}
	return e.scriptedQuickExecutor.Execute(ctx, operation)
}

// 进程退出把在途任务放回队列：不是失败、不消耗预算、不留悬空租约；重新进入立刻接着写。
func TestQuickWriteInterruptedByShutdownResumesFromQueue(t *testing.T) {
	authorityStore := openTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	executor := &interruptingExecutor{
		scriptedQuickExecutor: &scriptedQuickExecutor{now: testTime(), authorityStore: authorityStore},
		cancel:                cancel,
	}
	api := newTestAppWithExecutor(authorityStore, executor)
	api.now = testTime
	command := novelapp.QuickWriteCommand{
		ProjectID: "quick-book", UserID: "user-1", Premise: "一个失忆的邮差替亡者送完最后一封信",
		Chapters: 3, WorkerID: "quick-worker", LeaseDuration: time.Minute, CreatedAt: testTime(),
	}
	result, err := api.Novels.QuickWrite(ctx, command)
	if !errors.Is(err, context.Canceled) || result.RunState != model.RunRunning {
		t.Fatalf("interrupted quick write: state=%s err=%v", result.RunState, err)
	}
	background := context.Background()
	released, err := authorityStore.GetOperation(background, executor.interrupted)
	if err != nil {
		t.Fatalf("read interrupted operation: %v", err)
	}
	if released.State != model.OperationQueued || released.Attempt != 1 || released.LeaseOwner != "" || released.Error == "" {
		t.Fatalf("interrupted operation must be released to the queue: %+v", released)
	}

	// 租约本来还有 1 分钟，重新进入也不该撞"被占用"：接着 attempt 2 写完全书。
	command.CreatedAt = testTime().Add(time.Second)
	result, err = api.Novels.QuickWrite(background, command)
	if err != nil || result.RunState != model.RunCompleted || len(result.Chapters) != 3 {
		t.Fatalf("resume after interruption: %+v err=%v", result, err)
	}
	resumed, err := authorityStore.GetOperation(background, executor.interrupted)
	if err != nil || resumed.State != model.OperationSucceeded || resumed.Attempt != 2 {
		t.Fatalf("resumed operation = %+v err=%v", resumed, err)
	}
	if failures, err := authorityStore.CountOperationFailures(background, executor.interrupted); err != nil || failures != 0 {
		t.Fatalf("an interruption must not count as a failure: failures=%d err=%v", failures, err)
	}
}

// 执行途中用户暂停或取消单个任务：引擎收尾被围栏拒绝，协调器按任务的持久化状态落点
// （暂停 → 本轮暂停，取消 → 本轮取消），而不是把执行前的副本当成失败；之后照常续跑。
func TestQuickWriteSettlesOperationControlledMidExecution(t *testing.T) {
	for _, c := range []struct {
		name    string
		control func(*testApp, string) error
		want    model.CreationRunState
		resume  func(*testApp, string) error
	}{
		{"pause", func(api *testApp, id string) error {
			_, err := api.Tasks.PauseOperation(context.Background(), id, testTime().Add(time.Second))
			return err
		}, model.RunPaused, func(api *testApp, id string) error {
			_, err := api.Tasks.ResumeOperation(context.Background(), id, testTime().Add(2*time.Second))
			return err
		}},
		{"cancel", func(api *testApp, id string) error {
			_, err := api.Tasks.CancelOperation(context.Background(), id, testTime().Add(time.Second))
			return err
		}, model.RunCancelled, func(*testApp, string) error { return nil }},
	} {
		t.Run(c.name, func(t *testing.T) {
			ctx := context.Background()
			executor := &scriptedQuickExecutor{now: testTime()}
			api := newQuickTestApp(t, executor)
			var controlled string
			executor.onExecute = func(operation model.Operation) {
				if operation.Kind == model.OperationWriteChapter && controlled == "" {
					controlled = operation.ID
					if err := c.control(api, operation.ID); err != nil {
						t.Fatalf("%s operation: %v", c.name, err)
					}
				}
			}
			command := novelapp.QuickWriteCommand{
				ProjectID: "controlled-book", UserID: "user-1", Premise: "一个失忆的邮差替亡者送完最后一封信",
				Chapters: 3, WorkerID: "quick-worker", LeaseDuration: time.Minute, CreatedAt: testTime(),
			}
			result, err := api.Novels.QuickWrite(ctx, command)
			if err != nil || result.RunState != c.want {
				t.Fatalf("controlled run = %s (%s), err=%v", result.RunState, result.RunReason, err)
			}
			if err := c.resume(api, controlled); err != nil {
				t.Fatalf("resume operation: %v", err)
			}
			command.CreatedAt = testTime().Add(time.Hour)
			if result, err = api.Novels.QuickWrite(ctx, command); err != nil || result.RunState != model.RunCompleted || len(result.Chapters) != 3 {
				t.Fatalf("continue after %s: %+v err=%v", c.name, result, err)
			}
		})
	}
}
