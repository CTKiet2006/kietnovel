package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/CTKiet2006/kietnovel/internal/domain"
	"github.com/CTKiet2006/kietnovel/internal/errs"
	"github.com/CTKiet2006/kietnovel/internal/store"
	"github.com/voocel/agentcore/schema"
)

// SaveFoundationTool saves the foundation (premise/outline/characters); for the Architect only.
type SaveFoundationTool struct {
	store *store.Store
}

func NewSaveFoundationTool(store *store.Store) *SaveFoundationTool {
	return &SaveFoundationTool{store: store}
}

func (t *SaveFoundationTool) Name() string { return "save_foundation" }
func (t *SaveFoundationTool) Description() string {
	switch toolLang(t.store) {
	case "vi":
		return "Lưu các thiết lập cơ bản của tiểu thuyết (premise/outline/characters/world_rules/compass...). ĐÂY LÀ CỔNG LƯU TRỮ DUY NHẤT: nội dung chỉ nói trong tin nhắn mà không gọi tool sẽ bị mất. " +
			"type gồm: premise, outline, layered_outline, characters, world_rules, append_volume, update_compass, complete_book. " +
			"Với premise: content phải là chuỗi Markdown; các loại khác content truyền trực tiếp mảng hoặc đối tượng JSON. " +
			"append_volume: thêm quyển mới; update_compass: cập nhật hướng đi kết truyện; complete_book: tuyên bố hoàn thành toàn bộ tác phẩm. " +
			"append_volume / complete_book bắt buộc truyền tham số reason."
	case "en":
		return "Save novel foundation settings (premise/outline/characters/world_rules/compass, etc.). THIS IS THE PERSISTENCE GATEWAY: contents output in chat without calling this tool will be lost. " +
			"type choices: premise, outline, layered_outline, characters, world_rules, append_volume, update_compass, complete_book. " +
			"For premise: content must be a Markdown string; other types pass JSON arrays or objects directly. " +
			"append_volume: appends a new volume; update_compass: updates finale trajectory; complete_book: declares book completion. " +
			"append_volume / complete_book require the reason parameter."
	default:
		return "保存小说基础设定（premise/outline/characters/world_rules/compass 等）。**这是这些设定的持久化入口**：未经工具调用保存的内容不会进入 store，只在消息里输出 Markdown/JSON 等于丢失。type 可选 premise / outline / layered_outline / characters / world_rules / append_volume / update_compass / complete_book。premise 时 content 必须是 Markdown 字符串；其他类型 content 优先直接传 JSON 数组或对象。append_volume 追加新卷（content 为不带卷弧 index 的 VolumeOutline JSON，序号由系统生成；顶层带 \"final\": true 即宣告收官卷——全书在该卷收束，所有章节写完后自动完结，无需再调 complete_book）；update_compass 更新终局方向（content 为 StoryCompass JSON）；complete_book 宣告全书完结（content 传空对象 {}，直接推 Phase=Complete；工具会校验：大纲内章节已全部写完、无返工队列、compass 无未收束 open_threads——确认长线已收束须先 update_compass 清空 open_threads 落盘，想提前收束用 append_volume 的 final 收官卷）。append_volume / complete_book 必须带 reason 参数（一句话判定理由，对照完结判定清单，记入裁定审计）。scale 可选，仅允许 short / mid / long。"
	}
}
func (t *SaveFoundationTool) Label() string {
	switch toolLang(t.store) {
	case "vi":
		return "Lưu thiết lập"
	case "en":
		return "Save foundation"
	default:
		return "保存设定"
	}
}

// A writing tool (it updates Outline/Progress/Characters across domains); concurrency is forbidden.
func (t *SaveFoundationTool) ReadOnly(_ json.RawMessage) bool        { return false }
func (t *SaveFoundationTool) ConcurrencySafe(_ json.RawMessage) bool { return false }

func (t *SaveFoundationTool) Schema() map[string]any {
	typeDesc := "设定类型"
	contentDesc := "内容。premise 传 Markdown 字符串；其他类型直接传 JSON 数组或对象即可，也兼容传 JSON 字符串。"
	scaleDesc := "规划级别"
	reasonDesc := "卷末判定理由（append_volume / complete_book 时必填）：对照完结判定清单，一句话说明为何续卷、宣告收官或完结"

	switch toolLang(t.store) {
	case "vi":
		typeDesc = "Loại thiết lập (premise, outline, layered_outline, characters, world_rules, append_volume, update_compass, complete_book)"
		contentDesc = "Nội dung. premise truyền chuỗi Markdown; các loại khác truyền mảng hoặc đối tượng JSON"
		scaleDesc = "Cấp độ quy hoạch (short, mid, long)"
		reasonDesc = "Lý do phán định (bắt buộc khi append_volume / complete_book)"
	case "en":
		typeDesc = "Setting type (premise, outline, layered_outline, characters, world_rules, append_volume, update_compass, complete_book)"
		contentDesc = "Content payload. premise takes Markdown string; other types take JSON array or object directly"
		scaleDesc = "Planning scale (short, mid, long)"
		reasonDesc = "Adjudication reason (required for append_volume / complete_book)"
	}

	return schema.Object(
		schema.Property("type", schema.Enum(typeDesc, "premise", "outline", "layered_outline", "characters", "world_rules", "append_volume", "update_compass", "complete_book")).Required(),
		schema.Property("content", map[string]any{
			"description": contentDesc,
		}).Required(),
		schema.Property("scale", schema.Enum(scaleDesc, "short", "mid", "long")),
		schema.Property("reason", schema.String(reasonDesc)),
	)
}

func (t *SaveFoundationTool) Execute(_ context.Context, args json.RawMessage) (json.RawMessage, error) {
	var a struct {
		Type    string          `json:"type"`
		Content json.RawMessage `json:"content"`
		Scale   string          `json:"scale"`
		Reason  string          `json:"reason"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, fmt.Errorf("invalid args: %w: %w", errs.ErrToolArgs, err)
	}
	content, err := normalizeFoundationContent(a.Content)
	if err != nil {
		return nil, err
	}
	if a.Scale != "" {
		switch domain.PlanningTier(a.Scale) {
		case domain.PlanningTierShort, domain.PlanningTierMid, domain.PlanningTierLong:
		default:
			return nil, fmt.Errorf("invalid scale %q, expected short/mid/long: %w", a.Scale, errs.ErrToolArgs)
		}
	}

	result := map[string]any{"saved": true, "type": a.Type, "scale": a.Scale}

	// The full outline belongs to the planning phase only. The writing phase must use the protected incremental operations, and after completion the book must be reopened first;
	// otherwise it would bypass the completed-chapter protection and break the consistency between Progress and the chapter facts.
	progress, err := t.store.Progress.Load()
	if err != nil {
		return nil, fmt.Errorf("check foundation phase: %w: %w", errs.ErrStoreRead, err)
	}
	if (a.Type == "outline" || a.Type == "layered_outline") && progress != nil {
		switch progress.Phase {
		case domain.PhaseWriting:
			return nil, fmt.Errorf(
				"写作阶段禁止使用 %s 全量覆盖大纲。请使用 revise_outline 修订未发生章节、expand_next_arc 展开下一骨架弧，或 append_volume 追加新卷: %w",
				a.Type, errs.ErrToolPrecondition)
		case domain.PhaseComplete:
			return nil, fmt.Errorf(
				"全书已完结，禁止使用 %s 全量覆盖大纲。请先重开作品，再使用受保护的大纲修订或续写操作: %w",
				a.Type, errs.ErrToolPrecondition)
		}
	}
	if a.Scale != "" {
		if err := t.store.RunMeta.SetPlanningTier(domain.PlanningTier(a.Scale)); err != nil {
			return nil, fmt.Errorf("save planning tier: %w: %w", errs.ErrStoreWrite, err)
		}
	}

	// The end-of-volume choice of three (continue / finale / complete) is the heaviest semantic judgement in the whole book, and the reason must become an audit fact
	// (decisions.jsonl, the same stream as plan_start/intervention); otherwise a finale that comes too early /
	// a wrong call to continue can only be diagnosed by digging through the session log. The fact snapshot is the progress as of the moment of the decision (before the change is persisted).
	volumeEnd := a.Type == "append_volume" || a.Type == "complete_book"
	if volumeEnd && strings.TrimSpace(a.Reason) == "" {
		return nil, fmt.Errorf("%s 必须带 reason 参数：对照完结判定清单，一句话说明本次为何续卷、宣告收官或完结: %w", a.Type, errs.ErrToolArgs)
	}
	var volumeEndFacts json.RawMessage
	if volumeEnd {
		p, err := t.store.Progress.Load()
		if err != nil {
			return nil, fmt.Errorf("load progress for volume-end facts: %w: %w", errs.ErrStoreRead, err)
		}
		if p != nil {
			facts := map[string]any{"completed_chapters": len(p.CompletedChapters)}
			if p.Layered {
				outline, outlineErr := t.store.Outline.LoadOutline()
				if outlineErr != nil {
					return nil, fmt.Errorf("load outlined chapters for volume-end facts: %w: %w", errs.ErrStoreRead, outlineErr)
				}
				facts["dynamic_planning"] = true
				facts["outlined_chapters"] = len(outline)
			} else {
				facts["total_chapters"] = p.TotalChapters
			}
			volumeEndFacts, err = json.Marshal(facts)
			if err != nil {
				return nil, fmt.Errorf("marshal volume-end facts: %w", err)
			}
		}
	}

	decode := func(typeName string, out any) error {
		return decodeFoundationJSON(typeName, content, out)
	}

	switch a.Type {
	case "premise":
		if err := t.store.Outline.SavePremise(content); err != nil {
			return nil, fmt.Errorf("save premise: %w: %w", errs.ErrStoreWrite, err)
		}
		if err := t.store.Progress.AdvancePhase(domain.PhasePremise); err != nil {
			return nil, fmt.Errorf("update premise phase: %w: %w", errs.ErrStoreWrite, err)
		}

	case "outline":
		var entries []domain.OutlineEntry
		if err := decode("outline", &entries); err != nil {
			return nil, err
		}
		if err := t.store.Outline.SaveOutline(entries); err != nil {
			return nil, fmt.Errorf("save outline: %w: %w", errs.ErrStoreWrite, err)
		}
		if err := t.store.Progress.AdvancePhase(domain.PhaseOutline); err != nil {
			return nil, fmt.Errorf("update outline phase: %w: %w", errs.ErrStoreWrite, err)
		}
		if err := t.store.Progress.SetTotalChapters(len(entries)); err != nil {
			return nil, fmt.Errorf("set total chapters: %w: %w", errs.ErrStoreWrite, err)
		}
		if domain.PlanningTier(a.Scale) != domain.PlanningTierLong {
			if err := t.store.Progress.SetLayered(false); err != nil {
				return nil, fmt.Errorf("disable layered mode: %w: %w", errs.ErrStoreWrite, err)
			}
			if err := t.store.Progress.UpdateVolumeArc(0, 0); err != nil {
				return nil, fmt.Errorf("reset volume/arc: %w: %w", errs.ErrStoreWrite, err)
			}
			if err := t.store.Outline.ClearLayeredOutline(); err != nil {
				return nil, fmt.Errorf("clear layered outline: %w: %w", errs.ErrStoreWrite, err)
			}
		}
		result["chapters"] = len(entries)

	case "layered_outline":
		var volumes []domain.VolumeOutline
		if err := decode("layered_outline", &volumes); err != nil {
			return nil, err
		}
		if err := t.store.Outline.SaveLayeredOutline(volumes); err != nil {
			return nil, fmt.Errorf("save layered_outline: %w: %w", errs.ErrStoreWrite, err)
		}
		total := domain.EstimatedChapterCapacity(volumes)
		if err := t.store.Progress.AdvancePhase(domain.PhaseOutline); err != nil {
			return nil, fmt.Errorf("update outline phase: %w: %w", errs.ErrStoreWrite, err)
		}
		if err := t.store.Progress.SetTotalChapters(total); err != nil {
			return nil, fmt.Errorf("set total chapters: %w: %w", errs.ErrStoreWrite, err)
		}
		if err := t.store.Progress.SetLayered(true); err != nil {
			return nil, fmt.Errorf("enable layered mode: %w: %w", errs.ErrStoreWrite, err)
		}
		if len(volumes) > 0 && len(volumes[0].Arcs) > 0 {
			if err := t.store.Progress.UpdateVolumeArc(volumes[0].Index, volumes[0].Arcs[0].Index); err != nil {
				return nil, fmt.Errorf("set initial volume/arc: %w: %w", errs.ErrStoreWrite, err)
			}
		}
		result["volumes"] = len(volumes)
		result["dynamic_planning"] = true
		result["outlined_chapters"] = len(domain.FlattenOutline(volumes))

	case "characters":
		var chars []domain.Character
		if err := decode("characters", &chars); err != nil {
			return nil, err
		}
		if err := t.store.Characters.Save(chars); err != nil {
			return nil, fmt.Errorf("save characters: %w: %w", errs.ErrStoreWrite, err)
		}
		result["count"] = len(chars)

	case "world_rules":
		var rules []domain.WorldRule
		if err := decode("world_rules", &rules); err != nil {
			return nil, err
		}
		if err := t.store.World.SaveWorldRules(rules); err != nil {
			return nil, fmt.Errorf("save world_rules: %w: %w", errs.ErrStoreWrite, err)
		}
		result["count"] = len(rules)

	case "append_volume":
		p, err := t.store.Progress.Load()
		if err != nil {
			return nil, fmt.Errorf("load progress: %w: %w", errs.ErrStoreRead, err)
		}
		if p != nil && p.Phase == domain.PhaseComplete {
			return nil, fmt.Errorf("全书已完结（phase=complete），不允许追加新卷: %w", errs.ErrToolPrecondition)
		}
		var vol domain.VolumeOutline
		if err := decode("append_volume", &vol); err != nil {
			return nil, err
		}
		prior, err := t.store.Outline.LoadLayeredOutline()
		if err != nil {
			return nil, fmt.Errorf("load layered outline: %w: %w", errs.ErrStoreRead, err)
		}
		saved, err := t.store.AppendVolume(vol)
		if err != nil {
			return nil, fmt.Errorf("append volume: %w: %w", errs.ErrStoreWrite, err)
		}
		result["volume"] = saved.Index
		if saved.Final {
			result["final_volume"] = true
		} else if domain.FinaleVolume(prior) > 0 {
			// Fact echo: a previously declared finale state is cleared because an ordinary new volume was appended (the new volume becomes the last one)
			result["finale_released"] = true
		}
		result["arcs"] = len(saved.Arcs)
		chCount := 0
		for _, arc := range saved.Arcs {
			chCount += len(arc.Chapters)
		}
		if chCount > 0 {
			result["chapters"] = chCount
		}
		if err := consumeWriterFeedback(t.store); err != nil {
			return nil, err
		}

	case "complete_book":
		// The only entry point for completing the whole book: it pushes Phase=Complete directly.
		// Only allowed during the Writing phase, to stop a mistaken call in the planning phase from skipping the writing of the whole book.
		// Rejected while a rework queue exists -- guaranteeing that PendingRewrites has to finish before the book can end.
		progress, perr := t.store.Progress.Load()
		if perr != nil {
			return nil, fmt.Errorf("load progress: %w: %w", errs.ErrStoreRead, perr)
		}
		if progress == nil {
			return nil, fmt.Errorf("progress 未初始化: %w", errs.ErrToolPrecondition)
		}
		if progress.Phase != domain.PhaseWriting {
			return nil, fmt.Errorf("complete_book 仅在 writing 阶段可调用（当前 phase=%s）: %w", progress.Phase, errs.ErrToolPrecondition)
		}
		if len(progress.PendingRewrites) > 0 {
			return nil, fmt.Errorf("还有 %d 章在返工队列中，处理完再调 complete_book: %w", len(progress.PendingRewrites), errs.ErrToolPrecondition)
		}
		// The enumerable completion preconditions must live in the code layer (the three-way split); they cannot rely only on the
		// "completion decision checklist" in the prompt -- a real incident: planning had just been persisted, phase flipped to writing, and a weak model casually
		// called complete_book, marking the book complete at 0/68 chapters.
		if len(progress.CompletedChapters) == 0 {
			return nil, fmt.Errorf("一章未写不可完本;规划完成后写作由系统自动推进,无需调用 complete_book: %w", errs.ErrToolPrecondition)
		}
		next := progress.NextChapter()
		if progress.Layered {
			outline, outlineErr := t.store.Outline.LoadOutline()
			if outlineErr != nil {
				return nil, fmt.Errorf("load outlined chapters: %w: %w", errs.ErrStoreRead, outlineErr)
			}
			if next <= len(outline) {
				return nil, fmt.Errorf("当前详细大纲还有未写章节（下一章 %d/当前已细化 %d），不可完本；想提前收束请改用 append_volume 且卷 JSON 顶层带 \"final\": true 宣告收官卷: %w", next, len(outline), errs.ErrToolPrecondition)
			}
		} else if progress.TotalChapters > 0 && next <= progress.TotalChapters {
			return nil, fmt.Errorf("大纲内还有未写章节（下一章 %d/共 %d），不可完本；想提前收束请改用 append_volume 且卷 JSON 顶层带 \"final\": true 宣告收官卷: %w", next, progress.TotalChapters, errs.ErrToolPrecondition)
		}
		// An unresolved active long thread blocks completion -- the field contract of OpenThreads is exactly "must be resolved for an ending". This is not
		// a semantic re-judgement: if you really believe everything is resolved, first clear open_threads with update_compass and only then complete the book, turning
		// "waived in the reasoning" into an auditable persisted action (measured: when continuing an imported finished book, the architect cited precedent to bypass
		// item 3 of the completion checklist and completed the book outright, locking the user's wish to keep writing behind the completion rules).
		compass, err := t.store.Outline.LoadCompass()
		if err != nil {
			return nil, fmt.Errorf("load compass: %w: %w", errs.ErrStoreRead, err)
		}
		if compass != nil && len(compass.OpenThreads) > 0 {
			return nil, fmt.Errorf("compass 还有 %d 条活跃长线未收束（如：%s），不可完本。确认已全部收束请先 update_compass 清空 open_threads 再调 complete_book；仍需展开请 append_volume（可带 \"final\": true 宣告收官卷）: %w",
				len(compass.OpenThreads), compass.OpenThreads[0], errs.ErrToolPrecondition)
		}
		if err := t.store.Progress.MarkComplete(); err != nil {
			return nil, fmt.Errorf("mark complete: %w: %w", errs.ErrStoreWrite, err)
		}
		result["book_complete"] = true
		result["phase"] = string(domain.PhaseComplete)

	case "update_compass":
		var compass domain.StoryCompass
		if err := decode("compass", &compass); err != nil {
			return nil, err
		}
		// The tool layer forcibly overwrites LastUpdated with the current number of completed chapters and does not trust what the LLM filled in.
		// The LLM usually forgets to fill it in or leaves 0, which makes diag.CompassDrift report false positives and distorts Router routing.
		p, err := t.store.Progress.Load()
		if err != nil {
			return nil, fmt.Errorf("load progress: %w: %w", errs.ErrStoreRead, err)
		}
		if p != nil {
			compass.LastUpdated = p.LatestCompleted()
		}
		if err := t.store.Outline.SaveCompass(compass); err != nil {
			return nil, fmt.Errorf("save compass: %w: %w", errs.ErrStoreWrite, err)
		}
		result["ending_direction"] = compass.EndingDirection
		result["last_updated"] = compass.LastUpdated
		if err := consumeWriterFeedback(t.store); err != nil {
			return nil, err
		}

	default:
		return nil, fmt.Errorf("unknown type %q, expected premise/outline/layered_outline/characters/world_rules/append_volume/update_compass/complete_book: %w", a.Type, errs.ErrToolArgs)
	}

	if _, err := t.store.Checkpoints.AppendArtifact(domain.GlobalScope(), a.Type, foundationArtifact(a.Type)); err != nil {
		return nil, fmt.Errorf("checkpoint foundation %s: %w: %w", a.Type, errs.ErrStoreWrite, err)
	}

	if volumeEnd {
		t.recordVolumeEndDecision(a.Type, a.Reason, volumeEndFacts, result)
	}

	// It returns the remaining unfinished items. Even once the initial artifacts are all present, foundation_audit still remains; only
	// audit_foundation returning ready=true for the actually persisted version allows entry into writing.
	remaining, err := t.store.FoundationMissing()
	if err != nil {
		return nil, fmt.Errorf("load foundation state: %w: %w", errs.ErrStoreRead, err)
	}
	ready := len(remaining) == 0
	result["remaining"] = remaining
	result["foundation_ready"] = ready
	return json.Marshal(result)
}

func foundationArtifact(t string) string {
	switch t {
	case "premise":
		return "premise.md"
	case "outline":
		return "outline.json"
	case "layered_outline", "append_volume":
		return "layered_outline.json"
	case "complete_book":
		return "meta/progress.json"
	case "characters":
		return "characters.json"
	case "world_rules":
		return "world_rules.json"
	case "update_compass":
		return "meta/compass.json"
	default:
		return ""
	}
}

// decodeFoundationJSON parses the content field of save_foundation, attaching the line/column position on failure
// plus the most common fix hints, so the LLM can locate the problem directly on its next retry instead of guessing blindly.
func decodeFoundationJSON(typeName, content string, out any) error {
	err := json.Unmarshal([]byte(content), out)
	if err == nil {
		return nil
	}
	hint := `常见原因：字符串值中的双引号未转义为 \", 换行未转义为 \n, 或对象字段间漏了逗号。请整段重新生成一次。`
	if se, ok := err.(*json.SyntaxError); ok {
		line, col := offsetToLineCol(content, int(se.Offset))
		return fmt.Errorf("parse %s JSON (line %d col %d): %w — %s", typeName, line, col, err, hint)
	}
	return fmt.Errorf("parse %s JSON: %w — %s", typeName, err, hint)
}

func offsetToLineCol(s string, offset int) (int, int) {
	if offset < 0 {
		offset = 0
	}
	if offset > len(s) {
		offset = len(s)
	}
	line, col := 1, 1
	for i := 0; i < offset; i++ {
		if s[i] == '\n' {
			line++
			col = 1
		} else {
			col++
		}
	}
	return line, col
}

func normalizeFoundationContent(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", fmt.Errorf("content is required: %w", errs.ErrToolArgs)
	}

	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text, nil
	}

	if !json.Valid(raw) {
		return "", fmt.Errorf("invalid content: expected Markdown string or valid JSON value: %w", errs.ErrToolArgs)
	}
	return string(raw), nil
}

// recordVolumeEndDecision records the reason behind the end-of-volume choice of three (continue / finale / complete) into the decision audit.
// best-effort: the structural change is already persisted, so an audit failure only warns and does not roll back -- reporting an error would make the model retry an operation
// that has already succeeded (appending the volume again).
func (t *SaveFoundationTool) recordVolumeEndDecision(action, reason string, facts json.RawMessage, result map[string]any) {
	decision := map[string]any{"action": action}
	if v, ok := result["volume"]; ok {
		decision["volume"] = v
	}
	if _, ok := result["final_volume"]; ok {
		decision["final"] = true
	}
	raw, err := json.Marshal(decision)
	if err != nil {
		slog.Error("卷末裁定序列化失败", "module", "tools", "action", action, "err", err)
		return
	}
	if _, err := t.store.Decisions.Append(store.DecisionRecord{
		Kind:     "volume_end",
		Decider:  "architect",
		Facts:    facts,
		Decision: raw,
		Reason:   reason,
	}); err != nil {
		slog.Error("卷末裁定审计落盘失败", "module", "tools", "action", action, "err", err)
	}
}

// consumeWriterFeedback clears the already-processed planning feedback after a structural operation succeeds.
func consumeWriterFeedback(st *store.Store) error {
	if err := st.Outline.ClearOutlineFeedback(); err != nil {
		return fmt.Errorf("clear outline feedback: %w: %w", errs.ErrStoreWrite, err)
	}
	return nil
}
