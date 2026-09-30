package host

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/voocel/agentcore"
)

func testObserver2(t *testing.T, aborting *bool) (*observer, *[]Event) {
	t.Helper()
	var events []Event
	o := testObserver(&events)
	o.isAborting = func() bool { return *aborting }
	return o, &events
}

// TestDungChuYKhongBaoLoi: bấm tạm dừng hay vào đồng sáng tác giữa lúc model đang
// chạy sẽ làm context chết, và thông báo lỗi thành context canceled. Đó là thao tác
// bình thường, không được hiện thành lỗi đỏ — nếu không, mỗi lần tạm dừng đều trông
// như sách hỏng.
func TestDungChuYKhongBaoLoi(t *testing.T) {
	aborting := true
	o, events := testObserver2(t, &aborting)

	o.handleToolUpdate(agentcore.Event{
		Type: agentcore.EventToolExecUpdate,
		Progress: &agentcore.ProgressPayload{
			Agent: "writer", Tool: "save_foundation",
			Kind: agentcore.ProgressToolError, Message: context.Canceled.Error(),
		},
	})

	ev := (*events)[len(*events)-1]
	if ev.Level == "error" {
		t.Errorf("dừng chủ ý mà level = error, mong info: %+v", ev)
	}
	if ev.Failed {
		t.Error("dừng chủ ý mà Failed = true")
	}
	if !strings.Contains(ev.Summary, "đã dừng") {
		t.Errorf("thông báo chưa nói rõ đã dừng: %q", ev.Summary)
	}
}

// TestLoiThatVanBaoDo: nếu không dừng chủ ý, context canceled vẫn phải là lỗi thật.
// Cờ này chỉ được miễn khi đúng lúc dừng, không phải để giấu lỗi.
func TestLoiThatVanBaoDo(t *testing.T) {
	aborting := false
	o, events := testObserver2(t, &aborting)

	o.handleToolUpdate(agentcore.Event{
		Type: agentcore.EventToolExecUpdate,
		Progress: &agentcore.ProgressPayload{
			Agent: "writer", Tool: "save_foundation",
			Kind: agentcore.ProgressToolError, Message: context.Canceled.Error(),
		},
	})

	found := false
	for _, ev := range *events {
		if strings.Contains(ev.Summary, "context canceled") {
			found = true
			if ev.Level != "error" {
				t.Errorf("không dừng chủ ý mà level = %q, mong error", ev.Level)
			}
		}
	}
	if !found {
		t.Fatal("không thấy sự kiện lỗi context canceled")
	}
}

// TestDungChuYBaoInfoKhongDo: dừng chủ ý phải ra mức info, không đỏ, và nói rõ là
// đã dừng chứ không phải lỗi.
func TestDungChuYBaoInfoKhongDo(t *testing.T) {
	aborting := true
	o, events := testObserver2(t, &aborting)

	o.emitCallFinish(&activeCall{id: "e1", summary: "sinh phản hồi", depth: 1},
		"MODEL", "writer", context.Canceled)

	if len(*events) == 0 {
		t.Fatal("không phát sự kiện")
	}
	ev := (*events)[len(*events)-1]
	if ev.Level == "error" {
		t.Errorf("dừng chủ ý mà level = error, mong info: %+v", ev)
	}
	if ev.Failed {
		t.Error("dừng chủ ý mà Failed = true")
	}
	if !strings.Contains(ev.Summary, "đã dừng") {
		t.Errorf("thông báo chưa nói rõ đã dừng: %q", ev.Summary)
	}
	if ev.Kind != "" {
		t.Errorf("dừng chủ ý mà vẫn gắn Kind lỗi: %q", ev.Kind)
	}
}

// TestKhongDungChuYVanBaoLoiThat: cờ bật không đủ, thông điệp phải là context
// canceled. Lỗi khác xảy ra trong lúc dừng vẫn phải báo lỗi.
func TestKhongDungChuYVanBaoLoiThat(t *testing.T) {
	aborting := true
	o, events := testObserver2(t, &aborting)

	o.emitCallFinish(&activeCall{id: "e1", summary: "sinh phản hồi", depth: 1},
		"MODEL", "writer", errors.New("tool argument validation failed"))

	ev := (*events)[len(*events)-1]
	if ev.Level != "error" {
		t.Errorf("lỗi thật lúc dừng mà level = %q, mong error", ev.Level)
	}
	if !ev.Failed {
		t.Error("lỗi thật lúc dừng mà Failed = false")
	}
}

// TestKhongCoCallbackThiBaoLoiThiNghi: observer không có isAborting (chưa nối) thì
// coi như lỗi thật, thiên về báo lỗi hơn là bỏ sót.
func TestKhongCoCallbackThiBaoLoiThiNghi(t *testing.T) {
	var events []Event
	o := testObserver(&events) // không gán isAborting

	o.emitCallFinish(&activeCall{id: "e1", summary: "x", depth: 1},
		"MODEL", "writer", context.Canceled)

	ev := events[len(events)-1]
	if ev.Level != "error" {
		t.Errorf("không có callback mà level = %q, mong error", ev.Level)
	}
}
