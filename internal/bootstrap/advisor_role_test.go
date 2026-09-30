package bootstrap

import "testing"

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

// F. Fallback tường minh: advisor primary hỏng lúc gọi thì failoverModel chuyển
// sang fallback và báo reporter. Ở đây chỉ kiểm tra cấu hình fallback được dựng
// (gọi thật cần mạng, không làm trong unit test).
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
