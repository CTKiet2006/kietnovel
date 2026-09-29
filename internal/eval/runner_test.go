package eval

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/CTKiet2006/kietnovel/internal/host"
)

// fakeEngine mimics the host: after Abort it sends to done once, just like waitDone. done has a buffer of 1,
// so the test can assert whether drive drained Done (len(done)==0 means it was consumed) — this is the key invariant
// against send-on-closed-channel panics.
type fakeEngine struct {
	events chan host.Event
	stream chan string
	done   chan struct{}

	mu      sync.Mutex
	snap    host.UISnapshot
	aborted bool
}

func newFakeEngine() *fakeEngine {
	return &fakeEngine{
		events: make(chan host.Event, 4),
		stream: make(chan string),
		done:   make(chan struct{}, 1),
	}
}

func (f *fakeEngine) Events() <-chan host.Event { return f.events }
func (f *fakeEngine) Stream() <-chan string     { return f.stream }
func (f *fakeEngine) Done() <-chan struct{}     { return f.done }

func (f *fakeEngine) Snapshot() host.UISnapshot {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.snap
}

func (f *fakeEngine) Abort() bool {
	f.mu.Lock()
	f.aborted = true
	f.mu.Unlock()
	select { // mimic waitDone: after abort fires, send to done once
	case f.done <- struct{}{}:
	default:
	}
	return true
}

func (f *fakeEngine) wasAborted() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.aborted
}

// The timeout path must Abort and then drain to Done before returning the timeout error — otherwise RunCase's Close would
// race with waitDone over closing the done channel and panic (Codex review #1).
func TestDriveTimeoutDrainsToDone(t *testing.T) {
	f := newFakeEngine()
	err := drive(f, 1, RunOptions{Timeout: 30 * time.Millisecond})
	if err == nil || !strings.Contains(err.Error(), "超时") {
		t.Fatalf("超时应返回超时错误，得到 %v", err)
	}
	if !f.wasAborted() {
		t.Fatal("超时应触发 Abort")
	}
	if len(f.done) != 0 {
		t.Fatal("drive 必须 drain Done 后才返回（否则与 Close 竞争关闭通道 panic）")
	}
}

// Chapter ceiling reached: Abort, drain to Done, and return nil (a normal capped stop, not a timeout).
func TestDriveCapStopsAndDrains(t *testing.T) {
	f := newFakeEngine()
	f.mu.Lock()
	f.snap = host.UISnapshot{CompletedCount: 1}
	f.mu.Unlock()
	f.events <- host.Event{Category: "SYSTEM", Summary: "committed"} // trigger the cap check

	err := drive(f, 1, RunOptions{Timeout: time.Second})
	if err != nil {
		t.Fatalf("正常截停应返回 nil，得到 %v", err)
	}
	if !f.wasAborted() {
		t.Fatal("达到章数上限应 Abort")
	}
	if len(f.done) != 0 {
		t.Fatal("应 drain Done 后返回")
	}
}

// The engine reaches Done on its own (the book is written): no Abort needed, returns nil.
func TestDriveNaturalDoneReturnsNil(t *testing.T) {
	f := newFakeEngine()
	f.done <- struct{}{}

	err := drive(f, 1, RunOptions{Timeout: time.Second})
	if err != nil {
		t.Fatalf("自然完成应返回 nil，得到 %v", err)
	}
	if f.wasAborted() {
		t.Fatal("自然完成不应 Abort")
	}
}
