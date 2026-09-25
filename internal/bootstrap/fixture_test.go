package bootstrap_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/voocel/ainovel-cli/internal/app/task"
	"github.com/voocel/ainovel-cli/internal/bootstrap"
	"github.com/voocel/ainovel-cli/internal/domain/change"
	"github.com/voocel/ainovel-cli/internal/domain/model"
	"github.com/voocel/ainovel-cli/internal/domain/operation"
	"github.com/voocel/ainovel-cli/internal/infra/store"
)

type testApp struct {
	*bootstrap.App
	store      *store.Store
	changes    *change.Engine
	operations *operation.Engine
	now        func() time.Time
}

func newTestApp(s *store.Store, options ...bootstrap.Options) *testApp {
	var opts bootstrap.Options
	if len(options) > 0 {
		opts = options[0]
	}
	now := opts.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	changes := change.New(s)
	fixture := &testApp{store: s, changes: changes, operations: operation.NewEngine(s, changes, opts.Contracts...), now: now}
	opts.Now = func() time.Time { return fixture.now() }
	fixture.App = bootstrap.New(s, opts)
	return fixture
}
func newTestAppWithExecutor(s *store.Store, e operation.Executor) *testApp {
	return newTestApp(s, bootstrap.Options{Executors: task.ExecutorSet{LLM: e}})
}
func newTestAppWithExecutors(s *store.Store, e task.ExecutorSet, contracts ...operation.VerdictContract) *testApp {
	return newTestApp(s, bootstrap.Options{Executors: e, Contracts: contracts})
}

// Slot IDs are asserted by integration tests because recovery preserves them.
func runQuickID(runID string, parts ...string) string { return runID + ":" + strings.Join(parts, ":") }

// planID 是首次规划的槽位 ID：带请求章数与固定篇幅（D63），窗口为 3。
func planID(runID string, fixed int) string {
	requested := 3
	if fixed > 0 {
		requested = min(3, fixed)
	}
	return runQuickID(runID, "plan", fmt.Sprintf("r%d:f%d", requested, fixed))
}

// reviewAt 报告任务是否为本轮在 revision 上派发的审阅：审阅 ID 还含窗口与要求摘要（D62），
// 测试按 Revision 认领。
func reviewAt(operationID, runID string, revision model.Revision) bool {
	return strings.HasPrefix(operationID, runID+":review:") &&
		strings.Contains(operationID, ":r"+strconv.FormatInt(int64(revision), 10)+":")
}

// findReview 取本轮在 revision 上派发的第一个审阅任务。
func findReview(t *testing.T, s *store.Store, runID string, revision model.Revision) (model.Operation, bool) {
	t.Helper()
	ctx := context.Background()
	events, err := s.ListCreationRunEvents(ctx, runID)
	if err != nil {
		t.Fatalf("list run events: %v", err)
	}
	for _, event := range events {
		var payload struct {
			OperationID string `json:"operation_id"`
		}
		if event.Kind != model.RunEventOperationCreated || json.Unmarshal(event.Payload, &payload) != nil || !reviewAt(payload.OperationID, runID, revision) {
			continue
		}
		operation, err := s.GetOperation(ctx, payload.OperationID)
		if err != nil {
			t.Fatalf("read review operation: %v", err)
		}
		return operation, true
	}
	return model.Operation{}, false
}
