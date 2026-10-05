// Package rules 实现用户偏好的输入层（Policy）：把各来源的写作规则归一化、合并成
// 本书快照（见 snapshot.go），运行时由 novel_context 注入、commit_chapter 机械检查。
//
// Rule 是第四类事实，跟 Progress / Checkpoint / Artifact 并列，但性质相反：
// 前三类是系统输出，Rule 是用户意图的持久化输入。
//
// 设计约束（不可妥协）：
//   - 工具只返事实，不返指令（Violation 是事实，由 editor 决定是否触发重写）
//   - 不引入新的 verdict 路径（复用 PendingRewrites）
//   - 不引入严格度字段（severity 由规则类型固定映射，editor 自主语义裁定）
//   - 不动 Flow Router（rule 不参与路由）
package rules

// SourceKind 标记规则文件来源，仅用于生成来源标签（如 global:my-style.md）。
type SourceKind int

const (
	// SourceGlobal — 用户全局偏好（~/.ainovel/rules/ 目录下所有 .md，按文件名字典序合并），跨书复用。
	SourceGlobal SourceKind = iota
	// SourceProject — 本书规则（./.ainovel/rules/ 目录下所有 .md，按文件名字典序合并），优先级最高。
	SourceProject
)

// String 返回来源的可读名称，用于来源标签前缀。
func (k SourceKind) String() string {
	switch k {
	case SourceGlobal:
		return "global"
	case SourceProject:
		return "project"
	default:
		return "unknown"
	}
}

// Structured 装载机械可检的结构化规则字段（归一化各来源后的候选/合并结果）。
// 章节字数刻意不在此列：多长算一章是叙事完整性问题，属语义裁量（writer/editor），
// 数字化成机械硬线会诱导模型为跨线注水——字数意愿走 preferences 自然语言通道。
type Structured struct {
	Genre            string         `json:"genre,omitempty"`
	ForbiddenChars   []string       `json:"forbidden_chars,omitempty"`
	ForbiddenPhrases []string       `json:"forbidden_phrases,omitempty"`
	FatigueWords     map[string]int `json:"fatigue_words,omitempty"`
}

// IsEmpty 用于判定是否完全没有结构化规则；checker 可据此跳过。
func (s Structured) IsEmpty() bool {
	return s.Genre == "" &&
		len(s.ForbiddenChars) == 0 &&
		len(s.ForbiddenPhrases) == 0 &&
		len(s.FatigueWords) == 0
}

// Severity 标记 Violation 的严重等级。
// 固定映射（用户不可配置）：
//
//	forbidden_chars 出现             -> Error
//	forbidden_phrases 出现           -> Error
//	fatigue_words 超阈值             -> Warning
type Severity string

const (
	SeverityWarning Severity = "warning"
	SeverityError   Severity = "error"
)

// Violation 是 checker 的输出：本章违反了某条机械规则的事实陈述。
//
// 注意：commit_chapter 把 violations 透传到返回 JSON，不阻断 commit；
// editor 在审阅时把这些事实映射到现有七维（aesthetic/pacing/character/consistency），
// 由 LLM 自主决定是否升级 verdict 触发 polish/rewrite。
type Violation struct {
	Rule     string   `json:"rule"`             // forbidden_chars / forbidden_phrases / fatigue_words
	Target   string   `json:"target,omitempty"` // 具体违规对象（哪个词/字符）
	Limit    any      `json:"limit,omitempty"`  // 阈值；fatigue_words=int / forbidden_*=空
	Actual   any      `json:"actual"`           // 实际值：出现次数
	Severity Severity `json:"severity"`         // error / warning
}
