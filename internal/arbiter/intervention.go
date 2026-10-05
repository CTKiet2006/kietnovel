package arbiter

import (
	"context"
	"fmt"
	"strings"

	"github.com/CTKiet2006/kietnovel/internal/domain"
	"github.com/CTKiet2006/kietnovel/internal/llmcontract"
	storepkg "github.com/CTKiet2006/kietnovel/internal/store"
	"github.com/CTKiet2006/kietnovel/internal/utils"
	"github.com/voocel/agentcore"
	"github.com/voocel/agentcore/schema"
)

// InterventionFacts is the fact packet for intervention triage (a snapshot at Collect time).
// The Engine reconciles with Phase/QueueHead before executing a Dispatch at the boundary (a worker run sits between the
// arbitration and the execution, so the facts may have moved on; a mismatch → discard and re-ask with fresh facts).
type InterventionFacts struct {
	Phase                    string           `json:"phase,omitempty"`
	Flow                     string           `json:"flow,omitempty"`
	Title                    string           `json:"title,omitempty"`
	CompletedChapters        int              `json:"completed_chapters"`
	OutlinedChapters         int              `json:"outlined_chapters,omitempty"`
	DynamicPlanning          bool             `json:"dynamic_planning"`
	NextChapter              int              `json:"next_chapter,omitempty"`
	PendingRewrites          []int            `json:"pending_rewrites,omitempty"`
	ReopenCount              int              `json:"reopen_count,omitempty"` // cumulative number of times the user explicitly reopened a finished book with /reopen
	FoundationMissing        []string         `json:"foundation_missing,omitempty"`
	PlanningTier             string           `json:"planning_tier,omitempty"`
	AdvanceMode              string           `json:"advance_mode,omitempty"`
	HasAdvanceHold           bool             `json:"has_advance_hold"`
	AdvanceHoldAfter         string           `json:"advance_hold_after,omitempty"`
	AdvanceHoldTargetChapter int              `json:"advance_hold_target_chapter,omitempty"`
	AdvanceHoldReason        string           `json:"advance_hold_reason,omitempty"`
	Running                  bool             `json:"running"`                  // whether a run was in flight when the intervention arrived
	CheckpointSeq            int64            `json:"checkpoint_seq,omitempty"` // latest checkpoint at Collect time; used by the Engine for reconciliation
	RecentDecisions          []RecentDecision `json:"recent_decisions,omitempty"`
}

// RecentDecision is the intervention memory: a summary of the last few arbitrations, covering cross-intervention references like "how did last time's change go".
type RecentDecision struct {
	At     string `json:"at"`
	Input  string `json:"input"`
	Reason string `json:"reason,omitempty"`
}

// QueueHead returns the head of the rewrite queue (0 if there is none), used by the Engine for reconciliation.
func (f InterventionFacts) QueueHead() int {
	if len(f.PendingRewrites) > 0 {
		return f.PendingRewrites[0]
	}
	return 0
}

// CollectInterventionFacts reads all the triage facts from the store. Any failure to read a control fact returns an
// error explicitly; the Arbiter is forbidden from making semantic decisions on an incomplete snapshot assembled from zero values.
func CollectInterventionFacts(st *storepkg.Store) (InterventionFacts, error) {
	var f InterventionFacts
	if st == nil {
		return f, fmt.Errorf("store 不能为空")
	}
	missing, err := st.FoundationMissing()
	if err != nil {
		return f, fmt.Errorf("读取基础设定状态: %w", err)
	}
	f.FoundationMissing = missing
	book, err := st.Book.Load()
	if err != nil {
		return f, fmt.Errorf("读取作品信息: %w", err)
	}
	if book != nil {
		f.Title = book.Title
	}
	p, err := st.Progress.Load()
	if err != nil {
		return f, fmt.Errorf("读取进度: %w", err)
	}
	if p != nil {
		f.Phase = string(p.Phase)
		f.Flow = string(p.Flow)
		f.CompletedChapters = len(p.CompletedChapters)
		f.DynamicPlanning = p.Layered
		if p.Layered {
			outline, outlineErr := st.Outline.LoadOutline()
			if outlineErr != nil {
				return f, fmt.Errorf("读取当前详细大纲: %w", outlineErr)
			}
			f.OutlinedChapters = len(outline)
		} else {
			f.OutlinedChapters = p.TotalChapters
		}
		f.NextChapter = p.NextChapter()
		f.PendingRewrites = append([]int(nil), p.PendingRewrites...)
		f.ReopenCount = p.ReopenCount
	}
	meta, err := st.RunMeta.Load()
	if err != nil {
		return f, fmt.Errorf("读取运行元信息: %w", err)
	}
	if meta != nil {
		f.PlanningTier = string(meta.PlanningTier)
		f.AdvanceMode = string(meta.AdvanceMode)
		if meta.AdvanceHold != nil {
			f.HasAdvanceHold = true
			f.AdvanceHoldAfter = string(meta.AdvanceHold.After)
			f.AdvanceHoldTargetChapter = meta.AdvanceHold.TargetChapter
			f.AdvanceHoldReason = meta.AdvanceHold.Reason
		}
	}
	if cp := st.Checkpoints.LatestGlobal(); cp != nil {
		f.CheckpointSeq = cp.Seq
	}
	recent, err := st.Decisions.Recent(5)
	if err != nil {
		return f, fmt.Errorf("读取近期裁定: %w", err)
	}
	for _, r := range recent {
		if r.Kind != "intervention" {
			continue
		}
		f.RecentDecisions = append(f.RecentDecisions, RecentDecision{
			At: r.At, Input: utils.TruncateRunes(r.Input, 80), Reason: r.Reason,
		})
	}
	return f, nil
}

// AdvanceHoldOp is a one-shot pause action: pause at a work boundary, once the rework queue drains, or once the target chapter completes; it can also be cancelled.
type AdvanceHoldOp struct {
	Cancel        bool                    `json:"cancel,omitempty"`
	After         domain.AdvanceHoldAfter `json:"after,omitempty"`
	TargetChapter int                     `json:"target_chapter,omitempty"`
	Reason        string                  `json:"reason,omitempty"`
}

// ReopenOp is a whole-book rework: reopen the entire book into the rework state and enqueue the target chapter (legal only for phase=complete).
type ReopenOp struct {
	Chapters []int  `json:"chapters"`
	Reason   string `json:"reason,omitempty"`
}

// InterventionDecision is an intervention arbitration. Action combinations are free, and the Engine fixes the execution
// order: answer → rules → hold → reopen → dispatch; at most one dispatch (a type-level fact).
type InterventionDecision struct {
	Answer   string         `json:"answer,omitempty"`
	Rules    string         `json:"rules,omitempty"`
	Hold     *AdvanceHoldOp `json:"hold,omitempty"`
	Reopen   *ReopenOp      `json:"reopen,omitempty"`
	Dispatch *DispatchOp    `json:"dispatch,omitempty"`
	Reason   string         `json:"reason"`
}

var interventionContract = interventionContractFor("zh")

func interventionContractFor(lang string) llmcontract.Contract {
	desc := "用户干预裁定：回答、规则、暂停、重开与派单"
	ansDesc := "回显给用户的文字；无则为 null"
	rulesDesc := "要落盘的长效写作规则原文；无则为 null"
	holdCancelDesc := "是否取消既有一次性暂停"
	holdAfterDesc := "暂停触发点；取消时为 null"
	holdTargetDesc := "after=chapter 时的目标章节；其他情况为 null"
	holdReasonDesc := "用户诉求摘要；取消时可为 null"
	reopenChapsDesc := "需要重开的章节号"
	reopenReasonDesc := "重开理由"
	dispatchDesc := "派单目标；无需派单时为 null"
	reasonDesc := "一句话裁定理由"

	switch strings.ToLower(strings.TrimSpace(lang)) {
	case "vi":
		desc = "Phán quyết can thiệp của người dùng: Trả lời, quy tắc, tạm dừng, mở lại và giao việc"
		ansDesc = "Văn bản phản hồi cho người dùng; null nếu không có"
		rulesDesc = "Quy tắc văn phong dài hạn cần lưu đĩa; null nếu không có"
		holdCancelDesc = "Có hủy bỏ lệnh tạm dừng một lần hiện có hay không"
		holdAfterDesc = "Thời điểm kích hoạt tạm dừng; null khi hủy bỏ"
		holdTargetDesc = "Số chương mục tiêu khi after=chapter; null trong trường hợp khác"
		holdReasonDesc = "Tóm tắt yêu cầu của người dùng; null khi hủy bỏ"
		reopenChapsDesc = "Các số chương cần mở lại"
		reopenReasonDesc = "Lý do mở lại"
		dispatchDesc = "Mục tiêu phân phát công việc; null nếu không cần giao việc"
		reasonDesc = "Lý do phán quyết ngắn gọn"
	case "en":
		desc = "User intervention adjudication: answer, rules, hold, reopen, and dispatch"
		ansDesc = "Response text echoed to user; null if none"
		rulesDesc = "Writing rules to persist; null if none"
		holdCancelDesc = "Whether to cancel existing advance hold"
		holdAfterDesc = "Pause trigger point; null when cancelling"
		holdTargetDesc = "Target chapter when after=chapter; null otherwise"
		holdReasonDesc = "Summary of user request; null when cancelling"
		reopenChapsDesc = "Chapter numbers to reopen"
		reopenReasonDesc = "Reopen reason"
		dispatchDesc = "Dispatch target; null if no dispatch needed"
		reasonDesc = "Concise adjudication reason"
	}

	return llmcontract.Contract{
		Name:        "arbiter_intervention",
		Description: desc,
		Schema: schema.Object(
			schema.Property("answer", llmcontract.Nullable(schema.String(ansDesc))).Required(),
			schema.Property("rules", llmcontract.Nullable(schema.String(rulesDesc))).Required(),
			schema.Property("hold", llmcontract.Nullable(schema.Object(
				schema.Property("cancel", schema.Bool(holdCancelDesc)).Required(),
				schema.Property("after", llmcontract.Nullable(schema.Enum(holdAfterDesc, string(domain.AdvanceHoldAtBoundary), string(domain.AdvanceHoldAfterRewritesDrained), string(domain.AdvanceHoldAtChapter)))).Required(),
				schema.Property("target_chapter", llmcontract.Nullable(schema.Int(holdTargetDesc))).Required(),
				schema.Property("reason", llmcontract.Nullable(schema.String(holdReasonDesc))).Required(),
			))).Required(),
			schema.Property("reopen", llmcontract.Nullable(schema.Object(
				schema.Property("chapters", schema.Array(reopenChapsDesc, schema.Int(""))).Required(),
				schema.Property("reason", llmcontract.Nullable(schema.String(reopenReasonDesc))).Required(),
			))).Required(),
			schema.Property("dispatch", dispatchSchemaFor(dispatchDesc, lang)).Required(),
			schema.Property("reason", schema.String(reasonDesc)).Required(),
		),
	}
}

// ValidateAgainst mechanically validates against the facts (legality inside the scenario; cross-scenario actions are already excluded by the types).
func (d *InterventionDecision) ValidateAgainst(f InterventionFacts) error {
	if strings.TrimSpace(d.Reason) == "" {
		return fmt.Errorf("reason 不能为空")
	}
	if d.Answer == "" && d.Rules == "" && d.Hold == nil && d.Reopen == nil && d.Dispatch == nil {
		return fmt.Errorf("空决策：至少要有一个动作或 answer")
	}
	if err := d.Dispatch.validate(); err != nil {
		return err
	}
	if err := validateDispatchAgainst(d.Dispatch, f.Phase); err != nil {
		return err
	}
	complete := f.Phase == string(domain.PhaseComplete)
	if d.Reopen != nil {
		if !complete {
			return fmt.Errorf("reopen 仅限完本期（当前 phase=%s）", f.Phase)
		}
		if len(d.Reopen.Chapters) == 0 {
			return fmt.Errorf("reopen.chapters 不能为空")
		}
		for _, ch := range d.Reopen.Chapters {
			if ch < 1 || ch > f.CompletedChapters {
				return fmt.Errorf("reopen 章节 %d 越界（已完成 %d 章）", ch, f.CompletedChapters)
			}
		}
	}
	if complete && d.Dispatch != nil {
		return fmt.Errorf("完本期禁止直接派单；返工用 reopen（入队后由 Router 自动派发）")
	}
	if d.Hold != nil && !d.Hold.Cancel {
		if f.Phase != string(domain.PhaseWriting) {
			return fmt.Errorf("一次性暂停仅限写作期（当前 phase=%s）", f.Phase)
		}
		hold := domain.AdvanceHold{After: d.Hold.After, TargetChapter: d.Hold.TargetChapter, Reason: d.Hold.Reason}
		if err := hold.Validate(); err != nil {
			return fmt.Errorf("hold 无效: %w", err)
		}
		nextChapter := f.NextChapter
		if nextChapter == 0 {
			nextChapter = f.CompletedChapters + 1
		}
		if hold.After == domain.AdvanceHoldAtChapter && hold.TargetChapter < nextChapter {
			return fmt.Errorf("目标章节 %d 早于当前下一章 %d", hold.TargetChapter, nextChapter)
		}
	}
	return nil
}

// validateDispatchAgainst turns the stage discipline in the prompt into a mechanical defense. The Architect may maintain
// structure during both planning and writing; Writer/Editor can only consume work facts that are already complete and in writing.
func validateDispatchAgainst(dispatch *DispatchOp, phase string) error {
	if dispatch == nil {
		return nil
	}
	if phase == "" {
		return fmt.Errorf("缺少 phase，禁止执行派单")
	}
	if phase == string(domain.PhaseComplete) {
		return fmt.Errorf("完本期禁止直接派单")
	}
	switch dispatch.Agent {
	case "writer", "editor":
		if phase != string(domain.PhaseWriting) {
			return fmt.Errorf("%s 仅能在 writing 阶段派发（当前 phase=%s）", dispatch.Agent, phase)
		}
	}
	return nil
}

// DecideIntervention triages an intervention. Failure semantics: returning an error → the caller explicitly echoes
// the real failure reason and produces no writes at all (better to do nothing than to act wrongly).
func DecideIntervention(ctx context.Context, model agentcore.ChatModel, systemPrompt string, facts InterventionFacts, text string) (InterventionDecision, error) {
	payload, err := marshalPayload(struct {
		Intervention string            `json:"intervention"`
		Facts        InterventionFacts `json:"facts"`
	}{Intervention: text, Facts: facts})
	if err != nil {
		return InterventionDecision{}, err
	}
	lang := detectPromptLanguage(systemPrompt)
	return decide(ctx, model, interventionContractFor(lang), systemPrompt, payload, func(d *InterventionDecision) error {
		return d.ValidateAgainst(facts)
	})
}
