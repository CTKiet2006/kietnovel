package host

import "time"

// runObservedStep gives a full host-side LLM call a complete observable lifecycle.
// It only reuses the existing in-place start/finish update mechanism keyed by event ID,
// introduces no extra state, and does not mix structured JSON into the Worker's live output panel.
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

// runObservedDecision is the fixed shape of an Arbiter verdict: the Arbiter is still a non-streaming LLM call.
func runObservedDecision[T any](o *observer, label string, call func() (T, error)) (T, error) {
	return runObservedStep(o, "DECISION", "arbiter", label, call)
}
