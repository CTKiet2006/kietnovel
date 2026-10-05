package host

import (
	"errors"
	"testing"
)

func TestBookLeaseExclusiveAndReusable(t *testing.T) {
	dir := t.TempDir()

	first, err := acquireBookLease(dir)
	if err != nil {
		t.Fatalf("acquire first lease: %v", err)
	}
	second, err := acquireBookLease(dir)
	if second != nil {
		_ = second.Close()
		t.Fatal("second lease unexpectedly acquired")
	}
	if !errors.Is(err, ErrBookInUse) {
		t.Fatalf("second lease error = %v, want ErrBookInUse", err)
	}

	if err := first.Close(); err != nil {
		t.Fatalf("close first lease: %v", err)
	}
	third, err := acquireBookLease(dir)
	if err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
	t.Cleanup(func() {
		if err := third.Close(); err != nil {
			t.Errorf("close third lease: %v", err)
		}
	})
}

func TestBookLeaseAllowsDifferentDirectories(t *testing.T) {
	first, err := acquireBookLease(t.TempDir())
	if err != nil {
		t.Fatalf("acquire first directory: %v", err)
	}
	t.Cleanup(func() {
		if err := first.Close(); err != nil {
			t.Errorf("close first lease: %v", err)
		}
	})

	second, err := acquireBookLease(t.TempDir())
	if err != nil {
		t.Fatalf("acquire second directory: %v", err)
	}
	t.Cleanup(func() {
		if err := second.Close(); err != nil {
			t.Errorf("close second lease: %v", err)
		}
	})
}

func TestHostCloseReleasesBookLease(t *testing.T) {
	dir := t.TempDir()
	lease, err := acquireBookLease(dir)
	if err != nil {
		t.Fatalf("acquire lease: %v", err)
	}
	h := &Host{
		bookLease: lease,
		observer:  &observer{},
		engine:    &engine{},
		events:    make(chan Event, 1),
		streamCh:  make(chan string, 1),
		done:      make(chan struct{}, 1),
	}
	h.Close()

	next, err := acquireBookLease(dir)
	if err != nil {
		t.Fatalf("acquire after Host.Close: %v", err)
	}
	t.Cleanup(func() {
		if err := next.Close(); err != nil {
			t.Errorf("close next lease: %v", err)
		}
	})
}
