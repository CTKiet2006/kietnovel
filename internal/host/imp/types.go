// Package imp implements the staged semantic import pipeline for external novels (docs/import-pipeline.md).
//
// The model handles open-ended semantics; the code handles coordinates, coverage, types,
// hashing, ordering and idempotency. Every semantic artifact reaches the official book state
// only after validating in the isolated workspace (meta/import/). The next action is derived from artifacts alone (NextAction): no drift-prone stage enum is stored, and recovery does not depend on from=N.
package imp

import "time"

// Options controls one import run. On recovery its fields may be empty and are derived directly from the active workspace and the saved Intent.
type Options struct {
	SourcePath      string // 新导入必填；恢复时可空
	AutoConfirm     bool   // --yes：覆盖校验通过后自动接受切分
	StoryResolution string // --story=open|closed：仅 synthesis 返回 uncertain 时预选
	ContinueAfter   bool   // --continue：不创建导入完成 Hold
	Guidance        string // --guide：自然语言切分指导，落盘工作区后自然使旧切分失配重识别
	// AcceptSegmentation: explicit human confirmation after the TUI preview (y). Releases the
	// current segmentation once, writing no intent. Unlike --yes -- a blind go-ahead that never views the preview and refuses a segmentation carrying tolerance notes (Notes) -- y is a verdict made after reading the preview.
	AcceptSegmentation bool
}

// intent extracts from Options the user authorizations that must be persisted.
func (o Options) intent() Intent {
	return Intent{
		Version:             workspaceSchemaVersion,
		AutoConfirm:         o.AutoConfirm,
		StoryResolution:     o.StoryResolution,
		ContinueAfterImport: o.ContinueAfter,
	}
}

// Stage is the import pipeline's current stage, used only for UI display; it is not the source of truth for recovery (RFC §14.1).
type Stage string

const (
	StageIngesting            Stage = "ingesting"
	StageSegmenting           Stage = "segmenting"
	StageAwaitingConfirmation Stage = "awaiting_confirmation"
	StageAnalyzing            Stage = "analyzing"
	StageSynthesizing         Stage = "synthesizing"
	StageAwaitingStoryStatus  Stage = "awaiting_story_status"
	StageValidating           Stage = "validating"
	StagePublishing           Stage = "publishing"
	StageDone                 Stage = "done"
	StageError                Stage = "error"
)

// Event is a progress event emitted by the import pipeline. Events are projections and take no part in recovery.
type Event struct {
	Time      time.Time
	Stage     Stage
	Current   int       // 章节/区间进度
	Total     int       // 总数
	Message   string    // 人类可读描述
	Level     string    // ""=普通进度；"warn"=退避重试/校验重问等警示状态
	Key       string    // 非空时 UI 对同 Key 连续事件原地更新（如 7 次退避在一行变动），对齐事件面板 ID 机制
	RetryAt   time.Time // 非零 = 下次重试的截止时刻；UI 据此逐秒倒计时渲染，到点即清（请求已在途）
	Err       error     // StageError 时携带
	Continued bool      // StageDone 时由 Host 置位：是否已自动接力启动 Engine（--continue × auto）
}
