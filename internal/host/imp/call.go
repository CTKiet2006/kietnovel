package imp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/CTKiet2006/kietnovel/internal/llmcontract"
	"github.com/CTKiet2006/kietnovel/internal/llmretry"
	"github.com/voocel/agentcore"
	"github.com/voocel/litellm"
)

// callModel is the kernel's minimal dependency on the model, which makes it easy to inject a mock in tests.
type callModel interface {
	Generate(ctx context.Context, messages []agentcore.Message, tools []agentcore.ToolSpec, opts ...agentcore.CallOption) (*agentcore.LLMResponse, error)
}

// errTruncated signals that the model stopped on length (a capacity error). It carries the raw text so the caller can decide between failing and salvaging a prefix (§9.5).
type errTruncated struct {
	Raw string
}

func (e *errTruncated) Error() string { return "模型输出被长度截断（stop=length）" }

// errSemantic signals an output-layer failure that re-asking cannot repair; it carries the raw response
// so the runner can uniformly persist a failures/ failure artifact (§14.2). Shared by every semantic function.
type errSemantic struct {
	Raw string
	Err error
}

func (e *errSemantic) Error() string { return e.Err.Error() }
func (e *errSemantic) Unwrap() error { return e.Err }

// callProfile carries the thinking and observability options, derived from the ModelRuntime probed by the Host.
// The structured protocol is chosen independently by callStructured from the model facts and the static Contract.
type callProfile struct {
	thinking agentcore.ThinkingLevel
	// notify is optional: it echoes request-backoff retries / validation re-asks to the UI; nil means silent (§14.1).
	// retryAt non-zero = the deadline of the next retry, which the UI renders as a per-second countdown (the event carries only the deadline; the remaining time is computed at render time).
	notify func(msg string, retryAt time.Time)
	// progress is optional: it echoes internal progress inside long-running stages (segmentation chunk N/M, range digest N/M); nil means silent.
	// Segmentation/synthesis call the model chunk by chunk / range by range inside the function and one chunk can take minutes; without this the panel goes silent for the whole stretch and looks hung (§14.1).
	progress func(current, total int, msg string)
	// log is optional: the import-specific log (logs/import.log); nil falls back to the default logger.
	log *slog.Logger
}

func (p callProfile) logger() *slog.Logger {
	if p.log != nil {
		return p.log
	}
	return slog.Default()
}

// step echoes one ordinary progress update (internal progress of a long-running stage).
func (p callProfile) step(current, total int, format string, args ...any) {
	if p.progress != nil {
		p.progress(current, total, fmt.Sprintf(format, args...))
	}
}

// say echoes one long-running call status. A retry can stay silent for minutes (exponential backoff accumulating past 2 minutes),
// and without an echo the user would assume it hung.
func (p callProfile) say(format string, args ...any) {
	p.sayRetry(time.Time{}, format, args...)
}

// sayRetry echoes a status carrying the retry deadline, for the UI countdown.
func (p callProfile) sayRetry(retryAt time.Time, format string, args ...any) {
	if p.notify != nil {
		p.notify(fmt.Sprintf(format, args...), retryAt)
	}
}

// snippet squeezes multi-line text into a one-line short summary for the UI: whitespace merged, truncated to max runes.
func snippet(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > max {
		return string(r[:max]) + "…"
	}
	return s
}

// briefErr squeezes an error into one-line short text for the UI (the full error chain still goes to the log and the failure artifact).
// The adapter's structured facts come first: on truncation "which kind of error, which status code" is what must survive, the gateway message is expendable.
func briefErr(err error) string {
	s := err.Error()
	if d := modelErrDetail(err); d != "" {
		s = d + "：" + s
	}
	return snippet(s, 100)
}

// errTypeLabels turns litellm error classifications into short, at-a-glance Chinese labels.
var errTypeLabels = map[litellm.ErrorType]string{
	litellm.ErrorTypeAuth:            "鉴权失败",
	litellm.ErrorTypeRateLimit:       "限流",
	litellm.ErrorTypeNetwork:         "网络错误",
	litellm.ErrorTypeValidation:      "请求参数非法",
	litellm.ErrorTypeProvider:        "上游服务错误",
	litellm.ErrorTypeTimeout:         "超时",
	litellm.ErrorTypeQuota:           "配额不足",
	litellm.ErrorTypeModel:           "模型不可用",
	litellm.ErrorTypeInternal:        "内部错误",
	litellm.ErrorTypeContextOverflow: "上下文超限",
	litellm.ErrorTypeOverloaded:      "上游过载",
	litellm.ErrorTypeContentFilter:   "内容过滤拦截",
}

// modelErrDetail extracts the adapter's structured facts from the error chain (error class, HTTP status, provider, model).
// The gateway's message is often just one vague "Provider returned error", which on its own cannot tell a configuration error apart from an
// upstream failure or rate limit; litellm has always carried these facts, they just never reach the Error() text. The agentcore adapter's
// Unwrap explicitly lets callers that know about litellm use errors.As to reach the original error. Non-model-call errors return an empty string.
func modelErrDetail(err error) string {
	var le *litellm.LiteLLMError
	if !errors.As(err, &le) {
		return ""
	}
	parts := make([]string, 0, 4)
	if label := errTypeLabels[le.Type]; label != "" {
		parts = append(parts, label)
	}
	if le.StatusCode != 0 {
		parts = append(parts, fmt.Sprintf("HTTP %d", le.StatusCode))
	}
	if le.Provider != "" {
		parts = append(parts, le.Provider)
	}
	if le.Model != "" {
		parts = append(parts, le.Model)
	}
	return strings.Join(parts, "，")
}

// callOptions assembles this call's CallOption list: the output cap is always present; thinking is included per capability.
// thinking is sent only when it is not Auto -- any level (including off) is an illegal parameter for a model that does not support thinking (same policy as arbiter).
func (p callProfile) callOptions(maxTokens int) []agentcore.CallOption {
	opts := []agentcore.CallOption{agentcore.WithMaxTokens(maxTokens)}
	if p.thinking != agentcore.ThinkingAuto {
		opts = append(opts, agentcore.WithThinking(p.thinking))
	}
	return opts
}

// callStructured adapts the unified structured executor for the import layer and maps generic failures onto import artifact semantics.
func callStructured[T any](ctx context.Context, m callModel, contract llmcontract.Contract, systemPrompt, payload string, maxTokens int, prof callProfile, validate func(*T) error) (T, error) {
	out, err := llmcontract.Execute(ctx, m, llmcontract.Request[T]{
		Contract:     contract,
		SystemPrompt: systemPrompt,
		Payload:      payload,
		Options:      prof.callOptions(maxTokens),
		Validate:     validate,
		Agent:        "import",
		Hooks: llmcontract.Hooks{
			Resolved: func(res llmcontract.Resolution) {
				prof.logger().Debug("imp 结构化协议选择",
					"contract", contract.Name, "structured_mode", res.Mode,
					"capability_source", res.Source, "provider", res.Provider,
					"model", res.Model, "schema_fingerprint", contract.Fingerprint())
			},
			RequestRetry: func(ev llmretry.Event) {
				prof.sayRetry(time.Now().Add(ev.Delay), "模型请求失败（%s），进行第 %d 次重试", briefErr(ev.Err), ev.Attempt)
				prof.logger().Warn("imp 模型请求重试", "attempt", ev.Attempt, "delay", ev.Delay, "err", ev.Err)
			},
			Correction: func(ev llmcontract.Correction) {
				prof.say("输出校验未通过（%s），带错误反馈进行第 %d 次重问", briefErr(ev.Err), ev.Attempt+1)
				prof.logger().Warn("imp 结构化输出自愈", "attempt", ev.Attempt,
					"layer", ev.Layer, "structured_mode", ev.Mode, "err", ev.Err)
			},
		},
	})
	if err == nil {
		return out, nil
	}
	if ctx.Err() != nil {
		return out, ctx.Err()
	}
	var failure *llmcontract.Failure
	if !errors.As(err, &failure) {
		return out, fmt.Errorf("imp: %w", err)
	}
	switch failure.Kind {
	case llmcontract.FailureLength:
		return out, &errTruncated{Raw: failure.Raw}
	case llmcontract.FailureSafety, llmcontract.FailureContract, llmcontract.FailureProtocol:
		if failure.Raw != "" {
			return out, &errSemantic{Raw: failure.Raw, Err: fmt.Errorf("imp: %w", failure)}
		}
	case llmcontract.FailureRequest:
		if detail := modelErrDetail(failure); detail != "" {
			return out, fmt.Errorf("imp: 模型调用失败（%s）：%w", detail, failure)
		}
	}
	return out, fmt.Errorf("imp: %w", failure)
}
