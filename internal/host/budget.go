package host

import (
	"fmt"
	"math"
	"sync/atomic"

	"github.com/voocel/agentcore"
	"github.com/CTKiet2006/kietnovel/internal/bootstrap"
)

// Máy trạng thái ngân sách: tiến lên đơn điệu, mỗi lần chuyển kích hoạt đúng một tác dụng phụ, không lùi.
// Tăng ngân sách = người dùng cấp quyền lại = sửa cấu hình rồi khởi động lại/tạo Host mới, không lùi trạng thái trong instance hiện tại.
const (
	budgetNormal      int32 = iota // Chưa tới mực cảnh báo
	budgetWarned                   // Đã cảnh báo, chưa vượt
	budgetStopPending              // Đã vượt, chờ dừng ở ranh giới tác vụ con
	budgetStopped                  // Đã thực hiện dừng
)

// BudgetSentinel theo dõi chi phí tích luỹ và thực thi chính sách ngân sách của người dùng (khối budget trong cấu hình).
//
// Định vị hợp lý (architecture.md §8.3/§10): không đánh giá hành vi model — dừng khi vượt hạn
// tương đương Abort thủ công của người dùng vào đúng thời điểm đó, Host chỉ thay họ thực thi một chỉ lệnh đã ký trước. Nó ảnh hưởng dòng điều khiển nên
// không phải quan sát viên, mà là thành phần chính sách của Host ngang hàng với flow.Dispatcher; tầng Route/công cụ không biết tới.
//
// Thời điểm dừng: mặc định ở ranh giới tác vụ con (Host gọi đồng bộ HandleBoundary), không phí chương đang chạy dở;
// hardStop=true thì dừng ngay khi vượt. Xử lý ranh giới diễn ra trước khi flow.Dispatcher phát việc kế tiếp; tầng Route/công cụ không biết tới ngân sách.
type BudgetSentinel struct {
	limit     float64
	warnRatio float64
	hardStop  bool

	costNow func() float64              // Chi phí tích luỹ hiện tại (bọc usage.Totals; có thể tiêm stub cho test)
	abort   func(reason string)         // Bọc dừng của Host (kèm sự kiện nêu lý do)
	report  func(level, summary string) // Cửa ra cảnh báo (emitEvent + notify, do Host tiêm)

	state atomic.Int32

	// Phát hiện vùng mù tính phí: với model registry không có giá và provider không tự báo cost, mỗi lần ghi sổ tăng $0,
	// khiến ngân sách hỏng âm thầm. Đánh giá theo "nhiều bút ghi liên tiếp không tăng" chứ không theo total==0 — cách sau không bắt được trường hợp
	// /model chuyển sang model không có giá giữa chừng (total đứng ở giá trị lịch sử khác 0 nhưng không tăng).
	// Model miễn phí cũng dính, và cảnh báo "ngân sách sẽ không kích hoạt" vẫn đúng với chúng.
	lastTotal   atomic.Uint64 // math.Float64bits(chi phí tích luỹ lần callback trước)
	zeroStreak  atomic.Int32
	blindWarned atomic.Bool
}

// blindZeroStreak số bút ghi không tăng liên tiếp thì mới cảnh báo. Model tính phí bình thường mỗi bút tăng chắc > 0
// (cost là float cộng dồn không làm tròn), lấy 5 chỉ để tránh nhiễu gai cực đại, không phải ngưỡng chính sách có thể chỉnh.
const blindZeroStreak = 5

// NewBudgetSentinel tạo cảnh báo ngân sách; trả nil khi chính sách không bật (mọi method đều nil-safe).
func NewBudgetSentinel(cfg bootstrap.BudgetConfig, costNow func() float64, abort func(reason string), report func(level, summary string)) *BudgetSentinel {
	if !cfg.Enabled() {
		return nil
	}
	return &BudgetSentinel{
		limit:     cfg.BookUSD,
		warnRatio: cfg.WarnRatio,
		hardStop:  cfg.HardStop,
		costNow:   costNow,
		abort:     abort,
		report:    report,
	}
}

// OnCost do UsageTracker gọi sau mỗi lần ghi sổ, mang theo chi phí tích luỹ mới nhất (ngoài khoá).
// Một lần callback có thể nhảy qua hai cấp (normal→warned→stopPending), mỗi tác dụng phụ kích hoạt đúng một lần.
func (s *BudgetSentinel) OnCost(total float64) {
	if s == nil {
		return
	}
	if prev := s.lastTotal.Swap(math.Float64bits(total)); total == math.Float64frombits(prev) {
		if s.zeroStreak.Add(1) >= blindZeroStreak && s.blindWarned.CompareAndSwap(false, true) {
			s.report("warn", fmt.Sprintf("Vùng mù ngân sách: ghi sổ liên tục nhưng chi phí tích luỹ đứng ở $%.2f không tăng (model hiện tại không có giá trong registry và provider không tự báo cost, hoặc là model miễn phí) — trần ngân sách sẽ không kích hoạt", total))
		}
	} else {
		s.zeroStreak.Store(0)
	}
	if total >= s.limit*s.warnRatio && s.state.CompareAndSwap(budgetNormal, budgetWarned) {
		s.report("warn", fmt.Sprintf("Cảnh báo ngân sách: đã chi $%.2f, bằng %.0f%% ngân sách $%.2f", total, s.warnRatio*100, s.limit))
	}
	if total >= s.limit && s.state.CompareAndSwap(budgetWarned, budgetStopPending) {
		if s.hardStop {
			s.report("error", fmt.Sprintf("Hết ngân sách: đã chi $%.2f, vượt ngân sách $%.2f, dừng ngay", total, s.limit))
			s.stop(total)
			return
		}
		s.report("error", fmt.Sprintf("Hết ngân sách: đã chi $%.2f, vượt ngân sách $%.2f, sẽ dừng khi tác vụ con hiện tại xong", total, s.limit))
	}
}

// HandleEvent thực hiện lệnh dừng đang chờ ở ranh giới tác vụ con. Phải đăng ký trước Dispatcher.
// Không bỏ qua IsError — trả về lỗi cũng là một ranh giới, việc dừng không nên bị hoãn vì tác vụ con thất bại.
func (s *BudgetSentinel) HandleEvent(ev agentcore.Event) {
	if s == nil {
		return
	}
	if ev.Type != agentcore.EventToolExecEnd || ev.Tool != "subagent" {
		return
	}
	s.HandleBoundary()
}

func (s *BudgetSentinel) HandleBoundary() bool {
	if s == nil || s.state.Load() != budgetStopPending {
		return false
	}
	s.stop(s.costNow())
	return true
}

func (s *BudgetSentinel) stop(total float64) {
	if s.state.CompareAndSwap(budgetStopPending, budgetStopped) {
		s.abort(fmt.Sprintf("Dừng vì ngân sách: đã chi $%.2f, vượt ngân sách $%.2f; tăng budget.book_usd rồi chạy lại để viết tiếp", total, s.limit))
	}
}

// Refuse là kiểm tra trước khi chạy: trả lỗi từ chối nếu đã vượt ngân sách (được gọi ở các đường Start/Resume/Continue).
// Người dùng tăng ngân sách = cấp quyền lại, với cấu hình mới thì Refuse tự nhiên cho qua.
func (s *BudgetSentinel) Refuse() error {
	if s == nil {
		return nil
	}
	if cost := s.costNow(); cost >= s.limit {
		return fmt.Errorf("Cuốn này đã chi $%.2f, chạm trần ngân sách $%.2f; hãy tăng budget.book_usd trong cấu hình rồi thử lại", cost, s.limit)
	}
	return nil
}

// Limit trả về trần ngân sách (để TUI hiển thị); trả 0 khi không bật.
func (s *BudgetSentinel) Limit() float64 {
	if s == nil {
		return 0
	}
	return s.limit
}
