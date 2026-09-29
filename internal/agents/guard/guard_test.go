package guard

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/CTKiet2006/kietnovel/internal/domain"
	"github.com/CTKiet2006/kietnovel/internal/store"
	"github.com/voocel/agentcore"
)

func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatalf("init store: %v", err)
	}
	return s
}

// TestSubAgentGuard_HardStopReasonEscalatesImmediately verifies that when the model returns
// an unrecoverable provider-side refusal such as safety / content_filter, the subagent StopGuard
// must escalate immediately instead of injecting a nudge message.
//
// Background: in practice, when hy3-preview:free wrote chapter 2 it returned stop_reason='safety' 8 times in a row
// as refusals; the old logic kept injecting "you must commit", the model kept returning safety, and it only escalated after 3 accumulated blocks,
// after which the Engine re-ran the writer 3 times in total. Each attempt was a new SubAgent → the cache
// prefix was cold-started every time. After the fix the first safety escalates immediately and the Engine can pause straight away as an unrecoverable error.
//
// Note that only safety / content_filter are tested: StopReasonError / StopReasonAborted take the branch in
// agentcore loop.go that terminates the run outright and never calls StopGuard, so listing them would only
// introduce dead code.
func TestSubAgentGuard_HardStopReasonEscalatesImmediately(t *testing.T) {
	cases := []agentcore.StopReason{
		agentcore.StopReason("safety"),
		agentcore.StopReason("content_filter"),
	}
	for _, sr := range cases {
		t.Run(string(sr), func(t *testing.T) {
			s := newTestStore(t)
			guard := NewWriterStopGuard(s, nil)
			info := agentcore.StopInfo{
				TurnIndex: 1,
				Message:   agentcore.Message{StopReason: sr},
			}
			d := guard(context.Background(), info)
			if !d.Escalate {
				t.Fatalf("stop_reason=%q must escalate immediately, got %#v", sr, d)
			}
			if d.InjectMessage != "" {
				t.Fatalf("stop_reason=%q must not inject any message, got %q", sr, d.InjectMessage)
			}
		})
	}
}

// TestSubAgentGuard_NormalStopStillBlocks ensures that the blocking behavior for a normal stop_reason
// is unaffected by the hard-error bypass — when the LLM stops on its own without a commit it must still be nudged.
func TestSubAgentGuard_NormalStopStillBlocks(t *testing.T) {
	s := newTestStore(t)
	guard := NewWriterStopGuard(s, nil)
	info := agentcore.StopInfo{
		TurnIndex: 1,
		Message:   agentcore.Message{StopReason: agentcore.StopReasonStop},
	}
	d := guard(context.Background(), info)
	if d.Escalate {
		t.Fatal("normal stop must not escalate on first block")
	}
	if d.Allow {
		t.Fatal("normal stop must be blocked when no commit checkpoint exists")
	}
	if d.InjectMessage == "" {
		t.Fatal("normal stop must inject a follow-up message")
	}
}

// TestSubAgentGuard_ProgressBetweenBlocksResetsCounter verifies that when a new checkpoint appears between
// two blocks (for example the model drafting again after being nudged) the consecutive counter resets — escalation only
// punishes consecutive idling with no artifact at all, following the "progress resets" semantics (issue #75).
func TestSubAgentGuard_ProgressBetweenBlocksResetsCounter(t *testing.T) {
	s := newTestStore(t)
	guard := NewWriterStopGuard(s, nil)
	normalStop := agentcore.StopInfo{TurnIndex: 1, Message: agentcore.Message{StopReason: agentcore.StopReasonStop}}

	// Block → persist a new draft (progress) → block again: going back and forth past the threshold must still not escalate.
	for i := 0; i < subagentMaxConsecutiveBlocks+2; i++ {
		if d := guard(context.Background(), normalStop); d.Escalate {
			t.Fatalf("escalated at block %d despite progress between blocks", i)
		}
		if _, err := s.Checkpoints.Append(domain.ChapterScope(1), "draft", "drafts/01.draft.md", fmt.Sprintf("d%d", i)); err != nil {
			t.Fatalf("append draft: %v", err)
		}
	}
	// No progress: escalation happens only after consecutive artifact-free blocks fill up the threshold.
	for i := 0; i < subagentMaxConsecutiveBlocks; i++ {
		if d := guard(context.Background(), normalStop); d.Escalate {
			t.Fatalf("escalated too early at idle block %d", i)
		}
	}
	if d := guard(context.Background(), normalStop); !d.Escalate {
		t.Fatal("expected escalate after consecutive no-progress blocks")
	}
}

// TestWriterStopGuard_StageAwareBlockMessage verifies that the nudge message is assembled from the persisted steps:
// a static "you must call commit_chapter" misleads the model when prerequisite steps are missing or commit errors out (issue #75).
func TestWriterStopGuard_StageAwareBlockMessage(t *testing.T) {
	s := newTestStore(t)
	guard := NewWriterStopGuard(s, nil)
	normalStop := agentcore.StopInfo{TurnIndex: 1, Message: agentcore.Message{StopReason: agentcore.StopReasonStop}}

	// No artifact at all: it should guide the full flow instead of nudging for commit right away.
	d := guard(context.Background(), normalStop)
	if !strings.Contains(d.InjectMessage, "draft_chapter") || !strings.Contains(d.InjectMessage, "plan_chapter") {
		t.Fatalf("no-draft message should walk through the protocol, got %q", d.InjectMessage)
	}

	// Draft already persisted: it should nudge check_consistency to finish.
	if _, err := s.Checkpoints.Append(domain.ChapterScope(1), "draft", "drafts/01.draft.md", "d1"); err != nil {
		t.Fatalf("append draft: %v", err)
	}
	d = guard(context.Background(), normalStop)
	if !strings.Contains(d.InjectMessage, "check_consistency") {
		t.Fatalf("draft-only message should point to check_consistency, got %q", d.InjectMessage)
	}

	// Draft + consistency check done: only the commit is left, and it must leave a path for the commit-error case.
	if _, err := s.Checkpoints.Append(domain.ChapterScope(1), "consistency_check", "meta/checks/01.json", "c1"); err != nil {
		t.Fatalf("append consistency_check: %v", err)
	}
	d = guard(context.Background(), normalStop)
	if !strings.Contains(d.InjectMessage, "commit_chapter") || !strings.Contains(d.InjectMessage, "错误") {
		t.Fatalf("ready-to-commit message should mention commit and error handling, got %q", d.InjectMessage)
	}
}

// TestSubAgentGuard_BlockHookReceivesAgentAndReason verifies that the audit callback receives the correct
// agent name and reason sequence — the Host relies on it to surface blocks to the TUI.
func TestSubAgentGuard_BlockHookReceivesAgentAndReason(t *testing.T) {
	s := newTestStore(t)
	var agents, reasons []string
	guard := NewWriterStopGuard(s, func(agent, reason string, _ int32) {
		agents = append(agents, agent)
		reasons = append(reasons, reason)
	})
	normalStop := agentcore.StopInfo{TurnIndex: 1, Message: agentcore.Message{StopReason: agentcore.StopReasonStop}}

	for i := 0; i < subagentMaxConsecutiveBlocks+1; i++ {
		guard(context.Background(), normalStop)
	}
	if len(reasons) != subagentMaxConsecutiveBlocks+1 {
		t.Fatalf("hook called %d times, want %d", len(reasons), subagentMaxConsecutiveBlocks+1)
	}
	for i, agent := range agents {
		if agent != "writer" {
			t.Fatalf("hook call %d: agent = %q, want writer", i, agent)
		}
	}
	for i := 0; i < subagentMaxConsecutiveBlocks; i++ {
		if reasons[i] != "blocked" {
			t.Fatalf("reason[%d] = %q, want blocked", i, reasons[i])
		}
	}
	if last := reasons[len(reasons)-1]; last != "escalated" {
		t.Fatalf("last reason = %q, want escalated", last)
	}

	// hard_stop must be reported too.
	var hardReasons []string
	hardGuard := NewWriterStopGuard(s, func(_, reason string, _ int32) {
		hardReasons = append(hardReasons, reason)
	})
	hardGuard(context.Background(), agentcore.StopInfo{
		TurnIndex: 1,
		Message:   agentcore.Message{StopReason: agentcore.StopReason("safety")},
	})
	if len(hardReasons) != 1 || hardReasons[0] != "hard_stop" {
		t.Fatalf("hard stop hook reasons = %v, want [hard_stop]", hardReasons)
	}
}

// TestEditorStopGuard_TaskAware verifies task awareness: when dispatched to write an arc summary, save_review alone (a review)
// does not count as done; arc_summary must be produced to pass — this seals the starting point of the mid-volume skeleton arc livelock, Defect C.
func TestEditorStopGuard_TaskAware(t *testing.T) {
	normalStop := agentcore.StopInfo{TurnIndex: 1, Message: agentcore.Message{StopReason: agentcore.StopReasonStop}}

	// Summary task + only a review stored → must block (a review does not satisfy the arc_summary requirement).
	t.Run("summary task blocks on review only", func(t *testing.T) {
		s := newTestStore(t)
		guard := NewEditorStopGuard(s, "生成第 5 卷第 1 弧摘要（save_arc_summary）", nil)
		if _, err := s.Checkpoints.Append(domain.ArcScope(5, 1), "review", "reviews/v05a01.json", "d1"); err != nil {
			t.Fatalf("append review: %v", err)
		}
		if d := guard(context.Background(), normalStop); d.Allow {
			t.Fatal("summary task must NOT be satisfied by a review checkpoint")
		}
	})

	// Summary task + arc_summary already stored → pass.
	t.Run("summary task allows on arc_summary", func(t *testing.T) {
		s := newTestStore(t)
		guard := NewEditorStopGuard(s, "生成第 5 卷第 1 弧摘要（save_arc_summary）", nil)
		if _, err := s.Checkpoints.Append(domain.ArcScope(5, 1), "arc_summary", "summaries/arc-v05a01.json", "d1"); err != nil {
			t.Fatalf("append arc_summary: %v", err)
		}
		if d := guard(context.Background(), normalStop); !d.Allow {
			t.Fatal("summary task must be satisfied by an arc_summary checkpoint")
		}
	})

	// Review task + review stored → pass (the default lenient behavior is unchanged).
	t.Run("review task allows on review", func(t *testing.T) {
		s := newTestStore(t)
		guard := NewEditorStopGuard(s, "对第 5 卷第 1 弧做弧级评审（scope=arc）", nil)
		if _, err := s.Checkpoints.Append(domain.ArcScope(5, 1), "review", "reviews/v05a01.json", "d1"); err != nil {
			t.Fatalf("append review: %v", err)
		}
		if d := guard(context.Background(), normalStop); !d.Allow {
			t.Fatal("review task must be satisfied by a review checkpoint")
		}
	})
}
