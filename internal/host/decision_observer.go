package host

import "time"

// runObservedStep 给一次完整的宿主侧 LLM 调用补齐可观察生命周期。
// 只复用现有事件 ID 的开始/结束原地更新机制，不引入额外状态，
// 也不把结构化 JSON 混进 Worker 的实时输出面板。
func runObservedStep[T any](o *observer, category, agent, label string, call func() (T, error)) (T, error) {
	if o == nil {
		return call()
	}
	started := time.Now()
	id := nextEventID()
	o.emitAndLog(Event{
		ID:       id,
		Time:     started,
		Category: category,
		Agent:    agent,
		Summary:  label,
		Level:    "info",
	})

	result, err := call()
	finished := time.Now()
	ev := Event{
		ID:         id,
		Time:       started,
		FinishedAt: finished,
		Failed:     err != nil,
		Category:   category,
		Agent:      agent,
		Summary:    label,
		Level:      "success",
		Duration:   finished.Sub(started),
	}
	if err != nil {
		ev.Level = "error"
		ev.Detail = err.Error()
		ev.Kind = errorKind(err, err.Error())
	}
	o.emitEv(ev)
	o.persistEvent(ev)
	return result, err
}

// runObservedDecision 是 Arbiter 裁定的固定形状：Arbiter 仍是非流式 LLM 函数。
func runObservedDecision[T any](o *observer, label string, call func() (T, error)) (T, error) {
	return runObservedStep(o, "DECISION", "arbiter", label, call)
}
