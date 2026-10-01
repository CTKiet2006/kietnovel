package bootstrap

import (
	"context"
	"errors"
	"testing"
)

// TestLastTargetGhiFallback — identity audit khi failover:
// primary hỏng (lỗi retryable 429), fallback chạy, Usage không có identity →
// LastTarget phải là fallback, không phải primary.
func TestLastTargetGhiFallback(t *testing.T) {
	primary := &failoverFake{err: errors.New("429 too many requests")}
	fallback := &failoverFake{answer: "fallback-ok"}

	ms, err := NewModelSet(advisorTestConfig(true))
	if err != nil {
		t.Fatalf("NewModelSet: %v", err)
	}
	ms.models["advisor"] = NewSwappableModel("openrouter", "advisor-model", primary, nil)
	ms.fallbacks["advisor"] = []modelTarget{{provider: "openrouter", name: "fallback-model", model: fallback}}

	got := ms.ForRoleWithFailover("advisor", nil)
	fm, ok := got.(*failoverModel)
	if !ok {
		t.Fatalf("kiểu trả về %T", got)
	}
	resp, err := fm.Generate(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("fallback phải chạy được: %v", err)
	}
	if resp.Message.TextContent() != "fallback-ok" {
		t.Errorf("answer sai: %q", resp.Message.TextContent())
	}
	if p, n := fm.LastTarget(); p != "openrouter" || n != "fallback-model" {
		t.Errorf("LastTarget = %q/%q, mong openrouter/fallback-model", p, n)
	}
}

// TestLastTargetRongKhiChuaChay — chưa attempt nào thì rỗng, caller giữ identity
// cũ chứ không đoán.
func TestLastTargetRongKhiChuaChay(t *testing.T) {
	ms, err := NewModelSet(advisorTestConfig(true))
	if err != nil {
		t.Fatalf("NewModelSet: %v", err)
	}
	ms.models["advisor"] = NewSwappableModel("openrouter", "advisor-model", &failoverFake{answer: "x"}, nil)
	ms.fallbacks["advisor"] = []modelTarget{{provider: "openrouter", name: "fallback-model", model: &failoverFake{answer: "y"}}}
	got := ms.ForRoleWithFailover("advisor", nil)
	fm, ok := got.(*failoverModel)
	if !ok {
		t.Fatalf("kiểu trả về %T", got)
	}
	if p, n := fm.LastTarget(); p != "" || n != "" {
		t.Errorf("LastTarget phải rỗng khi chưa chạy, được %q/%q", p, n)
	}
}

// TestLastTargetGhiPrimaryKhiKhongFallback — primary chạy tốt thì LastTarget là
// primary. Phân biệt với trường hợp rỗng ở trên.
func TestLastTargetGhiPrimaryKhiKhongFallback(t *testing.T) {
	ms, err := NewModelSet(advisorTestConfig(true))
	if err != nil {
		t.Fatalf("NewModelSet: %v", err)
	}
	ms.models["advisor"] = NewSwappableModel("openrouter", "advisor-model", &failoverFake{answer: "x"}, nil)
	ms.fallbacks["advisor"] = []modelTarget{{provider: "openrouter", name: "fallback-model", model: &failoverFake{answer: "y"}}}
	got := ms.ForRoleWithFailover("advisor", nil)
	fm, ok := got.(*failoverModel)
	if !ok {
		t.Fatalf("kiểu trả về %T", got)
	}
	if _, err := fm.Generate(context.Background(), nil, nil); err != nil {
		t.Fatal(err)
	}
	if p, n := fm.LastTarget(); p != "openrouter" || n != "advisor-model" {
		t.Errorf("LastTarget = %q/%q, mong openrouter/advisor-model", p, n)
	}
}
