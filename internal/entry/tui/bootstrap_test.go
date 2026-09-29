package tui

import (
	"errors"
	"testing"

	"github.com/charmbracelet/bubbles/textarea"
)

func TestBootstrapExistingBookFailureStaysInWorkbench(t *testing.T) {
	m := Model{mode: modeNew, textarea: textarea.New()}
	next, cmd, handled := m.handleRuntimeMsg(bootstrapMsg{existing: true, err: errors.New("迁移失败")})
	if !handled || cmd == nil {
		t.Fatal("Khôi phục tác phẩm sẵn có thất bại vẫn phải refresh workbench")
	}
	got := next.(Model)
	if got.mode != modeRunning {
		t.Fatalf("Khôi phục tác phẩm sẵn có thất bại phải ở lại workbench, được mode=%v", got.mode)
	}
	if got.err == nil || got.err.Error() != "迁移失败" {
		t.Fatalf("Workbench phải hiển thị lỗi gốc, được %v", got.err)
	}
}

// TestBootstrapCompletedBookLandsOnDoneWorkbench giữ điểm rơi khởi động của sách đã hoàn thành: resumeLabel trả
// về nhãn rỗng khi complete, hành vi cũ rơi vào trang chào — trang chào không nhắc gì tới sách sẵn có,
// người dùng sẽ tưởng sách bị mất, và vị trí tự nhiên của /reopen, /export, nhập làm lại đều ở workbench trạng thái hoàn thành.
func TestBootstrapCompletedBookLandsOnDoneWorkbench(t *testing.T) {
	m := Model{mode: modeNew, textarea: textarea.New()}
	next, cmd, handled := m.handleRuntimeMsg(bootstrapMsg{completed: true})
	if !handled || cmd == nil {
		t.Fatal("completed bootstrap phải được xử lý và trả về lệnh")
	}
	got := next.(Model)
	if got.mode != modeDone {
		t.Fatalf("Sách đã hoàn thành phải rơi vào workbench hoàn thành, được mode=%v", got.mode)
	}
	if got.textarea.Placeholder != donePlaceholder {
		t.Fatalf("Phải đưa gợi ý trạng thái hoàn thành (gồm /reopen), được %q", got.textarea.Placeholder)
	}

	// Đã ở workbench (ví dụ nhận thêm bootstrap sau khi hoàn thành trong phiên) thì không được chuyển trạng thái lặp lại.
	m = Model{mode: modeRunning, textarea: textarea.New()}
	next, _, _ = m.handleRuntimeMsg(bootstrapMsg{completed: true})
	if next.(Model).mode != modeRunning {
		t.Fatal("Ngoài trang chào thì completed bootstrap không được chuyển trạng thái")
	}
}
