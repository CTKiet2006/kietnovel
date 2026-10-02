package diag

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CTKiet2006/kietnovel/internal/store"
	"github.com/voocel/agentcore"
)

// sentinel is a piece of "novel body text" that must never appear in an export.
const sentinel = "雪夜里主角揭穿了反派的惊天阴谋这是机密正文"

// writeSession writes a few messages into a temporary output directory in the sessions/*.jsonl format.
func writeSession(t *testing.T, rel string, msgs []agentcore.Message) string {
	t.Helper()
	dir := t.TempDir()
	writeSessionAt(t, dir, rel, msgs)
	return dir
}

func writeSessionAt(t *testing.T, dir, rel string, msgs []agentcore.Message) {
	t.Helper()
	path := filepath.Join(dir, "meta", "sessions", rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	var b strings.Builder
	for _, m := range msgs {
		data, err := json.Marshal(m)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		b.Write(data)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func commitCall(chapterRaw string) agentcore.Message {
	args := json.RawMessage(`{"chapter":` + chapterRaw + `,"content":"` + sentinel + sentinel + `"}`)
	return agentcore.Message{
		Role:    agentcore.RoleAssistant,
		Content: []agentcore.ContentBlock{agentcore.ToolCallBlock(agentcore.ToolCall{Name: "commit_chapter", Args: args})},
	}
}

func errResult(msg string) agentcore.Message {
	return agentcore.Message{
		Role:     agentcore.RoleTool,
		Content:  []agentcore.ContentBlock{agentcore.TextBlock(msg)},
		Metadata: map[string]any{"is_error": true},
	}
}

// TestExport_DeathLoopShape reproduces #34 end to end: the model stringifies the chapter of commit_chapter
// which causes a validation loop. It asserts the export can locate it and that no novel body text leaves the package.
func TestExport_DeathLoopShape(t *testing.T) {
	var msgs []agentcore.Message
	// a bare body text of a Worker output (<4KB, bypassing session_compact), which must be masked.
	msgs = append(msgs, agentcore.Message{
		Role:    agentcore.RoleAssistant,
		Content: []agentcore.ContentBlock{agentcore.TextBlock(sentinel)},
	})
	// 14 rounds of commit_chapter(chapter:"7") + InputValidationError.
	for range 14 {
		msgs = append(msgs, commitCall(`"7"`))
		msgs = append(msgs, errResult("InputValidationError: chapter must be int"))
	}

	dir := writeSession(t, filepath.Join("agents", "writer-ch07.jsonl"), msgs)
	s := store.NewStore(dir)
	rep, rc := Diagnose(s)
	out := string(RenderExport(rep, rc))

	if strings.Contains(out, sentinel) {
		t.Fatalf("小说正文出包了！导出包含 sentinel:\n%s", out)
	}
	if !strings.Contains(out, `chapter: "7"`) {
		t.Errorf("缺类型异常信号 chapter: \"7\"（#34 根因）\n%s", out)
	}
	if !strings.Contains(out, "InputValidationError") {
		t.Errorf("错误串未保留\n%s", out)
	}
	if !strings.Contains(out, "×14") {
		t.Errorf("重复聚合未列出 ×14\n%s", out)
	}
	// Phase 2: the runtime detection should judge this loop as a critical RepeatedToolError.
	if !strings.Contains(out, "Công cụ báo lỗi giống nhau lặp lại") {
		t.Errorf("运行时检测未产出 RepeatedToolError\n%s", out)
	}
	if !strings.Contains(out, "[critical]") {
		t.Errorf("14 次重复应升为 critical\n%s", out)
	}
}

// TestExport_NumberVsStringArg proves the scalar and string projections can tell the types apart:
// chapter:7 (number) stays 7, chapter:"7" (string) stays "7".
func TestExport_NumberVsStringArg(t *testing.T) {
	intDir := writeSession(t, filepath.Join("agents", "writer-ch07.jsonl"), []agentcore.Message{commitCall(`7`)})
	si := store.NewStore(intDir)
	repInt, rcInt := Diagnose(si)
	outInt := string(RenderExport(repInt, rcInt))
	if !strings.Contains(outInt, "chapter: 7") || strings.Contains(outInt, `chapter: "7"`) {
		t.Errorf("数字参数应渲染为 chapter: 7（不带引号）\n%s", outInt)
	}
}

// TestProjectValue_ProseArgRedacted guards the redaction boundary: short identifier-like values are kept,
// while Chinese/space-containing short values (e.g. dispatch task, chapter title) are always masked.
func TestProjectValue_ProseArgRedacted(t *testing.T) {
	keep := map[string]string{
		`"7"`:       `"7"`,       // stringified number (a #34 signal)
		`"premise"`: `"premise"`, // enumeration
		`"writer"`:  `"writer"`,  // role name
		`7`:         `7`,         // numeric scalar
		`true`:      `true`,      // bool scalar
	}
	for in, want := range keep {
		if got := projectValue([]byte(in)); got != want {
			t.Errorf("应保留 %s：got %q want %q", in, got, want)
		}
	}
	// containing Chinese / spaces -> must be masked and must not contain the raw text
	prose := []string{`"第7章 雪夜的真相"`, `"雪夜杀机"`, `"主角揭穿阴谋"`}
	for _, in := range prose {
		got := projectValue([]byte(in))
		if !strings.HasPrefix(got, "<redacted") {
			t.Errorf("中文/带空格短值应打码：%s → %q", in, got)
		}
		if strings.Contains(got, "雪夜") || strings.Contains(got, "主角") {
			t.Errorf("打码后仍含正文：%s → %q", in, got)
		}
	}
}

// TestWriteExport_WritesFile proves the pure function path: no TUI dependency, writes to a fixed relative path.
func TestWriteExport_WritesFile(t *testing.T) {
	dir := writeSession(t, filepath.Join("agents", "writer-ch07.jsonl"), []agentcore.Message{commitCall(`"7"`), errResult("boom")})
	s := store.NewStore(dir)

	rep, rc := Diagnose(s)
	path, err := WriteExport(s, rep, rc)
	if err != nil {
		t.Fatalf("WriteExport: %v", err)
	}
	if want := filepath.Join(dir, filepath.FromSlash(ExportRelPath)); path != want {
		t.Errorf("路径不对：got %s want %s", path, want)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !strings.Contains(string(data), "diag-export") {
		t.Errorf("文件内容异常\n%s", data)
	}
	if strings.Contains(string(data), sentinel) {
		t.Errorf("写出的文件夹带正文")
	}
}

// TestRedactMessage_DupSha proves that the same text appearing repeatedly yields the same sha (a loop signal).
func TestRedactMessage_DupSha(t *testing.T) {
	a := redactMessage("writer-ch07", agentcore.Message{
		Role:    agentcore.RoleAssistant,
		Content: []agentcore.ContentBlock{agentcore.TextBlock(sentinel)},
	})
	b := redactMessage("writer-ch07", agentcore.Message{
		Role:    agentcore.RoleAssistant,
		Content: []agentcore.ContentBlock{agentcore.TextBlock(sentinel)},
	})
	if a.TextSha == "" || a.TextSha != b.TextSha {
		t.Errorf("相同正文应得相同 sha：%q vs %q", a.TextSha, b.TextSha)
	}
	if a.Redacted != 1 {
		t.Errorf("应打码 1 个文本块，got %d", a.Redacted)
	}
}
