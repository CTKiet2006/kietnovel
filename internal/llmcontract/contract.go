// Package llmcontract is the unified contract and execution layer for direct structured returns: the static Contract
// is the single source of truth for the structure, and Execute uniformly handles capability selection, prompt preparation, request retry,
// Schema/DTO decoding and feedback self-healing.
// The protocol is decided before the request is sent; a rejected native request or a contract violation is surfaced as is, silently dropping the schema and resending is forbidden.
package llmcontract

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/voocel/agentcore"
	"github.com/voocel/agentcore/llm"
)

// Contract is the static contract for one direct structured return, defined next to the boundary DTOs.
type Contract struct {
	Name        string
	Description string
	Schema      map[string]any
}

// Mode is the structured protocol used by this call.
type Mode string

const (
	ModeNativeJSONSchema Mode = "native_json_schema"
	ModePromptContract   Mode = "prompt_contract"
)

// Source is where the capability judgement comes from.
type Source string

const (
	SourceConfig  Source = "config"  // declared explicitly by the user in ModelConfig.json_schema
	SourceAdapter Source = "adapter" // the model-level capability table of the provider adapter
	SourceUnknown Source = "unknown" // no declaration and capability unknown, so conservatively use the prompt contract
)

// Resolution is the protocol choice decided before the request is sent, used by the caller for branching and logging.
type Resolution struct {
	Mode     Mode
	Source   Source
	Strict   bool // whether to send strict in native mode
	Provider string
	Model    string
}

// jsonSchemaOverrider is implemented by the model wrapper that carries the config three-state override
// (bootstrap.SwappableModel and the wrapper layers that pass it through).
type jsonSchemaOverrider interface {
	JSONSchemaOverride() *bool
}

type modelInfoProvider interface {
	Info() llm.ModelInfo
}

// ModelFacts is a single-instant snapshot of what one capability resolution needs. The hot-swap wrapper implements this interface
// so Resolve does not mix in the state between two switches while reading capabilities, config overrides and model identity separately.
type ModelFacts struct {
	Capabilities       llm.Capabilities
	Info               llm.ModelInfo
	JSONSchemaOverride *bool
}

type modelFactsProvider interface {
	StructuredOutputFacts() ModelFacts
}

// Resolve reads the current model facts afresh on every call (after a hot swap the next call uses the new values):
// the config three-state wins first, then the adapter model-level capabilities, and unknown always falls back to the prompt contract.
func Resolve(model any) Resolution {
	res := Resolution{Mode: ModePromptContract, Source: SourceUnknown}

	var caps llm.Capabilities
	var info llm.ModelInfo
	var override *bool
	if fp, ok := model.(modelFactsProvider); ok {
		facts := fp.StructuredOutputFacts()
		caps, info, override = facts.Capabilities, facts.Info, facts.JSONSchemaOverride
	} else {
		if cp, ok := model.(llm.CapabilityProvider); ok {
			caps = cp.Capabilities()
		}
		if ip, ok := model.(modelInfoProvider); ok {
			info = ip.Info()
		}
		if o, ok := model.(jsonSchemaOverrider); ok {
			override = o.JSONSchemaOverride()
		}
	}
	res.Provider, res.Model = caps.Provider, caps.Model
	if res.Provider == "" {
		res.Provider = info.Provider
	}
	if res.Model == "" {
		res.Model = info.Name
	}

	if override != nil {
		res.Source = SourceConfig
		if *override {
			res.Mode = ModeNativeJSONSchema
			// The user declaring that the endpoint follows the Structured Outputs contract means strict by default;
			// only when the adapter explicitly says strict is unsupported do we send the schema without strict.
			res.Strict = caps.Structured.Strict != llm.SupportNo
		}
		return res
	}

	switch caps.Structured.JSONSchema {
	case llm.SupportYes:
		res.Mode = ModeNativeJSONSchema
		res.Source = SourceAdapter
		res.Strict = caps.Structured.Strict == llm.SupportYes
	case llm.SupportNo:
		res.Source = SourceAdapter
	}
	return res
}

// Plan resolves the protocol and builds the call options in native mode; prompt contract mode returns nil opts.
func Plan(model any, c Contract) ([]agentcore.CallOption, Resolution) {
	res := Resolve(model)
	if res.Mode != ModeNativeJSONSchema {
		return nil, res
	}
	return []agentcore.CallOption{
		agentcore.WithJSONSchema(c.Name, c.Description, c.Schema, res.Strict),
	}, res
}

// PreparePrompt keeps just one copy of the business-semantic prompt: native mode returns it verbatim; prompt
// contract mode auto-generates the format suffix from the same Schema. Callers maintain no second template, so a field
// change can never let the prompt and response_format diverge.
func PreparePrompt(base string, c Contract, res Resolution) (string, error) {
	if res.Mode != ModePromptContract {
		return base, nil
	}
	schemaJSON, err := json.Marshal(c.Schema)
	if err != nil {
		return "", fmt.Errorf("llmcontract: marshal %s prompt schema: %w", c.Name, err)
	}
	contract := "## 输出契约\n\n" +
		"只输出一个符合下列 JSON Schema 的 JSON 对象，不要输出解释、Markdown 围栏或标签本身。\n\n" +
		"<output-json-schema>\n" + string(schemaJSON) + "\n</output-json-schema>"
	if strings.TrimSpace(base) == "" {
		return contract, nil
	}
	return strings.TrimSpace(base) + "\n\n" + contract, nil
}

// Nullable widens a schema's type into a nullable union (["<t>","null"]), used in strict
// mode for the "all fields required, optional semantics expressed with null" idiom. It returns a copy and does not modify the passed map.
func Nullable(s map[string]any) map[string]any {
	out := maps.Clone(s)
	if t, ok := out["type"].(string); ok {
		out["type"] = []string{t, "null"}
	}
	switch values := out["enum"].(type) {
	case []string:
		enum := make([]any, 0, len(values)+1)
		for _, value := range values {
			enum = append(enum, value)
		}
		out["enum"] = append(enum, nil)
	case []any:
		enum := slices.Clone(values)
		for _, value := range enum {
			if value == nil {
				return out
			}
		}
		out["enum"] = append(enum, nil)
	}
	return out
}

// ValidateStrictReady recursively checks that the schema meets the structural preconditions of the OpenAI strict subset:
// every property of every object must be listed in required (optional semantics are expressed with a null union). litellm
// runs the same check at request time and automatically adds additionalProperties:false; contract tests use this function as a
// pre-assertion (RFC §11.1), so structural problems are not left to runtime.
func ValidateStrictReady(s map[string]any) error {
	return validateStrictReady(s, "$")
}

func validateStrictReady(s map[string]any, path string) error {
	if typeIncludes(s["type"], "object") {
		props, _ := s["properties"].(map[string]any)
		required, _ := s["required"].([]string)
		for name, sub := range props {
			if !slices.Contains(required, name) {
				return fmt.Errorf("%s.%s 未列入 required(strict 要求全属性 required)", path, name)
			}
			if subMap, ok := sub.(map[string]any); ok {
				if err := validateStrictReady(subMap, path+"."+name); err != nil {
					return err
				}
			}
		}
	}
	if items, ok := s["items"].(map[string]any); ok {
		return validateStrictReady(items, path+"[]")
	}
	return nil
}

func typeIncludes(t any, want string) bool {
	switch v := t.(type) {
	case string:
		return v == want
	case []string:
		return slices.Contains(v, want)
	}
	return false
}

// Fingerprint returns the first 12 hex digits of the sha256 of the schema's canonical JSON, for log correlation;
// encoding/json sorts map keys, so the same contract is naturally stable.
func (c Contract) Fingerprint() string {
	data, err := json.Marshal(c.Schema)
	if err != nil {
		return "unmarshalable"
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])[:12]
}
