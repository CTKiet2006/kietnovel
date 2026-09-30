package tui

import (
	"path/filepath"

	"github.com/CTKiet2006/kietnovel/internal/host"
	"github.com/CTKiet2006/kietnovel/internal/i18n"
	tea "github.com/charmbracelet/bubbletea"
)

// switchBook dựng lại toàn bộ phiên làm việc quanh một thư mục truyện khác.
//
// Vì sao dựng lại Model thay vì sửa vài ô: khi đổi truyện, MỌI thứ trên màn
// hình đều thuộc về truyện cũ — dàn ý, danh sách chương, xem trước, báo cáo,
// thanh trạng thái, lịch sử sự kiện. Sửa từng ô thì dễ sót một chỗ và để lại
// dữ liệu truyện cũ lẫn vào truyện mới. Dựng Model mới là cách chắc chắn sạch.
//
// Vì sao dựng Host mới: Host giữ Engine, goroutine, context và kênh sự kiện của
// đúng một thư mục. Không dựng lại thì engine cũ vẫn chạy và ghi vào truyện cũ.
//
// Thứ tự cố ý: dựng Host mới TRƯỚC, đóng Host cũ SAU. Nếu host.New lỗi (thường là
// truyện đang bị tiến trình khác giữ khoá), truyện cũ vẫn nguyên và người dùng
// không mất gì cả.
func (m Model) switchBook(dir string) (Model, tea.Cmd, error) {
	if sameDir(m.runtime.Dir(), dir) {
		return m, nil, errAlreadyOpen
	}
	cfg := m.cfg
	cfg.OutputDir = dir
	newRT, err := host.New(cfg, m.bundle, m.hostOpts...)
	if err != nil {
		return m, nil, err
	}
	old := m.runtime
	m.runtime = newRT
	old.Close()

	// Dựng Model mới quanh Host mới, rồi mang sang những thứ thuộc về người dùng
	// chứ không thuộc về truyện.
	fresh := NewModel(newRT, m.version)
	fresh.cfg = cfg
	fresh.bundle = m.bundle
	fresh.hostOpts = m.hostOpts
	fresh.disableUpdateCheck = m.disableUpdateCheck
	fresh.updateHint = m.updateHint
	// Lịch sử phím là của người dùng, không phải của truyện: giữ lại, đổi truyện
	// không có nghĩa mất đường lui.
	fresh.inputHistory = m.inputHistory
	fresh.historyIdx = m.historyIdx
	// Ô nhập thì KHÔNG mang sang: nội dung đang gõ thuộc về truyện cũ, gửi sang
	// truyện mới sẽ thành yêu cầu sai.
	return fresh, tea.Batch(bootstrapRuntime(newRT), m.textarea.Focus()), nil
}

// hasUncommittedWork báo truyện đang mở còn việc dở. Chuyển truyện lúc này sẽ
// mất phần chưa kịp lưu, nên phải hỏi trước.
func (m Model) hasUncommittedWork() (bool, string) {
	snap := m.runtime.Snapshot()
	if snap.IsRunning {
		return true, i18n.Tf("truyện đang chạy (đang xử lý chương %d)", maxInt(snap.CurrentChapter, snap.InProgressChapter))
	}
	if snap.InProgressChapter > snap.CompletedCount {
		return true, i18n.Tf("đang viết dở chương %d", snap.InProgressChapter)
	}
	if len(snap.PendingRewrites) > 0 {
		return true, i18n.Tf("còn %d chương chờ viết lại", len(snap.PendingRewrites))
	}
	return false, ""
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// booksRoot trả về thư mục gốc chứa output/, dùng khi cần dựng lại cây thư mục.
func booksRoot(m Model) string {
	return filepath.Dir(filepath.Dir(m.runtime.Dir()))
}
