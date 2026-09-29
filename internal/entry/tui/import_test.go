package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/CTKiet2006/kietnovel/internal/host/imp"
)

// TestImportHistoryCoalescesRetryLines giữ cập nhật tại chỗ của dòng thử lại: sự kiện liên tiếp cùng Key chỉ chiếm một dòng
// ("lần thứ N" nhảy trên một dòng), sau khi bị dòng tiến độ thường chen ngang thì mở dòng mới, giữ đúng thứ tự thời gian.
func TestImportHistoryCoalescesRetryLines(t *testing.T) {
	s := newImportState(1, "book.txt", 100, 40, nil)
	base := len(s.history)
	retry := func(msg string) imp.Event {
		return imp.Event{Time: time.Now(), Stage: imp.StageSegmenting, Message: msg, Level: "warn", Key: "retry:segmenting"}
	}
	s.appendEvent(retry("1s 后重试（第 1 次）"), 80)
	s.appendEvent(retry("2s 后重试（第 2 次）"), 80)
	s.appendEvent(retry("4s 后重试（第 3 次）"), 80)
	if got := len(s.history) - base; got != 1 {
		t.Fatalf("Thử lại cùng Key liên tiếp phải gộp thành 1 dòng, được %d", got)
	}
	if last := s.history[len(s.history)-1]; last.message != "4s 后重试（第 3 次）" {
		t.Fatalf("Dòng gộp phải cập nhật thành tin mới nhất, được %q", last.message)
	}
	// Sau khi bị dòng tiến độ thường chen ngang, lần thử lại mới mở dòng riêng.
	s.appendEvent(imp.Event{Time: time.Now(), Stage: imp.StageAnalyzing, Message: "分析第 1 章起的连续批次..."}, 80)
	s.appendEvent(retry("1s 后重试（第 1 次）"), 80)
	if got := len(s.history) - base; got != 3 {
		t.Fatalf("Sau khi bị chen ngang, thử lại phải mở dòng mới, tổng 3 dòng, được %d", got)
	}
}

// TestRenderImportLineWrapsWithoutClipping giữ chi tiết lỗi luôn thấy đầy đủ: thân tin xuống dòng theo
// chiều rộng còn lại sau tiền tố, dòng nối canh thẳng, không dòng nào được vượt quá contentW — viewport cắt cứng
// dòng quá rộng, mà HTTP status/provider/model trong lỗi chính là căn cứ tra lỗi, cắt mất là báo lỗi vô ích.
func TestRenderImportLineWrapsWithoutClipping(t *testing.T) {
	ln := importLine{
		at:      time.Now(),
		stage:   imp.StageSegmenting,
		message: "切分区间 L1..L171",
		err: errors.New("imp: 模型调用失败（请求参数非法，HTTP 400，openrouter，deepseek/deepseek-chat）：" +
			"Provider returned error: invalid request payload with a very long gateway message tail"),
	}
	const contentW = 80
	out := renderImportLine(ln, contentW, time.Now())
	// Xuống dòng có thể ngắt ở bất kỳ ký tự nào, bỏ khoảng trắng rồi so sánh, chỉ cần nội dung không mất chữ nào.
	norm := func(s string) string {
		return strings.Map(func(r rune) rune {
			if r == ' ' || r == '\n' {
				return -1
			}
			return r
		}, s)
	}
	for _, want := range []string{"HTTP 400", "openrouter", "gateway message tail"} {
		if !strings.Contains(norm(out), norm(want)) {
			t.Fatalf("Nội dung dòng thiếu %q: %q", want, out)
		}
	}
	for i, line := range strings.Split(out, "\n") {
		if w := lipgloss.Width(line); w > contentW {
			t.Fatalf("Dòng %d rộng %d vượt quá %d, sẽ bị viewport cắt: %q", i, w, contentW, line)
		}
	}
	// Terminal hẹp: tiền tố (timestamp+icon+tên giai đoạn dài) có thể chiếm hơn nửa chiều rộng, thân tin phải xuống dòng mới chứ không cố nhồi vượt rộng.
	ln.stage = imp.StageAwaitingConfirmation
	const narrowW = 40
	for i, line := range strings.Split(renderImportLine(ln, narrowW, time.Now()), "\n") {
		if w := lipgloss.Width(line); w > narrowW {
			t.Fatalf("Terminal hẹp dòng %d rộng %d vượt quá %d: %q", i, w, narrowW, line)
		}
	}
}

// TestRenderImportLineMultilineBlock giữ cách trình bày khối tin nhiều dòng (xem trước xác nhận tách chương): dòng nối
// thụt nhẹ tổng thể (2 cột), không canh theo rộng tiền tố — tiền tố 40+ cột sẽ ép cả khối danh sách chương sang nửa phải panel, nửa trái bỏ trống.
func TestRenderImportLineMultilineBlock(t *testing.T) {
	ln := importLine{
		at:      time.Now(),
		stage:   imp.StageAwaitingConfirmation,
		current: 157, total: 157,
		message: "已切分 157 章，请核对：\n  第1章 引子\n  第2章 我故意的\n",
	}
	const contentW = 100
	out := strings.Split(renderImportLine(ln, contentW, time.Now()), "\n")
	if len(out) != 3 {
		t.Fatalf("Phải là dòng tiền tố + 2 dòng thân, được %d dòng: %q", len(out), out)
	}
	for i, line := range out[1:] {
		if w := lipgloss.Width(line); w > contentW {
			t.Fatalf("Dòng %d quá rộng %d: %q", i+1, w, line)
		}
		if strings.HasPrefix(line, strings.Repeat(" ", 20)) {
			t.Fatalf("Dòng nối khối nhiều dòng không được canh theo rộng tiền tố: %q", line)
		}
		if !strings.HasPrefix(line, "  ") {
			t.Fatalf("Dòng nối khối nhiều dòng phải thụt nhẹ 2 cột: %q", line)
		}
	}
}

// TestWrapTextResetsAtNewlines giữ xuống dòng của tin nhiều dòng: chỗ '\n' bắt buộc reset đếm rộng dòng, nếu không chỉ
// cần một dòng kích hoạt xuống dòng thì mọi dòng sau đều bị nhận nhầm là quá rộng và chèn xuống dòng giả + thụt vào, cả bản xem trước xác nhận vỡ vụn.
func TestWrapTextResetsAtNewlines(t *testing.T) {
	in := strings.Repeat("宽", 30) + "\n短行一\n短行二"
	out := wrapText(in, 20)
	for i, l := range strings.Split(out, "\n") {
		if w := lipgloss.Width(l); w > 20 {
			t.Fatalf("Dòng %d rộng %d vượt quá 20: %q", i, w, l)
		}
	}
	if !strings.Contains(out, "\n短行一\n短行二") {
		t.Fatalf("Dòng ngắn sẵn có không được đánh vỡ: %q", out)
	}
}

// TestImportEscResumeGate giữ điểm rơi của Esc ở panel import: import mở từ trang chào mà kết thúc thành công,
// đóng panel bắt buộc chạy bù một lần khôi phục (Resume của bootstrap chỉ chạy lúc khởi động), nếu không người dùng kẹt ở trang chào
// không có lối viết tiếp; trạng thái lỗi cuối và cảnh workbench thì chỉ đóng panel; đang chạy mà Esc vẫn là hủy chứ không phải đóng.
func TestImportEscResumeGate(t *testing.T) {
	esc := tea.KeyMsg{Type: tea.KeyEsc}
	// tea.Batch sau khi chạy trả về BatchMsg (lệnh con không bị thực thi), nhờ đó phân biệt "focus+khôi phục" với focus thuần.
	isBatch := func(cmd tea.Cmd) bool {
		_, ok := cmd().(tea.BatchMsg)
		return ok
	}
	newM := func(mode appMode, st *importState) Model {
		return Model{mode: mode, importer: st, textarea: textarea.New()}
	}

	m := newM(modeNew, &importState{done: true, stage: imp.StageDone})
	next, cmd := m.handleImportKey(esc)
	if next.(Model).importer != nil {
		t.Fatal("Trạng thái cuối Esc phải đóng panel")
	}
	if !isBatch(cmd) {
		t.Fatal("Đóng panel sau import thành công từ trang chào phải kèm lệnh khôi phục")
	}

	m = newM(modeNew, &importState{done: true, stage: imp.StageError, err: errors.New("boom")})
	if _, cmd := m.handleImportKey(esc); isBatch(cmd) {
		t.Fatal("Trạng thái lỗi cuối không được kích khôi phục (sách có thể chưa import xong)")
	}

	m = newM(modeRunning, &importState{done: true, stage: imp.StageDone})
	if _, cmd := m.handleImportKey(esc); isBatch(cmd) {
		t.Fatal("Workbench đã có cổng riêng, không được kích khôi phục lặp lại")
	}

	canceled := false
	m = newM(modeNew, &importState{cancel: func() { canceled = true }})
	next, _ = m.handleImportKey(esc)
	if !canceled || next.(Model).importer == nil {
		t.Fatal("Đang chạy mà Esc phải hủy import và giữ panel chờ runner kết thúc")
	}
}

// TestRetryCountdown giữ hợp đồng kết xuất đếm ngược (panel sự kiện và panel import dùng chung):
// chưa đặt hạn hoặc đã tới giờ trả rỗng (request đang bay); thời gian còn lại làm tròn lên tới giây, giảm dần từng giây và không hiện 0s.
func TestRetryCountdown(t *testing.T) {
	now := time.Now()
	if got := retryCountdown(time.Time{}, now); got != "" {
		t.Fatalf("Hạn zero phải trả rỗng, được %q", got)
	}
	if got := retryCountdown(now.Add(-time.Second), now); got != "" {
		t.Fatalf("Đã tới giờ phải trả rỗng, được %q", got)
	}
	if got := retryCountdown(now.Add(7500*time.Millisecond), now); got != "thử lại sau 8s" {
		t.Fatalf("7.5s phải làm tròn lên 8s, được %q", got)
	}
	if got := retryCountdown(now.Add(300*time.Millisecond), now); got != "thử lại sau 1s" {
		t.Fatalf("Chưa tới 1s phải hiện 1s, được %q", got)
	}
}

// TestParseImportArgsGuide giữ phân tích --guide: hướng dẫn tự nhiên được chứa dấu cách (mọi token sau đều gộp vào),
// có thể kết hợp với tùy chọn khác (đặt ở cuối), nội dung rỗng thì báo lỗi.
func TestParseImportArgsGuide(t *testing.T) {
	opts, err := parseImportArgs([]string{"--guide=幕间·X", "也是", "独立章节"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.Guidance != "幕间·X 也是 独立章节" {
		t.Fatalf("Hướng dẫn chứa dấu cách phải gộp toàn bộ, được %q", opts.Guidance)
	}
	opts, err = parseImportArgs([]string{"book.txt", "--yes", "--guide=序章并入第一章"})
	if err != nil {
		t.Fatal(err)
	}
	if !opts.AutoConfirm || opts.SourcePath != "book.txt" || opts.Guidance != "序章并入第一章" {
		t.Fatalf("Phân tích kết hợp với tùy chọn khác không khớp: %+v", opts)
	}
	if _, err := parseImportArgs([]string{"--guide="}); err == nil {
		t.Fatal("--guide rỗng phải báo lỗi")
	}
}
