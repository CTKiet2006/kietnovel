package tui

import (
	"context"
	"errors"
	"fmt"
	"github.com/CTKiet2006/kietnovel/internal/i18n"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/CTKiet2006/kietnovel/internal/bootstrap"
	"github.com/CTKiet2006/kietnovel/internal/diag"
	"github.com/CTKiet2006/kietnovel/internal/host"
	"github.com/CTKiet2006/kietnovel/internal/store"
	buildversion "github.com/CTKiet2006/kietnovel/internal/version"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// Các loại message
type (
	eventMsg       host.Event
	snapshotMsg    host.UISnapshot
	doneMsg        struct{ complete bool } // complete=true cả sách xong, false dừng do lỗi
	abortResultMsg struct{ stopped bool }
	bootstrapMsg   struct {
		existing  bool // Đã có tác phẩm; dù khôi phục thành công hay không đều vào bàn làm việc
		resumed   bool
		completed bool // Trong thư mục là sách đã hoàn tất: vào bàn làm việc trạng thái xong, không về trang chào
		err       error
	}
	reportLoadedMsg struct {
		reqID      int
		report     diag.Report
		exportPath string // Đường dẫn tuyệt đối file chẩn đoán đã khử nhạy cảm; rỗng = xuất thất bại
		exportErr  error
		finishedAt time.Time
	}
	startResultMsg   struct{ err error }
	cocreateDeltaMsg struct {
		reqID int
		kind  string // host.CoCreateProgressThinking | host.CoCreateProgressReply
		text  string
	}
	// cocreateStreamItem là payload nội bộ của deltaCh, gửi kèm kind trực tiếp với văn bản tích lũy về TUI.
	cocreateStreamItem struct {
		kind string
		text string
	}
	cocreateDoneMsg struct {
		reqID int
		reply host.CoCreateReply
		err   error
	}
	steerResultMsg      struct{ err error }
	continueResultMsg   struct{ err error }
	spinnerTickMsg      time.Time
	eventSpinnerTickMsg time.Time // tick spinner của sự kiện đang tiến hành ở luồng sự kiện (nhanh hơn, độc lập với topbar/ngôi sao)
	streamDeltaMsg      string    // delta token trực tiếp
	streamClearMsg      struct{}  // Xóa bộ đệm trực tiếp (message mới bắt đầu)
	streamFlushTickMsg  struct{}  // Gộp đẩy output trực tiếp (chỉ đặt timer khi có dữ liệu chờ đẩy)
	quitResetMsg        struct{}  // Đặt lại hết giờ nhấn đôi Ctrl+C
	updateCheckMsg      struct {
		result *buildversion.CheckResult
		err    error
	}
)

// --- Hàm Cmd ---

// checkForUpdate hỏi upstream phiên bản mới dưới nền (timeout 5s, cache 24h điều tiết). Lỗi theo message
// trả về, Update ghi log chứ không làm phiền giao diện user.
func checkForUpdate(currentVersion string) tea.Cmd {
	return func() tea.Msg {
		configDir := bootstrap.DefaultConfigDir()
		if configDir == "" {
			return updateCheckMsg{err: errors.New(i18n.T("Không xác định được thư mục cache kiểm tra cập nhật"))}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		res, err := buildversion.CheckUpdate(ctx, buildversion.CheckOptions{
			CurrentVersion: currentVersion,
			CachePath:      filepath.Join(configDir, "update-check.json"),
		})
		return updateCheckMsg{result: res, err: err}
	}
}

// updateNotesPreviewWidth là độ rộng tóm tắt một dòng dùng chung cho trang chào và luồng sự kiện. Văn bản release
// từ xa không vào thẳng terminal: gỡ ký tự ANSI/điều khiển trước, rồi cắt tường minh, tránh chuỗi điều khiển terminal và dòng quá dài.
const updateNotesPreviewWidth = 56

func formatUpdateNotice(result *buildversion.CheckResult) string {
	notice := fmt.Sprintf(i18n.T("Phiên bản mới %s đã phát hành"), result.Latest)
	if preview := updateNotesPreview(result.Notes); preview != "" {
		notice += " · " + preview
	}
	// Tên lệnh nâng cấp phải khớp tên binary, nên đưa vào bản dịch bằng %s thay vì
	// ghép chuỗi. Giữ cả câu làm một đơn vị dịch vì thứ tự từ khác nhau giữa các
	// ngôn ngữ, tách mảnh ra ghép lại sẽ ra câu vỡ.
	return notice + " " + i18n.Tf("· Chạy %s update để nâng cấp", buildversion.AppName)
}

func updateNotesPreview(notes string) string {
	plain := ansi.Strip(notes)
	plain = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return -1
		}
		return r
	}, plain)
	for _, rawLine := range strings.Split(plain, "\n") {
		line := strings.TrimSpace(rawLine)
		line = strings.TrimSpace(strings.TrimLeft(line, "#>*-"))
		line = strings.Join(strings.Fields(line), " ")
		if line != "" {
			return truncate(line, updateNotesPreviewWidth)
		}
	}
	return ""
}

func listenEvents(rt *host.Host) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-rt.Events()
		if !ok {
			return nil
		}
		return eventMsg(ev)
	}
}

func listenDone(rt *host.Host) tea.Cmd {
	return func() tea.Msg {
		_, ok := <-rt.Done()
		if !ok {
			return nil
		}
		snap := rt.Snapshot()
		return doneMsg{complete: snap.Phase == "complete"}
	}
}

func tickSnapshot(rt *host.Host) tea.Cmd {
	return tea.Tick(3*time.Second, func(t time.Time) tea.Msg {
		return snapshotMsg(rt.Snapshot())
	})
}

func fetchSnapshot(rt *host.Host) tea.Cmd {
	return func() tea.Msg {
		return snapshotMsg(rt.Snapshot())
	}
}

func bootstrapRuntime(rt *host.Host) tea.Cmd {
	return func() tea.Msg {
		snapshot := rt.Snapshot()
		msg := bootstrapMsg{
			existing:  snapshot.Phase != "" || snapshot.BookTitle != "",
			completed: snapshot.Phase == "complete",
		}
		label, err := rt.Resume()
		if err != nil {
			msg.err = err
			return msg
		}
		if label.Empty() {
			if msg.existing {
				return msg
			}
			return nil
		}
		msg.resumed = true
		return msg
	}
}

// resumeBook chạy bù một lần cổng khôi phục trong phiên (bootstrap Resume chỉ chạy một lần lúc khởi động):
// đóng panel khi import xong, /reopen mở lại đều nhờ nó về lại bàn làm việc sáng tác.
// Không phát lại hàng đợi sự kiện —— sự kiện phiên này listenEvents thường trú đã hiện qua,
// phát lại sẽ echo trùng. Can thiệp chờ xử lý (như hướng viết tiếp mà /reopen đăng ký)
// do Resume đưa qua Arbiter phán quyết tiêu hóa trước, rồi mới chạy tiếp engine.
func resumeBook(rt *host.Host) tea.Cmd {
	return func() tea.Msg {
		snapshot := rt.Snapshot()
		label, err := rt.Resume()
		return bootstrapMsg{
			existing: snapshot.Phase != "" || snapshot.BookTitle != "", completed: snapshot.Phase == "complete",
			resumed: !label.Empty(), err: err,
		}
	}
}

func startRuntime(rt *host.Host, prompt string) tea.Cmd {
	return func() tea.Msg {
		// Phía khởi động xác định sinh snapshot quy tắc user của sách này (chuẩn hóa từ prompt gốc), phải trước StartPrepared.
		if err := rt.PrepareUserRules(prompt); err != nil {
			return startResultMsg{err: err}
		}
		err := rt.StartPrepared(prompt)
		return startResultMsg{err: err}
	}
}

func runCoCreate(rt *host.Host, state *cocreateState) tea.Cmd {
	history := state.session.History()
	ctx, cancel := context.WithCancel(context.Background())
	state.cancel = cancel
	state.deltaCh = make(chan cocreateStreamItem, 64)
	state.doneCh = make(chan cocreateDoneMsg, 1)
	// Đồng sáng tác theo giai đoạn mang tóm tắt trạng thái truyện, cho ra "brief hướng tiếp theo";
	// khởi động lạnh thì làm rõ nhu cầu từ số 0. Hai nhánh cùng chữ ký.
	stream := rt.CoCreateStream
	if state.stage {
		stream = rt.StageCoCreateStream
	}
	start := func() tea.Msg {
		go func() {
			reply, err := stream(ctx, history, func(kind, text string) {
				select {
				case state.deltaCh <- cocreateStreamItem{kind: kind, text: text}:
				default:
				}
			})
			state.doneCh <- cocreateDoneMsg{reply: reply, err: err}
			close(state.deltaCh)
			close(state.doneCh)
		}()
		return nil
	}
	return tea.Batch(start, listenCoCreateDelta(state), listenCoCreateDone(state))
}

func listenCoCreateDelta(state *cocreateState) tea.Cmd {
	if state == nil || state.deltaCh == nil {
		return nil
	}
	// Chụp tham chiếu cục bộ của channel: tránh listen cũ đọc nhầm channel mới khi state.deltaCh bị gán lại
	// sau này (dù luồng hiện tại không chạm tới, vẫn giữ để khỏi thành bẫy bảo trì).
	reqID := state.reqID
	ch := state.deltaCh
	return func() tea.Msg {
		item, ok := <-ch
		if !ok {
			return nil
		}
		return cocreateDeltaMsg{reqID: reqID, kind: item.kind, text: item.text}
	}
}

func listenCoCreateDone(state *cocreateState) tea.Cmd {
	if state == nil || state.doneCh == nil {
		return nil
	}
	reqID := state.reqID
	ch := state.doneCh
	return func() tea.Msg {
		result, ok := <-ch
		if !ok {
			return nil
		}
		result.reqID = reqID
		return result
	}
}

func steerRuntime(rt *host.Host, text string) tea.Cmd {
	return func() tea.Msg {
		return steerResultMsg{err: rt.Steer(text)}
	}
}

func continueRuntime(rt *host.Host, text string) tea.Cmd {
	return func() tea.Msg {
		err := rt.Continue(text)
		return continueResultMsg{err: err}
	}
}

// resumeFromCoCreate bơm brief hướng tiếp theo do đồng sáng tác giai đoạn cho ra rồi khôi phục sáng tác.
// Tái dùng continueResultMsg: thành công thì nối listenDone chạy tiếp, thất bại echo lỗi.
func resumeFromCoCreate(rt *host.Host, draft string) tea.Cmd {
	return func() tea.Msg {
		err := rt.ResumeFromCoCreate(draft)
		return continueResultMsg{err: err}
	}
}

// cancelCoCreate bỏ đồng sáng tác giai đoạn: xóa cờ chiếm giữ, giữ tạm dừng. Sự kiện chảy về qua kênh events, không cần trả message.
func cancelCoCreate(rt *host.Host) tea.Cmd {
	return func() tea.Msg {
		rt.CancelCoCreate()
		return nil
	}
}

func abortRuntime(rt *host.Host) tea.Cmd {
	return func() tea.Msg {
		return abortResultMsg{stopped: rt.Abort()}
	}
}

func loadReport(dir string, reqID int) tea.Cmd {
	return func() tea.Msg {
		s := store.NewStore(dir)
		// Diagnose = chẩn đoán sáng tác + phát hiện runtime, Finding runtime cũng vào báo cáo trên màn hình.
		rep, rc := diag.Diagnose(s)
		// Tái dùng rep+rc ghi file chẩn đoán đã khử nhạy cảm (xuất thất bại không ảnh hưởng báo cáo trên màn hình).
		exportPath, exportErr := diag.WriteExport(s, rep, rc)
		return reportLoadedMsg{
			reqID:      reqID,
			report:     rep,
			exportPath: exportPath,
			exportErr:  exportErr,
			finishedAt: time.Now(),
		}
	}
}

func tickSpinner() tea.Cmd {
	return tea.Tick(350*time.Millisecond, func(t time.Time) tea.Msg {
		return spinnerTickMsg(t)
	})
}

// tickEventSpinner đẩy spinner của dòng "đang tiến hành" ở luồng sự kiện. Độc lập với tickSpinner, nhịp nhanh hơn (150ms).
func tickEventSpinner() tea.Cmd {
	return tea.Tick(150*time.Millisecond, func(t time.Time) tea.Msg {
		return eventSpinnerTickMsg(t)
	})
}

// tickStreamFlush gộp delta trực tiếp trong một cửa sổ 16ms. Do delta chờ đẩy đầu tiên khởi động,
// đẩy xong thì dừng, lúc rảnh không đánh thức TUI liên tục.
func tickStreamFlush() tea.Cmd {
	return tea.Tick(16*time.Millisecond, func(t time.Time) tea.Msg {
		return streamFlushTickMsg{}
	})
}

func listenStream(rt *host.Host) tea.Cmd {
	return func() tea.Msg {
		delta, ok := <-rt.Stream()
		if !ok {
			return nil
		}
		// sentinel được phát thành streamClearMsg, bảo đảm cùng delta thường tới TUI
		// theo thứ tự emit trên cùng kênh. Hai kênh thì clearCh với streamCh mất thứ tự,
		// header ✻ hay bị nhét nhầm vào cuối đoạn thinking trước đó.
		if delta == host.StreamClearSentinel {
			return streamClearMsg{}
		}
		return streamDeltaMsg(delta)
	}
}
