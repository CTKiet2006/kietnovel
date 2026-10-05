package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/store"
)

func setupVolumeSummaryStore(t *testing.T) *store.Store {
	t.Helper()
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	if err := s.Progress.Init(1); err != nil {
		t.Fatal(err)
	}
	volumes := []domain.VolumeOutline{{
		Index: 1, Title: "终卷", Final: true,
		Arcs: []domain.ArcOutline{{
			Index: 1, Title: "收官弧",
			Chapters: []domain.OutlineEntry{{Title: "终章"}},
		}},
	}}
	if err := s.Outline.SaveLayeredOutline(volumes); err != nil {
		t.Fatal(err)
	}
	if err := s.Outline.SaveOutline(domain.FlattenOutline(volumes)); err != nil {
		t.Fatal(err)
	}
	if err := s.Progress.SetLayered(true); err != nil {
		t.Fatal(err)
	}
	if err := s.Progress.MarkChapterComplete(1, 100, "", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.World.SaveReview(domain.ReviewEntry{Chapter: 1, Scope: "arc", Verdict: "accept", Summary: "评审通过"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Summaries.SaveArcSummary(domain.ArcSummary{Volume: 1, Arc: 1, Title: "收官弧", Summary: "完成", KeyEvents: []string{"终局"}}); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSaveVolumeSummaryRejectsNonDueVolume(t *testing.T) {
	s := setupVolumeSummaryStore(t)
	args, err := json.Marshal(map[string]any{
		"volume": 2, "title": "未来卷", "summary": "尚未发生", "key_events": []string{"事件"},
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := NewSaveVolumeSummaryTool(s).Execute(context.Background(), args); err == nil || !strings.Contains(err.Error(), "聚合写入目标不匹配") {
		t.Fatalf("expected non-due volume rejection, got %v", err)
	}
	if summary, err := s.Summaries.LoadVolumeSummary(2); err != nil || summary != nil {
		t.Fatalf("future volume summary must not be persisted, summary=%+v err=%v", summary, err)
	}
}

func TestReconcileLayeredCompletionRepairsInterruptedVolumeSummary(t *testing.T) {
	s := setupVolumeSummaryStore(t)
	// 模拟进程在卷摘要已经落盘、Progress.MarkComplete 尚未执行时退出。
	if err := s.Summaries.SaveVolumeSummary(domain.VolumeSummary{Volume: 1, Title: "终卷", Summary: "全书收束", KeyEvents: []string{"终局"}}); err != nil {
		t.Fatal(err)
	}

	complete, err := ReconcileLayeredCompletion(s)
	if err != nil {
		t.Fatal(err)
	}
	if !complete {
		t.Fatal("persisted final volume summary should be reconciled to complete")
	}
	progress, err := s.Progress.Load()
	if err != nil {
		t.Fatal(err)
	}
	if progress.Phase != domain.PhaseComplete {
		t.Fatalf("phase = %s, want complete", progress.Phase)
	}
}

func TestSaveVolumeSummaryRetriesCheckpointThenCompletes(t *testing.T) {
	s := setupVolumeSummaryStore(t)
	checkpointPath := filepath.Join(s.Dir(), "meta", "checkpoints.jsonl")
	if err := os.MkdirAll(checkpointPath, 0o755); err != nil {
		t.Fatal(err)
	}
	args, err := json.Marshal(map[string]any{
		"volume": 1, "title": "终卷", "summary": "全书收束", "key_events": []string{"终局"},
	})
	if err != nil {
		t.Fatal(err)
	}
	tool := NewSaveVolumeSummaryTool(s)

	if _, err := tool.Execute(context.Background(), args); err == nil || !strings.Contains(err.Error(), "checkpoint volume summary") {
		t.Fatalf("expected checkpoint failure, got %v", err)
	}
	progress, err := s.Progress.Load()
	if err != nil {
		t.Fatal(err)
	}
	if progress.Phase != domain.PhaseWriting {
		t.Fatalf("completion must wait for checkpoint, phase=%s", progress.Phase)
	}
	if err := os.Remove(checkpointPath); err != nil {
		t.Fatal(err)
	}
	raw, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("idempotent checkpoint retry: %v", err)
	}
	var result map[string]any
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	if result["book_complete"] != true {
		t.Fatalf("checkpoint retry must reconcile completion, got %v", result)
	}
}
