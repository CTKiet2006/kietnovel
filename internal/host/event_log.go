package host

import (
	"context"
	"log/slog"
)

// LogEvent records one UI / runtime event through the process logger. Summary is the display text;
// Detail is the full diagnostic payload and is used as the log body whenever it is present.
func LogEvent(ev Event) {
	logEvent(slog.Default(), ev)
}

func logEvent(log *slog.Logger, ev Event) {
	if ev.Summary == "" && ev.Detail == "" {
		return
	}

	message := ev.Detail
	if message == "" {
		message = ev.Summary
	}
	attrs := []any{"module", "event"}
	if ev.Category != "" {
		attrs = append(attrs, "category", ev.Category)
	}
	if ev.Agent != "" {
		attrs = append(attrs, "agent", ev.Agent)
	}
	if ev.Kind != "" {
		attrs = append(attrs, "kind", ev.Kind)
	}
	if ev.ID != "" {
		attrs = append(attrs, "event_id", ev.ID)
		if ev.hasLifecycle() {
			state := "running"
			if !ev.FinishedAt.IsZero() {
				state = "completed"
			}
			attrs = append(attrs, "state", state)
		}
	}
	if ev.Depth > 0 {
		attrs = append(attrs, "depth", ev.Depth)
	}
	if ev.Failed {
		attrs = append(attrs, "failed", true)
	}
	if ev.Duration > 0 {
		attrs = append(attrs, "duration", ev.Duration)
	}
	if ev.Detail != "" && ev.Summary != "" && ev.Detail != ev.Summary {
		attrs = append(attrs, "summary", ev.Summary)
	}

	level := slog.LevelInfo
	switch ev.Level {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	log.Log(context.Background(), level, message, attrs...)
}
