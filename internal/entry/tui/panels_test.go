package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/CTKiet2006/kietnovel/internal/host"
	buildversion "github.com/CTKiet2006/kietnovel/internal/version"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

func TestRenderTopBarShowsVersion(t *testing.T) {
	out := renderTopBar(host.UISnapshot{
		Provider:  "openrouter",
		ModelName: "test-model",
		BookTitle: "测试小说",
	}, 120, "", "v1.2.3")
	if !strings.Contains(out, buildversion.AppName+" v1.2.3") {
		t.Fatalf("Top bar thiếu version: %q", out)
	}
}

func TestRenderDetailContentShowsSynopsis(t *testing.T) {
	out := ansi.Strip(renderDetailContent(host.UISnapshot{Synopsis: "少年在永夜中寻找黎明。"}, 40))
	if !strings.Contains(out, "Tóm tắt") || !strings.Contains(out, "少年在永夜中寻找黎明。") {
		t.Fatalf("Panel chi tiết thiếu tóm tắt: %q", out)
	}
}

func TestSameDetailSnapshotDetectsOutlineStateChanges(t *testing.T) {
	base := host.UISnapshot{Outline: []host.OutlineSnapshot{{Chapter: 1, Title: "第一章"}}}
	if !sameDetailSnapshot(base, base) {
		t.Fatal("Chi tiết giống nhau không được kích rebuild")
	}
	changed := base
	changed.InProgressChapter = 1
	if sameDetailSnapshot(base, changed) {
		t.Fatal("Đổi trạng thái chương bắt buộc rebuild chi tiết")
	}
}

func TestRenderErrorEventKeepsOneLineSummary(t *testing.T) {
	out := ansi.Strip(renderEventLine(host.Event{
		Time:     time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC),
		Category: "ERROR",
		Summary:  "commit_chapter 参数错误：" + strings.Repeat("秦越在材料中发现线索", 20),
	}, 60, 0))
	if strings.Contains(out, "\n") {
		t.Fatalf("Sự kiện ERROR phải giữ tóm tắt một dòng, được %q", out)
	}
	if !strings.HasSuffix(out, "...") {
		t.Fatalf("Tóm tắt ERROR quá rộng phải cắt ở TUI, được %q", out)
	}
}

func TestRenderRunningModelShowsStateAndElapsed(t *testing.T) {
	out := ansi.Strip(renderEventLine(host.Event{
		ID:       "model-1",
		Time:     time.Now().Add(-65 * time.Second),
		Category: "MODEL",
		Summary:  "Đang suy nghĩ",
		Depth:    1,
	}, 60, 0))
	if !strings.Contains(out, "Đang suy nghĩ") || !strings.Contains(out, "(1m5s)") {
		t.Fatalf("Model đang chạy phải hiện trạng thái và thời gian đã trôi, được %q", out)
	}
}

// TestRenderStatusBar giữ hợp đồng thông tin của thanh trạng thái đáy: danh tính model (cửa sổ+thinking), token phiên,
// chi phí/ngân sách, thư mục sách đều phải có (lột style rồi assert theo text thuần).
func TestRenderStatusBar(t *testing.T) {
	out := ansi.Strip(renderStatusBar(host.UISnapshot{
		Provider:           "openrouter",
		ModelName:          "test-model",
		ModelContextWindow: 200000,
		ThinkingLevel:      "medium",
		TotalInputTokens:   1_234_000,
		TotalOutputTokens:  89_300,
		TotalCostUSD:       0.31,
		BudgetLimitUSD:     5,
		TotalSavedUSD:      0.12,
	}, "/tmp/output", 120))
	for _, want := range []string{"test-model(200K,med)", "↑1.2M", "↓89.3k", "$0.31/$5.00", "tiết kiệm $0.12", "./output"} {
		if !strings.Contains(out, want) {
			t.Fatalf("Thanh trạng thái thiếu %q: %q", want, out)
		}
	}
}

func TestRenderStatusBarAutoThinkingAndEmpty(t *testing.T) {
	out := ansi.Strip(renderStatusBar(host.UISnapshot{
		ModelName:          "test-model",
		ModelContextWindow: 128000,
	}, "", 120))
	if !strings.Contains(out, "test-model(128K,auto)") {
		t.Fatalf("Thiếu chú thích auto của mức thinking: %q", out)
	}
	if out := ansi.Strip(renderStatusBar(host.UISnapshot{}, "", 120)); out != "SẴN SÀNG" {
		t.Fatalf("Snapshot rỗng phải lui về SẴN SÀNG, được %q", out)
	}
}

func TestRenderUsageLineSeparatesFullWidthNameAndTokens(t *testing.T) {
	out := renderUsageLine("gpt-5.6-sol", bodyTextColor, 5300, 0, 0.23, 32)
	if !strings.Contains(out, "gpt-5.6-sol 5.3k") {
		t.Fatalf("Tên model và token phải có khoảng cách nhìn thấy được: %q", out)
	}
}

func TestTruncateByDisplayWidth(t *testing.T) {
	// Chữ Hán thuần cắt theo rộng hiển thị: ngân sách 10 cột = 3 chữ Hán (6 cột) + "..." (3 cột), cắt theo rune sẽ tràn tới 17 cột
	got := truncate("临港市公共算法伦理审计员", 10)
	if w := lipgloss.Width(got); w > 10 {
		t.Errorf("truncate tràn rộng cột: %d > 10 (%q)", w, got)
	}
	if !strings.HasSuffix(got, "...") {
		t.Errorf("Cắt quá rộng phải kèm dấu ba chấm: %q", got)
	}
	// Hành vi ASCII giữ như cài đặt cũ
	if got := truncate("abcdef", 6); got != "abcdef" {
		t.Errorf("Chưa quá rộng thì không cắt: %q", got)
	}
	if got := truncate("abcdefgh", 6); got != "abc..." {
		t.Errorf("Cắt ASCII: got %q want %q", got, "abc...")
	}
}

func TestRenderDetailContentWrapsCJK(t *testing.T) {
	long := "沈砚（主角；临港市公共算法伦理审计员，台风夜事故的调查负责人，坚持程序正义）"
	const contentW = 40
	out := renderDetailContent(host.UISnapshot{
		Characters:       []string{long},
		SupportingCount:  1,
		RecentSupporting: []string{long},
		RecentSummaries:  []string{"第6章：" + long},
	}, contentW)
	for line := range strings.SplitSeq(out, "\n") {
		if w := lipgloss.Width(line); w > contentW {
			t.Errorf("Dòng tràn rộng panel: %d > %d (%q)", w, contentW, line)
		}
	}
	// Mô tả dài phải gập thành nhiều dòng (dòng nối thụt treo), chứ không cắt mất thông tin
	joined := strings.ReplaceAll(strings.ReplaceAll(out, "\n", ""), " ", "")
	if !strings.Contains(joined, "坚持程序正义") {
		t.Errorf("Gập dòng xong phải giữ mô tả đầy đủ, output thực tế:\n%s", out)
	}
}
