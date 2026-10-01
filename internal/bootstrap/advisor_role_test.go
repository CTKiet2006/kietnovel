package bootstrap

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/voocel/agentcore"
)

func advisorTestConfig(withAdvisor bool) Config {
	cfg := Config{
		Provider: "openrouter", ModelName: "default-model",
		Providers: map[string]ProviderConfig{
			"openrouter": {APIKey: "test-key", BaseURL: "https://openrouter.ai/api/v1"},
		},
	}
	if withAdvisor {
		cfg.Roles = map[string]RoleConfig{
			"advisor": {Provider: "openrouter", Model: "advisor-model"},
		}
	}
	return cfg
}

// TestRoleKeyChuanHoa — P1: "Writer"/"WRITER"/"ADVISOR" trong file cấu hình phải
// resolve đúng model khi lookup "writer"/"advisor". Trước fix, ValidateBase cho
// qua nhưng NewModelSet giữ key thô → lookup trượt silent về default model.
func TestRoleKeyChuanHoa(t *testing.T) {
	cfg := advisorTestConfig(false)
	cfg.Roles = map[string]RoleConfig{
		"Writer":  {Provider: "openrouter", Model: "writer-model"},
		"ADVISOR": {Provider: "openrouter", Model: "advisor-model"},
	}
	ms, err := NewModelSet(cfg)
	if err != nil {
		t.Fatalf("NewModelSet: %v", err)
	}
	for _, role := range []string{"writer", "Writer", "WRITER", " writer "} {
		got := ms.ForRole(role)
		sw, ok := got.(*SwappableModel)
		if !ok {
			t.Fatalf("ForRole(%q) kiểu %T", role, got)
		}
		if _, m := sw.Current(); m != "writer-model" {
			t.Errorf("ForRole(%q) ra model %q, mong writer-model (rơi default silent)", role, m)
		}
	}
	got := ms.ForRoleWithFailover("ADVISOR", nil)
	sw, ok := got.(*SwappableModel)
	if !ok {
		t.Fatalf("kiểu trả về %T", got)
	}
	if _, m := sw.Current(); m != "advisor-model" {
		t.Errorf("lookup ADVISOR ra %q, mong advisor-model", m)
	}
	if p, n, explicit := ms.CurrentSelection("Writer"); !explicit || n != "writer-model" {
		t.Errorf("CurrentSelection(Writer) = %q/%q explicit=%v", p, n, explicit)
	}
}

// TestResolveRoleTargetNguyenTu — object + metadata đọc cùng một snapshot.
func TestResolveRoleTargetNguyenTu(t *testing.T) {
	ms, err := NewModelSet(advisorTestConfig(true))
	if err != nil {
		t.Fatalf("NewModelSet: %v", err)
	}
	tgt := ms.ResolveRoleTarget("advisor", nil)
	if !tgt.Explicit || tgt.Provider != "openrouter" || tgt.Name != "advisor-model" {
		t.Errorf("target sai: %+v", tgt)
	}
	if tgt.Model == nil {
		t.Error("target.Model nil")
	}
	tgt2 := ms.ResolveRoleTarget("khong-co-role-nay", nil)
	if tgt2.Explicit {
		t.Error("role lạ phải Explicit=false (fallback default)")
	}
}

// E. Có advisor role → ForRoleWithFailover trả đúng model advisor, không phải default.
func TestAdvisorRoleDungModelRieng(t *testing.T) {
	ms, err := NewModelSet(advisorTestConfig(true))
	if err != nil {
		t.Fatalf("NewModelSet: %v", err)
	}
	got := ms.ForRoleWithFailover("advisor", nil)
	sw, ok := got.(*SwappableModel)
	if !ok {
		t.Fatalf("kiểu trả về %T, mong *SwappableModel", got)
	}
	if p, m := sw.Current(); p != "openrouter" || m != "advisor-model" {
		t.Errorf("advisor resolve thành %q/%q, mong openrouter/advisor-model", p, m)
	}
}

// E2. Không có advisor role → fallback default model, không lỗi.
func TestAdvisorRoleVangFallbackDefault(t *testing.T) {
	ms, err := NewModelSet(advisorTestConfig(false))
	if err != nil {
		t.Fatalf("NewModelSet: %v", err)
	}
	got := ms.ForRoleWithFailover("advisor", nil)
	sw, ok := got.(*SwappableModel)
	if !ok {
		t.Fatalf("kiểu trả về %T", got)
	}
	if p, m := sw.Current(); p != "openrouter" || m != "default-model" {
		t.Errorf("fallback sai: %q/%q, mong openrouter/default-model", p, m)
	}
}

// TestFailoverKhongThuKhiDaCancel — cancel đi trước fallback.
//
// Lỗi provider có thể bọc context.Canceled dưới dạng string mà errors.Is không
// bắt được ("provider client: context canceled"). Không có guard ctx.Err() thì
// failover thử fallback cho một request đã chết — đốt thêm tiền oan.
func TestFailoverKhongThuKhiDaCancel(t *testing.T) {
	var fallbackCalls int32
	primary := &failoverFake{err: errors.New("provider client: context canceled")}
	fallback := &failoverFake{answer: "fallback-ok", calls: &fallbackCalls}

	ms, err := NewModelSet(advisorTestConfig(true))
	if err != nil {
		t.Fatalf("NewModelSet: %v", err)
	}
	// Thay primary/fallback bằng fake: cùng package nên gán trực tiếp.
	ms.models["advisor"] = NewSwappableModel("openrouter", "advisor-model", primary, nil)
	ms.fallbacks["advisor"] = []modelTarget{{provider: "openrouter", name: "fallback-model", model: fallback}}

	got := ms.ForRoleWithFailover("advisor", nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // request đã chết trước khi gọi
	_, err = got.Generate(ctx, nil, nil)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("phải context.Canceled, được %v", err)
	}
	if atomic.LoadInt32(&fallbackCalls) != 0 {
		t.Errorf("fallback bị gọi %d lần cho request đã chết", fallbackCalls)
	}
}

type failoverFake struct {
	answer string
	err    error
	calls  *int32
}

func (m *failoverFake) Generate(_ context.Context, _ []agentcore.Message, _ []agentcore.ToolSpec, _ ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	if m.calls != nil {
		atomic.AddInt32(m.calls, 1)
	}
	if m.err != nil {
		return nil, m.err
	}
	return &agentcore.LLMResponse{Message: agentcore.Message{
		Role:    agentcore.RoleAssistant,
		Content: []agentcore.ContentBlock{{Type: agentcore.ContentText, Text: m.answer}},
	}}, nil
}

func (m *failoverFake) GenerateStream(ctx context.Context, _ []agentcore.Message, _ []agentcore.ToolSpec, _ ...agentcore.CallOption) (<-chan agentcore.StreamEvent, error) {
	ch := make(chan agentcore.StreamEvent)
	close(ch)
	return ch, ctx.Err()
}

func (m *failoverFake) SupportsTools() bool { return false }
func TestAdvisorFallbackDuocDung(t *testing.T) {
	cfg := advisorTestConfig(true)
	cfg.Roles["advisor"] = RoleConfig{
		Provider: "openrouter", Model: "advisor-model",
		Fallbacks: []ModelRef{{Provider: "openrouter", Model: "fallback-model"}},
	}
	ms, err := NewModelSet(cfg)
	if err != nil {
		t.Fatalf("NewModelSet: %v", err)
	}
	var reported []FailoverEvent
	got := ms.ForRoleWithFailover("advisor", func(ev FailoverEvent) {
		reported = append(reported, ev)
	})
	// Có fallback tường minh thì trả failoverModel bọc primary.
	// Cùng package nên đọc currentTarget trực tiếp để kiểm tra primary.
	fm, ok := got.(*failoverModel)
	if !ok {
		t.Fatalf("kiểu trả về %T, mong *failoverModel khi có fallback", got)
	}
	cur := fm.currentTarget()
	if cur.provider != "openrouter" || cur.name != "advisor-model" {
		t.Errorf("primary sai: %q/%q", cur.provider, cur.name)
	}
}
