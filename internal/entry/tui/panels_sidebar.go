package tui

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/voocel/ainovel-cli/internal/host"
)

// renderStateContent dựng nội dung thuần của sidebar trạng thái (không khung/viền), dùng cho stateVP.SetContent.
func renderStateContent(snap host.UISnapshot, contentW int) string {
	contentW = max(12, contentW)
	agents := sidebarAgents(snap.Agents)
	idleAgents := sidebarIdleAgents(snap.Agents)
	var sections []string

	if snap.RecoveryLabel != "" {
		sections = append(sections, lipgloss.NewStyle().Foreground(colorMuted).Italic(true).
			Render(truncate(snap.RecoveryLabel, contentW)))
	}

	var overview strings.Builder
	overview.WriteString(renderField("Trạng thái", snapshotRuntimeStateLabel(snap.RuntimeState)))
	overview.WriteString(renderField("Giai đoạn", snapshotPhaseLabel(snap.Phase)))
	overview.WriteString(renderField("Luồng", snapshotFlowLabel(snap.Flow)))
	if snap.AdvanceMode == "review" {
		advance := "Duyệt từng chương"
		if snap.AdvancePermitChapter > 0 {
			advance = fmt.Sprintf("Đã duyệt chương %d", snap.AdvancePermitChapter)
		}
		overview.WriteString(renderField("Chạy tiếp", advance))
	} else if snap.AdvanceMode == "auto" {
		overview.WriteString(renderField("Chạy tiếp", "Tự động"))
	}
	if snap.Layered {
		overview.WriteString(renderField("Đã xong", fmt.Sprintf("%d chương", snap.CompletedCount)))
		// Quy hoạch động phân tầng: cột phải chỉ hiện các chương đã bung của arc hiện tại,
		// "Đã dàn ý" cũng dùng cùng tiêu chí này, nếu không sẽ lẫn ước tính thô
		// EstimatedChapters của arc khung (ví dụ 92) vào, lệch với dàn ý đang thấy.
		// Giá trị progress.TotalChapters chỉ dùng nội bộ cho ContextProfile, đừng để lọt ra UI.
		if planned := len(snap.Outline); planned > 0 {
			overview.WriteString(renderField("Đã dàn ý", fmt.Sprintf("%d chương", planned)))
		}
	} else {
		switch {
		case snap.TotalChapters > 0:
			overview.WriteString(renderField("Tiến độ", fmt.Sprintf("%d / %d chương", snap.CompletedCount, snap.TotalChapters)))
		default:
			overview.WriteString(renderField("Đã xong", fmt.Sprintf("%d chương", snap.CompletedCount)))
		}
	}
	overview.WriteString(renderField("Số chữ", formatNumber(snap.TotalWordCount)))
	if label, ch := inProgressDisplay(snap); label != "" {
		overview.WriteString(renderField(label, fmt.Sprintf("Chương %d", ch)))
	}
	if headline := snapshotHeadline(snap); headline != "" {
		label := "Hiện tại"
		if !snap.IsRunning {
			label = "Chờ khôi phục"
		}
		overview.WriteString(renderHighlightField(label, truncate(headline, contentW-10)))
	}
	sections = append(sections, renderSidebarSection("Tổng quan", overview.String(), contentW))

	if len(agents) > 0 {
		var agentBody strings.Builder
		for _, agent := range agents {
			agentBody.WriteString(renderAgentLine(agent, contentW))
			agentBody.WriteString("\n")
		}
		if len(idleAgents) > 0 {
			agentBody.WriteString(lipgloss.NewStyle().Foreground(colorDim).Render("Chờ: " + truncate(strings.Join(idleAgents, " · "), max(8, contentW-2))))
			agentBody.WriteString("\n")
		}
		sections = append(sections, renderSidebarSection("Vai trò chạy", agentBody.String(), contentW))
	}

	if len(snap.PendingRewrites) > 0 {
		var rewrite strings.Builder
		rewrite.WriteString(renderHighlightField("Hàng đợi", fmt.Sprintf("%v", snap.PendingRewrites)))
		if snap.RewriteReason != "" {
			rewrite.WriteString(renderField("Lý do", truncate(snap.RewriteReason, contentW-10)))
		}
		sections = append(sections, renderSidebarSection("Làm lại", rewrite.String(), contentW))
	}

	if snap.PendingSteer != "" {
		sections = append(sections, renderSidebarSection("Can thiệp",
			renderHighlightField("Chờ xử lý", truncate(snap.PendingSteer, contentW-10)), contentW))
	}
	if snap.HasAdvanceHold {
		sections = append(sections, renderSidebarSection("Chờ duyệt",
			renderHighlightField("Đang chờ", truncate(snap.AdvanceHoldReason, contentW-10)), contentW))
	}

	if body := renderUsageSidebar(snap, contentW); body != "" {
		sections = append(sections, renderSidebarSection("Mức dùng", body, contentW))
	}

	if body := renderCacheSidebar(snap, contentW); body != "" {
		sections = append(sections, renderSidebarSection("Bộ đệm", body, contentW))
	}

	return strings.Join(sections, "\n\n")
}

func renderAgentLine(agent host.AgentSnapshot, width int) string {
	stateColor := taskStatusColor(agent.State)
	icon := lipgloss.NewStyle().Foreground(stateColor).Render(agentStateIcon(agent.State))
	badge := lipgloss.NewStyle().Foreground(stateColor).Render(agentStateLabel(agent.State))
	name := lipgloss.NewStyle().Bold(true).Foreground(bodyTextColor).Render(agentDisplayName(agent.Name))
	line := icon + " " + name + " " + badge

	taskLine := agentTaskLine(agent)
	if taskLine != "" {
		line += "\n" + lipgloss.NewStyle().Foreground(colorDim).Render("  "+truncate(taskLine, max(8, width-2)))
	}

	detail := agent.Summary
	if agent.Tool != "" {
		detail = agent.Tool
	}
	// Giữ cả nhãn cũ "待命" để tương thích dữ liệu snapshot/host cũ có thể còn gửi nhãn này.
	if agent.State == "idle" && (detail == "Chờ" || detail == "待命") {
		detail = ""
	}
	if detail != "" && detail != taskLine {
		line += "\n" + lipgloss.NewStyle().Foreground(colorMuted).Render("  "+truncate(detail, max(8, width-2)))
	}
	if ctx := agentContextLine(agent); ctx != "" {
		line += "\n" + lipgloss.NewStyle().Foreground(colorDim).Italic(true).Render("  "+truncate(ctx, max(8, width-2)))
	}
	return line
}

func renderSidebarSection(title, body string, width int) string {
	body = strings.TrimRight(body, "\n")
	if body == "" {
		return ""
	}
	lineW := max(0, width-lipgloss.Width(title)-1)
	header := panelTitleStyle.Render(title) + " " +
		lipgloss.NewStyle().Foreground(colorDim).Render(strings.Repeat("─", lineW))
	card := lipgloss.NewStyle().
		Border(lipgloss.NormalBorder(), false, false, false, true).
		BorderForeground(colorDim).
		PaddingLeft(1).
		Render(body)
	return header + "\n" + card
}

func sidebarAgents(agents []host.AgentSnapshot) []host.AgentSnapshot {
	var out []host.AgentSnapshot
	for _, agent := range agents {
		if agent.State == "idle" {
			continue
		}
		out = append(out, agent)
	}
	if len(out) == 0 {
		out = append(out, agents...)
	}
	sort.SliceStable(out, func(i, j int) bool {
		li, lj := out[i], out[j]
		if agentStateRank(li.State) != agentStateRank(lj.State) {
			return agentStateRank(li.State) < agentStateRank(lj.State)
		}
		return agentOrder(li.Name) < agentOrder(lj.Name)
	})
	return out
}

func sidebarIdleAgents(agents []host.AgentSnapshot) []string {
	var names []string
	hasActive := false
	for _, agent := range agents {
		if agent.State != "idle" {
			hasActive = true
			continue
		}
		names = append(names, agentDisplayName(agent.Name))
	}
	if !hasActive {
		return nil
	}
	sort.Strings(names)
	return names
}

// inProgressDisplay tính nhãn và số chương cho trường "đang tiến hành".
// Chọn động từ theo flow (trau chuốt/viết lại/viết); khi in_progress_chapter lệch với flow thì coi là stale:
//   - ở chế độ polishing/rewriting mà chương không nằm trong pending_rewrites → lùi về chương đầu hàng đợi
//   - trường bằng 0 thì không render
func inProgressDisplay(snap host.UISnapshot) (label string, chapter int) {
	ch := snap.InProgressChapter
	switch snap.Flow {
	case "polishing":
		if ch <= 0 || !slices.Contains(snap.PendingRewrites, ch) {
			if len(snap.PendingRewrites) == 0 {
				return "", 0
			}
			ch = snap.PendingRewrites[0]
		}
		return "Đang trau chuốt", ch
	case "rewriting":
		if ch <= 0 || !slices.Contains(snap.PendingRewrites, ch) {
			if len(snap.PendingRewrites) == 0 {
				return "", 0
			}
			ch = snap.PendingRewrites[0]
		}
		return "Đang viết lại", ch
	default:
		if ch <= 0 {
			return "", 0
		}
		return "Đang viết", ch
	}
}

func snapshotHeadline(snap host.UISnapshot) string {
	if snap.PendingSteer != "" {
		if !snap.IsRunning {
			return "Chờ khôi phục: xử lý can thiệp"
		}
		return "Chờ xử lý can thiệp"
	}
	if len(snap.PendingRewrites) > 0 {
		if !snap.IsRunning {
			return "Chờ khôi phục: xử lý làm lại"
		}
		return "Chờ xử lý làm lại"
	}
	if snap.AdvanceMode == "review" && !snap.IsRunning && snap.Phase == "writing" {
		return "Duyệt từng chương: chờ duyệt chương tiếp"
	}
	return ""
}

func snapshotPhaseLabel(phase string) string {
	switch phase {
	case "premise":
		return "Tiền đề"
	case "outline":
		return "Dàn ý"
	case "writing":
		return "Viết"
	case "complete":
		return "Hoàn thành"
	case "init":
		return "Khởi tạo"
	default:
		if phase == "" {
			return "-"
		}
		return phase
	}
}

func snapshotRuntimeStateLabel(state string) string {
	switch state {
	case "starting":
		return "Đang khởi động"
	case "running":
		return "Đang chạy"
	case "pausing":
		return "Đang tạm dừng"
	case "paused":
		return "Đã tạm dừng"
	case "completed":
		return "Hoàn thành"
	default:
		return "Chờ"
	}
}

func snapshotFlowLabel(flow string) string {
	switch flow {
	case "":
		return "-"
	case "writing":
		return "Viết"
	case "reviewing":
		return "Duyệt"
	case "rewriting":
		return "Viết lại"
	case "polishing":
		return "Trau chuốt"
	case "steering":
		return "Can thiệp"
	default:
		return flow
	}
}

func renderUsageSidebar(snap host.UISnapshot, width int) string {
	if snap.TotalInputTokens <= 0 && snap.TotalOutputTokens <= 0 && snap.TotalCostUSD <= 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(renderField("Vào", formatTokensCompact(snap.TotalInputTokens)))
	b.WriteString(renderField("Ra", formatTokensCompact(snap.TotalOutputTokens)))
	if cost := formatCostUSD(snap.TotalCostUSD); cost != "" {
		b.WriteString(renderField("Chi phí", cost))
	}
	if saved := formatCostUSD(snap.TotalSavedUSD); saved != "" {
		b.WriteString(renderField("Tiết kiệm", saved))
	}
	if snap.BudgetLimitUSD > 0 {
		pct := snap.TotalCostUSD / snap.BudgetLimitUSD * 100
		b.WriteString(renderField("Ngân sách", fmt.Sprintf("$%.2f/$%.2f (%.0f%%)", snap.TotalCostUSD, snap.BudgetLimitUSD, pct)))
	}

	agentStats := usageStatsByCost(snap.CachePerAgent)
	if len(agentStats) > 0 {
		b.WriteString(renderUsageGroupHeader("Vai trò", width))
		limit := min(len(agentStats), 4)
		for i := 0; i < limit; i++ {
			a := agentStats[i]
			b.WriteString(renderUsageLine(agentDisplayName(a.Role), eventAgentColor(a.Role), a.Input, a.Output, a.Cost, width))
			b.WriteString("\n")
		}
	}
	modelStats := usageStatsByCost(snap.CachePerModel)
	if len(modelStats) > 0 {
		b.WriteString(renderUsageGroupHeader("Mô hình", width))
		limit := min(len(modelStats), 4)
		for i := 0; i < limit; i++ {
			a := modelStats[i]
			b.WriteString(renderUsageLine(modelDisplayName(a.Model), bodyTextColor, a.Input, a.Output, a.Cost, width))
			b.WriteString("\n")
		}
	}
	return b.String()
}

func usageStatsByCost(in []host.AgentCacheStat) []host.AgentCacheStat {
	out := append([]host.AgentCacheStat(nil), in...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Cost != out[j].Cost {
			return out[i].Cost > out[j].Cost
		}
		return out[i].Input+out[i].Output > out[j].Input+out[j].Output
	})
	return out
}

func renderUsageGroupHeader(label string, width int) string {
	line := lipgloss.NewStyle().Foreground(colorDim).
		Render(strings.Repeat("·", max(8, width-lipgloss.Width(label)-3)))
	return lipgloss.NewStyle().Foreground(colorMuted).Render(label+" ") + line + "\n"
}

func renderUsageLine(name string, color lipgloss.TerminalColor, input, output int, cost float64, width int) string {
	nameW := 11
	if width < 24 {
		nameW = 8
	}
	nameCell := lipgloss.NewStyle().Foreground(color).Width(nameW).
		Render(truncate(name, nameW))
	tokens := formatTokensCompact(input + output)
	right := tokens
	if costStr := formatCostUSD(cost); costStr != "" {
		right += " · " + costStr
	}
	// Khi tên chiếm trọn đúng cột cố định, padding không để lại khoảng trắng đuôi; tách tường minh để tránh
	// tên model dính liền với mức dùng kiểu "gpt-5.6-sol5.3k".
	return fitInlineLine(nameCell+" "+lipgloss.NewStyle().Foreground(colorDim).Render(right), width)
}

func modelDisplayName(model string) string {
	model = strings.TrimSpace(model)
	if model == "" {
		return "unknown"
	}
	parts := strings.Split(model, "/")
	if len(parts) >= 3 {
		return strings.Join(parts[1:], "/")
	}
	if len(parts) == 2 {
		return parts[1]
	}
	return model
}

// renderCacheSidebar render khối "Bộ đệm" ở cột trái.
//
// Ba trạng thái:
//  1. Chưa tiêu thụ token nào: trả về rỗng, section không render
//  2. Mọi role trong phiên hiện tại đều chạy model không hỗ trợ prompt cache: chỉ render một dòng gợi ý "chưa bật"
//  3. Đã bật: trên cùng "hit tích lũy/gần đây · tiết kiệm · đọc/ghi" + phân cách + dòng per-role
//
// Dòng per-role khi capable hiện cặp số "tích lũy/gần đây%"; khi không capable hiện "chưa bật".
// So sánh tích lũy với N lần gần nhất giúp phân biệt "bị kéo tụt đầu phiên" với "hit thấp ổn định".
func renderCacheSidebar(snap host.UISnapshot, width int) string {
	// Upstream streaming không gửi final usage chunk của OpenAI —— toàn bộ dữ liệu tích lũy bằng 0,
	// nhưng đây không phải "chưa bật cache" cũng không phải "mức dùng thấp nên bị ẩn",
	// phải gợi ý tường minh, nếu không user sẽ mãi tưởng code cache cột trái không hiện.
	// Ưu tiên cao nhất.
	if snap.MissingAssistantUsage > 0 && snap.TotalInputTokens <= 0 {
		warn := lipgloss.NewStyle().Foreground(colorError).Bold(true).
			Render(fmt.Sprintf("⚠ Upstream chưa trả usage (%d lần)", snap.MissingAssistantUsage))
		hint := lipgloss.NewStyle().Foreground(colorDim).Italic(true).
			Render(truncate("Kiểm tra provider stream_options.include_usage", max(8, width-2)))
		return warn + "\n" + hint + "\n"
	}

	if snap.TotalInputTokens <= 0 && snap.TotalCacheWriteTokens <= 0 {
		return ""
	}

	// Chưa từng bật → hiện một dòng giải thích, tránh user hiểu lầm "hit 0% cần đi dò lỗi"
	if !snap.OverallCacheCapable && snap.TotalCacheReadTokens == 0 && snap.TotalCacheWriteTokens == 0 {
		return lipgloss.NewStyle().Foreground(colorDim).Italic(true).
			Render(truncate("Mô hình hiện tại chưa bật prompt cache", max(8, width-2))) + "\n"
	}

	var b strings.Builder

	// Chỉ số tổng hợp trên cùng: tích lũy + gần N lần mỗi cái một dòng, nhãn nói rõ,
	// tránh kiểu "X% · gần N Y%" trộn ba loại phân cách (phần trăm / chấm giữa / chữ) gây khó hiểu.
	overallHit := cacheHitRate(snap.TotalCacheReadTokens, snap.TotalInputTokens)
	b.WriteString(renderField("Trúng tích lũy", colorPercent(overallHit)))
	if snap.OverallRecentSamples > 0 && snap.OverallRecentInput > 0 {
		recent := cacheHitRate(snap.OverallRecentCacheRead, snap.OverallRecentInput)
		b.WriteString(renderField(fmt.Sprintf("Trúng gần (%d)", snap.OverallRecentSamples), colorPercent(recent)))
	}

	if savedStr := formatCostUSD(snap.TotalSavedUSD); savedStr != "" {
		b.WriteString(renderField("Tiết kiệm", savedStr))
	}

	// Lượng đọc/ghi chia hai dòng. Ghi bằng 0 là bình thường ở giao thức họ OpenAI / Gemini ——
	// hai nhà này cache trong suốt tự động, ghi cache hoàn toàn miễn phí (lần trượt đầu tính giá input thường,
	// lập cache không thu thêm phí), nên giao thức không expose trường cache_creation, không cần thiết.
	// Chỉ họ Anthropic / Bedrock mới báo lượng ghi, vì ghi của họ thu thêm phí (5m +25% / 1h +100%),
	// phải đưa con số này cho user để tính tiền.
	b.WriteString(renderField("Đọc cache", formatTokensCompact(snap.TotalCacheReadTokens)))
	if snap.TotalCacheWriteTokens > 0 {
		b.WriteString(renderField("Ghi cache", formatTokensCompact(snap.TotalCacheWriteTokens)))
	} else if snap.TotalCacheReadTokens > 0 {
		hint := lipgloss.NewStyle().Foreground(colorDim).Italic(true).Render("(cache tự động, không phụ phí)")
		b.WriteString(renderField("Ghi cache", "0 "+hint))
	}

	// Đứt mạch = tiền tố không rút ngắn mà hit tụt mạnh (các đợt tụt hợp lệ như đổi chương/nén đã miễn trừ).
	// Số lần nhiều thường trỏ về server đuổi cache hoặc trạm trung chuyển xoay upstream,
	// chi tiết xem warn "đứt mạch cache" trong tui.log.
	if snap.TotalCacheBreaks > 0 {
		v := lipgloss.NewStyle().Foreground(colorReview).Render(fmt.Sprintf("%d lần", snap.TotalCacheBreaks))
		b.WriteString(renderField("Đứt mạch", v))
	}

	// Arbiter theo thiết kế không tham gia prompt cache (phán quyết một lần cỡ KB, không có tiền tố ổn định để tái dùng),
	// để thường trú "chưa bật" hay "0%" chỉ khiến người ta đi dò lỗi vô ích; panel mức dùng vẫn ghi đủ sổ của nó.
	var roles []host.AgentCacheStat
	for _, a := range snap.CachePerAgent {
		if a.Role != "arbiter" {
			roles = append(roles, a)
		}
	}
	if len(roles) > 0 {
		b.WriteString(lipgloss.NewStyle().Foreground(colorDim).
			Render(strings.Repeat("·", max(8, width-12))))
		b.WriteString("\n")
		for _, a := range roles {
			b.WriteString(renderCacheAgentLine(a, width))
			b.WriteString("\n")
		}
	}
	return b.String()
}

// colorPercent tô màu phần trăm theo bậc hit rồi chuyển thành chuỗi, chỉ dùng cho cột giá trị.
func colorPercent(p float64) string {
	return lipgloss.NewStyle().Foreground(cacheHitColor(p)).Bold(true).
		Render(formatPercent(p))
}

// renderCacheAgentLine render một dòng role: role + tỉ lệ hit + lượng đọc cache / tổng input.
//
// Đặt cả tử số lẫn mẫu số ra (cacheRead / input) để user liếc qua là kiểm được nguồn gốc tỉ lệ hit,
// cũng nhận ra dữ liệu may mắn "phần trăm cao nhưng mẫu nhỏ" (ví dụ độ tin cậy của 100% / 1k
// thấp hơn 80% / 300k).
//
// Phần trăm ưu tiên giá trị ổn định của cửa sổ trượt; khi trong cửa sổ chưa có mẫu thì lùi về tích lũy.
// Toàn cột trái chỉ chỗ này dùng "/", ngữ nghĩa chuyên biệt (dấu chia toán học:
// lượng hit cache / tổng lượng input), không lẫn với phân cách khác.
//
// Ba trạng thái:
//
//	Chưa bật     "WRITER        Chưa bật"
//	Đã bật       "WRITER        85%  · 323k / 394k"
//	Không cache  hiện rõ "chưa bật", không trộn 0/0 gây nhiễu phán đoán
func renderCacheAgentLine(a host.AgentCacheStat, width int) string {
	// Tên role giữ hoàn toàn nhất quán với khu "Vai trò chạy"; Width lấy 12 để ARCHITECT dài nhất
	// vẫn giữ được 1 cột trắng đuôi làm phân cách, các role khác tự điền phải.
	roleStyle := lipgloss.NewStyle().Foreground(eventAgentColor(a.Role)).Width(12)
	role := roleStyle.Render(agentDisplayName(a.Role))

	if !a.CacheCapable {
		dim := lipgloss.NewStyle().Foreground(colorDim).Italic(true)
		_ = width
		return role + dim.Render("Chưa bật")
	}

	// Ưu tiên hit ổn định; khi trong cửa sổ chưa có mẫu thì lùi về tích lũy.
	hit := cacheHitRate(a.RecentCacheRead, a.RecentInput)
	if a.RecentSamples == 0 || a.RecentInput == 0 {
		hit = cacheHitRate(a.CacheRead, a.Input)
	}
	// Phần trăm cố định rộng 4 cột ("100%"), tránh cột lượng đọc nhảy trái phải giữa "5%" và "85%".
	pctCell := lipgloss.NewStyle().Width(4).
		Render(colorPercent(hit))

	// Lượng đọc tích lũy / input tích lũy — dù phần trăm phía trên là giá trị cửa sổ trượt,
	// tử mẫu vẫn dùng tích lũy, vì "nhìn ra quy mô" mới là mục đích chính của cột này;
	// phần trăm đứng riêng cung cấp tín hiệu ổn định là đủ.
	tokens := lipgloss.NewStyle().Foreground(colorDim).Render(
		" · " + formatTokensCompact(a.CacheRead) + " / " + formatTokensCompact(a.Input))
	_ = width
	return role + pctCell + tokens
}

// cacheHitRate chia trực tiếp ra phần trăm theo ngữ nghĩa input đã gồm cacheRead.
// input == 0 thì trả về 0, tránh hit giả.
func cacheHitRate(cacheRead, input int) float64 {
	if input <= 0 {
		return 0
	}
	return float64(cacheRead) / float64(input) * 100
}

// cacheHitColor tô màu hit: ≥50% xanh lá / 20–50% vàng / <20% đỏ.
// Ngược hướng với tỉ lệ dùng context: hit cache càng cao càng khỏe.
func cacheHitColor(percent float64) lipgloss.AdaptiveColor {
	switch {
	case percent >= 50:
		return colorSuccess
	case percent >= 20:
		return colorReview
	default:
		return colorError
	}
}

func formatPercent(p float64) string {
	if p <= 0 {
		return "0%"
	}
	if p < 10 {
		return fmt.Sprintf("%.1f%%", p)
	}
	return fmt.Sprintf("%.0f%%", p)
}

// formatTokensCompact render số token thành dạng gọn "8.2k" / "1.4M".
// Dùng cho dòng per-role hẹp, tránh chen lấn với kiểu dấu phẩy của formatNumber.
func formatTokensCompact(n int) string {
	if n <= 0 {
		return "0"
	}
	if n >= 1_000_000 {
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	}
	if n >= 1000 {
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprintf("%d", n)
}

func contextScopeLabel(scope string) string {
	switch scope {
	case "baseline":
		return "Cơ sở"
	case "projected":
		return "Dự kiến"
	case "recovered":
		return "Khôi phục"
	case "committed":
		return "Đã chốt"
	case "skipped":
		return "Bỏ qua (ngắt mạch)"
	default:
		return scope
	}
}

func contextStrategyLabel(strategy string) string {
	switch strategy {
	case "":
		return ""
	case "tool_result_microcompact":
		return "Nén nhẹ kết quả tool"
	case "light_trim":
		return "Cắt gọn nhẹ"
	case "full_summary":
		return "Tóm tắt đầy đủ"
	default:
		return strategy
	}
}

func agentDisplayName(name string) string {
	return strings.ToUpper(name)
}

func agentTaskLine(agent host.AgentSnapshot) string {
	if agent.TaskKind != "" {
		return taskKindLabel(agent.TaskKind)
	}
	if agent.Summary != "" {
		return agent.Summary
	}
	return ""
}

func agentContextLine(agent host.AgentSnapshot) string {
	ctx := agent.Context
	if ctx.ContextWindow <= 0 || ctx.Tokens <= 0 {
		return ""
	}
	percentColor := contextPercentColor(ctx.Percent)
	percentStr := lipgloss.NewStyle().Foreground(percentColor).Render(fmt.Sprintf("ctx %.0f%%", ctx.Percent))
	parts := []string{percentStr}
	if scope := contextScopeLabel(ctx.Scope); scope != "" {
		parts = append(parts, scope)
	}
	if strategy := contextStrategyLabel(ctx.Strategy); strategy != "" {
		parts = append(parts, strategy)
	}
	return strings.Join(parts, " · ")
}

func agentStateRank(state string) int {
	switch state {
	case "running":
		return 0
	case "failed":
		return 1
	default:
		return 2
	}
}

func agentOrder(name string) int {
	switch {
	case strings.HasPrefix(name, "architect"):
		return 0
	case name == "editor":
		return 2
	case name == "writer":
		return 3
	default:
		return 9
	}
}

func agentStateLabel(state string) string {
	switch state {
	case "running":
		return "Đang chạy"
	case "failed":
		return "Lỗi"
	case "idle":
		return "Chờ"
	default:
		return state
	}
}

func agentStateIcon(state string) string {
	switch state {
	case "running":
		return "●"
	case "failed":
		return "×"
	default:
		return "·"
	}
}

func taskStatusColor(status string) lipgloss.AdaptiveColor {
	switch status {
	case "running":
		return colorSuccess
	case "queued":
		return colorMuted
	case "failed", "canceled":
		return colorError
	case "succeeded":
		return colorSuccess
	default:
		return colorDim
	}
}

func taskKindLabel(kind string) string {
	switch kind {
	case "foundation_plan":
		return "Kế hoạch nền"
	case "chapter_write":
		return "Viết chương"
	case "chapter_review":
		return "Duyệt chương"
	case "chapter_rewrite":
		return "Viết lại chương"
	case "chapter_polish":
		return "Trau chuốt chương"
	case "arc_expand":
		return "Mở rộng arc"
	case "volume_append":
		return "Dàn ý tập tiếp"
	case "steer_apply":
		return "Xử lý can thiệp"
	default:
		return kind
	}
}
