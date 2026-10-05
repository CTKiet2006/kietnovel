package logger

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestSetupFileWritesDefaultLog(t *testing.T) {
	previous := slog.Default()

	dir := t.TempDir()
	cleanup, err := SetupFile(dir, "test.log", false,
		slog.String("version", "v1.2.3"),
		slog.String("commit", "abc123"),
		slog.String("built", "2026-08-03"),
	)
	if err != nil {
		t.Fatalf("SetupFile: %v", err)
	}
	slog.Info("logger-test-message")
	cleanup()
	if slog.Default() != previous {
		t.Fatal("cleanup 应恢复先前的默认 logger")
	}

	data, err := os.ReadFile(filepath.Join(dir, "logs", "test.log"))
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if !strings.Contains(string(data), "logger-test-message") {
		t.Fatalf("log missing message: %q", data)
	}
	if !regexp.MustCompile(`time=\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}(Z|[+-]\d{2}:\d{2})`).Match(data) {
		t.Fatalf("日志时间应包含日期、毫秒和时区: %q", data)
	}
	if !strings.Contains(string(data), "msg=日志会话开始") || !strings.Contains(string(data), "session=") {
		t.Fatalf("日志应包含可关联的会话边界与 session 属性: %q", data)
	}
	for _, want := range []string{"version=v1.2.3", "commit=abc123", "built=2026-08-03"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("日志应包含构建标识 %q: %q", want, data)
		}
	}
}

func TestSetupFileReturnsOpenError(t *testing.T) {
	previous := slog.Default()
	var fallback bytes.Buffer
	slog.SetDefault(slog.New(slog.NewTextHandler(&fallback, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	blocker := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	cleanup, err := SetupFile(blocker, "test.log", false)
	if err == nil {
		if cleanup != nil {
			cleanup()
		}
		t.Fatal("日志目录不可创建时应返回错误")
	}
	if cleanup != nil {
		t.Fatal("失败时不应返回清理函数")
	}
	slog.Info("fallback-remains-visible")
	if !strings.Contains(fallback.String(), "fallback-remains-visible") {
		t.Fatal("文件日志初始化失败后应保留原默认 logger")
	}
}
