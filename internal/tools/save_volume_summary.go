package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/CTKiet2006/kietnovel/internal/domain"
	"github.com/CTKiet2006/kietnovel/internal/errs"
	"github.com/CTKiet2006/kietnovel/internal/flow"
	"github.com/CTKiet2006/kietnovel/internal/store"
	"github.com/voocel/agentcore/schema"
)

// SaveVolumeSummaryTool saves the volume-level summary; the Editor calls it at the end of a volume.
type SaveVolumeSummaryTool struct {
	store *store.Store
}

func NewSaveVolumeSummaryTool(store *store.Store) *SaveVolumeSummaryTool {
	return &SaveVolumeSummaryTool{store: store}
}

func (t *SaveVolumeSummaryTool) Name() string { return "save_volume_summary" }
func (t *SaveVolumeSummaryTool) Description() string {
	return "保存卷级摘要（长篇模式，卷结束时调用）"
}
func (t *SaveVolumeSummaryTool) Label() string { return "保存卷摘要" }

// A writing tool; concurrency is forbidden.
func (t *SaveVolumeSummaryTool) ReadOnly(_ json.RawMessage) bool        { return false }
func (t *SaveVolumeSummaryTool) ConcurrencySafe(_ json.RawMessage) bool { return false }

func (t *SaveVolumeSummaryTool) Schema() map[string]any {
	return schema.Object(
		schema.Property("volume", schema.Int("卷号")).Required(),
		schema.Property("title", schema.String("卷标题")).Required(),
		schema.Property("summary", schema.String("卷摘要（500字以内）")).Required(),
		schema.Property("key_events", schema.Array("卷内关键事件", schema.String(""))).Required(),
	)
}

func (t *SaveVolumeSummaryTool) Execute(_ context.Context, args json.RawMessage) (json.RawMessage, error) {
	var a struct {
		Volume    int      `json:"volume"`
		Title     string   `json:"title"`
		Summary   string   `json:"summary"`
		KeyEvents []string `json:"key_events"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, fmt.Errorf("invalid args: %w", err)
	}
	if a.Volume <= 0 {
		return nil, fmt.Errorf("volume must be > 0")
	}
	if strings.TrimSpace(a.Title) == "" || strings.TrimSpace(a.Summary) == "" {
		return nil, fmt.Errorf("title and summary are required: %w", errs.ErrToolArgs)
	}
	volSummary := domain.VolumeSummary{
		Volume:    a.Volume,
		Title:     a.Title,
		Summary:   a.Summary,
		KeyEvents: a.KeyEvents,
	}
	existing, err := t.store.Summaries.LoadVolumeSummary(a.Volume)
	if err != nil {
		return nil, fmt.Errorf("load volume summary: %w: %w", errs.ErrStoreRead, err)
	}
	if existing != nil {
		if !reflect.DeepEqual(*existing, volSummary) {
			return nil, fmt.Errorf("卷 %d 摘要已存在且内容不同，拒绝覆盖: %w", a.Volume, errs.ErrToolConflict)
		}
	} else {
		if err := requireAggregateTarget(t.store, flow.AggregateVolumeSummary, a.Volume, 0, 0); err != nil {
			return nil, err
		}
		if err := t.store.Summaries.SaveVolumeSummary(volSummary); err != nil {
			return nil, fmt.Errorf("save volume summary: %w: %w", errs.ErrStoreWrite, err)
		}
	}

	if _, err := t.store.Checkpoints.AppendArtifact(
		domain.VolumeScope(a.Volume), "volume_summary",
		fmt.Sprintf("summaries/vol-v%02d.json", a.Volume),
	); err != nil {
		return nil, fmt.Errorf("checkpoint volume summary: %w", err)
	}

	result := map[string]any{"saved": true, "type": "volume_summary", "volume": a.Volume}
	// The completion trigger point on the finale main path: the last piece of the end-of-volume wrap-up trio is the volume summary, and once it lands, if the book already
	// satisfies the completion condition, MarkComplete is called in place (the completion check always happens in the tool where the last fact lands,
	// the same pattern as commit_chapter; the predicate is layeredComplete in commit_chapter.go).
	complete, err := ReconcileLayeredCompletion(t.store)
	if err != nil {
		return nil, fmt.Errorf("reconcile book completion: %w", err)
	}
	if complete {
		result["book_complete"] = true
	}

	return json.Marshal(result)
}
