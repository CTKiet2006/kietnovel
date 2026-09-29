package host

import (
	"strings"
	"testing"

	"github.com/CTKiet2006/kietnovel/internal/domain"
	storepkg "github.com/CTKiet2006/kietnovel/internal/store"
)

// TestHostReopen guards the user-level reopen exit of /reopen: finishing a book is a heavy decision, so a
// reopen can only be asked for explicitly by the user - rejected while unfinished, rejected while running;
// a successful reopen rolls phase back to writing, and the bundled continuation direction is registered as a pending steer (PendingSteer), injected after an Arbiter verdict on resume before running continues.
func TestHostReopen(t *testing.T) {
	st := storepkg.NewStore(t.TempDir())
	h := &Host{store: st, events: make(chan Event, 8)}

	if err := st.Progress.Init(2); err != nil {
		t.Fatal(err)
	}
	if err := h.Reopen(""); err == nil {
		t.Fatal("未完结的书应拒绝重开")
	}

	_ = st.Progress.UpdatePhase(domain.PhaseWriting)
	if err := st.Progress.MarkComplete(); err != nil {
		t.Fatal(err)
	}
	if err := h.Reopen("以八十年大限开新卷"); err != nil {
		t.Fatalf("完结书重开应成功：%v", err)
	}
	p, _ := st.Progress.Load()
	if p.Phase != domain.PhaseWriting {
		t.Fatalf("重开后 phase 应为 writing，得 %s", p.Phase)
	}
	if len(p.PendingRewrites) != 0 || p.ReopenedFromComplete {
		t.Fatalf("续写重开不得携带返工语义：%+v", p)
	}
	// The reopen count must be persisted: only then does the progress digest of a re-finish differ from the previous one - with the same digest the checkpoint
	// dedupes idempotently, a byte-identical re-finish produces no new checkpoint, and StopGuard would misread a successful finish as an idle termination.
	if p.ReopenCount != 1 {
		t.Fatalf("重开计数应为 1，得 %d", p.ReopenCount)
	}
	meta, _ := st.RunMeta.Load()
	if meta == nil || !strings.Contains(meta.PendingSteer, "八十年大限") {
		t.Fatalf("续写方向应登记为待处理干预，得 %+v", meta)
	}

	running := &Host{store: st, lifecycle: lifecycleRunning, events: make(chan Event, 1)}
	if err := running.Reopen(""); err == nil {
		t.Fatal("引擎运行中应拒绝重开")
	}
}
