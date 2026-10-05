package tui

import "testing"

func TestRevisionCommandIsRegisteredAndNeedsIdle(t *testing.T) {
	spec, ok := commandRegistryInstance().Find("sync")
	if !ok {
		t.Fatal("expected /sync command to be registered")
	}
	if !spec.NeedsIdle || !spec.AutoExecute {
		t.Fatalf("unexpected /sync policy: %+v", spec)
	}
	if !hasPaletteItem(builtinCommandItems(), "sync") {
		t.Fatal("expected /sync in command palette")
	}
}
