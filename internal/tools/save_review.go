package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/CTKiet2006/kietnovel/internal/domain"
	"github.com/CTKiet2006/kietnovel/internal/errs"
	"github.com/CTKiet2006/kietnovel/internal/flow"
	"github.com/CTKiet2006/kietnovel/internal/llmcontract"
	"github.com/CTKiet2006/kietnovel/internal/store"
	"github.com/voocel/agentcore/schema"
)

// SaveReviewTool saves the Editor's review result.
type SaveReviewTool struct {
	store *store.Store
}

func NewSaveReviewTool(store *store.Store) *SaveReviewTool {
	return &SaveReviewTool{store: store}
}

func (t *SaveReviewTool) Name() string { return "save_review" }
func (t *SaveReviewTool) Description() string {
	switch toolLang(t.store) {
	case "vi":
		return "Lưu kết quả thẩm định và cập nhật trạng thái quy trình. verdict gồm accept/polish/rewrite. " +
			"Editor đưa ra verdict dựa trên toàn bộ ngữ cảnh, công cụ chỉ kiểm tra sự kiện và cập nhật nguyên tử vào Progress. " +
			"Trả về các sự kiện có cấu trúc: verdict / affected_chapters / next_flow / next_chapter"
	case "en":
		return "Save review results and update flow state. verdict is one of accept/polish/rewrite. " +
			"Editor decides verdict based on full context; tool validates facts and atomically updates Progress. " +
			"Returns structured facts: verdict / affected_chapters / next_flow / next_chapter"
	default:
		return "保存审阅结果并更新流程状态。verdict 为 accept/polish/rewrite 之一。" +
			"Editor 依据完整上下文作出 verdict，工具只校验事实并原子更新 Progress。" +
			"返回结构化事实：verdict / affected_chapters / next_flow / next_chapter"
	}
}
func (t *SaveReviewTool) Label() string {
	switch toolLang(t.store) {
	case "vi":
		return "Lưu thẩm định"
	case "en":
		return "Save review"
	default:
		return "保存审阅"
	}
}

// A writing tool (it updates both reviews/ and Progress's PendingRewrites/Flow); concurrency is forbidden.
func (t *SaveReviewTool) ReadOnly(_ json.RawMessage) bool        { return false }
func (t *SaveReviewTool) ConcurrencySafe(_ json.RawMessage) bool { return false }
func (t *SaveReviewTool) StrictSchema() bool                     { return true }

func (t *SaveReviewTool) Schema() map[string]any {
	typeDesc := "问题维度；可使用评审提示中的基础维度，也可写更准确的具体维度"
	sevDesc := "严重程度"
	descDesc := "问题描述"
	evDesc := "证据：原文片段、具体情节或状态数据"
	sugDesc := "修改建议；无需建议时为 null"
	chapDesc := "该问题证据实际所在的章节；弧评审必须落在任务给定区间"
	reqDesc := "该问题是否应立即触发所列章节返工，由 Editor 结合整体阅读体验判断"

	dimDesc := "评价维度；由当前评审任务和 rubric 决定"
	scoreDesc := "评分（0-100）"
	comDesc := "该维度的简要结论和证据；每个维度必填"

	revChapDesc := "审阅的章节号（全局审阅填最新章节号）"
	scopeDesc := "审阅范围"
	dimsDesc := "分维度评分；基础 rubric 由 Editor 提示提供，可按任务补充更具体维度"
	issuesDesc := "发现的问题"
	cStatDesc := "章节契约完成度；不适用时为 null"
	cMissDesc := "未完成或违背的 contract 条目；无则为空数组"
	cNoteDesc := "对 contract 履行情况的简要说明；无则为 null"
	verdDesc := "审阅结论"
	sumDesc := "审阅总结"

	switch toolLang(t.store) {
	case "vi":
		typeDesc = "Chiều kích vấn đề"
		sevDesc = "Mức độ nghiêm trọng (critical, error, warning)"
		descDesc = "Mô tả vấn đề"
		evDesc = "Bằng chứng: trích đoạn nguyên văn, tình tiết cụ thể hoặc dữ liệu trạng thái"
		sugDesc = "Đề xuất sửa đổi; null nếu không cần"
		chapDesc = "Các chương thực tế phát hiện vấn đề này"
		reqDesc = "Vấn đề này có kích hoạt viết lại ngay lập tức hay không"
		dimDesc = "Chiều kích đánh giá"
		scoreDesc = "Điểm số (0-100)"
		comDesc = "Kết luận vắn tắt và bằng chứng cho chiều kích này"
		revChapDesc = "Số chương được thẩm định"
		scopeDesc = "Phạm vi thẩm định (chapter, global, arc)"
		dimsDesc = "Điểm số theo từng chiều kích"
		issuesDesc = "Danh sách vấn đề phát hiện được"
		cStatDesc = "Mức độ hoàn thành cam kết chương (contract); null nếu không áp dụng"
		cMissDesc = "Các điều khoản cam kết chưa đạt hoặc vi phạm; mảng rỗng nếu không có"
		cNoteDesc = "Ghi chú giải thích tình hình thực hiện cam kết; null nếu không có"
		verdDesc = "Kết luận thẩm định (accept, polish, rewrite)"
		sumDesc = "Tổng kết thẩm định"
	case "en":
		typeDesc = "Issue dimension"
		sevDesc = "Severity (critical, error, warning)"
		descDesc = "Issue description"
		evDesc = "Evidence: prose snippet, specific plot beat, or state data"
		sugDesc = "Revision suggestion; null if not needed"
		chapDesc = "Chapters where this issue is located"
		reqDesc = "Whether this issue should immediately trigger a rewrite"
		dimDesc = "Evaluation dimension"
		scoreDesc = "Score (0-100)"
		comDesc = "Summary and evidence for this dimension"
		revChapDesc = "Reviewed chapter number"
		scopeDesc = "Review scope (chapter, global, arc)"
		dimsDesc = "Dimensional scores"
		issuesDesc = "Identified issues"
		cStatDesc = "Chapter contract fulfillment status; null if inapplicable"
		cMissDesc = "Unfulfilled or breached contract clauses; empty array if none"
		cNoteDesc = "Contract fulfillment notes; null if none"
		verdDesc = "Review verdict (accept, polish, rewrite)"
		sumDesc = "Review summary"
	}

	issueSchema := schema.Object(
		schema.Property("type", schema.String(typeDesc)).Required(),
		schema.Property("severity", schema.Enum(sevDesc, "critical", "error", "warning")).Required(),
		schema.Property("description", schema.String(descDesc)).Required(),
		schema.Property("evidence", schema.String(evDesc)).Required(),
		schema.Property("suggestion", llmcontract.Nullable(schema.String(sugDesc))).Required(),
		schema.Property("chapters", schema.Array(chapDesc, schema.Int(""))).Required(),
		schema.Property("requires_change", schema.Bool(reqDesc)).Required(),
	)
	dimensionSchema := schema.Object(
		schema.Property("dimension", schema.String(dimDesc)).Required(),
		schema.Property("score", schema.Int(scoreDesc)).Required(),
		schema.Property("comment", schema.String(comDesc)).Required(),
	)
	return schema.Object(
		schema.Property("chapter", schema.Int(revChapDesc)).Required(),
		schema.Property("scope", schema.Enum(scopeDesc, "chapter", "global", "arc")).Required(),
		schema.Property("dimensions", schema.Array(dimsDesc, dimensionSchema)).Required(),
		schema.Property("issues", schema.Array(issuesDesc, issueSchema)).Required(),
		schema.Property("contract_status", llmcontract.Nullable(schema.Enum(cStatDesc, "met", "partial", "missed"))).Required(),
		schema.Property("contract_misses", schema.Array(cMissDesc, schema.String(""))).Required(),
		schema.Property("contract_notes", llmcontract.Nullable(schema.String(cNoteDesc))).Required(),
		schema.Property("verdict", schema.Enum(verdDesc, "accept", "polish", "rewrite")).Required(),
		schema.Property("summary", schema.String(sumDesc)).Required(),
	)
}

func (t *SaveReviewTool) Execute(_ context.Context, args json.RawMessage) (json.RawMessage, error) {
	var r domain.ReviewEntry
	if err := json.Unmarshal(args, &r); err != nil {
		return nil, fmt.Errorf("invalid args: %w", err)
	}
	if r.Chapter <= 0 {
		return nil, fmt.Errorf("chapter must be > 0")
	}
	boundary, err := t.normalizeReviewEntry(&r)
	if err != nil {
		return nil, err
	}
	reviewOutcome, err := reviewFlow(r.Verdict)
	if err != nil {
		return nil, err
	}

	affected := r.AffectedChapters

	progress, err := t.store.Progress.Load()
	if err != nil {
		return nil, fmt.Errorf("load progress: %w", err)
	}
	if progress == nil || !slices.Contains(progress.CompletedChapters, r.Chapter) {
		return nil, fmt.Errorf("review chapter %d must be completed", r.Chapter)
	}
	scope := domain.ChapterScope(r.Chapter)
	artifact := fmt.Sprintf("reviews/%02d.json", r.Chapter)
	var existing *domain.ReviewEntry
	switch r.Scope {
	case "arc":
		scope = domain.ArcScope(boundary.Volume, boundary.Arc)
		existing, err = t.store.World.LoadReview(r.Chapter)
		if err != nil {
			return nil, fmt.Errorf("load arc review: %w", err)
		}
		if existing != nil && existing.Scope != "arc" {
			existing = nil
		}
	case "global":
		artifact = fmt.Sprintf("reviews/%02d-global.json", r.Chapter)
		existing, err = t.store.World.LoadGlobalReview(r.Chapter)
		if err != nil {
			return nil, fmt.Errorf("load global review: %w", err)
		}
	}
	if existing != nil {
		if !reflect.DeepEqual(*existing, r) {
			return nil, fmt.Errorf("第 %d 章聚合评审已存在且内容不同，拒绝覆盖: %w", r.Chapter, errs.ErrToolConflict)
		}
		return t.finishReview(r, progress, scope, artifact)
	}
	switch r.Scope {
	case "arc":
		if err := requireAggregateTarget(t.store, flow.AggregateArcReview, boundary.Volume, boundary.Arc, r.Chapter); err != nil {
			return nil, err
		}
	case "global":
		if err := requireAggregateTarget(t.store, flow.AggregateGlobalReview, 0, 0, r.Chapter); err != nil {
			return nil, err
		}
	}

	// First apply the control state atomically, then save the review artifact. If the second step fails the rework intent still exists;
	// once the Writer drains the queue, routing re-dispatches the Editor because the review artifact is missing, so the review is never skipped.
	latest, err := t.store.Progress.ApplyReviewOutcome(reviewOutcome, affected, r.Summary)
	if err != nil {
		return nil, fmt.Errorf("apply review outcome: %w", err)
	}
	if err := t.store.World.SaveReview(r); err != nil {
		return nil, fmt.Errorf("save review: %w", err)
	}

	return t.finishReview(r, latest, scope, artifact)
}

func (t *SaveReviewTool) finishReview(
	r domain.ReviewEntry,
	progress *domain.Progress,
	scope domain.Scope,
	artifact string,
) (json.RawMessage, error) {
	if _, err := t.store.Checkpoints.AppendArtifact(scope, "review", artifact); err != nil {
		return nil, fmt.Errorf("checkpoint review: %w", err)
	}

	// Use the Progress snapshot returned by the atomic update as the fact, to avoid a second read opening a new failure window.
	nextFlow := string(domain.FlowWriting)
	nextChapter := 0
	if progress != nil {
		nextFlow = string(progress.Flow)
		nextChapter = progress.NextChapter()
	}

	return json.Marshal(map[string]any{
		"saved":             true,
		"chapter":           r.Chapter,
		"scope":             r.Scope,
		"verdict":           r.Verdict,
		"affected_chapters": r.AffectedChapters,
		"issues":            len(r.Issues),
		"next_flow":         nextFlow,
		"next_chapter":      nextChapter,
	})
}

func (t *SaveReviewTool) normalizeReviewEntry(r *domain.ReviewEntry) (*store.ArcBoundary, error) {
	switch r.Scope {
	case "chapter", "global", "arc":
	default:
		return nil, fmt.Errorf("invalid review scope: %q", r.Scope)
	}
	if len(r.AffectedChapters) > 0 {
		return nil, fmt.Errorf("affected_chapters is derived from issues[].chapters; do not submit it")
	}
	if strings.TrimSpace(r.Summary) == "" {
		return nil, fmt.Errorf("summary is required")
	}
	if r.ContractStatus != "" && r.ContractStatus != "met" && r.ContractStatus != "partial" && r.ContractStatus != "missed" {
		return nil, fmt.Errorf("invalid contract_status: %q", r.ContractStatus)
	}
	for _, miss := range r.ContractMisses {
		if strings.TrimSpace(miss) == "" {
			return nil, fmt.Errorf("contract_misses cannot contain empty entries")
		}
	}
	var boundary *store.ArcBoundary
	if r.Scope == "arc" {
		var err error
		boundary, err = t.store.Outline.CheckArcBoundary(r.Chapter)
		if err != nil {
			return nil, fmt.Errorf("check arc scope: %w", err)
		}
		if boundary == nil || !boundary.IsArcEnd || boundary.EndChapter != r.Chapter {
			return nil, fmt.Errorf("arc review chapter must be an arc endpoint")
		}
	}

	affectedSet := make(map[int]struct{})
	for i := range r.Issues {
		issue := &r.Issues[i]
		if strings.TrimSpace(issue.Description) == "" {
			return nil, fmt.Errorf("issue description is required")
		}
		if strings.TrimSpace(issue.Evidence) == "" {
			return nil, fmt.Errorf("issue evidence is required")
		}
		switch issue.Severity {
		case "critical", "error", "warning":
		default:
			return nil, fmt.Errorf("invalid issue severity: %q", issue.Severity)
		}
		if len(issue.Chapters) == 0 && r.Scope == "chapter" {
			issue.Chapters = []int{r.Chapter}
		}
		if len(issue.Chapters) == 0 {
			return nil, fmt.Errorf("issue chapters are required when scope=%s", r.Scope)
		}
		issue.Chapters = uniqueSortedChapters(issue.Chapters)
		for _, chapter := range issue.Chapters {
			switch r.Scope {
			case "chapter":
				if chapter != r.Chapter {
					return nil, fmt.Errorf("chapter review issue must reference chapter %d, got %d", r.Chapter, chapter)
				}
			case "global":
				if chapter <= 0 || chapter > r.Chapter {
					return nil, fmt.Errorf("global review issue chapter %d outside 1-%d", chapter, r.Chapter)
				}
			case "arc":
				if chapter < boundary.StartChapter || chapter > boundary.EndChapter {
					return nil, fmt.Errorf("arc review issue chapter %d outside %d-%d", chapter, boundary.StartChapter, boundary.EndChapter)
				}
			}
			if issue.RequiresChange {
				affectedSet[chapter] = struct{}{}
			}
		}
	}
	if err := validateDimensions(r.Dimensions); err != nil {
		return nil, err
	}
	derived := make([]int, 0, len(affectedSet))
	for chapter := range affectedSet {
		derived = append(derived, chapter)
	}
	slices.Sort(derived)
	if r.Verdict == "accept" && len(derived) > 0 {
		return nil, fmt.Errorf("accept review cannot contain issues with requires_change=true")
	}
	if (r.Verdict == "rewrite" || r.Verdict == "polish") && len(derived) == 0 {
		return nil, fmt.Errorf("verdict=%s requires at least one issue with requires_change=true", r.Verdict)
	}
	r.AffectedChapters = derived
	return boundary, nil
}

func uniqueSortedChapters(chapters []int) []int {
	seen := make(map[int]struct{}, len(chapters))
	for _, chapter := range chapters {
		seen[chapter] = struct{}{}
	}
	result := make([]int, 0, len(seen))
	for chapter := range seen {
		result = append(result, chapter)
	}
	slices.Sort(result)
	return result
}

// reviewFlow is the only mapping point between the literary verdict and the persistence protocol. The verdict is decided by the Editor;
// only the three control outcomes the Router can recover from are accepted here.
func reviewFlow(verdict string) (domain.FlowState, error) {
	switch verdict {
	case "accept":
		return domain.FlowWriting, nil
	case "polish":
		return domain.FlowPolishing, nil
	case "rewrite":
		return domain.FlowRewriting, nil
	default:
		return "", fmt.Errorf("invalid review verdict: %q", verdict)
	}
}

func validateDimensions(dimensions []domain.DimensionScore) error {
	if len(dimensions) == 0 {
		return fmt.Errorf("dimensions must contain at least one evidence-based assessment")
	}

	seen := make(map[string]struct{}, len(dimensions))
	for _, dim := range dimensions {
		name := strings.TrimSpace(dim.Dimension)
		if name == "" {
			return fmt.Errorf("dimension name is required")
		}
		if _, ok := seen[name]; ok {
			return fmt.Errorf("duplicate dimension: %s", name)
		}
		seen[name] = struct{}{}
		if dim.Score < 0 || dim.Score > 100 {
			return fmt.Errorf("invalid score for %s: %d", dim.Dimension, dim.Score)
		}
		if strings.TrimSpace(dim.Comment) == "" {
			return fmt.Errorf("dimension comment is required: %s", dim.Dimension)
		}
	}
	return nil
}
