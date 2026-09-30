package host

import (
	"context"
	"fmt"
	"strings"
	"time"

	"encoding/json"
	"github.com/voocel/agentcore"
	"log/slog"
)

// retryPrefix trả về Msg chứ không trả string, để TUI dịch được: điền số sẵn ở
// đây thì "Thử lại (lần 2)" và "Thử lại (lần 3)" là hai chuỗi khác nhau, bảng dịch
// không thể liệt kê hết.
func retryPrefix(attempt, maxRetries int, delay time.Duration) Msg {
	if maxRetries <= 0 {
		if text := formatRetryDelay(delay); text != "" {
			return Msg{Key: "Thử lại (lần %d, %s nữa): ", Args: []any{attempt, text}}
		}
		return Msg{Key: "Thử lại (lần %d): ", Args: []any{attempt}}
	}
	if text := formatRetryDelay(delay); text != "" {
		return Msg{Key: "Thử lại (%d/%d, %s nữa): ", Args: []any{attempt, maxRetries, text}}
	}
	return Msg{Key: "Thử lại (%d/%d): ", Args: []any{attempt, maxRetries}}
}

func formatRetryDelay(delay time.Duration) string {
	if delay <= 0 {
		return ""
	}
	seconds := int64(delay / time.Second)
	if delay%time.Second != 0 {
		seconds++
	}
	if seconds < 1 {
		seconds = 1
	}
	return (time.Duration(seconds) * time.Second).String()
}

func (o *observer) handleThinkingProgress(ev agentcore.Event) {
	agent := ev.Progress.Agent
	thinking := ev.Progress.Thinking
	if agent == "" || thinking == "" {
		return
	}
	o.updateModelState(agent, Msg{Key: "Đang suy nghĩ"})

	prev := o.lastThinkingByAgent[agent]
	delta := thinking
	if strings.HasPrefix(thinking, prev) {
		delta = thinking[len(prev):]
	}
	o.lastThinkingByAgent[agent] = thinking
	if delta == "" {
		return
	}
	o.emitStreamDelta(delta, true)
}

func (o *observer) handleContextProgress(ev agentcore.Event) {
	if ev.Progress == nil || len(ev.Progress.Meta) == 0 {
		return
	}
	var payload struct {
		Tokens        int     `json:"tokens"`
		ContextWindow int     `json:"context_window"`
		Percent       float64 `json:"percent"`
		Scope         string  `json:"scope"`
		Strategy      string  `json:"strategy"`
	}
	if json.Unmarshal(ev.Progress.Meta, &payload) != nil {
		return
	}

	agent := ev.Progress.Agent
	if agent == "" {
		return
	}

	// update the agent snapshot (always visible in the TUI sidebar)
	o.updateAgent(agent, func(a *agentState) {
		a.context = AgentContextSnapshot{
			Tokens:        payload.Tokens,
			ContextWindow: payload.ContextWindow,
			Percent:       payload.Percent,
			Scope:         payload.Scope,
			Strategy:      payload.Strategy,
		}
	})

	level := "info"
	if payload.Percent > 85 {
		level = "warn"
	}
	summary := fmt.Sprintf("%s ngữ cảnh %.0f%% (%d/%d) chiến lược: %s", agent, payload.Percent, payload.Tokens, payload.ContextWindow, payload.Strategy)

	if payload.Strategy != "" {
		// compaction was triggered -> event stream + log
		ctxEv := Event{Time: time.Now(), Category: "SYSTEM", Agent: agent, Summary: summary, Level: level, Depth: 1}
		o.emitEv(ctxEv)
		o.persistEvent(ctxEv)
	} else {
		// ordinary usage report -> log only
		slogLevel := slog.LevelInfo
		if level == "warn" {
			slogLevel = slog.LevelWarn
		}
		slog.Log(context.Background(), slogLevel, summary, "module", "context", "agent", agent)
	}
}
