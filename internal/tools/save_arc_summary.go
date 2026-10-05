package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/CTKiet2006/kietnovel/internal/domain"
	"github.com/CTKiet2006/kietnovel/internal/errs"
	"github.com/CTKiet2006/kietnovel/internal/flow"
	"github.com/CTKiet2006/kietnovel/internal/store"
	"github.com/voocel/agentcore/schema"
)

// SaveArcSummaryTool saves the arc-level summary, the character snapshot and the writing rules; the Editor calls it at the end of an arc.
type SaveArcSummaryTool struct {
	store *store.Store
}

func NewSaveArcSummaryTool(store *store.Store) *SaveArcSummaryTool {
	return &SaveArcSummaryTool{store: store}
}

func (t *SaveArcSummaryTool) Name() string { return "save_arc_summary" }
func (t *SaveArcSummaryTool) Description() string {
	switch toolLang(t.store) {
	case "vi":
		return "Lưu tóm tắt cấp độ arc, snapshot trạng thái nhân vật và quy tắc sáng tác (chế độ trường thiên, gọi khi kết thúc arc)"
	case "en":
		return "Save arc-level summary, character state snapshots, and writing rules (longform mode, called at arc conclusion)"
	default:
		return "保存弧级摘要、角色状态快照和写作规则（长篇模式，弧结束时调用）"
	}
}
func (t *SaveArcSummaryTool) Label() string {
	switch toolLang(t.store) {
	case "vi":
		return "Lưu tóm tắt arc"
	case "en":
		return "Save arc summary"
	default:
		return "保存弧摘要"
	}
}

// A writing tool; concurrency is forbidden.
func (t *SaveArcSummaryTool) ReadOnly(_ json.RawMessage) bool        { return false }
func (t *SaveArcSummaryTool) ConcurrencySafe(_ json.RawMessage) bool { return false }

func (t *SaveArcSummaryTool) Schema() map[string]any {
	nameDesc := "角色名"
	statusDesc := "当前状态（存活/受伤/失踪等）"
	powerDesc := "能力变化"
	motDesc := "当前动机"
	relDesc := "关键关系变化"
	voiceRulesDesc := "2-3 条语言特征规则（每条 ≤30 字）"
	proseDesc := "3-5 条叙述风格规则（每条 ≤50 字，要具体可执行）"
	diaDesc := "核心角色的对话特征规则"
	taboosDesc := "本小说需避免的写法"
	volDesc := "卷号"
	arcDesc := "弧号"
	titleDesc := "弧标题"
	sumDesc := "弧摘要（500字以内）"
	keyEventsDesc := "弧内关键事件"
	snapDesc := "角色状态快照"
	styleRulesDesc := "写作规则"

	switch toolLang(t.store) {
	case "vi":
		nameDesc = "Tên nhân vật"
		statusDesc = "Trạng thái hiện tại (còn sống / bị thương / mất tích...)"
		powerDesc = "Biến chuyển năng lực / sức mạnh"
		motDesc = "Động cơ hành động hiện tại"
		relDesc = "Biến chuyển quan hệ then chốt"
		voiceRulesDesc = "2-3 quy tắc đặc trưng ngôn ngữ nhân vật"
		proseDesc = "3-5 quy tắc văn phong tự sự (cụ thể, khả thi)"
		diaDesc = "Quy tắc đặc trưng đối thoại của các nhân vật nòng cốt"
		taboosDesc = "Các điều cấm kỵ cần tránh trong truyện"
		volDesc = "Số thứ tự quyển"
		arcDesc = "Số thứ tự arc"
		titleDesc = "Tiêu đề arc"
		sumDesc = "Tóm tắt arc (dưới 500 từ)"
		keyEventsDesc = "Các sự kiện then chốt trong arc"
		snapDesc = "Snapshot trạng thái nhân vật"
		styleRulesDesc = "Bộ quy tắc văn phong"
	case "en":
		nameDesc = "Character name"
		statusDesc = "Current status (alive / injured / missing, etc.)"
		powerDesc = "Ability / power progression"
		motDesc = "Current motivation"
		relDesc = "Key relationship changes"
		voiceRulesDesc = "2-3 character voice rules"
		proseDesc = "3-5 narrative style rules"
		diaDesc = "Dialogue rules for core cast"
		taboosDesc = "Stylistic taboos to avoid"
		volDesc = "Volume index"
		arcDesc = "Arc index"
		titleDesc = "Arc title"
		sumDesc = "Arc summary (under 500 words)"
		keyEventsDesc = "Key arc events"
		snapDesc = "Character state snapshots"
		styleRulesDesc = "Style rules"
	}

	snapshotSchema := schema.Object(
		schema.Property("name", schema.String(nameDesc)).Required(),
		schema.Property("status", schema.String(statusDesc)).Required(),
		schema.Property("power", schema.String(powerDesc)),
		schema.Property("motivation", schema.String(motDesc)).Required(),
		schema.Property("relations", schema.String(relDesc)),
	)
	voiceSchema := schema.Object(
		schema.Property("name", schema.String(nameDesc)).Required(),
		schema.Property("rules", schema.Array(voiceRulesDesc, schema.String(""))).Required(),
	)
	styleRulesSchema := schema.Object(
		schema.Property("prose", schema.Array(proseDesc, schema.String(""))).Required(),
		schema.Property("dialogue", schema.Array(diaDesc, voiceSchema)).Required(),
		schema.Property("taboos", schema.Array(taboosDesc, schema.String(""))),
	)
	styleRulesSchema["description"] = styleRulesDesc
	return schema.Object(
		schema.Property("volume", schema.Int(volDesc)).Required(),
		schema.Property("arc", schema.Int(arcDesc)).Required(),
		schema.Property("title", schema.String(titleDesc)).Required(),
		schema.Property("summary", schema.String(sumDesc)).Required(),
		schema.Property("key_events", schema.Array(keyEventsDesc, schema.String(""))).Required(),
		schema.Property("character_snapshots", schema.Array(snapDesc, snapshotSchema)).Required(),
		schema.Property("style_rules", styleRulesSchema).Required(),
	)
}

func (t *SaveArcSummaryTool) Execute(_ context.Context, args json.RawMessage) (json.RawMessage, error) {
	var a struct {
		Volume             int                        `json:"volume"`
		Arc                int                        `json:"arc"`
		Title              string                     `json:"title"`
		Summary            string                     `json:"summary"`
		KeyEvents          []string                   `json:"key_events"`
		CharacterSnapshots []domain.CharacterSnapshot `json:"character_snapshots"`
		StyleRules         *arcSummaryStyleRules      `json:"style_rules"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		if strings.Contains(err.Error(), "style_rules.dialogue") {
			return nil, fmt.Errorf("invalid args: style_rules.dialogue must be an array of objects {name, rules}, not strings: %w: %w", errs.ErrToolArgs, err)
		}
		return nil, fmt.Errorf("invalid args: %w: %w", errs.ErrToolArgs, err)
	}
	if a.Volume <= 0 || a.Arc <= 0 {
		return nil, fmt.Errorf("volume and arc must be > 0: %w", errs.ErrToolArgs)
	}
	if strings.TrimSpace(a.Title) == "" || strings.TrimSpace(a.Summary) == "" {
		return nil, fmt.Errorf("title and summary are required: %w", errs.ErrToolArgs)
	}
	if err := validateArcSummaryStyleRules(a.StyleRules); err != nil {
		return nil, err
	}
	for i := range a.CharacterSnapshots {
		a.CharacterSnapshots[i].Volume = a.Volume
		a.CharacterSnapshots[i].Arc = a.Arc
	}
	arcSummary := domain.ArcSummary{
		Volume: a.Volume, Arc: a.Arc, Title: a.Title, Summary: a.Summary, KeyEvents: a.KeyEvents,
	}
	rules := domain.WritingStyleRules{
		Volume:    a.Volume,
		Arc:       a.Arc,
		Prose:     a.StyleRules.Prose,
		Dialogue:  a.StyleRules.Dialogue,
		Taboos:    a.StyleRules.Taboos,
		UpdatedAt: time.Now().Format(time.RFC3339),
	}
	replay, err := t.arcSummaryReplay(arcSummary, a.CharacterSnapshots, rules)
	if err != nil {
		return nil, err
	}
	if !replay {
		if err := requireAggregateTarget(t.store, flow.AggregateArcSummary, a.Volume, a.Arc, 0); err != nil {
			return nil, err
		}
		if len(a.CharacterSnapshots) > 0 {
			if err := t.store.Characters.SaveSnapshots(a.Volume, a.Arc, a.CharacterSnapshots); err != nil {
				return nil, fmt.Errorf("save character snapshots: %w: %w", errs.ErrStoreWrite, err)
			}
		}
		if err := t.store.World.SaveStyleRules(rules); err != nil {
			return nil, fmt.Errorf("save style rules: %w: %w", errs.ErrStoreWrite, err)
		}

		// The arc summary is the Router's completion marker and is written as the last semantic artifact. If an earlier step
		// fails, the summary stays missing and after recovery the Router still re-dispatches this task.
		if err := t.store.Summaries.SaveArcSummary(arcSummary); err != nil {
			return nil, fmt.Errorf("save arc summary: %w: %w", errs.ErrStoreWrite, err)
		}
	}

	artifacts := []string{fmt.Sprintf("summaries/arc-v%02da%02d.json", a.Volume, a.Arc)}
	if len(a.CharacterSnapshots) > 0 {
		artifacts = append(artifacts, fmt.Sprintf("meta/snapshots/v%02da%02d.json", a.Volume, a.Arc))
	}
	artifacts = append(artifacts, "meta/style_rules.json")

	if _, err := t.store.Checkpoints.AppendArtifacts(
		domain.ArcScope(a.Volume, a.Arc), "arc_summary", artifacts...,
	); err != nil {
		return nil, fmt.Errorf("checkpoint arc summary: %w: %w", errs.ErrStoreWrite, err)
	}

	return json.Marshal(map[string]any{
		"saved": true, "type": "arc_summary",
		"volume": a.Volume, "arc": a.Arc,
		"snapshots":         len(a.CharacterSnapshots),
		"style_rules_saved": true,
	})
}

// arcSummaryReplay only lets through an idempotent wrap-up whose content is exactly identical, for retries where the semantic artifact is already on disk but
// the checkpoint append failed. Any difference is an explicit conflict; a retry must never overwrite historical aggregate facts.
func (t *SaveArcSummaryTool) arcSummaryReplay(
	summary domain.ArcSummary,
	snapshots []domain.CharacterSnapshot,
	rules domain.WritingStyleRules,
) (bool, error) {
	existing, err := t.store.Summaries.LoadArcSummary(summary.Volume, summary.Arc)
	if err != nil {
		return false, fmt.Errorf("load arc summary: %w: %w", errs.ErrStoreRead, err)
	}
	if existing == nil {
		return false, nil
	}
	storedSnapshots, err := t.store.Characters.LoadSnapshots(summary.Volume, summary.Arc)
	if err != nil {
		return false, fmt.Errorf("load character snapshots: %w: %w", errs.ErrStoreRead, err)
	}
	storedRules, err := t.store.World.LoadStyleRules()
	if err != nil {
		return false, fmt.Errorf("load style rules: %w: %w", errs.ErrStoreRead, err)
	}
	if storedRules != nil {
		rules.UpdatedAt = storedRules.UpdatedAt
	}
	if !reflect.DeepEqual(*existing, summary) ||
		!slices.Equal(storedSnapshots, snapshots) ||
		storedRules == nil || !reflect.DeepEqual(*storedRules, rules) {
		return false, fmt.Errorf("第 %d 卷第 %d 弧摘要已存在但关联工件不同，拒绝覆盖: %w", summary.Volume, summary.Arc, errs.ErrToolConflict)
	}
	return true, nil
}

type arcSummaryStyleRules struct {
	Prose    []string                `json:"prose"`
	Dialogue []domain.CharacterVoice `json:"dialogue"`
	Taboos   []string                `json:"taboos"`
}

func validateArcSummaryStyleRules(rules *arcSummaryStyleRules) error {
	if rules == nil {
		return fmt.Errorf("style_rules is required: %w", errs.ErrToolArgs)
	}
	if len(rules.Prose) == 0 {
		return fmt.Errorf("style_rules.prose is required: %w", errs.ErrToolArgs)
	}
	if len(rules.Dialogue) == 0 {
		return fmt.Errorf("style_rules.dialogue is required; expected array of objects {name, rules}: %w", errs.ErrToolArgs)
	}
	for i, voice := range rules.Dialogue {
		if strings.TrimSpace(voice.Name) == "" {
			return fmt.Errorf("style_rules.dialogue[%d].name is required: %w", i, errs.ErrToolArgs)
		}
		if len(voice.Rules) == 0 {
			return fmt.Errorf("style_rules.dialogue[%d].rules is required: %w", i, errs.ErrToolArgs)
		}
		for j, rule := range voice.Rules {
			if strings.TrimSpace(rule) == "" {
				return fmt.Errorf("style_rules.dialogue[%d].rules[%d] is empty: %w", i, j, errs.ErrToolArgs)
			}
		}
	}
	return nil
}
