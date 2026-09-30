package rules

import (
	"os"
	"path/filepath"
)

// LoadOptions enumerates the rules-file source directories that RawFileSources scans and normalizes.
//
// A missing directory is not an error; the scan skips it silently.
type LoadOptions struct {
	// HomeRulesDir is the ~/.kietnovel/rules/ directory; every top-level .md under it is scanned (merged in filename lexicographic order). Empty means skip.
	HomeRulesDir string

	// ProjectRulesDir is the ./.kietnovel/rules/ directory (mirroring the global one, again scanning every top-level .md under it). Empty means skip.
	ProjectRulesDir string
}

// kietnovelDirName is the dotdir name that kietnovel shares at the user / project levels.
// That makes the global ~/.kietnovel/rules/ and the project ./.kietnovel/rules/ symmetric.
const kietnovelDirName = ".kietnovel"

// DefaultProjectRulesDir builds the absolute path of ./.kietnovel/rules/ (from the given project directory).
// The caller passes the project root, so the loader never depends on cwd internally; it mirrors DefaultHomeRulesDir.
func DefaultProjectRulesDir(projectDir string) string {
	if projectDir == "" {
		return ""
	}
	return filepath.Join(projectDir, kietnovelDirName, "rules")
}

// DefaultHomeRulesDir builds the absolute path of the ~/.kietnovel/rules/ directory.
// It returns an empty string when home cannot be resolved (the caller skips that source).
func DefaultHomeRulesDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, kietnovelDirName, "rules")
}

// homeRulesReadme is the guidance written to ~/.kietnovel/rules/README.txt on first-run bootstrap.
// The .txt suffix is deliberate rather than .md -- the scan only recognises .md, so this guidance is never normalized as a rule.
const homeRulesReadme = `这里放全局写作偏好，跨所有书生效。

新建一个 .md 文件（如 my-style.md），用大白话写要求就行——
不需要任何格式、不需要 YAML：

    # 角色
    - 主角林尘别写成圣母，外冷内热即可
    # 风格
    - 多用身体感知（指节发白）替代情绪标签（紧张）
    - 对话别太书面，每章 3000 字左右
    - 不要出现"某种程度上"这种 AI 腔

写完不用管格式：系统会用模型把这些自然语言要求归一化成结构化约束
（字数范围、禁用词、疲劳词阈值等），写作时自动遵循、提交时自动自检。

多个 .md 按文件名字典序合并；点开头的隐藏文件、非 .md 文件都会被忽略
（所以这份 README.txt 不会被当成规则）。

常见 AI 套句、疲劳词的机械基线已内置，开箱即用，不写也没关系。

加载优先级（高 → 低）：./.kietnovel/rules/*.md（本书） > ~/.kietnovel/rules/*.md（这里） > 内置默认
`

// EnsureHomeRulesDir makes a best effort to create the ~/.kietnovel/rules/ directory and write the README.txt guidance,
// so users discover this global preference extension point and learn how to write for it.
// It is a nice-to-have on a non-critical path: a failed home lookup or write error is swallowed silently and never blocks startup.
func EnsureHomeRulesDir() {
	if dir := DefaultHomeRulesDir(); dir != "" {
		_ = ensureRulesDirAt(dir)
	}
}

// ensureRulesDirAt creates the directory and writes README.txt from the current bootstrap template; it is the testable core of EnsureHomeRulesDir.
// README.txt is a system-generated bootstrap file (user preferences live in *.md, which is not scanned in), and every call overwrites it with
// the latest template -- nothing is carried over, so no version-compatibility logic is needed at all.
func ensureRulesDirAt(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "README.txt"), []byte(homeRulesReadme), 0o644)
}

// DefaultOptions builds the usual LoadOptions from the current working directory.
//
// It suits a single call during Host startup, so the user-rules service reuses one source configuration.
// If cwd cannot be resolved, ProjectRulesDir stays empty (the scan skips that source).
//
// Path semantics: ProjectRulesDir is bound to the **current working directory (cwd)**, not to outputDir.
// A user who cds elsewhere to start writing a different book naturally gets ./.kietnovel/rules/ following cwd; to share across books,
// put them in the global ~/.kietnovel/rules/ directory instead (every .md under it is loaded).
func DefaultOptions() LoadOptions {
	cwd, _ := os.Getwd()
	return LoadOptions{
		HomeRulesDir:    DefaultHomeRulesDir(),
		ProjectRulesDir: DefaultProjectRulesDir(cwd),
	}
}
