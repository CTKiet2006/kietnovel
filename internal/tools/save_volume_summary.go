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
	switch toolLang(t.store) {
	case "vi":
		return "Lưu tóm tắt cấp độ quyển (chế độ trường thiên, gọi khi kết thúc quyển)"
	case "en":
		return "Save volume-level summary (longform mode, called at volume conclusion)"
	default:
		return "保存卷级摘要（长篇模式，卷结束时调用）"
	}
}
func (t *SaveVolumeSummaryTool) Label() string {
	switch toolLang(t.store) {
	case "vi":
		return "Lưu tóm tắt quyển"
	case "en":
		return "Save volume summary"
	default:
		return "保存卷摘要"
	}
}

// A writing tool; concurrency is forbidden.
func (t *SaveVolumeSummaryTool) ReadOnly(_ json.RawMessage) bool        { return false }
func (t *SaveVolumeSummaryTool) ConcurrencySafe(_ json.RawMessage) bool { return false }

func (t *SaveVolumeSummaryTool) Schema() map[string]any {
	volDesc := "卷号"
	titleDesc := "卷标题"
	sumDesc := "卷摘要（500字以内）"
	keyEventsDesc := "卷内关键事件"

	switch toolLang(t.store) {
	case "vi":
		volDesc = "Số thứ tự quyển"
		titleDesc = "Tiêu đề quyển"
		sumDesc = "Tóm tắt quyển (dưới 500 từ)"
		keyEventsDesc = "Các sự kiện then chốt trong quyển"
	case "en":
		volDesc = "Volume index"
		titleDesc = "Volume title"
		sumDesc = "Volume summary (under 500 words)"
		keyEventsDesc = "Key volume events"
	}

	return schema.Object(
		schema.Property("volume", schema.Int(volDesc)).Required(),
		schema.Property("title", schema.String(titleDesc)).Required(),
		schema.Property("summary", schema.String(sumDesc)).Required(),
		schema.Property("key_events", schema.Array(keyEventsDesc, schema.String(""))).Required(),
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
