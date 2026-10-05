package tui

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/voocel/ainovel-cli/internal/bootstrap"
	"github.com/voocel/ainovel-cli/internal/diag"
	"github.com/voocel/ainovel-cli/internal/host"
	"github.com/voocel/ainovel-cli/internal/store"
	buildversion "github.com/voocel/ainovel-cli/internal/version"
)

// 消息类型
type (
	eventMsg       host.Event
	snapshotMsg    host.UISnapshot
	doneMsg        struct{ complete bool } // complete=true 全书完成，false 出错停止
	abortResultMsg struct{ stopped bool }
	bootstrapMsg   struct {
		existing  bool // 已有作品；无论恢复是否成功都应进入工作台
		resumed   bool
		completed bool // 目录里是本已完结的书：落完成态工作台而非欢迎页
		err       error
	}
	reportLoadedMsg struct {
		reqID      int
		report     diag.Report
		exportPath string // 脱敏诊断文件绝对路径；空 = 导出失败
		exportErr  error
		finishedAt time.Time
	}
	startResultMsg   struct{ err error }
	cocreateDeltaMsg struct {
		reqID int
		kind  string // host.CoCreateProgressThinking | host.CoCreateProgressReply
		text  string
	}
	// cocreateStreamItem 是 deltaCh 内部载荷，把流式 kind 与累积文本一起送达 TUI。
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
	eventSpinnerTickMsg time.Time // 事件流进行中事件的 spinner tick（更快、独立于顶栏/星星）
	streamDeltaMsg      string    // 流式 token 增量
	streamClearMsg      struct{}  // 清空流式缓冲（新消息开始）
	streamFlushTickMsg  struct{}  // 流式刷新节流（仅有待刷数据时调度）
	quitResetMsg        struct{}  // 双次 Ctrl+C 超时重置
	updateCheckMsg      struct {
		result *buildversion.CheckResult
		err    error
	}
)

// --- Cmd 函数 ---

// checkForUpdate 后台查询上游新版本（5s 超时，24h 缓存节流）。错误随消息
// 返回，由 Update 写日志但不打扰用户界面。
func checkForUpdate(currentVersion string) tea.Cmd {
	return func() tea.Msg {
		configDir := bootstrap.DefaultConfigDir()
		if configDir == "" {
			return updateCheckMsg{err: fmt.Errorf("无法确定更新检查缓存目录")}
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

// updateNotesPreviewWidth 是欢迎页与事件流共用的单行摘要宽度。远端 release
// 文本不直接进入终端：先移除 ANSI/控制字符，再显式截断，避免终端控制序列和超长行。
const updateNotesPreviewWidth = 56

func formatUpdateNotice(result *buildversion.CheckResult) string {
	notice := fmt.Sprintf("新版本 %s 已发布", result.Latest)
	if preview := updateNotesPreview(result.Notes); preview != "" {
		notice += " · " + preview
	}
	return notice + " · 运行 ainovel-cli update 升级"
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
		if label == "" {
			if msg.existing {
				return msg
			}
			return nil
		}
		msg.resumed = true
		return msg
	}
}

// resumeBook 会话中补跑一次恢复门禁（bootstrap 的 Resume 只在启动时跑一次）：
// 导入完成关面板、/reopen 重开后都靠它落回创作工作台。不重放事件队列——本会话事件
// 已由常驻 listenEvents 呈现过，重放会重复回显。待处理干预（如 /reopen 登记的续写
// 方向）由 Resume 先经 Arbiter 裁定消化，再续跑引擎。
func resumeBook(rt *host.Host) tea.Cmd {
	return func() tea.Msg {
		snapshot := rt.Snapshot()
		label, err := rt.Resume()
		return bootstrapMsg{
			existing: snapshot.Phase != "" || snapshot.BookTitle != "", completed: snapshot.Phase == "complete",
			resumed: label != "", err: err,
		}
	}
}

func startRuntime(rt *host.Host, prompt string) tea.Cmd {
	return func() tea.Msg {
		// 启动侧确定性生成本书用户规则快照（用原始 prompt 归一化），须在 StartPrepared 前。
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
	// 阶段共创带故事状态摘要、产出"后续方向 brief"；冷启动从零澄清需求。两者签名一致。
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
	// 抓取 channel 局部引用：避免后续 state.deltaCh 被 reassign 时
	// 旧 listen 闭包错读新 channel（虽然当前流程不触发，留作维护陷阱不应该）。
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

// resumeFromCoCreate 把阶段共创产出的后续方向 brief 注入并恢复创作。
// 复用 continueResultMsg：成功即接 listenDone 续跑，失败回显错误。
func resumeFromCoCreate(rt *host.Host, draft string) tea.Cmd {
	return func() tea.Msg {
		err := rt.ResumeFromCoCreate(draft)
		return continueResultMsg{err: err}
	}
}

// cancelCoCreate 放弃阶段共创：清占用标记、保持暂停。事件经 events 通道回流，无需返回消息。
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
		// Diagnose = 创作诊断 + 运行时检测，运行时 Finding 也进屏上报告。
		rep, rc := diag.Diagnose(s)
		// 复用 rep+rc 写出脱敏诊断文件（导出失败不影响屏上报告）。
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

// tickEventSpinner 驱动事件流"进行中"行的 spinner。独立于 tickSpinner，节奏更快（150ms）。
func tickEventSpinner() tea.Cmd {
	return tea.Tick(150*time.Millisecond, func(t time.Time) tea.Msg {
		return eventSpinnerTickMsg(t)
	})
}

// tickStreamFlush 合并一个 16ms 窗口内的流式增量。它由首个待刷 delta 启动，
// 刷完即停止，空闲时不会持续唤醒 TUI。
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
		// sentinel 派发为 streamClearMsg，保证与正常 delta 在同一通道里按 emit
		// 顺序到达 TUI。双通道时 clearCh 与 streamCh 之间无序，✻ header 经常被
		// 错塞到上一段 thinking 末尾。
		if delta == host.StreamClearSentinel {
			return streamClearMsg{}
		}
		return streamDeltaMsg(delta)
	}
}
