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
			// 保留日期、毫秒和时区；日志跨进程追加时仍能准确对齐代码版本与会话。
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

// FileLogger 返回写入 outputDir/logs/filename 的独立 logger 与清理函数，
// 供需要独立日志文件的子系统（如导入流程）使用。打开失败回退默认 logger 不中断业务，
// 但错误必须返回给调用方向用户呈现——否则 UI 指引用户去看一个并不存在的日志文件。
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

// SetupFile 初始化默认 logger 到文件，返回清理函数。
// alsoStderr=true 时同时输出到 stderr。
// 日志目录或文件无法打开时返回错误，调用方必须显式处理；禁止切到 io.Discard
// 后继续运行，否则恰好在最需要排障时丢失全部运行日志。
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
