package tui

import (
	"strings"
	"testing"

	"github.com/voocel/ainovel-cli/internal/host"
)

func TestAdvanceCommandsAreRegistered(t *testing.T) {
	registry := commandRegistryInstance()
	review, ok := registry.Find("review")
	if !ok || review.NeedsIdle {
		t.Fatalf("/review should be available while running: %+v", review)
	}
	next, ok := registry.Find("next")
	if !ok || !next.NeedsIdle || !next.AutoExecute {
		t.Fatalf("/next should be an idle one-shot command: %+v", next)
	}
	items := builtinCommandItems()
	if !hasPaletteItem(items, "review") || !hasPaletteItem(items, "next") {
		t.Fatalf("advance commands missing from palette: %+v", items)
	}
}

func TestReviewWaitingPlaceholder(t *testing.T) {
	m := Model{
		mode: modeRunning,
		snapshot: host.UISnapshot{
			RuntimeState: "paused",
			Phase:        "writing",
			AdvanceMode:  "review",
		},
	}
	m.syncRuntimePlaceholder()
	if got := m.textarea.Placeholder; !strings.Contains(got, "/next") || !strings.Contains(got, "修改意见") {
		t.Fatalf("review placeholder should expose both choices, got %q", got)
	}
}
