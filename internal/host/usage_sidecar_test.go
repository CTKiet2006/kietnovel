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
	tr.RecordSidecar("advisor", "sp:9", agentcore.Usage{Input: 100000, Output: 50000}, "p", "m")
	if len(onCostCalls) != base {
		t.Fatalf("RecordSidecar gọi onCost %d lần — Engine có thể bị abort vì /sp", len(onCostCalls)-base)
	}
}

// TestRecordSidecarVanGhiNhanTien — tách abort không có nghĩa mất dấu vết.
// Tiền thật đã đốt thì perAgent["advisor"] phải có.
func TestRecordSidecarVanGhiNhanTien(t *testing.T) {
	tr := NewUsageTracker(nil, nil)
	tr.RecordSidecar("advisor", "sp:9", agentcore.Usage{Input: 1000, Output: 200}, "p", "m")

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

// TestRecordSidecarKhongBanOverallCacheBreaks — isolation phải trọn vẹn:
// sidecar gây đứt cache (hit giảm mạnh, prefix không co) thì perAgent advisor
// tăng, nhưng overall.CacheBreaks đứng yên. Test cũ chỉ assert Totals/onCost.
func TestRecordSidecarKhongBanOverallCacheBreaks(t *testing.T) {
	tr := NewUsageTracker(nil, nil)
	// Lần 1 đặt baseline: prefix 10000, hit 9000. Cùng task = cùng lineage nên
	// lần 2 so với baseline lần 1.
	tr.RecordSidecar("advisor", "sp:7", agentcore.Usage{Input: 10000, Output: 10, CacheRead: 9000}, "p", "m")
	// Lần 2: prefix không co (10000), hit rơi về 0 → đủ điều kiện break
	// (>5% và >=2000 tokens).
	tr.RecordSidecar("advisor", "sp:7", agentcore.Usage{Input: 10000, Output: 10, CacheRead: 0}, "p", "m")

	tr.mu.Lock()
	overallBreaks := tr.overall.CacheBreaks
	per := tr.perAgent["advisor"]
	tr.mu.Unlock()
	if overallBreaks != 0 {
		t.Errorf("overall.CacheBreaks = %d — sidecar làm bẩn số liệu Engine", overallBreaks)
	}
	if per == nil || per.CacheBreaks != 1 {
		t.Errorf("perAgent[advisor].CacheBreaks phải = 1, được %+v", per)
	}
}

// TestRecordThuongVanTangOverallCacheBreaks — đối chứng: đường thường vẫn phải
// tăng overall như cũ, refactor tách sidecar không được làm mất hành vi này.
func TestRecordThuongVanTangOverallCacheBreaks(t *testing.T) {
	tr := NewUsageTracker(nil, nil)
	msg := func(in, read int) agentcore.Message {
		return agentcore.Message{
			Role:    agentcore.RoleAssistant,
			Content: []agentcore.ContentBlock{{Type: agentcore.ContentText, Text: "x"}},
			Usage:   &agentcore.Usage{Input: in, Output: 10, CacheRead: read},
		}
	}
	tr.Record("writer", "draft", msg(10000, 9000))
	tr.Record("writer", "draft", msg(10000, 0))
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if tr.overall.CacheBreaks != 1 {
		t.Errorf("overall.CacheBreaks = %d, mong 1", tr.overall.CacheBreaks)
	}
}
func TestRecordSidecarNilAnToan(t *testing.T) {
	var tr *UsageTracker
	tr.RecordSidecar("advisor", "sp:9", agentcore.Usage{Input: 1}, "", "")
}

// TestSidecarLineageMoiMoiRequest — mỗi /sp request là một prompt lineage mới
// (snapshot/question khác nhau, model có thể đổi). Hai request khác task thì lần
// 2 chỉ đặt baseline mới, KHÔNG so với baseline của request trước — nếu không,
// detector báo "đứt cache" oan cho một lineage hoàn toàn mới.
func TestSidecarLineageMoiMoiRequest(t *testing.T) {
	tr := NewUsageTracker(nil, nil)
	tr.RecordSidecar("advisor", "sp:1", agentcore.Usage{Input: 10000, Output: 10, CacheRead: 9000}, "p", "m")
	tr.RecordSidecar("advisor", "sp:2", agentcore.Usage{Input: 10000, Output: 10, CacheRead: 0}, "p", "m")

	tr.mu.Lock()
	defer tr.mu.Unlock()
	if tr.overall.CacheBreaks != 0 {
		t.Errorf("overall.CacheBreaks = %d", tr.overall.CacheBreaks)
	}
	if per := tr.perAgent["advisor"]; per == nil || per.CacheBreaks != 0 {
		t.Errorf("lineage khác nhau không được tính break: %+v", per)
	}
}
