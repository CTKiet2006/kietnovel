package host

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/CTKiet2006/kietnovel/internal/utils"
	"github.com/voocel/agentcore"
)

// handleToolUpdate handles the Worker progress relay (ProgressPayload): TOOL rows, streamed body,
// thinking, retry, context. The Engine feeds it via observer.workerProgress.
func (o *observer) handleToolUpdate(ev agentcore.Event) {
	if ev.Progress == nil {
		return
	}
	switch ev.Progress.Kind {
	case agentcore.ProgressToolDelta:
		if ev.Progress.Delta != "" {
			o.handleSubagentDelta(ev.Progress)
		}
	case agentcore.ProgressToolStart:
		// a tool call inside the Worker (e.g. writer -> draft_chapter).
		if ev.Progress.Agent == "" || ev.Progress.Tool == "" {
			break
		}
		toolName := displayToolName(ev.Progress.Tool, ev.Progress.Args)
		id := nextEventID()
		o.toolStarts[ev.Progress.Agent] = &activeCall{id: id, start: time.Now(), summary: toolName, depth: 1}
		o.emitAndLog(Event{
			ID:       id,
			Time:     time.Now(),
			Category: "TOOL",
			Agent:    ev.Progress.Agent,
			Summary:  toolName,
			Level:    "info",
			Depth:    1,
		})
		o.updateAgent(ev.Progress.Agent, func(a *agentState) {
			a.state = "working"
			a.tool = ev.Progress.Tool
			a.summary = fmt.Sprintf("%s → %s", ev.Progress.Agent, toolName)
		})
		o.emitFallbackStreamHeader(ev.Progress.Tool)
	case agentcore.ProgressToolEnd:
		delete(o.streamExtractors, ev.Progress.Agent)
		if ev.Progress.Agent == "" {
			return
		}
		call, ok := o.toolStarts[ev.Progress.Agent]
		if !ok {
			return
		}
		delete(o.toolStarts, ev.Progress.Agent)
		// same-ID update event: the TUI locates the original TOOL row by ID and fills in FinishedAt / Duration.
		// Summary / Depth go along too, so a runtime queue replay can restore the complete row.
		finishEv := Event{
			ID:         call.id,
			Time:       call.start,
			FinishedAt: time.Now(),
			Category:   "TOOL",
			Agent:      ev.Progress.Agent,
			Summary:    call.summary,
			Level:      "info",
			Depth:      call.depth,
			Duration:   time.Since(call.start),
		}
		o.emitEv(finishEv)
		o.persistEvent(finishEv)
	case agentcore.ProgressThinking:
		o.handleThinkingProgress(ev)
	case agentcore.ProgressRetry:
		// only show the wait time explicitly reported upstream, so a local estimate cannot disagree with the real backoff rhythm.
		// Summary does not embed a static delay - the UI counts down from RetryAt every second; Detail / the log keep the delay snapshot from emission time.
		delay := retryProgressDelay(ev.Progress)
		retryEv := Event{
			ID:         o.retryEventID(ev.Progress.Agent, ev.Progress.Attempt),
			Time:       time.Now(),
			Category:   "SYSTEM",
			Agent:      ev.Progress.Agent,
			Summary:    retryPrefix(ev.Progress.Attempt, ev.Progress.MaxRetries, 0).String() + utils.TruncateRunes(ev.Progress.Message, 80),
			SummaryMsg: Ptr(retryPrefix(ev.Progress.Attempt, ev.Progress.MaxRetries, 0)),
			Detail:     retryPrefix(ev.Progress.Attempt, ev.Progress.MaxRetries, delay).String() + ev.Progress.Message,
			Kind:       errorKind(nil, ev.Progress.Message),
			Level:      "warn",
			Depth:      1,
		}
		if delay > 0 {
			retryEv.RetryAt = retryEv.Time.Add(delay)
		}
		o.emitEv(retryEv)
		o.persistEvent(retryEv)
	case agentcore.ProgressToolError:
		delete(o.streamExtractors, ev.Progress.Agent)
		msg := ev.Progress.Message
		if msg == "" {
			msg = "unknown error"
		}
		// If a TOOL row is in progress, mark it failed in place and put the full error into Detail.
		// One failure only produces one ERROR-level event, so that the TOOL failed state plus the added ERROR
		// detail is not misread in tui.log as two separate faults.
		if call, ok := o.toolStarts[ev.Progress.Agent]; ok {
			delete(o.toolStarts, ev.Progress.Agent)
			// Dừng chủ ý giữa chừng (tạm dừng, vào đồng sáng tác, chạm trần
			// ngân sách) không phải lỗi: đánh dấu ✕ đỏ sẽ làm thao tác bình
			// thường trông như hỏng. Báo "đã dừng" ở mức info.
			stopped := o.stoppedOnPurpose(msg)
			detail := fmt.Sprintf("%s lỗi: %s", ev.Progress.Tool, msg)
			if stopped {
				detail = fmt.Sprintf("%s đã dừng: %s", ev.Progress.Tool, msg)
			}
			finishEv := Event{
				ID:         call.id,
				Time:       call.start,
				FinishedAt: time.Now(),
				Failed:     !stopped,
				Category:   "TOOL",
				Agent:      ev.Progress.Agent,
				Summary:    fmt.Sprintf("%s lỗi: %s", call.summary, utils.TruncateRunes(msg, 100)),
				SummaryMsg: Ptr(Msg{Key: "%s lỗi: %s", Args: []any{call.summary, utils.TruncateRunes(msg, 100)}}),
				Detail:     detail,
				Kind:       errorKind(nil, msg),
				Level:      "error",
				Depth:      call.depth,
				Duration:   time.Since(call.start),
			}
			if stopped {
				finishEv.Summary = fmt.Sprintf("%s đã dừng: %s", call.summary, utils.TruncateRunes(msg, 100))
				finishEv.SummaryMsg = Ptr(Msg{Key: "%s đã dừng: %s",
					Args: []any{call.summary, utils.TruncateRunes(msg, 100)}})
				finishEv.Level = "info"
				finishEv.Kind = ""
			}
			o.emitEv(finishEv)
			o.persistEvent(finishEv)
			return
		}
		// The rare progress stream without a start cannot be updated in place, so keep a separate ERROR event to expose the fault.
		stopped := o.stoppedOnPurpose(msg)
		summary := fmt.Sprintf("%s lỗi: %s", ev.Progress.Tool, utils.TruncateRunes(msg, 100))
		summaryKey := "%s lỗi: %s"
		level := "error"
		if stopped {
			summary = fmt.Sprintf("%s đã dừng: %s", ev.Progress.Tool, utils.TruncateRunes(msg, 100))
			summaryKey = "%s đã dừng: %s"
			level = "info"
		}
		errEv := Event{
			Time:       time.Now(),
			Category:   "ERROR",
			Agent:      ev.Progress.Agent,
			Summary:    summary,
			SummaryMsg: Ptr(Msg{Key: summaryKey, Args: []any{ev.Progress.Tool, utils.TruncateRunes(msg, 100)}}),
			Detail:     fmt.Sprintf("%s lỗi: %s", ev.Progress.Tool, msg),
			Kind:       errorKind(nil, msg),
			Level:      level,
			Depth:      1,
		}
		o.emitEv(errEv)
		o.persistEvent(errEv)
	case agentcore.ProgressContext:
		o.handleContextProgress(ev)
	}
}

func retryProgressDelay(p *agentcore.ProgressPayload) time.Duration {
	if p == nil {
		return 0
	}
	if len(p.Meta) > 0 {
		var meta struct {
			DelayMS int64 `json:"retry_delay_ms"`
		}
		if json.Unmarshal(p.Meta, &meta) == nil && meta.DelayMS > 0 {
			return time.Duration(meta.DelayMS) * time.Millisecond
		}
	}
	return 0
}

func dispatchSummary(agent, task string) string {
	if agent == "" {
		agent = "subagent"
	}
	if task == "" {
		return agent
	}
	firstLine := strings.TrimSpace(strings.SplitN(task, "\n", 2)[0])
	if firstLine == "" {
		return agent
	}
	return agent + " (" + utils.TruncateRunes(firstLine, 30) + ")"
}

// dispatchDetail dựng phần Detail của sự kiện DISPATCH. Detail là field log đầy đủ
// (TUI không đọc), nên ở đây dùng tiếng Việt thường, không qua i18n — host không
// import i18n, và dịch ở tầng log chỉ thêm rắc rối cho không ai đọc.
func dispatchDetail(task, reason string) string {
	var parts []string
	if strings.TrimSpace(reason) != "" {
		parts = append(parts, "Lý do phân công: "+reason)
	}
	if strings.TrimSpace(task) != "" {
		parts = append(parts, "Nhiệm vụ đầy đủ:\n"+task)
	}
	return strings.Join(parts, "\n")
}

func (o *observer) emitCallFinish(call *activeCall, category, agentName string, callErr error) {
	if call == nil {
		return
	}
	failed := callErr != nil
	// Dừng chủ ý làm context chết, nên callErr thành context canceled. Đó không
	// phải lỗi — báo "đã dừng" ở mức info, không tô đỏ, không gắn Kind lỗi.
	stopped := failed && o.stoppedOnPurpose(callErr.Error())
	level := "success"
	if failed && !stopped {
		level = "error"
	}
	summary := call.summary
	summaryMsg := call.summaryMsg
	detail := ""
	kind := ""
	if failed {
		detail = callErr.Error()
		kind = errorKind(callErr, detail)
		summary = fmt.Sprintf("%s lỗi: %s", call.summary, utils.TruncateRunes(detail, 100))
		summaryMsg = Msg{Key: "%s lỗi: %s", Args: []any{call.summary, utils.TruncateRunes(detail, 100)}}
	}
	if stopped {
		summary = fmt.Sprintf("%s đã dừng: %s", call.summary, utils.TruncateRunes(detail, 100))
		summaryMsg = Msg{Key: "%s đã dừng: %s", Args: []any{call.summary, utils.TruncateRunes(detail, 100)}}
		kind = ""
	}
	finishEv := Event{
		ID:         call.id,
		Time:       call.start,
		FinishedAt: time.Now(),
		Failed:     failed && !stopped,
		Category:   category,
		Agent:      agentName,
		Summary:    summary,
		SummaryMsg: Ptr(summaryMsg),
		Detail:     detail,
		Kind:       kind,
		Level:      level,
		Depth:      call.depth,
		Duration:   time.Since(call.start),
	}
	o.emitEv(finishEv)
	o.persistEvent(finishEv)
}

func displayToolName(tool string, args json.RawMessage) string {
	if len(args) == 0 {
		return tool
	}
	switch tool {
	case "save_foundation":
		var p struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(args, &p) == nil && p.Type != "" {
			return fmt.Sprintf("%s[%s]", tool, p.Type)
		}
	case "commit_chapter", "plan_chapter", "draft_chapter", "check_consistency":
		var p struct {
			Chapter int `json:"chapter"`
		}
		if json.Unmarshal(args, &p) == nil && p.Chapter > 0 {
			return fmt.Sprintf("%s (chương %d)", tool, p.Chapter)
		}
	case "save_review":
		var p struct {
			Chapter int    `json:"chapter"`
			Scope   string `json:"scope"`
			Verdict string `json:"verdict"`
		}
		if json.Unmarshal(args, &p) == nil {
			label := ""
			switch p.Scope {
			case "arc":
				label = "cung này"
			case "global":
				label = "toàn cục"
			default:
				if p.Chapter > 0 {
					label = fmt.Sprintf("chương %d", p.Chapter)
				}
			}
			if label == "" {
				return tool
			}
			if p.Verdict != "" {
				return fmt.Sprintf("%s(%s·%s)", tool, label, p.Verdict)
			}
			return fmt.Sprintf("%s(%s)", tool, label)
		}
	case "novel_context":
		var p struct {
			Chapter int `json:"chapter"`
		}
		if json.Unmarshal(args, &p) == nil && p.Chapter > 0 {
			return fmt.Sprintf("%s (chương %d)", tool, p.Chapter)
		}
	case "read_chapter":
		var p struct {
			Chapter   int    `json:"chapter"`
			Source    string `json:"source"`
			Character string `json:"character"`
		}
		if json.Unmarshal(args, &p) == nil && p.Chapter > 0 {
			suffix := ""
			if p.Character != "" {
				suffix = "· đối thoại " + p.Character
			} else if p.Source == "draft" {
				suffix = "· bản nháp"
			}
			return fmt.Sprintf("%s (chương %d%s)", tool, p.Chapter, suffix)
		}
	}
	return tool
}
