package userrules

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/CTKiet2006/kietnovel/internal/llmcontract"
	"github.com/voocel/agentcore"
	"github.com/voocel/agentcore/llm"
)

func TestExtractJSON_StripsCodeFences(t *testing.T) {
	cases := []struct{ in, wantHas string }{
		{"```json\n{\"a\":1}\n```", `"a":1`},
		{"```\n{\"a\":1}\n```", `"a":1`},
		{"前缀解释\n{\"a\":1}\n后缀", `"a":1`},
		{"{\"a\":1}", `"a":1`},
	}
	for _, c := range cases {
		got := llmcontract.ExtractJSONObject(c.in)
		if got == "" {
			t.Fatalf("extractJSON(%q) 返回空", c.in)
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(got), &m); err != nil {
			t.Fatalf("extractJSON(%q)=%q 不是合法 JSON: %v", c.in, got, err)
		}
	}
	if llmcontract.ExtractJSONObject("没有任何 JSON") != "" {
		t.Fatal("无 JSON 时应返回空串")
	}
}

func TestParseNormalizerJSON_FullOutput(t *testing.T) {
	raw := "```json\n" + `{
  "structured": {
    "genre": "都市",
    "forbidden_chars": [],
    "forbidden_phrases": ["某种程度上"],
    "fatigue_words": [{"word": "竟然", "max_per_chapter": 2}]
  },
  "preferences": "主角冷静克制",
  "uncertain": ["少用比喻：无阈值"]
}` + "\n```"
	body := llmcontract.ExtractJSONObject(raw)
	if err := llmcontract.ValidateJSON(normalizeContract.Schema, []byte(body)); err != nil {
		t.Fatalf("应解析成功: %v", err)
	}
	var out normalizerOutput
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("应解码成功: %v", err)
	}
	cand, err := out.toCandidate("startup_prompt")
	if err != nil {
		t.Fatalf("toCandidate: %v", err)
	}
	if cand.Structured.Genre != "都市" {
		t.Fatalf("genre 解析错误：%+v", cand.Structured)
	}
	if len(cand.Structured.ForbiddenPhrases) != 1 || cand.Structured.ForbiddenPhrases[0] != "某种程度上" {
		t.Fatalf("forbidden_phrases 解析错误：%v", cand.Structured.ForbiddenPhrases)
	}
	if cand.Structured.FatigueWords["竟然"] != 2 {
		t.Fatalf("fatigue_words 数组应转成 map：%v", cand.Structured.FatigueWords)
	}
	if cand.Preferences != "主角冷静克制" {
		t.Fatalf("preferences 解析错误：%q", cand.Preferences)
	}
	if len(cand.Uncertain) != 1 {
		t.Fatalf("uncertain 应有 1 条，得到 %v", cand.Uncertain)
	}
}

// fatigue entry validation: an empty word and a non-positive-integer cap are both business errors that can be fed back for correction.
func TestToCandidateRejectsInvalidFatigueEntries(t *testing.T) {
	bad := normalizerOutput{Structured: normalizerStructured{
		FatigueWords: []fatigueWordEntry{{Word: " ", MaxPerChapter: 2}},
	}}
	if _, err := bad.toCandidate("x"); err == nil {
		t.Fatal("空词条目应报错")
	}
	bad = normalizerOutput{Structured: normalizerStructured{
		FatigueWords: []fatigueWordEntry{{Word: "竟然", MaxPerChapter: 0}},
	}}
	if _, err := bad.toCandidate("x"); err == nil {
		t.Fatal("非正整数阈值应报错")
	}
}

func TestParseNormalizerJSON_GarbageFails(t *testing.T) {
	if body := llmcontract.ExtractJSONObject("模型只回了一句话，没有 JSON"); body != "" {
		t.Fatal("无 JSON 应解析失败（触发降级）")
	}
	if body := llmcontract.ExtractJSONObject("{ 不完整"); body != "" {
		t.Fatal("残缺 JSON 应解析失败")
	}
}

// Contract test (RFC §11.1): the root is object and all properties (including the nested structured/fatigue_words entries) are required.
func TestNormalizeContractIsStrictReady(t *testing.T) {
	if normalizeContract.Schema["type"] != "object" {
		t.Fatal("根必须是 object")
	}
	if err := llmcontract.ValidateStrictReady(normalizeContract.Schema); err != nil {
		t.Fatal(err)
	}
}

func TestNormalize_NilModelErrors(t *testing.T) {
	// No model available: it returns an explicit error, and the Service layer degrades it to raw preferences.
	var n *Normalizer = NewNormalizer(nil)
	if _, err := n.Normalize(t.Context(), "startup_prompt", "每章1200字，主角冷静"); err == nil {
		t.Fatal("无模型应返回错误")
	}
}

// scriptedModel is a minimal fake ChatModel: it emits preset replies in call order and records the messages received on the last
// round, so a test can assert whether feedback-style retry merged the correction prompt into the next round's conversation. Once the replies are used up it repeats the last one.
type scriptedModel struct {
	replies  []string
	calls    int
	lastMsgs []agentcore.Message
	lastCfg  agentcore.CallConfig
	err      error // when non-nil, Generate always returns this error
	cancel   context.CancelFunc
	cancelAt int
}

func (m *scriptedModel) Generate(_ context.Context, messages []agentcore.Message, _ []agentcore.ToolSpec, opts ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	var cfg agentcore.CallConfig
	for _, o := range opts {
		o(&cfg)
	}
	m.lastCfg = cfg
	m.lastMsgs = messages
	m.calls++
	if m.cancel != nil && m.cancelAt > 0 && m.calls >= m.cancelAt {
		m.cancel()
	}
	if m.err != nil {
		return nil, m.err
	}
	i := m.calls - 1
	if i >= len(m.replies) {
		i = len(m.replies) - 1
	}
	return &agentcore.LLMResponse{Message: agentcore.Message{
		Role:    agentcore.RoleAssistant,
		Content: []agentcore.ContentBlock{agentcore.TextBlock(m.replies[i])},
	}}, nil
}

func (m *scriptedModel) GenerateStream(context.Context, []agentcore.Message, []agentcore.ToolSpec, ...agentcore.CallOption) (<-chan agentcore.StreamEvent, error) {
	return nil, nil
}

func (m *scriptedModel) SupportsTools() bool { return false }

// Feedback-style retry: the first round emits bad JSON and only the second round is valid. Normalize should succeed, and the second round's
// conversation must carry the previous round's bad output and the correction prompt (feedback-style, not a blind verbatim retry).
func TestNormalize_FeedbackRetryRecovers(t *testing.T) {
	model := &scriptedModel{replies: []string{
		"这不是 JSON",
		`{"structured":{"genre":"","forbidden_chars":[],"forbidden_phrases":["某种程度上"],"fatigue_words":[]},"preferences":"","uncertain":[]}`,
	}}
	n := NewNormalizer(model)

	cand, err := n.Normalize(t.Context(), "startup_prompt", "不要出现某种程度上")
	if err != nil {
		t.Fatalf("次轮已返回合法 JSON，不应失败: %v", err)
	}
	if len(cand.Structured.ForbiddenPhrases) != 1 {
		t.Fatalf("应解析出 forbidden_phrases，got %+v", cand.Structured)
	}
	if model.calls != 2 {
		t.Fatalf("应在第 2 次成功，实际调用 %d 次", model.calls)
	}

	var sawBad, sawHint bool
	for _, msg := range model.lastMsgs {
		text := msg.TextContent()
		if text == "这不是 JSON" {
			sawBad = true
		}
		if strings.Contains(text, "JSON Schema") && strings.Contains(text, "错误：") {
			sawHint = true
		}
	}
	if !sawBad || !sawHint {
		t.Errorf("次轮应并入上一轮坏输出与纠正提示，sawBad=%v sawHint=%v", sawBad, sawHint)
	}
	system := model.lastMsgs[0].TextContent()
	if !strings.Contains(system, "<output-json-schema>") || !strings.Contains(system, `"fatigue_words"`) {
		t.Fatalf("prompt contract 应从 Contract 自动附加 schema:\n%s", system)
	}
}

// Normalization does not override the model's thinking default; ordinary chat models reject an explicit off.
func TestNormalize_LeavesThinkingUnspecifiedAndReservesTokens(t *testing.T) {
	model := &scriptedModel{replies: []string{`{"structured":{"genre":"","forbidden_chars":[],"forbidden_phrases":[],"fatigue_words":[]},"preferences":"x","uncertain":[]}`}}
	n := NewNormalizer(model)

	if _, err := n.Normalize(t.Context(), "startup_prompt", "随便一条规则"); err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if model.lastCfg.ThinkingLevel != agentcore.ThinkingAuto {
		t.Errorf("不应发送 thinking 参数，got %q", model.lastCfg.ThinkingLevel)
	}
	if model.lastCfg.MaxTokens != normalizeMaxTokens {
		t.Errorf("max_tokens 应为 %d，got %d", normalizeMaxTokens, model.lastCfg.MaxTokens)
	}
}

// Bad JSON all the way through: there is no fixed attempt cap, it keeps feeding back and re-asking until the context is canceled.
func TestNormalize_FeedbackRetryContinuesUntilContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	model := &scriptedModel{replies: []string{"坏"}, cancel: cancel, cancelAt: 4}
	n := NewNormalizer(model)

	_, err := n.Normalize(ctx, "startup_prompt", "每章1200字")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("应由 context 结束自愈循环，得 %v", err)
	}
	if model.calls != 4 {
		t.Fatalf("context 取消前应持续调用，实际 %d", model.calls)
	}
}

type terminalTestError struct{}

func (terminalTestError) Error() string   { return "401 authentication failed" }
func (terminalTestError) Retryable() bool { return false }

type retryableTestError struct{}

func (retryableTestError) Error() string             { return "provider unavailable" }
func (retryableTestError) Retryable() bool           { return true }
func (retryableTestError) RetryAfter() time.Duration { return time.Millisecond }

// A terminal error (401 etc.) must not be blindly retried: exactly 1 call, then the error is returned.
func TestNormalize_TerminalErrorStopsImmediately(t *testing.T) {
	model := &scriptedModel{err: terminalTestError{}}
	n := NewNormalizer(model)

	_, err := n.Normalize(t.Context(), "startup_prompt", "规则")
	if err == nil || !errors.As(err, &terminalTestError{}) {
		t.Fatalf("应透出终止错误: %v", err)
	}
	if model.calls != 1 {
		t.Fatalf("终止错误不应重试，实际调用 %d 次", model.calls)
	}
}

// retryable request errors are retried with backoff by llmretry.
type flakyModel struct {
	scriptedModel
	failures int
}

func (m *flakyModel) Generate(ctx context.Context, msgs []agentcore.Message, tools []agentcore.ToolSpec, opts ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	if m.scriptedModel.calls < m.failures {
		m.scriptedModel.calls++
		return nil, retryableTestError{}
	}
	return m.scriptedModel.Generate(ctx, msgs, tools, opts...)
}

func TestNormalize_RetryableErrorRecovers(t *testing.T) {
	model := &flakyModel{
		scriptedModel: scriptedModel{replies: []string{`{"structured":{"genre":"","forbidden_chars":[],"forbidden_phrases":[],"fatigue_words":[]},"preferences":"x","uncertain":[]}`}},
		failures:      2,
	}
	n := NewNormalizer(model)
	cand, err := n.Normalize(t.Context(), "startup_prompt", "规则")
	if err != nil || cand.Preferences != "x" {
		t.Fatalf("退避后应成功: %+v %v", cand, err)
	}
}

// nativeRulesModel declares support for native JSON Schema.
type nativeRulesModel struct {
	*scriptedModel
}

func (m *nativeRulesModel) Capabilities() llm.Capabilities {
	return llm.Capabilities{
		Provider:   "openai",
		Model:      "gpt-test",
		Structured: llm.StructuredCapabilities{JSONSchema: llm.SupportYes, Strict: llm.SupportYes},
	}
}

func TestNormalize_NativeSendsSchemaAndRejectsFences(t *testing.T) {
	// Native mode: the schema goes into the request; bare JSON succeeds.
	model := &nativeRulesModel{&scriptedModel{replies: []string{
		`{"structured":{"genre":"","forbidden_chars":[],"forbidden_phrases":[],"fatigue_words":[]},"preferences":"x","uncertain":[]}`,
	}}}
	n := NewNormalizer(model)
	cand, err := n.Normalize(t.Context(), "startup_prompt", "规则")
	if err != nil || cand.Preferences != "x" {
		t.Fatalf("native 归一化失败: %+v %v", cand, err)
	}
	rf := model.lastCfg.ResponseFormat
	if rf == nil || rf.JSONSchema == nil || rf.JSONSchema.Name != "userrules_normalize" {
		t.Fatalf("native 模式应发送 schema: %+v", rf)
	}
	if got := model.lastMsgs[0].TextContent(); got != normalizerSystemPrompt {
		t.Fatalf("native 模式不应向提示词重复注入 schema:\n%s", got)
	}

	// Fenced output = contract violation: error out immediately, no extractJSON, no re-asking.
	fenced := &nativeRulesModel{&scriptedModel{replies: []string{
		"```json\n{\"structured\":{},\"preferences\":\"x\",\"uncertain\":[]}\n```",
	}}}
	n = NewNormalizer(fenced)
	_, err = n.Normalize(t.Context(), "startup_prompt", "规则")
	if err == nil || !strings.Contains(err.Error(), "契约违约") {
		t.Fatalf("期望契约违约错误, got %v", err)
	}
	if fenced.calls != 1 {
		t.Fatalf("契约违约不应重问，实际 %d 次", fenced.calls)
	}
}
