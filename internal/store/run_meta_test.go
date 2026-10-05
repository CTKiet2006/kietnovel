package store

import (
	"errors"
	"testing"

	"github.com/voocel/ainovel-cli/internal/domain"
)

func TestSaveAndLoadRunMeta(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	meta := domain.RunMeta{
		StartedAt: "2026-03-07T10:00:00+08:00",
		Provider:  "openrouter",
		Style:     "fantasy",
		Model:     "gpt-4o",
	}
	if err := store.RunMeta.Save(meta); err != nil {
		t.Fatalf("SaveRunMeta: %v", err)
	}

	loaded, err := store.RunMeta.Load()
	if err != nil {
		t.Fatalf("LoadRunMeta: %v", err)
	}
	if loaded.Style != "fantasy" {
		t.Errorf("style mismatch: %s", loaded.Style)
	}
	if loaded.Provider != "openrouter" {
		t.Errorf("provider mismatch: %s", loaded.Provider)
	}
	if loaded.Model != "gpt-4o" {
		t.Errorf("model mismatch: %s", loaded.Model)
	}
}

func TestLoadRunMeta_Empty(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	meta, err := store.RunMeta.Load()
	if err != nil {
		t.Fatalf("LoadRunMeta on empty: %v", err)
	}
	if meta != nil {
		t.Fatalf("expected nil, got %+v", meta)
	}
}

func TestInitRunMeta_PreservesHistory(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	// 先建立带运行意图的 RunMeta
	_ = store.RunMeta.Save(domain.RunMeta{
		StartedAt:    "old",
		Provider:     "openai",
		Style:        "fantasy",
		Model:        "old-model",
		PendingSteer: "待处理",
	})

	// Init 应保留 PendingSteer 等运行意图事实
	_ = store.RunMeta.Init("suspense", "openrouter", "new-model")

	meta, _ := store.RunMeta.Load()
	if meta.Style != "suspense" {
		t.Errorf("style should be updated, got %s", meta.Style)
	}
	if meta.Provider != "openrouter" {
		t.Errorf("provider should be updated, got %s", meta.Provider)
	}
	if meta.Model != "new-model" {
		t.Errorf("model should be updated, got %s", meta.Model)
	}
	if meta.PendingSteer != "待处理" {
		t.Errorf("pending steer should be preserved, got %s", meta.PendingSteer)
	}
	if meta.AdvanceMode != domain.ChapterAdvanceAuto {
		t.Errorf("missing advance mode should initialize to auto, got %q", meta.AdvanceMode)
	}
}

func TestSetAndClearPendingSteer(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	// 设置 PendingSteer
	if err := store.RunMeta.SetPendingSteer("主角改成女性"); err != nil {
		t.Fatalf("SetPendingSteer: %v", err)
	}
	meta, _ := store.RunMeta.Load()
	if meta.PendingSteer != "主角改成女性" {
		t.Errorf("expected pending steer, got %s", meta.PendingSteer)
	}

	// 清除
	if err := store.RunMeta.ClearPendingSteer(); err != nil {
		t.Fatalf("ClearPendingSteer: %v", err)
	}
	meta, _ = store.RunMeta.Load()
	if meta.PendingSteer != "" {
		t.Errorf("expected empty pending steer, got %s", meta.PendingSteer)
	}
}

func TestSetPlanningTier(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	if err := store.RunMeta.SetPlanningTier(domain.PlanningTierLong); err != nil {
		t.Fatalf("SetPlanningTier: %v", err)
	}

	meta, err := store.RunMeta.Load()
	if err != nil {
		t.Fatalf("LoadRunMeta: %v", err)
	}
	if meta == nil {
		t.Fatal("expected run meta to exist")
	}
	if meta.PlanningTier != domain.PlanningTierLong {
		t.Fatalf("expected planning tier %q, got %q", domain.PlanningTierLong, meta.PlanningTier)
	}
}

func TestClearPendingSteer_Noop(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	// 空 meta 上调用不报错
	if err := store.RunMeta.ClearPendingSteer(); err != nil {
		t.Fatalf("ClearPendingSteer on empty: %v", err)
	}
}

func TestAdvanceControlRoundTrip(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	if err := store.RunMeta.Init("fantasy", "openrouter", "m"); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := store.RunMeta.SetAdvanceMode(domain.ChapterAdvanceReview); err != nil {
		t.Fatalf("SetAdvanceMode: %v", err)
	}
	if err := store.RunMeta.GrantAdvancePermit(3); err != nil {
		t.Fatalf("GrantAdvancePermit: %v", err)
	}
	hold := domain.AdvanceHold{After: domain.AdvanceHoldAfterRewritesDrained, Reason: "重写第3章"}
	if err := store.RunMeta.SetAdvanceHold(hold); err != nil {
		t.Fatalf("SetAdvanceHold: %v", err)
	}

	meta, _ := store.RunMeta.Load()
	if meta.AdvanceMode != domain.ChapterAdvanceReview || meta.AdvancePermitChapter != 3 {
		t.Fatalf("advance mode/permit round trip: %+v", meta)
	}
	if meta.AdvanceHold == nil || *meta.AdvanceHold != hold {
		t.Fatalf("advance hold round trip: %+v", meta.AdvanceHold)
	}

	if err := store.RunMeta.ClearAdvancePermit(3); err != nil {
		t.Fatalf("ClearAdvancePermit: %v", err)
	}
	if err := store.RunMeta.ClearAdvanceHold(hold); err != nil {
		t.Fatalf("ClearAdvanceHold: %v", err)
	}
	meta, _ = store.RunMeta.Load()
	if meta.AdvancePermitChapter != 0 || meta.AdvanceHold != nil {
		t.Fatalf("advance intent should be consumed: %+v", meta)
	}
}

func TestInitRunMeta_PreservesAdvanceIntent(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	if err := store.RunMeta.Init("fantasy", "openrouter", "m"); err != nil {
		t.Fatal(err)
	}
	_ = store.RunMeta.SetAdvanceMode(domain.ChapterAdvanceReview)
	_ = store.RunMeta.GrantAdvancePermit(7)
	hold := domain.AdvanceHold{After: domain.AdvanceHoldAtBoundary, Reason: "验收"}
	_ = store.RunMeta.SetAdvanceHold(hold)
	// 进程重启路径：Host.New 每次都会调 Init，用户运行意图必须存活。
	_ = store.RunMeta.Init("fantasy", "openrouter", "m")

	meta, _ := store.RunMeta.Load()
	if meta.AdvanceMode != domain.ChapterAdvanceReview || meta.AdvancePermitChapter != 7 {
		t.Fatalf("advance mode/permit should survive Init, got %+v", meta)
	}
	if meta.AdvanceHold == nil || *meta.AdvanceHold != hold {
		t.Fatalf("advance hold should survive Init, got %+v", meta.AdvanceHold)
	}
}

func TestAdvanceControlRejectsConflictingIntent(t *testing.T) {
	store := NewStore(t.TempDir())
	if err := store.RunMeta.Init("fantasy", "openrouter", "m"); err != nil {
		t.Fatal(err)
	}
	if err := store.RunMeta.GrantAdvancePermit(1); err == nil {
		t.Fatal("auto mode must reject permit")
	}
	if err := store.RunMeta.SetAdvanceMode(domain.ChapterAdvanceReview); err != nil {
		t.Fatal(err)
	}
	if err := store.RunMeta.GrantAdvancePermit(2); err != nil {
		t.Fatal(err)
	}
	if err := store.RunMeta.GrantAdvancePermit(3); err == nil {
		t.Fatal("conflicting permit must fail")
	}
	hold := domain.AdvanceHold{After: domain.AdvanceHoldAtBoundary, Reason: "停"}
	if err := store.RunMeta.SetAdvanceHold(hold); err != nil {
		t.Fatal(err)
	}
	if err := store.RunMeta.SetAdvanceHold(domain.AdvanceHold{After: domain.AdvanceHoldAtBoundary, Reason: "另一条"}); err == nil {
		t.Fatal("conflicting hold must fail")
	}
	if err := (domain.AdvanceHold{After: domain.AdvanceHoldAtChapter, Reason: "缺目标"}).Validate(); err == nil {
		t.Fatal("chapter hold without target must fail")
	}
	if err := (domain.AdvanceHold{After: domain.AdvanceHoldAtBoundary, TargetChapter: 3, Reason: "错误目标"}).Validate(); err == nil {
		t.Fatal("non-chapter hold with target must fail")
	}
	if err := store.RunMeta.ClearAdvanceHold(domain.AdvanceHold{After: domain.AdvanceHoldAtBoundary, Reason: "旧值"}); err == nil {
		t.Fatal("compare-and-clear must reject changed hold")
	}
	if err := store.RunMeta.SetAdvanceMode(domain.ChapterAdvanceAuto); err != nil {
		t.Fatal(err)
	}
	meta, _ := store.RunMeta.Load()
	if meta.AdvancePermitChapter != 0 || meta.AdvanceHold == nil {
		t.Fatalf("auto should clear permit but preserve hold: %+v", meta)
	}
}

func TestInitRunMeta_UnknownAdvanceModeDoesNotWrite(t *testing.T) {
	store := NewStore(t.TempDir())
	original := domain.RunMeta{Style: "old", AdvanceMode: "future"}
	if err := store.RunMeta.Save(original); err != nil {
		t.Fatal(err)
	}
	err := store.RunMeta.Init("new", "openrouter", "m")
	var unsupported *domain.UnsupportedAdvanceModeError
	if !errors.As(err, &unsupported) {
		t.Fatalf("expected UnsupportedAdvanceModeError, got %v", err)
	}
	meta, loadErr := store.RunMeta.Load()
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if meta.Style != "old" || meta.AdvanceMode != "future" {
		t.Fatalf("failed Init must not rewrite RunMeta: %+v", meta)
	}
}

// TestRunMetaInit_PreservesPlanStart 规划期(裁定已落盘、首个 foundation 未落盘)
// 崩溃重启时,Host.New 的 RunMeta.Init 不得清掉 PlanStart——它是恢复规划师身份的
// 唯一依据(engine.planStartFallback)。
func TestRunMetaInit_PreservesPlanStart(t *testing.T) {
	store := NewStore(t.TempDir())
	if err := store.RunMeta.SetStartPrompt("写个悬疑短篇"); err != nil {
		t.Fatalf("set start prompt: %v", err)
	}
	rec := domain.PlanStartRecord{RawPrompt: "写个悬疑短篇", Planner: "architect_short", PlannerTask: "任务全文", DecisionID: "dec-x"}
	if err := store.RunMeta.SetPlanStart(rec); err != nil {
		t.Fatalf("set plan start: %v", err)
	}
	// 模拟进程重启:Host.New 会再次 Init
	if err := store.RunMeta.Init("default", "openrouter", "m"); err != nil {
		t.Fatalf("init: %v", err)
	}
	meta, err := store.RunMeta.Load()
	if err != nil || meta == nil {
		t.Fatalf("load: %v", err)
	}
	if meta.PlanStart == nil || meta.PlanStart.Planner != "architect_short" {
		t.Fatalf("Init 必须保留 PlanStart, got %+v", meta.PlanStart)
	}
	// StartPrompt 同样是跨重启事实:裁定失败后它是引擎补裁的唯一依据。
	if meta.StartPrompt != "写个悬疑短篇" {
		t.Fatalf("Init 必须保留 StartPrompt, got %q", meta.StartPrompt)
	}
}
