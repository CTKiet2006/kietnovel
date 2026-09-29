// Package userrules is the service layer for normalizing user rules: it takes the natural-language rules from
// each source through a structured LLM call to normalize them into candidate structured fields, then lets rules.BuildSnapshot deterministically merge them into this book's snapshot.
//
// Layered responsibilities:
//   - rules package: pure data + deterministic merge (Snapshot / Candidate / BuildSnapshot / SystemDefaults)
//   - this package: LLM normalization + orchestration + persistence (depends on agentcore + store + rules)
//
// Normalization is an enhancement path, not a precondition for the main writing run: any source that fails degrades to raw preferences, and the main writing run must continue.
package userrules

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/CTKiet2006/kietnovel/internal/llmcontract"
	"github.com/CTKiet2006/kietnovel/internal/rules"
	"github.com/voocel/agentcore"
	"github.com/voocel/agentcore/schema"
)

// normalizeMaxTokens is the output cap for one normalization run (thinking tokens and the JSON output share this budget).
// The normalization JSON itself is small (usually <1k); the generous headroom here is the thinking budget for "reasoning models that cannot turn thinking off" —
// budget it too tightly and thinking squeezes the JSON, causing truncation and parse failures. max_tokens is a cap, not a billed quantity, so raising it costs nothing.
const normalizeMaxTokens = 8192

// normalizeContract sits next to the boundary DTO: all fields required, fatigue_words as an array of objects
// (strict mode forbids maps with dynamic keys), and both modes share the same DTO convention.
var normalizeContract = llmcontract.Contract{
	Name:        "userrules_normalize",
	Description: "把用户自然语言写作规则归一化为结构化字段",
	Schema: schema.Object(
		schema.Property("structured", schema.Object(
			schema.Property("genre", schema.String("题材;无则空字符串")).Required(),
			schema.Property("forbidden_chars", schema.Array("禁止出现的字符", schema.String("字符"))).Required(),
			schema.Property("forbidden_phrases", schema.Array("禁止出现的短语(字面精确匹配)", schema.String("短语"))).Required(),
			schema.Property("fatigue_words", schema.Array("疲劳词及每章出现上限", schema.Object(
				schema.Property("word", schema.String("疲劳词")).Required(),
				schema.Property("max_per_chapter", schema.Int("每章出现次数上限(正整数)")).Required(),
			))).Required(),
		)).Required(),
		schema.Property("preferences", schema.String("自然语言风格/人物/审美偏好;无则空字符串")).Required(),
		schema.Property("uncertain", schema.Array("故意未提升到 structured 的项+原因", schema.String("条目"))).Required(),
	),
}

// Normalizer normalizes the natural-language rules of a single source into a rules.Candidate.
type Normalizer struct {
	model agentcore.ChatModel
}

// NewNormalizer builds the normalizer from one ChatModel. Normalization is a one-shot startup tool,
// so it should be given a model with stronger capabilities (such as the default model of ModelSet); it does not need to follow the weaker model used for writing.
//
// Normalization does not override thinking: an explicit off is itself a reasoning parameter that only some models support,
// and ordinary chat models reject it. The provider/model default is kept, while normalizeMaxTokens
// reserves the output budget for models that cannot turn thinking off.
func NewNormalizer(model agentcore.ChatModel) *Normalizer {
	return &Normalizer{model: model}
}

// Normalize normalizes one source. On failure it returns an error (carrying the real reason), and the caller decides the degradation
// (Service.normalizeOrDegrade records a degraded candidate) — technical errors are no longer disguised as normal results,
// and terminal errors (auth/permission etc.) are not retried.
func (n *Normalizer) Normalize(ctx context.Context, source, text string) (rules.Candidate, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return rules.Candidate{Source: source}, nil
	}
	if n == nil || n.model == nil {
		return rules.Candidate{}, fmt.Errorf("归一化模型未配置")
	}

	out, err := llmcontract.Execute(ctx, n.model, llmcontract.Request[normalizerOutput]{
		Contract:     normalizeContract,
		SystemPrompt: normalizerSystemPrompt,
		Payload:      text,
		Options:      []agentcore.CallOption{agentcore.WithMaxTokens(normalizeMaxTokens)},
		Validate: func(out *normalizerOutput) error {
			_, err := out.toCandidate(source)
			return err
		},
		Agent: "rules",
		Hooks: llmcontract.Hooks{
			Resolved: func(res llmcontract.Resolution) {
				slog.Debug("规则归一化协议选择", "module", "rules", "source", source,
					"contract", normalizeContract.Name, "structured_mode", res.Mode,
					"capability_source", res.Source, "provider", res.Provider, "model", res.Model,
					"schema_fingerprint", normalizeContract.Fingerprint())
			},
			Correction: func(ev llmcontract.Correction) {
				slog.Warn("规则归一化输出自愈", "module", "rules", "source", source,
					"attempt", ev.Attempt, "layer", ev.Layer, "structured_mode", ev.Mode, "err", ev.Err)
			},
		},
	})
	if err != nil {
		return rules.Candidate{}, fmt.Errorf("归一化失败: %w", err)
	}
	return out.toCandidate(source)
}

// degraded builds a degraded candidate: when normalization fails the raw text is taken as a style preference and no mechanical rule is extracted.
// uncertain tags the source (to make it easy to echo back "which sources could not be parsed") but carries no technical error detail — technical errors only go to the log.
func degraded(source, text string) rules.Candidate {
	return rules.Candidate{
		Source:      source,
		Preferences: text,
		Uncertain:   []string{source + "：归一化失败，已按原文作为风格偏好处理（未提炼机械规则）"},
		Degraded:    true,
	}
}

// normalizerOutput is the boundary DTO agreed by the normalizer (shared by both modes): uncertain is a fixed
// array of strings and fatigue_words a fixed array of objects — the shape is pinned by the contract, so no more shape guessing.
type normalizerOutput struct {
	Structured  normalizerStructured `json:"structured"`
	Preferences string               `json:"preferences"`
	Uncertain   []string             `json:"uncertain"`
}

type normalizerStructured struct {
	Genre            string             `json:"genre"`
	ForbiddenChars   []string           `json:"forbidden_chars"`
	ForbiddenPhrases []string           `json:"forbidden_phrases"`
	FatigueWords     []fatigueWordEntry `json:"fatigue_words"`
}

type fatigueWordEntry struct {
	Word          string `json:"word"`
	MaxPerChapter int    `json:"max_per_chapter"`
}

// toCandidate validates the boundary DTO and converts it into a domain candidate: a fatigue entry must have a non-empty word and a positive-integer cap
// (validation errors can be fed back to the model to fix), and on the domain side it is still map[string]int.
func (o normalizerOutput) toCandidate(source string) (rules.Candidate, error) {
	var fatigue map[string]int
	for _, e := range o.Structured.FatigueWords {
		word := strings.TrimSpace(e.Word)
		if word == "" {
			return rules.Candidate{}, fmt.Errorf("fatigue_words 含空词条目")
		}
		if e.MaxPerChapter < 1 {
			return rules.Candidate{}, fmt.Errorf("fatigue_words[%q].max_per_chapter 必须是正整数, got %d", word, e.MaxPerChapter)
		}
		if fatigue == nil {
			fatigue = make(map[string]int, len(o.Structured.FatigueWords))
		}
		fatigue[word] = e.MaxPerChapter
	}
	return rules.Candidate{
		Source: source,
		Structured: rules.Structured{
			Genre:            strings.TrimSpace(o.Structured.Genre),
			ForbiddenChars:   nonEmpty(o.Structured.ForbiddenChars),
			ForbiddenPhrases: nonEmpty(o.Structured.ForbiddenPhrases),
			FatigueWords:     fatigue,
		},
		Preferences: strings.TrimSpace(o.Preferences),
		Uncertain:   nonEmpty(o.Uncertain),
	}, nil
}

func nonEmpty(in []string) []string {
	var out []string
	for _, s := range in {
		if t := strings.TrimSpace(s); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// normalizerSystemPrompt only describes the normalization semantics, the output structure is maintained in one place by normalizeContract.
// It has been validated with 10 real examples (including the threshold-invention trap) to confirm that conservative promotion holds (10/10).
const normalizerSystemPrompt = `你是 AI 小说写作系统的「规则归一化器」。你读取用户某一个来源的长期写作规则（自然语言），把明确且可机械检查的规则提升到 structured，其余内容归入 preferences 或 uncertain。

【保守提升——最重要】
- 只有用户明确、无歧义时才写入 structured。
- forbidden_chars/forbidden_phrases 是 error 级:只有「不要出现X/禁用X/别写X」这类明确禁止才提升。
- fatigue_words:只有同时给出「明确的词」和「明确的次数阈值」才提升;「少用X/别老用X」没给数字的放进 preferences,绝不自己发明阈值。
- 字数/篇幅类意愿(「每章3000字」「短一点」)一律放 preferences:章节长度是叙事节奏问题,由创作时自然把握,不做机械检查。
- 不可机械检查、无明确阈值、依赖语境的,一律放 preferences。
- 原则:宁可漏进 structured,也不要错误提升(那会每章误报)。

preferences 用一段可读的自然语言保留风格、人物与审美偏好。
uncertain 说明你故意没有提升到 structured 的项目及原因。`
