package logger

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

func newTextHandler(w io.Writer, level slog.Level) slog.Handler {
	return slog.NewTextHandler(w, &slog.HandlerOptions{
		Level: level,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			// Keeps the date, milliseconds and time zone, so logs appended across processes still line up exactly with code version and session.
			if a.Key == slog.TimeKey {
				a.Value = slog.StringValue(a.Value.Time().Format("2006-01-02T15:04:05.000Z07:00"))
			}
			return a
		},
	})
}

func newSessionLogger(w io.Writer, level slog.Level, sessionAttrs ...slog.Attr) (*slog.Logger, string) {
	sessionID := fmt.Sprintf("%s-p%d", time.Now().Format("20060102T150405.000Z0700"), os.Getpid())
	attrs := make([]slog.Attr, 0, len(sessionAttrs)+1)
	attrs = append(attrs, slog.String("session", sessionID))
	attrs = append(attrs, sessionAttrs...)
	handler := newTextHandler(w, level).WithAttrs(attrs)
	return slog.New(handler), sessionID
}

// FileLogger returns an independent logger writing to outputDir/logs/filename together with a cleanup func,
// for subsystems that need their own log file (such as the import flow). A failed open falls back to the default logger without breaking the run,
// but the error must still be returned so the caller can surface it -- otherwise the UI points the user at a log file that does not exist.
func FileLogger(outputDir, filename string) (*slog.Logger, func(), error) {
	f, err := openLogFile(outputDir, filename)
	if err != nil {
		return slog.Default(), func() {}, err
	}
	logger, sessionID := newSessionLogger(f, slog.LevelDebug)
	logger.Info("日志会话开始", "module", "logger", "session_id", sessionID)
	return logger, func() {
		logger.Info("日志会话结束", "module", "logger", "session_id", sessionID)
		_ = f.Close()
	}, nil
}

// SetupFile points the default logger at a file and returns a cleanup func.
// With alsoStderr=true it also writes to stderr.
// It returns an error when the log directory or file cannot be opened and the caller must handle it explicitly; falling back to
// io.Discard and carrying on is forbidden, or the whole run log is lost exactly when troubleshooting matters most.
func SetupFile(outputDir, filename string, alsoStderr bool, sessionAttrs ...slog.Attr) (func(), error) {
	f, err := openLogFile(outputDir, filename)
	if err != nil {
		return nil, err
	}

	var w io.Writer = f
	if alsoStderr {
		w = io.MultiWriter(os.Stderr, f)
	}
	previous := slog.Default()
	logger, sessionID := newSessionLogger(w, slog.LevelDebug, sessionAttrs...)
	slog.SetDefault(logger)
	logger.Info("日志会话开始", "module", "logger", "session_id", sessionID)

	return func() {
		logger.Info("日志会话结束", "module", "logger", "session_id", sessionID)
		slog.SetDefault(previous)
		_ = f.Close()
	}, nil
}

func openLogFile(outputDir, filename string) (*os.File, error) {
	logPath := filepath.Join(outputDir, "logs", filename)
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return nil, fmt.Errorf("create log directory %q: %w", filepath.Dir(logPath), err)
	}

	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open log file %q: %w", logPath, err)
	}
	return f, nil
}
