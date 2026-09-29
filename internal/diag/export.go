package diag

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/CTKiet2006/kietnovel/internal/store"
)

// ExportRelPath is the fixed location of the redacted diagnostic file relative to the output directory (one overwriting copy).
const ExportRelPath = "meta/diag-export.md"

// Export runs the full diagnosis + rendering + write and returns the absolute path written. Meant for headless / external calls.
func Export(s *store.Store) (string, error) {
	rep, rc := Diagnose(s)
	return WriteExport(s, rep, rc)
}

// WriteExport renders and writes an already computed Report + RuntimeCapture without capturing again.
// It lets the /diag command reuse the result of Diagnose.
func WriteExport(s *store.Store, rep Report, rc RuntimeCapture) (string, error) {
	data := RenderExport(rep, rc)
	abs := filepath.Join(s.Dir(), filepath.FromSlash(ExportRelPath))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(abs, data, 0o644); err != nil {
		return "", err
	}
	return abs, nil
}

// RenderExport combines the writing Report and the runtime capture into a redacted Markdown.
func RenderExport(rep Report, rc RuntimeCapture) []byte {
	var b strings.Builder
	st := rep.Stats

	b.WriteString("# diag-export\n\n")
	fmt.Fprintf(&b, "> 生成时间 %s · %s/%s\n", time.Now().Format("2006-01-02 15:04:05"), rc.GoOS, rc.GoArch)
	b.WriteString("> ⚠️ 已脱敏：小说正文 / prompt / 思考已移除，仅保留行为骨架。可直接贴到 issue。\n\n")

	// 1. Environment
	b.WriteString("## 1. 环境\n\n")
	fmt.Fprintf(&b, "- 阶段 `%s`", orDash(st.Phase))
	if st.Flow != "" {
		fmt.Fprintf(&b, " / flow `%s`", st.Flow)
	}
	fmt.Fprintf(&b, " · 章节 %d/%d · 字数 %d\n", st.CompletedChapters, st.TotalChapters, st.TotalWords)
	if st.PlanningTier != "" {
		fmt.Fprintf(&b, "- 规划 `%s`\n", st.PlanningTier)
	}
	for _, m := range rc.Models {
		fmt.Fprintf(&b, "- %s → `%s` / `%s`\n", m.Agent, orDash(m.Provider), orDash(m.Model))
	}

	// 2. Diagnostic findings (runtime only; writing diagnostics contain plot/setups, so they stay on the /diag screen report and are not put into the shareable export)
	b.WriteString("\n## 2. 诊断发现（运行时）\n\n")
	rf := runtimeFindings(&rc)
	sortFindings(rf)
	if len(rf) == 0 {
		b.WriteString("未发现运行时异常。\n")
	} else {
		for _, f := range rf {
			fmt.Fprintf(&b, "- [%s] %s\n", f.Severity, f.Title)
			if f.Evidence != "" {
				fmt.Fprintf(&b, "  - 证据：%s\n", f.Evidence)
			}
			if f.Suggestion != "" {
				fmt.Fprintf(&b, "  - → %s\n", f.Suggestion)
			}
		}
	}

	// 3. Runtime signals (raw aggregation)
	b.WriteString("\n## 3. 运行时信号\n\n")
	wrote := false
	if rc.CurrentStep != "" {
		fmt.Fprintf(&b, "- 当前 step `%s`\n", rc.CurrentStep)
		wrote = true
	}
	if rc.StuckStep != "" {
		fmt.Fprintf(&b, "- ⚠️ 卡住：连续停在 `%s` ×%d\n", rc.StuckStep, rc.StuckCount)
		wrote = true
	}
	if len(rc.Repeats) > 0 {
		b.WriteString("- 高频签名（近端窗口 ≥3 次，含正常重复工具，仅供参考）：\n")
		for _, r := range rc.Repeats {
			fmt.Fprintf(&b, "  - `%s` ×%d\n", r.Sig, r.Count)
		}
		wrote = true
	}
	if len(rc.DupContent) > 0 {
		b.WriteString("- 反复生成同段文本（同 sha）：\n")
		for _, d := range rc.DupContent {
			fmt.Fprintf(&b, "  - sha=%s ×%d\n", d.Sha, d.Count)
		}
		wrote = true
	}
	if len(rc.LogKinds) > 0 {
		b.WriteString("- 日志错误分类：")
		b.WriteString(joinKinds(rc.LogKinds))
		b.WriteString("\n")
		wrote = true
	}
	if rc.LogErrors > 0 || rc.LogWarns > 0 {
		fmt.Fprintf(&b, "- 日志 error ×%d · warn ×%d\n", rc.LogErrors, rc.LogWarns)
		wrote = true
	}
	if rc.StopGuard > 0 {
		fmt.Fprintf(&b, "- StopGuard 拦截 ×%d\n", rc.StopGuard)
		wrote = true
	}
	if !wrote {
		b.WriteString("- 无明显运行时异常信号。\n")
	}

	// 4. The tail of the behaviour skeleton
	fmt.Fprintf(&b, "\n## 4. 行为骨架尾巴（末 %d 条）\n\n", len(rc.Tail))
	if len(rc.Tail) == 0 {
		b.WriteString("（无会话记录）\n")
	} else {
		b.WriteString("```\n")
		for _, ev := range rc.Tail {
			b.WriteString(formatSkel(ev))
			b.WriteString("\n")
		}
		b.WriteString("```\n")
	}

	// 5. Redaction self-check
	b.WriteString("\n## 5. 脱敏自检\n\n")
	fmt.Fprintf(&b, "- 打码文本块 %d 处 · 正文出包 0 处\n", rc.RedactedTexts)
	if len(rc.Sources) > 0 {
		fmt.Fprintf(&b, "- 数据源：%s\n", strings.Join(rc.Sources, " · "))
	}

	return []byte(b.String())
}

// formatSkel renders one skeleton entry as a single line to show the dispatch order.
func formatSkel(ev SkelEvent) string {
	var parts []string
	parts = append(parts, "["+ev.Agent+"/"+ev.Role+"]")
	for _, t := range ev.Tools {
		parts = append(parts, t.Name+formatArgs(t.Args)+invalidTag(t))
	}
	if ev.ErrClass != "" {
		parts = append(parts, "err: "+ev.ErrClass)
	}
	if len(ev.Tools) == 0 && ev.ErrClass == "" && ev.TextSha != "" {
		parts = append(parts, "text<sha="+ev.TextSha+">")
	}
	return strings.Join(parts, " ")
}

func formatArgs(args map[string]string) string {
	if len(args) == 0 {
		return ""
	}
	keys := make([]string, 0, len(args))
	for k := range args {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	pairs := make([]string, 0, len(keys))
	for _, k := range keys {
		pairs = append(pairs, k+": "+args[k])
	}
	return "{" + strings.Join(pairs, ", ") + "}"
}

func invalidTag(t SkelTool) string {
	if !t.Invalid {
		return ""
	}
	if t.ParseErr != "" {
		return " ⚠️args-invalid(" + firstLine(t.ParseErr, 80) + ")"
	}
	return " ⚠️args-invalid"
}

func joinKinds(m map[string]int) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s ×%d", k, m[k]))
	}
	return strings.Join(parts, " · ")
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
