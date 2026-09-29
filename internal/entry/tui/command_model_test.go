package tui

import (
	"testing"

	"github.com/voocel/agentcore"
	"github.com/voocel/ainovel-cli/internal/host"
)

type fakeModelRuntime struct {
	providers   []string
	models      map[string][]host.ConfiguredModel
	curProvider string
	curModel    string
	thinking    map[string]string // role -> ý định gốc đã lưu
	available   []agentcore.ThinkingLevel
	setCalls    []struct{ role, level string }
	switchCalls int
}

func (f *fakeModelRuntime) ConfiguredProviders() []string { return f.providers }
func (f *fakeModelRuntime) ConfiguredModelOptions(provider string) []host.ConfiguredModel {
	return f.models[provider]
}
func (f *fakeModelRuntime) CurrentModelSelection(role string) (string, string, bool) {
	return f.curProvider, f.curModel, true
}
func (f *fakeModelRuntime) AvailableThinking(role string) []agentcore.ThinkingLevel {
	return f.available
}
func (f *fakeModelRuntime) CurrentThinking(role string) string { return f.thinking[role] }
func (f *fakeModelRuntime) SwitchModel(role, provider, model string) error {
	f.switchCalls++
	f.curProvider, f.curModel = provider, model
	return nil
}
func (f *fakeModelRuntime) SetRoleThinking(role, level string) error {
	f.setCalls = append(f.setCalls, struct{ role, level string }{role, level})
	if f.thinking == nil {
		f.thinking = map[string]string{}
	}
	f.thinking[role] = level
	return nil
}

// Ý định mức suy luận đã lưu cao hơn khả năng model hiện tại, panel không hiển thị được, user giữ nguyên trường mức suy luận rồi áp dụng,
// không được xóa nhầm ý định thành giá trị mặc định ban đầu.
func TestModelSwitchKeepsUnrepresentableThinkingIntent(t *testing.T) {
	rt := &fakeModelRuntime{
		providers:   []string{"proxy"},
		models:      map[string][]host.ConfiguredModel{"proxy": {{Name: "chat-only"}}},
		curProvider: "proxy", curModel: "chat-only",
		thinking:  map[string]string{"writer": "high"},
		available: nil, // Model hiện tại chỉ có một nấc "kế thừa"
	}
	st := newModelSwitchState(rt, "writer")
	if st.thinkingKey() != "" {
		t.Fatalf("high không hiển thị được thì panel phải rơi vào nấc kế thừa, được %q", st.thinkingKey())
	}
	if err := st.apply(rt); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(rt.setCalls) != 0 {
		t.Fatalf("Chưa đổi mức suy luận thì không ghi lại: %+v", rt.setCalls)
	}
	if rt.thinking["writer"] != "high" {
		t.Fatalf("Ý định bị xóa thành %q, phải giữ high", rt.thinking["writer"])
	}
}

// User đổi tường minh mức suy luận trong panel thì phải ghi lại giá trị mới.
func TestModelSwitchAppliesExplicitThinkingChange(t *testing.T) {
	rt := &fakeModelRuntime{
		providers:   []string{"proxy"},
		models:      map[string][]host.ConfiguredModel{"proxy": {{Name: "m"}}},
		curProvider: "proxy", curModel: "m",
		thinking:  map[string]string{"writer": ""},
		available: []agentcore.ThinkingLevel{"low", "high"},
	}
	st := newModelSwitchState(rt, "writer")
	st.focus = modelFocusThinking
	st.cycle(1, rt) // Di chuyển trường mức suy luận
	want := st.thinkingKey()
	if want == "" {
		t.Fatal("Điều kiện test: phải đã chuyển tới nấc suy luận không rỗng nào đó")
	}
	if err := st.apply(rt); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(rt.setCalls) != 1 || rt.setCalls[0].level != want {
		t.Fatalf("Đổi tường minh phải ghi lại %q, được %+v", want, rt.setCalls)
	}
}
