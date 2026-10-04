package userrules

import (
	"context"
	"testing"
	"time"

	"github.com/CTKiet2006/kietnovel/internal/rules"
	"github.com/CTKiet2006/kietnovel/internal/store"
	"github.com/voocel/agentcore"
)

// nil model + empty rules directories: every normalization degrades, but a snapshot can still be produced (system_defaults as the fallback) and persisted.
// The two directories in LoadOptions{} are empty strings, so RawFileSources returns nil and the test never touches a real disk.
func newDegradedService(t *testing.T) (*Service, *store.Store) {
	t.Helper()
	st := store.NewStore(t.TempDir())
	return NewService(st, nil, rules.LoadOptions{}), st
}

func TestService_Build_DegradesButPersists(t *testing.T) {
	svc, st := newDegradedService(t)

	snap, err := svc.Build(t.Context(), "每章1200字，主角冷静克制")
	if err != nil {
		t.Fatalf("Build 不应报错（降级而非阻断）：%v", err)
	}
	if snap.Status != rules.StatusDegraded {
		t.Fatalf("无模型应降级，status=%q", snap.Status)
	}
	// system_defaults always backstops the mechanical baseline.
	if len(snap.Structured.FatigueWords) == 0 || len(snap.Structured.ForbiddenPhrases) == 0 {
		t.Fatalf("应保留 system_defaults 机械基线，got %+v", snap.Structured)
	}
	// The startup prompt degrades to raw preferences, the raw text is not lost.
	if snap.Preferences == "" {
		t.Fatal("降级应把启动 prompt 原文记入 preferences")
	}

	// Already persisted: GetOrBuild reads back the same one instead of rebuilding it.
	reloaded, err := st.UserRules.Load()
	if err != nil || reloaded == nil {
		t.Fatalf("快照应已落盘：err=%v snap=%v", err, reloaded)
	}
	if reloaded.Preferences != snap.Preferences {
		t.Fatal("落盘内容与返回不一致")
	}
}

func TestService_GetOrBuildInitializesMissingSnapshot(t *testing.T) {
	svc, st := newDegradedService(t)

	if cur, _ := st.UserRules.Load(); cur != nil {
		t.Fatal("初始应无快照")
	}
	snap, err := svc.GetOrBuild(t.Context())
	if err != nil {
		t.Fatalf("GetOrBuild 不应报错：%v", err)
	}
	if len(snap.Structured.FatigueWords) == 0 {
		t.Fatal("惰性生成应含 system_defaults")
	}
	if cur, _ := st.UserRules.Load(); cur == nil {
		t.Fatal("GetOrBuild 应顺带落盘")
	}
}

func TestService_ReconcileDefaultsForLanguage(t *testing.T) {
	svc, st := newDegradedService(t)
	// Lock book language to Vietnamese
	if err := st.BookLanguage.Save("vi"); err != nil {
		t.Fatalf("Save BookLanguage: %v", err)
	}

	// Save an old snapshot with Chinese defaults
	zhSnap := rules.BuildSnapshot([]rules.Candidate{rules.SystemDefaultsForLanguage("zh")})
	zhSnap.Preferences = "custom preference"
	if err := st.UserRules.Save(&zhSnap); err != nil {
		t.Fatalf("Save UserRules: %v", err)
	}

	// GetOrBuild should reconcile Chinese defaults to Vietnamese defaults
	reconciled, err := svc.GetOrBuild(t.Context())
	if err != nil {
		t.Fatalf("GetOrBuild: %v", err)
	}

	// Should no longer have Chinese forbidden phrase "某种程度上"
	for _, p := range reconciled.Structured.ForbiddenPhrases {
		if p == "某种程度上" {
			t.Errorf("expected Chinese forbidden phrase to be removed")
		}
	}
	// Should have Vietnamese forbidden phrase
	hasViPhrase := false
	for _, p := range reconciled.Structured.ForbiddenPhrases {
		if p == "ở một mức độ nào đó" {
			hasViPhrase = true
			break
		}
	}
	if !hasViPhrase {
		t.Errorf("expected Vietnamese forbidden phrase to be present")
	}

	// Custom preferences should be preserved
	if reconciled.Preferences != "custom preference" {
		t.Errorf("expected preferences to be preserved, got %q", reconciled.Preferences)
	}
}

func TestService_AddRuntimeRule_PersistsAndReturnsCandidate(t *testing.T) {
	svc, st := newDegradedService(t)

	const text = "以后少用比喻"
	merged, cand, err := svc.AddRuntimeRule(t.Context(), text)
	if err != nil {
		t.Fatalf("AddRuntimeRule 不应报错：%v", err)
	}
	// The candidate is meant for echoing back: with no model it degrades, the raw text goes into preferences.
	if !cand.Degraded {
		t.Fatal("无模型时本次候选应降级")
	}
	if cand.Preferences != text {
		t.Fatalf("候选应保留原文，got %q", cand.Preferences)
	}
	// The overlaid snapshot contains that entry and has been persisted.
	if merged.Preferences == "" {
		t.Fatal("叠加后 preferences 不应为空")
	}
	reloaded, err := st.UserRules.Load()
	if err != nil || reloaded == nil {
		t.Fatalf("叠加后应落盘：err=%v", err)
	}
	if reloaded.Status != rules.StatusDegraded {
		t.Fatalf("含降级来源，status 应为 degraded，got %q", reloaded.Status)
	}
}

// alwaysRetryableModel always returns a retryable error: llmretry keeps retrying with backoff,
// and only the context can stop it — exactly the deadlock shape of issue #125.
type alwaysRetryableModel struct{ scriptedModel }

func (m *alwaysRetryableModel) Generate(context.Context, []agentcore.Message, []agentcore.ToolSpec, ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	m.calls++
	return nil, retryableTestError{}
}

// issue #125 regression: when the provider keeps rate-limiting or becoming unavailable, Build must be terminated by the context and degrade,
// and must never retry forever and deadlock the book-creation flow.
func TestService_BuildStopsAtContextDeadline(t *testing.T) {
	st := store.NewStore(t.TempDir())
	svc := NewService(st, &alwaysRetryableModel{}, rules.LoadOptions{})

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	type result struct {
		snap *rules.Snapshot
		err  error
	}
	done := make(chan result, 1)
	go func() {
		snap, err := svc.Build(ctx, "每章1200字，主角冷静克制")
		done <- result{snap, err}
	}()

	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("超时应降级而非阻断开书：%v", got.err)
		}
		if got.snap.Status != rules.StatusDegraded {
			t.Fatalf("归一化超时应降级，status=%q", got.snap.Status)
		}
		if got.snap.Preferences == "" {
			t.Fatal("降级应保留启动 prompt 原文")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Build 未受 context 约束，已卡死（issue #125）")
	}
}
