package host

import (
	"context"
	"strings"
	"testing"

	"github.com/CTKiet2006/kietnovel/internal/domain"
	"github.com/CTKiet2006/kietnovel/internal/host/imp"
	"github.com/CTKiet2006/kietnovel/internal/store"
)

// newFlagTestHost builds a minimal Host, just enough to drive the cocreating flag state machine and the
// concurrency guards. emitEvent uses a non-blocking channel, so buffering events is enough and no
// observer is needed. The running-state branch of PauseForCoCreate calls Engine Abort (reusing the
// already verified Esc pause path), so it is not unit-tested here; this only covers the non-running state plus the flag/guard logic.
func newFlagTestHost(lc lifecycle, cocreating bool) *Host {
	return &Host{
		lifecycle:  lc,
		cocreating: cocreating,
		engine:     &engine{}, // acquireExclusive 查 engine.isRunning()（停止窗口门禁）
		events:     make(chan Event, 16),
	}
}

func TestPauseForCoCreate_NonRunningSetsFlag(t *testing.T) {
	h := newFlagTestHost(lifecycleIdle, false)
	if !h.PauseForCoCreate() {
		t.Fatal("idle 态应允许进入阶段共创")
	}
	if !h.cocreating {
		t.Error("进入后 cocreating 应为 true")
	}
	if h.lifecycle != lifecycleIdle {
		t.Errorf("非运行态进入不应改 lifecycle，得 %s", h.lifecycle)
	}
}

func TestPauseForCoCreate_RejectsCompleted(t *testing.T) {
	h := newFlagTestHost(lifecycleCompleted, false)
	if h.PauseForCoCreate() {
		t.Error("全书完成后不应允许进入阶段共创")
	}
	if h.cocreating {
		t.Error("拒绝后不应置位 cocreating")
	}
}

func TestPauseForCoCreate_RejectsReentrant(t *testing.T) {
	h := newFlagTestHost(lifecyclePaused, true)
	if h.PauseForCoCreate() {
		t.Error("已在共创中应拒绝重入")
	}
}

func TestCancelCoCreate_ClearsFlag(t *testing.T) {
	h := newFlagTestHost(lifecyclePaused, true)
	h.CancelCoCreate()
	if h.cocreating {
		t.Error("取消后 cocreating 应清空")
	}
	if h.lifecycle != lifecyclePaused {
		t.Errorf("取消不应改 lifecycle，得 %s", h.lifecycle)
	}
}

func TestCancelCoCreate_NoopWhenNotCocreating(t *testing.T) {
	h := newFlagTestHost(lifecycleRunning, false)
	h.CancelCoCreate() // 不应 panic，不应改状态
	if h.cocreating || h.lifecycle != lifecycleRunning {
		t.Error("非共创态 CancelCoCreate 应为 no-op")
	}
}

func TestResumeFromCoCreate_RejectsEmptyDraft(t *testing.T) {
	h := newFlagTestHost(lifecyclePaused, true)
	if err := h.ResumeFromCoCreate("   "); err == nil {
		t.Fatal("空 draft 应报错")
	}
	if !h.cocreating {
		t.Error("空 draft 在清标记前返回，cocreating 应保持 true")
	}
}

func TestResumeFromCoCreate_RejectsWhenNotCocreating(t *testing.T) {
	h := newFlagTestHost(lifecyclePaused, false)
	err := h.ResumeFromCoCreate("## 后续走向\n- 进入第二卷")
	if err == nil || !strings.Contains(err.Error(), "not in co-create") {
		t.Fatalf("非共创态应报 not in co-create，得 %v", err)
	}
}

func TestAcquireExclusive(t *testing.T) {
	cases := []struct {
		name       string
		lc         lifecycle
		cocreating bool
		exclusive  string
		wantErr    string // 空=期望放行
	}{
		{"running", lifecycleRunning, false, "", "đang viết"},
		{"cocreating", lifecyclePaused, true, "", "đồng sáng tác"},
		{"busy", lifecycleIdle, false, "nhập truyện", "Đang nhập truyện"},
		{"idle free", lifecycleIdle, false, "", ""},
		{"paused free", lifecyclePaused, false, "", ""},
	}
	// Abort stop window: lifecycle is already paused but the engine goroutine has not fully exited, it must still be rejected -
	// otherwise the import would write the same store concurrently with the engine's wrap-up.
	drain := newFlagTestHost(lifecyclePaused, false)
	drain.engine.running = true
	if err := drain.acquireExclusive("nhập truyện"); err == nil {
		t.Fatal("引擎排水期应拒绝独占作业")
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newFlagTestHost(c.lc, c.cocreating)
			h.exclusive = c.exclusive
			err := h.acquireExclusive("nhập truyện")
			if c.wantErr == "" {
				if err != nil {
					t.Fatalf("应放行，得 %v", err)
				}
				if h.exclusive != "nhập truyện" {
					t.Fatalf("放行后应登记占用，得 %q", h.exclusive)
				}
				h.releaseExclusive()
				if h.exclusive != "" {
					t.Fatalf("释放后占用应清空，得 %q", h.exclusive)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Fatalf("应含 %q，得 %v", c.wantErr, err)
			}
			if !strings.Contains(err.Error(), "nhập truyện") {
				t.Errorf("错误文案应带 action %q，得 %v", "nhập truyện", err)
			}
		})
	}
}

// TestExclusiveBlocksCreationEntries guards #2: while a background exclusive job (import / imitation write) is running,
// not only is the second background job blocked, the creative write entry points (Continue / Resume) and new background jobs
// must be blocked too, otherwise Continue lets the Arbiter change state before the engine is stopped by the gate, and the engine can run ahead during Resume / next.
func TestExclusiveBlocksCreationEntries(t *testing.T) {
	h := newFlagTestHost(lifecycleIdle, false)
	h.exclusive = "导入"
	if _, err := h.ImportFrom(context.Background(), imp.Options{}); err == nil {
		t.Error("独占作业期间 ImportFrom 应被拒")
	}
	if err := h.Continue("继续写"); err == nil {
		t.Error("独占作业期间 Continue 应被拒（须在 Arbiter 裁定前挡住）")
	}
	if _, err := h.Resume(); err == nil {
		t.Error("独占作业期间 Resume 应被拒")
	}
}

// TestStageCoCreate_OccupancyBlocksConcurrentEntries verifies that every exclusive entry point is blocked inside the co-create window:
// import / start / resume / continue must all be rejected while cocreating, closing the gap where only ==running was checked during the paused period.
func TestStageCoCreate_OccupancyBlocksConcurrentEntries(t *testing.T) {
	h := newFlagTestHost(lifecycleIdle, false)
	if !h.PauseForCoCreate() {
		t.Fatal("进入阶段共创失败")
	}

	if _, err := h.ImportFrom(context.Background(), imp.Options{}); err == nil {
		t.Error("共创窗口内 ImportFrom 应被拒")
	}
	if err := h.StartPrepared("写个新故事"); err == nil {
		t.Error("共创窗口内 StartPrepared 应被拒")
	}
	if _, err := h.Resume(); err == nil {
		t.Error("共创窗口内 Resume 应被拒")
	}
	if err := h.Continue("继续写"); err == nil {
		t.Error("共创窗口内 Continue 应被拒")
	}

	// Occupancy is released after leaving co-create (Cancel here; the Resume steer path is covered by integration tests)
	h.CancelCoCreate()
	if h.cocreating {
		t.Fatal("退出后占用标记应解除")
	}
}

func TestBuildStoryStateSummary_NilStore(t *testing.T) {
	if got := buildStoryStateSummary(nil); got != "" {
		t.Errorf("nil store 应返回空串，得 %q", got)
	}
}

func TestBuildStoryStateSummary_Populated(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init(100); err != nil {
		t.Fatal(err)
	}
	if err := st.Book.Save(domain.BookMetadata{Title: "影之诗", Synopsis: "少年追索失落的影子。"}); err != nil {
		t.Fatal(err)
	}
	p, _ := st.Progress.Load()
	p.CompletedChapters = []int{1, 2, 3}
	p.TotalWordCount = 12000
	if err := st.Progress.Save(p); err != nil {
		t.Fatal(err)
	}
	if err := st.Outline.SaveCompass(domain.StoryCompass{
		EndingDirection: "主角登临绝巅",
		OpenThreads:     []string{"师门血仇未报"},
		EstimatedScale:  "预计 4-6 卷",
	}); err != nil {
		t.Fatal(err)
	}

	got := buildStoryStateSummary(st)
	for _, want := range []string{"影之诗", "已完成 3 章", "下一章为第 4 章", "主角登临绝巅", "师门血仇未报", "预计 4-6 卷"} {
		if !strings.Contains(got, want) {
			t.Errorf("摘要应含 %q，实际:\n%s", want, got)
		}
	}
}

func TestBuildStoryStateSummaryUsesDynamicPlanningWording(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init(66); err != nil {
		t.Fatal(err)
	}
	if err := st.Outline.SaveLayeredOutline([]domain.VolumeOutline{{
		Index: 1, Title: "卷一", Arcs: []domain.ArcOutline{
			{Index: 1, Chapters: []domain.OutlineEntry{{Title: "一"}, {Title: "二"}}},
			{Index: 2, EstimatedChapters: 64},
		},
	}}); err != nil {
		t.Fatal(err)
	}
	p, err := st.Progress.Load()
	if err != nil {
		t.Fatal(err)
	}
	p.Layered = true
	if err := st.Progress.Save(p); err != nil {
		t.Fatal(err)
	}

	got := buildStoryStateSummary(st)
	if !strings.Contains(got, "当前已细化 2 章（后续按弧动态规划）") {
		t.Fatalf("动态规划摘要口径错误:\n%s", got)
	}
	if strings.Contains(got, "66") || strings.Contains(got, "规划 2 章") {
		t.Fatalf("动态规划摘要不得暗示固定总章数:\n%s", got)
	}
}
