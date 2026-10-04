package tui

import (
	"strings"
	"testing"

	"github.com/CTKiet2006/kietnovel/internal/utils"
)

func TestRenderStreamContent_ToggleThinking(t *testing.T) {
	// Format: [chính văn rỗng] \x02 [suy nghĩ] \x02 [chính văn tiếp]
	raw := utils.ThinkingSep + "This is internal reasoning" + utils.ThinkingSep + "Đây là chính văn chương 1."

	// Default: thinking is shown
	withThinking := renderStreamContent([]string{raw}, 60, "", false)
	if !strings.Contains(withThinking, "reasoning") {
		t.Errorf("expected thinking to be displayed, got %q", withThinking)
	}
	if !strings.Contains(withThinking, "Đây là chính văn") {
		t.Errorf("expected body to be displayed, got %q", withThinking)
	}

	// Hidden: thinking is stripped
	hiddenThinking := renderStreamContent([]string{raw}, 60, "", true)
	if strings.Contains(hiddenThinking, "reasoning") {
		t.Errorf("expected thinking to be hidden, got %q", hiddenThinking)
	}
	if !strings.Contains(hiddenThinking, "Đây là chính văn") {
		t.Errorf("expected body to be preserved when thinking is hidden, got %q", hiddenThinking)
	}
}
