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
	call := &activeCall{id: nextEventID(), start: now, summary: "Đang chờ model", depth: 1}
	o.modelStarts[agent] = call
	o.emitAndLog(Event{
		ID:       call.id,
		Time:     now,
		Category: "MODEL",
		Agent:    agent,
		Summary:  call.summary,
		Level:    "info",
		Depth:    call.depth,
	})
}

func (o *observer) updateModelState(agent, summary string) {
	call := o.modelStarts[agent]
	if call == nil || summary == "" || call.summary == summary {
		return
	}
	call.summary = summary
	o.emitEv(Event{
		ID:       call.id,
		Time:     call.start,
		Category: "MODEL",
		Agent:    agent,
		Summary:  summary,
		Level:    "info",
		Depth:    call.depth,
	})
}

func (o *observer) finishModelResponse(agent string) {
	call := o.modelStarts[agent]
	if call == nil {
		return
	}
	delete(o.modelStarts, agent)
	call.summary = "Đã có phản hồi model"
	// MODEL là sự kiện quan sát thời gian thực, chỉ ghi log và đẩy UI, không vào hàng đợi của runtime.
	o.emitEv(Event{
		ID:         call.id,
		Time:       call.start,
		FinishedAt: time.Now(),
		Category:   "MODEL",
		Agent:      agent,
		Summary:    call.summary,
		Level:      "success",
		Depth:      call.depth,
		Duration:   time.Since(call.start),
	})
}
