package arbiter

import (
	"context"
	"fmt"
	"strings"

	"github.com/CTKiet2006/kietnovel/internal/llmcontract"
	"github.com/voocel/agentcore"
	"github.com/voocel/agentcore/schema"
)

// FailureFacts is the fact packet shared by the worker_failure / deadlock scenarios:
// the Engine has already done the deterministic classification (retry / bad arguments and the like never get here), so
// everything reaching the Arbiter is the residue that "deterministic code cannot find a way out of".
type FailureFacts struct {
	Kind          string   `json:"kind"` // worker_failure | deadlock
	Agent         string   `json:"agent,omitempty"`
	Task          string   `json:"task,omitempty"`
	Error         string   `json:"error,omitempty"` // worker_failure: error text
	ErrorKind     string   `json:"error_kind,omitempty"`
	Repeats       int      `json:"repeats,omitempty"` // deadlock: how many times the same instruction was dispatched
	Phase         string   `json:"phase,omitempty"`
	NextChapter   int      `json:"next_chapter,omitempty"`
	PendingQueue  []int    `json:"pending_rewrites,omitempty"`
	FoundationGap []string `json:"foundation_missing,omitempty"`
	FactWarnings  []string `json:"fact_warnings,omitempty"`
}

// FailureDecision is a failure / deadlock arbitration.
type FailureDecision struct {
	Action   string      `json:"action"` // retry | reroute | abort
	Dispatch *DispatchOp `json:"dispatch,omitempty"`
	Reason   string      `json:"reason"`
}

func (d *FailureDecision) ValidateAgainst(f FailureFacts) error {
	if strings.TrimSpace(d.Reason) == "" {
		return fmt.Errorf("reason 不能为空")
	}
	switch d.Action {
	case "retry", "abort":
		return nil
	case "reroute":
		if d.Dispatch == nil {
			return fmt.Errorf("reroute 必须附 dispatch")
		}
		if err := d.Dispatch.validate(); err != nil {
			return err
		}
		return validateDispatchAgainst(d.Dispatch, f.Phase)
	default:
		return fmt.Errorf("action 非法: %q（可选 retry / reroute / abort）", d.Action)
	}
}

// failureContract sits next to FailureDecision: action is a closed enum, dispatch is a nullable object
// (non-null only for reroute); cross-field combinations are still checked against the facts by ValidateAgainst.
var failureContract = failureContractFor("zh")

func failureContractFor(lang string) llmcontract.Contract {
	desc := "失败/僵局裁定:给出出路"
	actionDesc := "出路"
	dispatchDesc := "派单目标(仅 reroute 时给出,否则为 null)"
	reasonDesc := "裁定理由"

	switch strings.ToLower(strings.TrimSpace(lang)) {
	case "vi":
		desc = "Phán quyết sự cố/bế tắc: Đưa ra lối thoát"
		actionDesc = "Lối thoát (retry, reroute, abort)"
		dispatchDesc = "Mục tiêu phân phát (chỉ khi reroute, trường hợp khác là null)"
		reasonDesc = "Lý do phán quyết"
	case "en":
		desc = "Failure/deadlock adjudication: provide resolution path"
		actionDesc = "Resolution action (retry, reroute, abort)"
		dispatchDesc = "Dispatch target (only when reroute, null otherwise)"
		reasonDesc = "Adjudication reason"
	}

	return llmcontract.Contract{
		Name:        "arbiter_failure",
		Description: desc,
		Schema: schema.Object(
			schema.Property("action", schema.Enum(actionDesc, "retry", "reroute", "abort")).Required(),
			schema.Property("dispatch", dispatchSchemaFor(dispatchDesc, lang)).Required(),
			schema.Property("reason", schema.String(reasonDesc)).Required(),
		),
	}
}

// DecideFailure arbitrates a failure / deadlock. Failure semantics: returning an error → the Engine takes the most
// conservative path (pause + notify); it never arbitrates indefinitely.
func DecideFailure(ctx context.Context, model agentcore.ChatModel, systemPrompt string, facts FailureFacts) (FailureDecision, error) {
	payload, err := marshalPayload(facts)
	if err != nil {
		return FailureDecision{}, err
	}
	lang := detectPromptLanguage(systemPrompt)
	return decide(ctx, model, failureContractFor(lang), systemPrompt, payload, func(d *FailureDecision) error {
		return d.ValidateAgainst(facts)
	})
}
