package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CTKiet2006/kietnovel/internal/domain"
	"github.com/CTKiet2006/kietnovel/internal/store"
)

func TestSaveFoundationStopsOnCorruptProgress(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "meta", "progress.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]any{"type": "premise", "content": "# 测试"})
	if _, err := NewSaveFoundationTool(st).Execute(context.Background(), args); err == nil {
		t.Fatal("progress 损坏时必须在写入 premise 前失败")
	}
	if _, err := os.Stat(filepath.Join(dir, "premise.md")); !os.IsNotExist(err) {
		t.Fatalf("失败调用不应写 premise，stat err=%v", err)
	}
}

func TestSaveFoundationPersistsPlanningTier(t *testing.T) {
	dir := t.TempDir()
	store := store.NewStore(dir)
	if err := store.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	tool := NewSaveFoundationTool(store)
	args, err := json.Marshal(map[string]any{
		"type":    "premise",
		"content": "# 测试书名\n\n## 题材和基调\n测试",
		"scale":   "long",
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	if _, err := tool.Execute(context.Background(), args); err != nil {
		t.Fatalf("Execute: %v", err)
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

func TestSaveFoundationPremiseDoesNotOwnBookMetadata(t *testing.T) {
	dir := t.TempDir()
	store := store.NewStore(dir)
	if err := store.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := store.Progress.Init(0); err != nil {
		t.Fatalf("Init progress: %v", err)
	}
	if err := store.Book.Save(domain.BookMetadata{Title: "长夜燃灯", Synopsis: "旧城熄灯后，少年追索失踪真相。"}); err != nil {
		t.Fatalf("Save book: %v", err)
	}

	tool := NewSaveFoundationTool(store)
	args, err := json.Marshal(map[string]any{
		"type": "premise",
		"content": `# 长夜燃灯

## 题材和基调
东方玄幻，冷硬求生。`,
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	if _, err := tool.Execute(context.Background(), args); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	book, err := store.Book.Load()
	if err != nil {
		t.Fatalf("Load book: %v", err)
	}
	if book == nil || book.Title != "长夜燃灯" {
		t.Fatalf("premise 不得改写作品信息: %+v", book)
	}
}

func TestSaveFoundationCanRevisePremiseAfterOutline(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := st.Progress.Init(0); err != nil {
		t.Fatalf("Init progress: %v", err)
	}
	if err := st.Progress.UpdatePhase(domain.PhaseOutline); err != nil {
		t.Fatalf("UpdatePhase outline: %v", err)
	}

	args, _ := json.Marshal(map[string]any{
		"type":    "premise",
		"content": "# 新书名\n\n修订后的故事前提。",
	})
	if _, err := NewSaveFoundationTool(st).Execute(context.Background(), args); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	p, err := st.Progress.Load()
	if err != nil {
		t.Fatalf("Load progress: %v", err)
	}
	if p.Phase != domain.PhaseOutline {
		t.Fatalf("phase = %s, want outline", p.Phase)
	}
	if cp := st.Checkpoints.LatestByStep(domain.GlobalScope(), "premise"); cp == nil {
		t.Fatal("修订后的 premise 应生成 checkpoint")
	}
}

func TestSaveFoundationRejectsFullOutlineAfterComplete(t *testing.T) {
	tests := []struct {
		name    string
		typeArg string
		content any
	}{
		{
			name: "flat", typeArg: "outline",
			content: []map[string]any{{"chapter": 1, "title": "覆盖后", "core_event": "变化", "hook": "继续", "scenes": []string{}}},
		},
		{
			name: "layered", typeArg: "layered_outline",
			content: []map[string]any{{
				"index": 1, "title": "覆盖卷", "theme": "变化",
				"arcs": []map[string]any{{
					"index": 1, "title": "覆盖弧", "goal": "变化",
					"chapters": []map[string]any{{"title": "覆盖后", "core_event": "变化", "hook": "继续", "scenes": []string{}}},
				}},
			}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := store.NewStore(t.TempDir())
			if err := s.Init(); err != nil {
				t.Fatal(err)
			}
			if err := s.Progress.Init(1); err != nil {
				t.Fatal(err)
			}
			if err := s.Outline.SaveOutline([]domain.OutlineEntry{{Chapter: 1, Title: "原始标题"}}); err != nil {
				t.Fatal(err)
			}
			if err := s.Progress.MarkComplete(); err != nil {
				t.Fatal(err)
			}

			args, _ := json.Marshal(map[string]any{"type": tt.typeArg, "content": tt.content})
			if _, err := NewSaveFoundationTool(s).Execute(context.Background(), args); err == nil || !strings.Contains(err.Error(), "已完结") {
				t.Fatalf("完结后全量覆盖必须被拒绝，err=%v", err)
			}
			outline, err := s.Outline.LoadOutline()
			if err != nil {
				t.Fatal(err)
			}
			if len(outline) != 1 || outline[0].Title != "原始标题" {
				t.Fatalf("被拒调用修改了 outline: %+v", outline)
			}
			if _, err := os.Stat(filepath.Join(s.Dir(), "layered_outline.json")); tt.typeArg == "layered_outline" && !os.IsNotExist(err) {
				t.Fatalf("被拒调用写入了 layered_outline.json: %v", err)
			}
		})
	}
}

func TestSaveFoundationOutlineClearsLayeredStateWhenDowngrading(t *testing.T) {
	dir := t.TempDir()
	store := store.NewStore(dir)
	if err := store.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := store.Progress.Init(0); err != nil {
		t.Fatalf("InitProgress: %v", err)
	}

	tool := NewSaveFoundationTool(store)

	layeredArgs, err := json.Marshal(map[string]any{
		"type":    "layered_outline",
		"content": `[{"index":1,"title":"第一卷","theme":"主题","arcs":[{"index":1,"title":"第一弧","goal":"目标","chapters":[{"chapter":1,"title":"第一章","core_event":"开局","hook":"继续"}]}]}]`,
		"scale":   "long",
	})
	if err != nil {
		t.Fatalf("Marshal layered args: %v", err)
	}
	rawResult, err := tool.Execute(context.Background(), layeredArgs)
	if err != nil {
		t.Fatalf("Execute layered outline: %v", err)
	}
	var layeredResult map[string]any
	if err := json.Unmarshal(rawResult, &layeredResult); err != nil {
		t.Fatalf("Unmarshal layered result: %v", err)
	}
	if layeredResult["dynamic_planning"] != true || layeredResult["outlined_chapters"] != float64(1) {
		t.Fatalf("layered result 应报告当前已细化章节: %#v", layeredResult)
	}
	if _, exists := layeredResult["chapters"]; exists {
		t.Fatalf("layered result 不得把内部容量估算暴露为 chapters: %#v", layeredResult)
	}

	outlineArgs, err := json.Marshal(map[string]any{
		"type":    "outline",
		"content": `[{"chapter":1,"title":"第一章","core_event":"改为中篇","hook":"继续"}]`,
		"scale":   "mid",
	})
	if err != nil {
		t.Fatalf("Marshal outline args: %v", err)
	}
	if _, err := tool.Execute(context.Background(), outlineArgs); err != nil {
		t.Fatalf("Execute outline: %v", err)
	}

	progress, err := store.Progress.Load()
	if err != nil {
		t.Fatalf("LoadProgress: %v", err)
	}
	if progress == nil {
		t.Fatal("expected progress to exist")
	}
	if progress.Layered {
		t.Fatal("expected layered mode to be disabled")
	}
	if progress.CurrentVolume != 0 || progress.CurrentArc != 0 {
		t.Fatalf("expected volume/arc reset, got volume=%d arc=%d", progress.CurrentVolume, progress.CurrentArc)
	}

	volumes, err := store.Outline.LoadLayeredOutline()
	if err != nil {
		t.Fatalf("LoadLayeredOutline: %v", err)
	}
	if len(volumes) != 0 {
		t.Fatalf("expected layered outline cleared, got %d volumes", len(volumes))
	}

	meta, err := store.RunMeta.Load()
	if err != nil {
		t.Fatalf("LoadRunMeta: %v", err)
	}
	if meta == nil {
		t.Fatal("expected run meta to exist")
	}
	if meta.PlanningTier != domain.PlanningTierMid {
		t.Fatalf("expected planning tier %q, got %q", domain.PlanningTierMid, meta.PlanningTier)
	}
}

func TestSaveFoundationAppendVolume(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init(0); err != nil {
		t.Fatalf("InitProgress: %v", err)
	}

	tool := NewSaveFoundationTool(s)

	// First create the initial layered_outline (volume 1)
	layeredArgs, _ := json.Marshal(map[string]any{
		"type": "layered_outline",
		"content": []map[string]any{{
			"index": 1, "title": "第一卷", "theme": "起步",
			"arcs": []map[string]any{{
				"index": 1, "title": "首弧", "goal": "目标",
				"chapters": []map[string]any{{"title": "第一章", "core_event": "开局", "hook": "继续"}},
			}},
		}},
		"scale": "long",
	})
	if _, err := tool.Execute(context.Background(), layeredArgs); err != nil {
		t.Fatalf("Execute layered: %v", err)
	}

	// append_volume: append volume 2
	appendArgs, _ := json.Marshal(map[string]any{
		"type":   "append_volume",
		"reason": "主线仍有多条长线未收束，需继续第二卷",
		"content": map[string]any{
			"index": 2, "title": "第二卷", "theme": "升级",
			"arcs": []map[string]any{{
				"index": 1, "title": "弧一", "goal": "目标",
				"chapters": []map[string]any{{"title": "新章", "core_event": "推进", "hook": "钩子"}},
			}},
		},
	})
	res, err := tool.Execute(context.Background(), appendArgs)
	if err != nil {
		t.Fatalf("Execute append_volume: %v", err)
	}
	var result map[string]any
	json.Unmarshal(res, &result)
	if result["volume"] != float64(2) {
		t.Fatalf("expected volume=2, got %v", result["volume"])
	}

	// Verify the outline has 2 volumes
	volumes, _ := s.Outline.LoadLayeredOutline()
	if len(volumes) != 2 {
		t.Fatalf("expected 2 volumes, got %d", len(volumes))
	}
	if volumes[1].Title != "第二卷" {
		t.Fatalf("expected title '第二卷', got %q", volumes[1].Title)
	}

	// The end-of-volume decision reason must go into the decision audit
	recs, _ := s.Decisions.Recent(1)
	if len(recs) != 1 || recs[0].Kind != "volume_end" || recs[0].Decider != "architect" {
		t.Fatalf("append_volume 应落一条 volume_end 裁定审计, got %+v", recs)
	}
	if recs[0].Reason == "" || !strings.Contains(string(recs[0].Decision), `"append_volume"`) {
		t.Fatalf("审计记录应含 reason 与 action, got %+v", recs[0])
	}
}

func TestExpandNextArcCalibratesTarget(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init(5); err != nil {
		t.Fatalf("InitProgress: %v", err)
	}
	if err := s.Outline.SaveLayeredOutline([]domain.VolumeOutline{{
		Index: 1, Title: "第一卷", Theme: "选择",
		Arcs: []domain.ArcOutline{
			{Index: 1, Title: "已完成弧", Goal: "建立同盟", Chapters: []domain.OutlineEntry{{Title: "分裂", CoreEvent: "同盟意外破裂"}}},
			{Index: 2, Title: "旧标题", Goal: "维持同盟", EstimatedChapters: 4},
		},
	}}); err != nil {
		t.Fatalf("SaveLayeredOutline: %v", err)
	}
	// Before v0.7.9, append_volume allowed the model to omit the mechanical index; the new tool derives it from the structural position.
	legacy, err := os.ReadFile(filepath.Join(dir, "layered_outline.json"))
	if err != nil {
		t.Fatal(err)
	}
	legacy = []byte(strings.ReplaceAll(string(legacy), `"index": 2`, `"index": 0`))
	if err := os.WriteFile(filepath.Join(dir, "layered_outline.json"), legacy, 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := s.Progress.Load()
	if err != nil {
		t.Fatal(err)
	}
	p.CompletedChapters = []int{1}
	if err := s.Progress.Save(p); err != nil {
		t.Fatal(err)
	}

	tool := NewExpandNextArcTool(s)
	args, _ := json.Marshal(map[string]any{
		"title": "裂盟之后",
		"goal":  "让分裂后的双方以不同选择推进同一主线",
		"chapters": []map[string]any{{
			"title": "各走一边", "core_event": "双方分别追索真相", "hook": "两条线索意外重合", "scenes": []string{"分道", "追索"},
		}},
	})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute expand_next_arc: %v", err)
	}
	var facts map[string]any
	if err := json.Unmarshal(result, &facts); err != nil {
		t.Fatalf("Unmarshal result: %v", err)
	}
	if facts["title"] != "裂盟之后" || facts["goal"] != "让分裂后的双方以不同选择推进同一主线" {
		t.Fatalf("expected calibrated facts, got %+v", facts)
	}
	volumes, err := s.Outline.LoadLayeredOutline()
	if err != nil {
		t.Fatalf("LoadLayeredOutline: %v", err)
	}
	if got := volumes[0].Arcs[1]; got.Title != "裂盟之后" || got.Goal != "让分裂后的双方以不同选择推进同一主线" || len(got.Chapters) != 1 {
		t.Fatalf("unexpected expanded arc: %+v", got)
	}
}

func TestSaveFoundationAppendVolumeAssignsIndexes(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init(0); err != nil {
		t.Fatalf("InitProgress: %v", err)
	}

	tool := NewSaveFoundationTool(s)

	// The initial volume
	layeredArgs, _ := json.Marshal(map[string]any{
		"type": "layered_outline",
		"content": []map[string]any{{
			"index": 1, "title": "第一卷", "theme": "起步",
			"arcs": []map[string]any{{
				"index": 1, "title": "首弧", "goal": "目标",
				"chapters": []map[string]any{{"title": "第一章", "core_event": "开局", "hook": "继续"}},
			}},
		}},
		"scale": "long",
	})
	tool.Execute(context.Background(), layeredArgs)

	// The mechanical ordinal supplied by the model is ignored; the store generates one in story order.
	appendArgs, _ := json.Marshal(map[string]any{
		"type":   "append_volume",
		"reason": "测试理由",
		"content": map[string]any{
			"index": 99, "title": "第二卷", "theme": "x",
			"arcs": []map[string]any{{
				"index": 99, "title": "弧一", "goal": "目标",
				"chapters": []map[string]any{{"title": "章", "core_event": "事件", "hook": "钩子"}},
			}},
		},
	})
	result, err := tool.Execute(context.Background(), appendArgs)
	if err != nil {
		t.Fatalf("Execute append_volume: %v", err)
	}
	var facts map[string]any
	if err := json.Unmarshal(result, &facts); err != nil {
		t.Fatal(err)
	}
	if facts["volume"] != float64(2) {
		t.Fatalf("expected assigned volume 2, got %+v", facts)
	}
	volumes, err := s.Outline.LoadLayeredOutline()
	if err != nil {
		t.Fatal(err)
	}
	if volumes[1].Index != 2 || volumes[1].Arcs[0].Index != 1 {
		t.Fatalf("unexpected indexes: %+v", volumes[1])
	}
}

// TestSaveFoundationAppendVolumeRejectsAfterComplete verifies that append_volume is not allowed after Phase=Complete.
// This replaces the old "reject appending to a Final volume" semantics (the Final field has been removed).
func TestSaveFoundationAppendVolumeRejectsAfterComplete(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init(0); err != nil {
		t.Fatalf("InitProgress: %v", err)
	}
	if err := s.Progress.MarkComplete(); err != nil {
		t.Fatalf("MarkComplete: %v", err)
	}

	tool := NewSaveFoundationTool(s)
	appendArgs, _ := json.Marshal(map[string]any{
		"type":   "append_volume",
		"reason": "测试理由",
		"content": map[string]any{
			"index": 1, "title": "尝试续写", "theme": "x",
			"arcs": []map[string]any{{
				"index": 1, "title": "弧", "goal": "g",
				"chapters": []map[string]any{{"title": "章", "core_event": "e", "hook": "h"}},
			}},
		},
	})
	if _, err := tool.Execute(context.Background(), appendArgs); err == nil {
		t.Fatal("expected error when appending after Phase=Complete")
	}
}

func TestSaveFoundationUpdateCompass(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	tool := NewSaveFoundationTool(s)
	args, _ := json.Marshal(map[string]any{
		"type": "update_compass",
		"content": map[string]any{
			"ending_direction": "主角面对最终抉择",
			"open_threads":     []string{"线索A", "关系B"},
			"estimated_scale":  "预计 4-6 卷",
		},
	})
	_, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute update_compass: %v", err)
	}

	compass, err := s.Outline.LoadCompass()
	if err != nil {
		t.Fatalf("LoadCompass: %v", err)
	}
	if compass == nil || compass.EndingDirection != "主角面对最终抉择" {
		t.Fatalf("unexpected compass: %+v", compass)
	}
	if len(compass.OpenThreads) != 2 {
		t.Fatalf("expected 2 open threads, got %d", len(compass.OpenThreads))
	}
}

func TestSaveFoundationUpdateCompassOverridesLastUpdated(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Save(&domain.Progress{
		Phase:             domain.PhaseWriting,
		CompletedChapters: []int{1, 2, 3, 5, 4}, // out of order, verifying that max is taken rather than len
	}); err != nil {
		t.Fatalf("Save progress: %v", err)
	}

	tool := NewSaveFoundationTool(s)
	args, _ := json.Marshal(map[string]any{
		"type": "update_compass",
		"content": map[string]any{
			"ending_direction": "主角面对最终抉择",
			"open_threads":     []string{"线索A"},
			"last_updated":     0, // the LLM usually forgets to fill this in or leaves 0
		},
	})
	if _, err := tool.Execute(context.Background(), args); err != nil {
		t.Fatalf("Execute update_compass: %v", err)
	}

	compass, err := s.Outline.LoadCompass()
	if err != nil {
		t.Fatalf("LoadCompass: %v", err)
	}
	if compass.LastUpdated != 5 {
		t.Fatalf("expected LastUpdated=5 (max of CompletedChapters), got %d", compass.LastUpdated)
	}
}

func TestSaveFoundationUpdateCompassRequiresDirection(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	tool := NewSaveFoundationTool(s)
	args, _ := json.Marshal(map[string]any{
		"type":    "update_compass",
		"content": map[string]any{"estimated_scale": "3 卷"},
	})
	_, err := tool.Execute(context.Background(), args)
	if err == nil {
		t.Fatal("expected error when ending_direction is empty")
	}
}

func TestSaveFoundationAcceptsDirectJSONArrayContent(t *testing.T) {
	dir := t.TempDir()
	store := store.NewStore(dir)
	if err := store.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	tool := NewSaveFoundationTool(store)
	args, err := json.Marshal(map[string]any{
		"type": "outline",
		"content": []map[string]any{
			{
				"chapter":    1,
				"title":      "第一章",
				"core_event": "主角登场",
				"hook":       "继续",
				"scenes":     []string{"场景一", "场景二"},
			},
		},
		"scale": "short",
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	if _, err := tool.Execute(context.Background(), args); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	outline, err := store.Outline.LoadOutline()
	if err != nil {
		t.Fatalf("LoadOutline: %v", err)
	}
	if len(outline) != 1 || outline[0].Title != "第一章" {
		t.Fatalf("unexpected outline: %+v", outline)
	}
}

// completeBookSetup builds a minimal Store in the writing phase with 2 chapters, for the complete_book
// test series. Tool-layer checks (all enumerable, in code rather than in the prompt): progress is initialised,
// PendingRewrites is empty, at least one chapter has been written, and the outline has no unwritten chapters.
func completeBookSetup(t *testing.T) *store.Store {
	t.Helper()
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init(2); err != nil {
		t.Fatalf("InitProgress: %v", err)
	}
	_ = s.Progress.UpdatePhase(domain.PhaseWriting)
	return s
}

func TestSaveFoundationCompleteBookPushesPhaseComplete(t *testing.T) {
	s := completeBookSetup(t)
	for ch := 1; ch <= 2; ch++ {
		if err := s.Progress.MarkChapterComplete(ch, 3000, "", ""); err != nil {
			t.Fatalf("MarkChapterComplete(%d): %v", ch, err)
		}
	}
	tool := NewSaveFoundationTool(s)
	args, _ := json.Marshal(map[string]any{
		"type": "complete_book", "content": map[string]any{},
		"reason": "两章大纲全部写完，终局命题已回答",
	})
	res, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute complete_book: %v", err)
	}
	var result map[string]any
	_ = json.Unmarshal(res, &result)
	if result["book_complete"] != true {
		t.Fatalf("expected book_complete=true, got %+v", result)
	}
	if result["phase"] != string(domain.PhaseComplete) {
		t.Fatalf("expected phase=complete, got %v", result["phase"])
	}
	progress, _ := s.Progress.Load()
	if progress.Phase != domain.PhaseComplete {
		t.Fatalf("expected progress.Phase=complete, got %s", progress.Phase)
	}

	// The reason for the completion decision must go into the decision audit (the fact snapshot is taken at the moment of the decision)
	recs, _ := s.Decisions.Recent(1)
	if len(recs) != 1 || recs[0].Kind != "volume_end" || recs[0].Decider != "architect" {
		t.Fatalf("complete_book 应落一条 volume_end 裁定审计, got %+v", recs)
	}
	if recs[0].Reason == "" || !strings.Contains(string(recs[0].Decision), `"complete_book"`) {
		t.Fatalf("审计记录应含 reason 与 action, got %+v", recs[0])
	}
	if !strings.Contains(string(recs[0].Facts), `"completed_chapters":2`) {
		t.Fatalf("审计 facts 应含判定时刻进度, got %s", recs[0].Facts)
	}
}

// TestSaveFoundationCompleteBookRejectsZeroChapters reproduces a real incident: planning had just been persisted
// and phase had auto-flipped to writing, so a weak model casually called complete_book -- with not one chapter written it must be rejected,
// otherwise the entire book gets skipped (0/68 chapters marked complete).
func TestSaveFoundationCompleteBookRejectsZeroChapters(t *testing.T) {
	s := completeBookSetup(t)
	tool := NewSaveFoundationTool(s)
	args, _ := json.Marshal(map[string]any{
		"type": "complete_book", "content": map[string]any{},
		"reason": "测试理由",
	})
	if _, err := tool.Execute(context.Background(), args); err == nil {
		t.Fatal("一章未写的 complete_book 必须被拒")
	}
	progress, _ := s.Progress.Load()
	if progress.Phase != domain.PhaseWriting {
		t.Fatalf("phase 应保持 writing, got %s", progress.Phase)
	}
}

// TestSaveFoundationCompleteBookRejectsOpenThreads guards the tool-level enforcement of "no completion while a long thread is unresolved"
// defence: the OpenThreads contract is exactly "must be resolved for an ending", but in practice the architect waives an unresolved thread in its reasoning as
// "deliberate authorial blank space" and completes the book outright (in the scenario of continuing an imported finished book, the user's wish to keep writing is locked behind the completion rules). Any waiver must
// be explicitly persisted -- update_compass must clear open_threads before completion is allowed.
func TestSaveFoundationCompleteBookRejectsOpenThreads(t *testing.T) {
	s := completeBookSetup(t)
	for ch := 1; ch <= 2; ch++ {
		if err := s.Progress.MarkChapterComplete(ch, 3000, "", ""); err != nil {
			t.Fatalf("MarkChapterComplete(%d): %v", ch, err)
		}
	}
	if err := s.Outline.SaveCompass(domain.StoryCompass{
		EndingDirection: "潜在终局", OpenThreads: []string{"八十年大限走向", "精变重逢可能"},
	}); err != nil {
		t.Fatalf("SaveCompass: %v", err)
	}
	tool := NewSaveFoundationTool(s)
	args, _ := json.Marshal(map[string]any{
		"type": "complete_book", "content": map[string]any{}, "reason": "主线已闭合",
	})
	_, err := tool.Execute(context.Background(), args)
	if err == nil || !strings.Contains(err.Error(), "open_threads") {
		t.Fatalf("open_threads 非空应拒绝完本并指引 update_compass，得：%v", err)
	}
	if p, _ := s.Progress.Load(); p.Phase != domain.PhaseWriting {
		t.Fatalf("phase 应保持 writing，得 %s", p.Phase)
	}
	// Allowed once the explicit resolution is persisted (update_compass clears open_threads).
	if err := s.Outline.SaveCompass(domain.StoryCompass{EndingDirection: "终局已达成"}); err != nil {
		t.Fatalf("SaveCompass: %v", err)
	}
	if _, err := tool.Execute(context.Background(), args); err != nil {
		t.Fatalf("长线清空后完本应放行：%v", err)
	}
}

// TestSaveFoundationCompleteBookRejectsUnwrittenChapters when the outline still has unwritten chapters
// completion is not allowed (the regular path for closing early is a final finale volume).
func TestSaveFoundationCompleteBookRejectsUnwrittenChapters(t *testing.T) {
	s := completeBookSetup(t)
	if err := s.Progress.MarkChapterComplete(1, 3000, "", ""); err != nil {
		t.Fatalf("MarkChapterComplete: %v", err)
	}
	tool := NewSaveFoundationTool(s)
	args, _ := json.Marshal(map[string]any{
		"type": "complete_book", "content": map[string]any{},
		"reason": "测试理由",
	})
	_, err := tool.Execute(context.Background(), args)
	if err == nil {
		t.Fatal("大纲内有未写章节的 complete_book 必须被拒")
	}
	if !strings.Contains(err.Error(), "final") {
		t.Fatalf("拒绝文案应引导 final 收官卷路径, got %v", err)
	}
	progress, _ := s.Progress.Load()
	if progress.Phase != domain.PhaseWriting {
		t.Fatalf("phase 应保持 writing, got %s", progress.Phase)
	}
}

func TestSaveFoundationCompleteBookRejectsBeforeWriting(t *testing.T) {
	// A mistaken complete_book call during planning must be rejected, otherwise it would skip the writing of the whole book outright.
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init(0); err != nil {
		t.Fatalf("InitProgress: %v", err)
	}
	_ = s.Progress.UpdatePhase(domain.PhasePremise)
	_ = s.Progress.UpdatePhase(domain.PhaseOutline)
	tool := NewSaveFoundationTool(s)
	args, _ := json.Marshal(map[string]any{
		"type": "complete_book", "content": map[string]any{},
		"reason": "测试理由",
	})
	if _, err := tool.Execute(context.Background(), args); err == nil {
		t.Fatal("expected error when phase != writing")
	}
	progress, _ := s.Progress.Load()
	if progress.Phase != domain.PhaseOutline {
		t.Fatalf("phase should remain outline, got %s", progress.Phase)
	}
}

// TestSaveFoundationVolumeEndRequiresReason the end-of-volume choice of three must carry a reason --
// it is the heaviest semantic judgement in the whole book, and the reason must become an audit fact rather than being scattered across the session log.
func TestSaveFoundationVolumeEndRequiresReason(t *testing.T) {
	s := completeBookSetup(t)
	tool := NewSaveFoundationTool(s)
	for _, typ := range []string{"append_volume", "complete_book"} {
		args, _ := json.Marshal(map[string]any{
			"type": typ, "content": map[string]any{},
		})
		_, err := tool.Execute(context.Background(), args)
		if err == nil || !strings.Contains(err.Error(), "reason") {
			t.Fatalf("%s 缺 reason 必须被拒且文案提及 reason, got %v", typ, err)
		}
	}
	if recs, _ := s.Decisions.Recent(1); len(recs) != 0 {
		t.Fatalf("被拒调用不应产生审计记录, got %+v", recs)
	}
}

func TestSaveFoundationCompleteBookRejectsWithPendingRewrites(t *testing.T) {
	s := completeBookSetup(t)
	if err := s.Progress.MarkChapterComplete(2, 3000, "", ""); err != nil {
		t.Fatalf("MarkChapterComplete: %v", err)
	}
	if err := s.Progress.SetPendingRewrites([]int{2}, "尾章节奏过快"); err != nil {
		t.Fatalf("SetPendingRewrites: %v", err)
	}
	tool := NewSaveFoundationTool(s)
	args, _ := json.Marshal(map[string]any{
		"type": "complete_book", "content": map[string]any{},
		"reason": "测试理由",
	})
	if _, err := tool.Execute(context.Background(), args); err == nil {
		t.Fatal("expected error when PendingRewrites non-empty")
	}
	progress, _ := s.Progress.Load()
	if progress.Phase == domain.PhaseComplete {
		t.Fatalf("phase should not be Complete with PendingRewrites: %s", progress.Phase)
	}
}
