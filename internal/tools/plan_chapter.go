package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/CTKiet2006/kietnovel/internal/domain"
	"github.com/CTKiet2006/kietnovel/internal/errs"
	"github.com/CTKiet2006/kietnovel/internal/store"
	"github.com/voocel/agentcore/schema"
)

// PlanChapterTool saves the chapter plan; the Agent decides the planning granularity on its own.
type PlanChapterTool struct {
	store *store.Store
}

func NewPlanChapterTool(store *store.Store) *PlanChapterTool {
	return &PlanChapterTool{store: store}
}

func (t *PlanChapterTool) Name() string { return "plan_chapter" }
func (t *PlanChapterTool) Description() string {
	switch toolLang(t.store) {
	case "vi":
		return "Lưu ý đồ sáng tác của chương. Agent tự chủ quyết định độ chi tiết quy hoạch, không ép buộc phân chia phân cảnh"
	case "en":
		return "Save chapter writing outline and beats. Agent autonomously decides planning granularity without forcing rigid scene splits"
	default:
		return "保存章节写作构思。Agent 自主决定规划粒度，不强制场景拆分"
	}
}
func (t *PlanChapterTool) Label() string {
	switch toolLang(t.store) {
	case "vi":
		return "Quy hoạch chương"
	case "en":
		return "Plan chapter"
	default:
		return "规划章节"
	}
}

// A writing tool; concurrency is forbidden.
func (t *PlanChapterTool) ReadOnly(_ json.RawMessage) bool        { return false }
func (t *PlanChapterTool) ConcurrencySafe(_ json.RawMessage) bool { return false }

func (t *PlanChapterTool) Schema() map[string]any {
	chapterDesc := "章节号"
	titleDesc := "暂定章节标题；写作后可按正文调整"
	goalDesc := "本章目标"
	conflictDesc := "核心冲突"
	hookDesc := "章末钩子"
	emotionArcDesc := "情绪曲线"
	notesDesc := "自由备忘（任何你觉得写作时需要记住的东西）"
	requiredBeatsDesc := "本章必须完成的推进项"
	forbiddenMovesDesc := "本章明确不能发生的推进"
	continuityChecksDesc := "本章需特别核对的连续性点"
	evaluationFocusDesc := "Editor 重点检查项"
	emotionTargetDesc := "可选：本章希望读者主要感受到的情绪"
	payoffPointsDesc := "可选：关键章希望回应的情节点或兑现点"
	hookGoalDesc := "可选：章末希望驱动的追读欲望或悬念目标"

	switch toolLang(t.store) {
	case "vi":
		chapterDesc = "Số chương"
		titleDesc = "Tiêu đề dự kiến của chương; có thể điều chỉnh sau khi viết chính văn"
		goalDesc = "Mục tiêu cốt lõi của chương"
		conflictDesc = "Xung đột chính"
		hookDesc = "Móc câu treo kịch tính cuối chương"
		emotionArcDesc = "Đường cong cảm xúc"
		notesDesc = "Ghi chú tự do (bất cứ điều gì cần nhớ khi viết)"
		requiredBeatsDesc = "Các nhịp tình tiết bắt buộc phải hoàn thành trong chương"
		forbiddenMovesDesc = "Các hành động/diễn biến bị cấm tuyệt đối trong chương này"
		continuityChecksDesc = "Các điểm kiểm tra tính liên tục đối chiếu với các chương trước"
		evaluationFocusDesc = "Trọng tâm kiểm tra của Editor"
		emotionTargetDesc = "Tùy chọn: Cảm xúc chủ đạo muốn độc giả cảm nhận"
		payoffPointsDesc = "Tùy chọn: Điểm thỏa mãn / tháo gỡ cảm xúc then chốt"
		hookGoalDesc = "Tùy chọn: Mục tiêu khơi gợi cảm giác tò mò / thúc đẩy đọc tiếp cuối chương"
	case "en":
		chapterDesc = "Chapter number"
		titleDesc = "Tentative chapter title; can be adjusted after writing prose"
		goalDesc = "Core chapter goal"
		conflictDesc = "Primary conflict"
		hookDesc = "End-of-chapter hook"
		emotionArcDesc = "Emotional arc"
		notesDesc = "Free-form notes (anything to keep in mind during drafting)"
		requiredBeatsDesc = "Required narrative beats to accomplish in this chapter"
		forbiddenMovesDesc = "Forbidden developments that must not occur in this chapter"
		continuityChecksDesc = "Continuity points to explicitly verify against prior chapters"
		evaluationFocusDesc = "Key review checklist items for Editor"
		emotionTargetDesc = "Optional: Dominant emotional experience intended for the reader"
		payoffPointsDesc = "Optional: Key narrative or emotional payoffs delivered"
		hookGoalDesc = "Optional: Target suspense or curiosity driver for the chapter hook"
	}

	return schema.Object(
		schema.Property("chapter", schema.Int(chapterDesc)).Required(),
		schema.Property("title", schema.String(titleDesc)).Required(),
		schema.Property("goal", schema.String(goalDesc)).Required(),
		schema.Property("conflict", schema.String(conflictDesc)).Required(),
		schema.Property("hook", schema.String(hookDesc)).Required(),
		schema.Property("emotion_arc", schema.String(emotionArcDesc)),
		schema.Property("notes", schema.String(notesDesc)),
		schema.Property("required_beats", schema.Array(requiredBeatsDesc, schema.String(""))),
		schema.Property("forbidden_moves", schema.Array(forbiddenMovesDesc, schema.String(""))),
		schema.Property("continuity_checks", schema.Array(continuityChecksDesc, schema.String(""))),
		schema.Property("evaluation_focus", schema.Array(evaluationFocusDesc, schema.String(""))),
		schema.Property("emotion_target", schema.String(emotionTargetDesc)),
		schema.Property("payoff_points", schema.Array(payoffPointsDesc, schema.String(""))),
		schema.Property("hook_goal", schema.String(hookGoalDesc)),
	)
}

func (t *PlanChapterTool) Execute(_ context.Context, args json.RawMessage) (json.RawMessage, error) {
	plan, err := decodeChapterPlanArgs(args)
	if err != nil {
		return nil, fmt.Errorf("invalid args: %w: %w", errs.ErrToolArgs, err)
	}
	if plan.Chapter <= 0 {
		return nil, fmt.Errorf("chapter must be > 0: %w", errs.ErrToolArgs)
	}
	completed, err := t.store.Progress.IsChapterCompleted(plan.Chapter)
	if err != nil {
		return nil, fmt.Errorf("load progress: %w: %w", errs.ErrStoreRead, err)
	}
	if completed {
		reason := fmt.Sprintf("第 %d 章已提交完成，不能重新规划", plan.Chapter)
		switch toolLang(t.store) {
		case "vi":
			reason = fmt.Sprintf("Chương %d đã nộp hoàn thành, không thể quy hoạch lại", plan.Chapter)
		case "en":
			reason = fmt.Sprintf("Chapter %d has already been committed and cannot be replanned", plan.Chapter)
		}
		return json.Marshal(map[string]any{
			"chapter":   plan.Chapter,
			"skipped":   true,
			"completed": true,
			"reason":    reason,
		})
	}
	if err := t.store.Progress.ValidateChapterWork(plan.Chapter); err != nil {
		return nil, err
	}
	if err := EnsureChapterExpanded(t.store, plan.Chapter); err != nil {
		return nil, err
	}

	if err := t.store.Drafts.SaveChapterPlan(plan); err != nil {
		return nil, fmt.Errorf("save chapter plan: %w", err)
	}
	if err := t.store.Progress.StartChapter(plan.Chapter); err != nil {
		return nil, fmt.Errorf("mark chapter in progress: %w", err)
	}

	if _, err := t.store.Checkpoints.AppendArtifact(
		domain.ChapterScope(plan.Chapter), "plan",
		fmt.Sprintf("drafts/%02d.plan.json", plan.Chapter),
	); err != nil {
		return nil, fmt.Errorf("checkpoint chapter plan: %w", err)
	}

	nextStep := "立即调用 draft_chapter(chapter=本章节号, content=完整正文字符串) 写入正文，不要重复规划同一章"
	lang := ""
	if t.store != nil && t.store.BookLanguage != nil {
		lang, _ = t.store.BookLanguage.Load()
	}
	switch strings.ToLower(strings.TrimSpace(lang)) {
	case "vi":
		nextStep = "Gọi ngay draft_chapter(chapter=số chương, content=nội dung chương đầy đủ) để viết chính văn, không lặp lại kế hoạch của cùng một chương"
	case "en":
		nextStep = "Call draft_chapter(chapter=chapter_number, content=full_prose_string) to write prose immediately; do not replan the same chapter"
	}

	return json.Marshal(map[string]any{
		"planned":   true,
		"chapter":   plan.Chapter,
		"next_step": nextStep,
	})
}

func decodeChapterPlanArgs(args json.RawMessage) (domain.ChapterPlan, error) {
	var a struct {
		Chapter          int      `json:"chapter"`
		Title            string   `json:"title"`
		Goal             string   `json:"goal"`
		Conflict         string   `json:"conflict"`
		Hook             string   `json:"hook"`
		EmotionArc       string   `json:"emotion_arc"`
		Notes            string   `json:"notes"`
		RequiredBeats    []string `json:"required_beats"`
		ForbiddenMoves   []string `json:"forbidden_moves"`
		ContinuityChecks []string `json:"continuity_checks"`
		EvaluationFocus  []string `json:"evaluation_focus"`
		EmotionTarget    string   `json:"emotion_target"`
		PayoffPoints     []string `json:"payoff_points"`
		HookGoal         string   `json:"hook_goal"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return domain.ChapterPlan{}, err
	}

	return domain.ChapterPlan{
		Chapter:    a.Chapter,
		Title:      a.Title,
		Goal:       a.Goal,
		Conflict:   a.Conflict,
		Hook:       a.Hook,
		EmotionArc: a.EmotionArc,
		Notes:      a.Notes,
		Contract: domain.ChapterContract{
			RequiredBeats:    a.RequiredBeats,
			ForbiddenMoves:   a.ForbiddenMoves,
			ContinuityChecks: a.ContinuityChecks,
			EvaluationFocus:  a.EvaluationFocus,
			EmotionTarget:    a.EmotionTarget,
			PayoffPoints:     a.PayoffPoints,
			HookGoal:         a.HookGoal,
		},
	}, nil
}
