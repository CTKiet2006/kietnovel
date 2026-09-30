package host

import (
	"testing"

	"github.com/voocel/agentcore"
)

// TestRecordSidecarKhongTriggerAbort — invariant cốt lõi của /sp.
//
// UsageTracker → onCost → BudgetSentinel.stop → abortWithEvent: một câu hỏi phụ
// mà đẩy tổng vượt trần rồi dừng cả máy đang viết là sai nguyên tắc. RecordSidecar
// phải ghi nhận tiền (perAgent + perModel + persist) mà KHÔNG gọi onCost.
func TestRecordSidecarKhongTriggerAbort(t *testing.T) {
	tr := NewUsageTracker(nil, nil)
	var onCostCalls []float64
	tr.SetOnCost(func(total float64) { onCostCalls = append(onCostCalls, total) })

	// Giả lập Engine sắp chạm trần: một Record thường với cost lớn gọi onCost.
	tr.Record("writer", "draft", agentcore.Message{
		Role:    agentcore.RoleAssistant,
		Content: []agentcore.ContentBlock{{Type: agentcore.ContentText, Text: "x"}},
		Usage:   &agentcore.Usage{Input: 1000, Output: 500},
	})
	if len(onCostCalls) == 0 {
		t.Fatal("Record thường phải gọi onCost (tiền đề của test sai)")
	}
	base := len(onCostCalls)

	// Advisor đốt thêm — dù cost lớn tới đâu cũng không được gọi onCost thêm.
	tr.RecordSidecar("advisor", agentcore.Usage{Input: 100000, Output: 50000}, "p", "m")
	if len(onCostCalls) != base {
		t.Fatalf("RecordSidecar gọi onCost %d lần — Engine có thể bị abort vì /sp", len(onCostCalls)-base)
	}
}

// TestRecordSidecarVanGhiNhanTien — tách abort không có nghĩa mất dấu vết.
// Tiền thật đã đốt thì perAgent["advisor"] phải có.
func TestRecordSidecarVanGhiNhanTien(t *testing.T) {
	tr := NewUsageTracker(nil, nil)
	tr.RecordSidecar("advisor", agentcore.Usage{Input: 1000, Output: 200}, "p", "m")

	tr.mu.Lock()
	per := tr.perAgent["advisor"]
	tr.mu.Unlock()
	if per == nil {
		t.Fatal("perAgent[advisor] rỗng — tiền /sp mất dấu")
	}
	if per.Input != 1000 || per.Output != 200 {
		t.Errorf("perAgent[advisor] = %+v, sai số", per)
	}
}

// TestRecordSidecarNilAnToan — tracker nil không được panic (đường audit gọi).
func TestRecordSidecarNilAnToan(t *testing.T) {
	var tr *UsageTracker
	tr.RecordSidecar("advisor", agentcore.Usage{Input: 1}, "", "")
}
