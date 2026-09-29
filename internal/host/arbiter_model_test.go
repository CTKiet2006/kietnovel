package host

import (
	"context"
	"testing"

	"github.com/voocel/agentcore"
	"github.com/voocel/agentcore/llm"
)

type plainTrackedTestModel struct{}

func (*plainTrackedTestModel) Generate(context.Context, []agentcore.Message, []agentcore.ToolSpec, ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	return &agentcore.LLMResponse{}, nil
}

func (*plainTrackedTestModel) GenerateStream(context.Context, []agentcore.Message, []agentcore.ToolSpec, ...agentcore.CallOption) (<-chan agentcore.StreamEvent, error) {
	ch := make(chan agentcore.StreamEvent)
	close(ch)
	return ch, nil
}

func (*plainTrackedTestModel) SupportsTools() bool { return true }

type capableTrackedTestModel struct {
	*plainTrackedTestModel
	caps llm.Capabilities
}

func (m *capableTrackedTestModel) Capabilities() llm.Capabilities { return m.caps }

func TestUsageTrackedModelPreservesOptionalCapabilities(t *testing.T) {
	want := llm.Capabilities{
		Provider: "openai",
		Model:    "gpt-chat",
		Thinking: llm.ThinkingCapabilities{Supported: llm.SupportNo},
	}
	inner := &capableTrackedTestModel{plainTrackedTestModel: &plainTrackedTestModel{}, caps: want}
	wrapped := newUsageTrackedModel(inner, "arbiter", func(string, string, agentcore.AgentMessage) {})
	cp, ok := wrapped.(llm.CapabilityProvider)
	if !ok {
		t.Fatal("usage wrapper dropped CapabilityProvider")
	}
	if got := cp.Capabilities(); got.Provider != want.Provider || got.Model != want.Model || got.Thinking.Supported != llm.SupportNo {
		t.Fatalf("capabilities changed through wrapper: %+v", got)
	}

	plain := newUsageTrackedModel(&plainTrackedTestModel{}, "arbiter", func(string, string, agentcore.AgentMessage) {})
	if _, ok := plain.(llm.CapabilityProvider); ok {
		t.Fatal("wrapper must not invent capabilities for an unknown model")
	}
}

type overrideCapableTestModel struct {
	*capableTrackedTestModel
	override *bool
}

func (m *overrideCapableTestModel) JSONSchemaOverride() *bool { return m.override }

// The usage wrapper must pass through the config json_schema override value; when inner carries
// none it returns nil ("not configured") and does not fabricate a capability.
func TestUsageTrackedModelForwardsJSONSchemaOverride(t *testing.T) {
	tr := true
	inner := &overrideCapableTestModel{
		capableTrackedTestModel: &capableTrackedTestModel{plainTrackedTestModel: &plainTrackedTestModel{}},
		override:                &tr,
	}
	wrapped := newUsageTrackedModel(inner, "arbiter", func(string, string, agentcore.AgentMessage) {})
	o, ok := wrapped.(interface{ JSONSchemaOverride() *bool })
	if !ok {
		t.Fatal("usage wrapper dropped JSONSchemaOverride")
	}
	if v := o.JSONSchemaOverride(); v == nil || !*v {
		t.Fatalf("override 未透传: %v", v)
	}

	capsOnly := newUsageTrackedModel(&capableTrackedTestModel{plainTrackedTestModel: &plainTrackedTestModel{}}, "arbiter", func(string, string, agentcore.AgentMessage) {})
	o, ok = capsOnly.(interface{ JSONSchemaOverride() *bool })
	if !ok {
		t.Fatal("capability wrapper should expose JSONSchemaOverride")
	}
	if v := o.JSONSchemaOverride(); v != nil {
		t.Fatalf("inner 无覆盖时应为 nil: %v", v)
	}
}
