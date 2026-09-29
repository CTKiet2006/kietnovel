package host

import (
	"fmt"
	"slices"

	"github.com/CTKiet2006/kietnovel/internal/domain"
	"github.com/CTKiet2006/kietnovel/internal/flow"
	"github.com/CTKiet2006/kietnovel/internal/store"
)

// ChapterAdvanceGate là thành phần chính sách tiến viết duy nhất của Host:
//   - AdvanceHold: thực hiện lần tạm dừng một lần mà can thiệp này đã ký;
//   - review permit: chặn chương mới tiến về phía trước khi chưa được cấp quyền.
//
// Nó không tham gia Route, không diễn giải Task/Reason, cũng không đưa ra phán đoán văn chương.
type ChapterAdvanceGate struct {
	store  *store.Store
	pause  func(reason string)
	report func(level, summary string)
}

func NewChapterAdvanceGate(s *store.Store, pause func(reason string), report func(level, summary string)) *ChapterAdvanceGate {
	return &ChapterAdvanceGate{store: s, pause: pause, report: report}
}

// HandleBoundary tiêu thụ hold trúng và đối soát giấy phép chương. Trả true nghĩa là Engine phải dừng.
// Khi ở auto và không có hold thì chỉ đọc RunMeta một lần, không đụng Progress/PendingCommit/checkpoint.
func (g *ChapterAdvanceGate) HandleBoundary() bool {
	if g == nil || g.store == nil {
		return false
	}
	meta, err := g.store.RunMeta.Load()
	if err != nil {
		return g.fail(fmt.Errorf("Không đọc được RunMeta: %w", err))
	}
	if meta == nil {
		return g.fail(fmt.Errorf("RunMeta chưa khởi tạo"))
	}
	if !meta.AdvanceMode.Valid() {
		return g.fail(&domain.UnsupportedAdvanceModeError{Mode: meta.AdvanceMode})
	}
	if meta.AdvanceMode == domain.ChapterAdvanceAuto && meta.AdvancePermitChapter != 0 {
		return g.fail(fmt.Errorf("Chế độ auto còn sót giấy phép chương %d", meta.AdvancePermitChapter))
	}

	if meta.AdvanceHold != nil {
		if g.handleHold(*meta.AdvanceHold) {
			return true
		}
		// handleHold có thể tiêu thụ hold do xong sách sinh ra; tiếp tục đối soát permit.
	}
	if meta.AdvanceMode == domain.ChapterAdvanceAuto {
		return false
	}
	return g.reconcilePermit(meta.AdvancePermitChapter)
}

func (g *ChapterAdvanceGate) handleHold(hold domain.AdvanceHold) bool {
	progress, err := g.store.Progress.Load()
	if err != nil {
		return g.fail(fmt.Errorf("Không đọc được Progress để phân giải tạm dừng một lần: %w", err))
	}
	resolution, err := flow.ResolveAdvanceHold(&hold, progress)
	if err != nil {
		return g.fail(err)
	}
	if hold.After == domain.AdvanceHoldAtChapter && progress.LatestCompleted() >= hold.TargetChapter {
		stable, err := g.targetChapterCommitted(progress, hold.TargetChapter)
		if err != nil {
			return g.fail(err)
		}
		if !stable {
			return false
		}
	}
	switch resolution {
	case flow.AdvanceHoldKeep:
		return false
	case flow.AdvanceHoldConsume:
		if err := g.store.RunMeta.ClearAdvanceHold(hold); err != nil {
			return g.fail(fmt.Errorf("Không tiêu thụ được tạm dừng một lần: %w", err))
		}
		g.reportEvent("info", withAdvanceReason("Đã xong sách, ý định tạm dừng một lần được gỡ bỏ", hold.Reason))
		return false
	case flow.AdvanceHoldConsumeAndStop:
		if err := g.store.RunMeta.ClearAdvanceHold(hold); err != nil {
			return g.fail(fmt.Errorf("Không tiêu thụ được tạm dừng một lần: %w", err))
		}
		msg := "Đã tạm dừng tại ranh giới công việc hiện tại theo yêu cầu của bạn"
		switch hold.After {
		case domain.AdvanceHoldAfterRewritesDrained:
			msg = "Hàng đợi viết lại đã hết, đã tạm dừng chờ duyệt"
		case domain.AdvanceHoldAtChapter:
			msg = fmt.Sprintf("Đã viết đến chương %d, tạm dừng theo yêu cầu của bạn", hold.TargetChapter)
		}
		g.pauseNow(withAdvanceReason(msg, hold.Reason))
		return true
	default:
		return g.fail(fmt.Errorf("Kết quả phân giải tạm dừng một lần không rõ: %d", resolution))
	}
}

func (g *ChapterAdvanceGate) targetChapterCommitted(progress *domain.Progress, chapter int) (bool, error) {
	pending, err := g.store.Signals.LoadPendingCommit()
	if err != nil {
		return false, fmt.Errorf("Không đọc được PendingCommit để đối soát chương đích: %w", err)
	}
	if pending != nil {
		return false, nil
	}
	if !slices.Contains(progress.CompletedChapters, chapter) {
		return false, fmt.Errorf("Chương đích %d không có trong danh sách đã hoàn thành", chapter)
	}
	if g.store.Checkpoints.LatestByStep(domain.ChapterScope(chapter), "commit") == nil {
		return false, fmt.Errorf("Chương đích %d đã đánh dấu hoàn thành nhưng thiếu commit checkpoint", chapter)
	}
	return true, nil
}

func (g *ChapterAdvanceGate) reconcilePermit(permit int) bool {
	if permit == 0 {
		return false
	}
	if permit < 0 {
		return g.fail(fmt.Errorf("Giấy phép chương không được âm: %d", permit))
	}
	progress, err := g.store.Progress.Load()
	if err != nil {
		return g.fail(fmt.Errorf("Không đọc được Progress để đối soát giấy phép chương: %w", err))
	}
	if progress == nil {
		return g.fail(fmt.Errorf("Thiếu Progress, không thể đối soát giấy phép chương %d", permit))
	}
	pending, err := g.store.Signals.LoadPendingCommit()
	if err != nil {
		return g.fail(fmt.Errorf("Không đọc được PendingCommit để đối soát giấy phép chương: %w", err))
	}
	completed := slices.Contains(progress.CompletedChapters, permit)
	if completed {
		if pending != nil {
			if pending.Chapter != permit {
				return g.fail(fmt.Errorf("Giấy phép chương %d xung đột với PendingCommit chương %d", permit, pending.Chapter))
			}
			return false
		}
		if g.store.Checkpoints.LatestByStep(domain.ChapterScope(permit), "commit") == nil {
			return g.fail(fmt.Errorf("Chương %d đã đánh dấu hoàn thành nhưng thiếu commit checkpoint", permit))
		}
		if err := g.store.RunMeta.ClearAdvancePermit(permit); err != nil {
			return g.fail(fmt.Errorf("Không tiêu thụ được giấy phép chương %d: %w", permit, err))
		}
		return false
	}
	if permit != progress.NextChapter() {
		return g.fail(fmt.Errorf("Giấy phép chương %d không khớp với chương kế tiếp hiện tại %d", permit, progress.NextChapter()))
	}
	return false
}

// Allow thực hiện kiểm tra giấy phép cuối cùng trước khi phát việc cho Worker.
func (g *ChapterAdvanceGate) Allow(inst *flow.Instruction) (bool, error) {
	if g == nil || g.store == nil {
		return true, nil
	}
	meta, err := g.store.RunMeta.Load()
	if err != nil {
		return false, fmt.Errorf("Không đọc được RunMeta: %w", err)
	}
	if meta == nil {
		return false, fmt.Errorf("RunMeta chưa khởi tạo")
	}
	if !meta.AdvanceMode.Valid() {
		return false, &domain.UnsupportedAdvanceModeError{Mode: meta.AdvanceMode}
	}
	if meta.AdvanceMode == domain.ChapterAdvanceAuto {
		if meta.AdvancePermitChapter != 0 {
			return false, fmt.Errorf("Chế độ auto còn sót giấy phép chương %d", meta.AdvancePermitChapter)
		}
		return true, nil
	}
	progress, err := g.store.Progress.Load()
	if err != nil {
		return false, fmt.Errorf("Không đọc được Progress: %w", err)
	}
	pending, err := g.store.Signals.LoadPendingCommit()
	if err != nil {
		return false, fmt.Errorf("Không đọc được PendingCommit: %w", err)
	}
	if !flow.StartsForwardChapter(inst, progress, pending) {
		return true, nil
	}
	target := inst.Chapter
	if target == 0 {
		target = progress.NextChapter()
	}
	if meta.AdvancePermitChapter == target {
		return true, nil
	}
	if meta.AdvancePermitChapter != 0 {
		return false, fmt.Errorf("Phát việc chương %d không khớp với giấy phép chương %d", target, meta.AdvancePermitChapter)
	}
	if hold := meta.AdvanceHold; hold != nil && hold.After == domain.AdvanceHoldAtChapter && target <= hold.TargetChapter {
		return true, nil
	}
	latest := progress.LatestCompleted()
	message := fmt.Sprintf("Đã hoàn thành đến chương %d, chế độ duyệt từng chương đang chờ cấp quyền chương %d; dùng /next để viết, hoặc nhập góp ý sửa đổi", latest, target)
	if latest == 0 {
		message = fmt.Sprintf("Phần quy hoạch đã sẵn sàng, chế độ duyệt từng chương đang chờ cấp quyền chương %d; dùng /next để viết, hoặc nhập góp ý sửa đổi", target)
	}
	g.pauseNow(message)
	return false, nil
}

func (g *ChapterAdvanceGate) fail(err error) bool {
	g.pauseNow("Lỗi điều khiển đẩy chương, đã tạm dừng: " + err.Error())
	return true
}

func (g *ChapterAdvanceGate) pauseNow(reason string) {
	if g.pause != nil {
		g.pause(reason)
		return
	}
	g.reportEvent("error", reason)
}

func (g *ChapterAdvanceGate) reportEvent(level, summary string) {
	if g.report != nil {
		g.report(level, summary)
	}
}

func withAdvanceReason(msg, reason string) string {
	if reason == "" {
		return msg
	}
	return msg + " (yêu cầu: " + reason + ")"
}
