package host

import (
	"time"

	"github.com/voocel/agentcore"
)

// handleWorkerEvent chiếu phản hồi model theo đúng ranh giới thật của AgentLoop:
// TurnStart xảy ra trước khi gửi request, assistant MessageEnd xảy ra trước khi chạy tool.
func (o *observer) handleWorkerEvent(agent string, ev agentcore.Event) {
	switch ev.Type {
	case agentcore.EventTurnStart:
		o.startModelResponse(agent)
	case agentcore.EventMessageEnd:
		if ev.Message != nil && ev.Message.GetRole() == agentcore.RoleAssistant {
			o.finishModelResponse(agent)
		}
	}
}

func (o *observer) startModelResponse(agent string) {
	if agent == "" {
		return
	}
	now := time.Now()
	call := &activeCall{
		id: nextEventID(), start: now, depth: 1,
		summary: modelStateWaiting.String(), summaryMsg: modelStateWaiting,
	}
	o.modelStarts[agent] = call
	o.emitAndLog(Event{
		ID:         call.id,
		Time:       now,
		Category:   "MODEL",
		Agent:      agent,
		Summary:    call.summary,
		SummaryMsg: &call.summaryMsg,
		Level:      "info",
		Depth:      call.depth,
	})
}

// modelStateLabels là nhãn trạng thái của một lần gọi model. Dùng hằng số thay vì
// chuỗi rời rạc để so khớp nhanh: trùng Key là trùng nhãn, không cần phát lại event.
var (
	modelStateWaiting = Msg{Key: "Đang chờ model"}
	modelStateReply   = Msg{Key: "Sinh phản hồi"}
	modelStateDone    = Msg{Key: "Đã có phản hồi model"}
)

// updateModelState đổi nhãn trạng thái của một lần gọi model. Nhận Msg chứ không
// nhận string: đây là nhãn hiển thị, cần dịch được ở vi/en/zh, nên phải giữ
// format string và tham số tách rời thay vì điền sẵn rồi mới gửi.
func (o *observer) updateModelState(agent string, msg Msg) {
	call := o.modelStarts[agent]
	if call == nil || msg.Empty() || call.summaryMsg.Key == msg.Key {
		return
	}
	call.summaryMsg = msg
	o.emitEv(Event{
		ID:         call.id,
		Time:       call.start,
		Category:   "MODEL",
		Agent:      agent,
		Summary:    msg.String(),
		SummaryMsg: &msg,
		Level:      "info",
		Depth:      call.depth,
	})
}

func (o *observer) finishModelResponse(agent string) {
	call := o.modelStarts[agent]
	if call == nil {
		return
	}
	delete(o.modelStarts, agent)
	call.summary, call.summaryMsg = modelStateDone.String(), modelStateDone
	// MODEL là sự kiện quan sát thời gian thực, chỉ ghi log và đẩy UI, không vào hàng đợi của runtime.
	o.emitEv(Event{
		ID:         call.id,
		Time:       call.start,
		FinishedAt: time.Now(),
		Category:   "MODEL",
		Agent:      agent,
		Summary:    call.summary,
		SummaryMsg: &call.summaryMsg,
		Level:      "success",
		Depth:      call.depth,
		Duration:   time.Since(call.start),
	})
}
