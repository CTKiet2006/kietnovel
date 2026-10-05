package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"
)

const ChapterRecordVersion = 1

type ChapterOrigin string

const (
	ChapterOriginGenerated ChapterOrigin = "generated"
	ChapterOriginUser      ChapterOrigin = "user"
	ChapterOriginLegacy    ChapterOrigin = "legacy"
)

// ChapterFacts 是一章正文对应的完整结构化事实，也是所有派生状态的输入。
type ChapterFacts struct {
	Title               string              `json:"title"`
	Summary             string              `json:"summary"`
	Characters          []string            `json:"characters"`
	KeyEvents           []string            `json:"key_events"`
	TimelineEvents      []TimelineEvent     `json:"timeline_events"`
	ForeshadowUpdates   []ForeshadowUpdate  `json:"foreshadow_updates"`
	RelationshipChanges []RelationshipEntry `json:"relationship_changes"`
	StateChanges        []StateChange       `json:"state_changes"`
	CastIntros          []CastIntro         `json:"cast_intros"`
	HookType            string              `json:"hook_type,omitempty"`
	DominantStrand      string              `json:"dominant_strand,omitempty"`
	Feedback            *OutlineFeedback    `json:"feedback,omitempty"`
}

// StyleDelta 记录用户修订相对系统版本体现出的写作偏好。
type StyleDelta struct {
	Prose    []string         `json:"prose"`
	Dialogue []CharacterVoice `json:"dialogue"`
	Taboos   []string         `json:"taboos"`
}

// MergeStyleDelta 合并持久风格证据并保持规则唯一。
func MergeStyleDelta(base, next StyleDelta) StyleDelta {
	merged := StyleDelta{
		Prose:  mergeTextRules(base.Prose, next.Prose),
		Taboos: mergeTextRules(base.Taboos, next.Taboos),
	}
	voices := make(map[string]int)
	for _, source := range [][]CharacterVoice{base.Dialogue, next.Dialogue} {
		for _, voice := range source {
			name := strings.TrimSpace(voice.Name)
			idx, ok := voices[name]
			if !ok {
				idx = len(merged.Dialogue)
				voices[name] = idx
				merged.Dialogue = append(merged.Dialogue, CharacterVoice{Name: name})
			}
			merged.Dialogue[idx].Rules = mergeTextRules(merged.Dialogue[idx].Rules, voice.Rules)
		}
	}
	return merged
}

func mergeTextRules(groups ...[]string) []string {
	seen := make(map[string]bool)
	var result []string
	for _, group := range groups {
		for _, item := range group {
			item = strings.TrimSpace(item)
			if item == "" || seen[item] {
				continue
			}
			seen[item] = true
			result = append(result, item)
		}
	}
	return result
}

// ChapterRecord 保存最近一次已接纳的章节正文及其完整事实。
// chapters/*.md 是可编辑工作区，本记录是判断外部修订的基线。
type ChapterRecord struct {
	Version       int           `json:"version"`
	Chapter       int           `json:"chapter"`
	Revision      int           `json:"revision"`
	Origin        ChapterOrigin `json:"origin"`
	Content       string        `json:"content"`
	ContentSHA256 string        `json:"content_sha256"`
	Facts         ChapterFacts  `json:"facts"`
	StyleDelta    StyleDelta    `json:"style_delta"`
	AcceptedAt    time.Time     `json:"accepted_at"`
}

// AuthorRevisionStyle 是所有已接纳用户修订的确定性风格投影。
type AuthorRevisionStyle struct {
	Prose     []string         `json:"prose"`
	Dialogue  []CharacterVoice `json:"dialogue"`
	Taboos    []string         `json:"taboos"`
	UpdatedAt time.Time        `json:"updated_at"`
}

type RevisionStage string

const (
	RevisionStagePrepared           RevisionStage = "prepared"
	RevisionStageRecordsApplied     RevisionStage = "records_applied"
	RevisionStageProjectionsApplied RevisionStage = "projections_applied"
)

type RevisionAnalysis struct {
	ChangeSummary    string           `json:"change_summary"`
	StoryChanged     bool             `json:"story_changed"`
	Facts            ChapterFacts     `json:"facts"`
	StyleDelta       StyleDelta       `json:"style_delta"`
	OutlineImpact    *OutlineFeedback `json:"outline_impact,omitempty"`
	DownstreamIssues []string         `json:"downstream_issues"`
}

type PendingRevisionItem struct {
	Chapter       int              `json:"chapter"`
	BaseSHA256    string           `json:"base_sha256"`
	CurrentSHA256 string           `json:"current_sha256"`
	Record        ChapterRecord    `json:"record"`
	Analysis      RevisionAnalysis `json:"analysis"`
}

type PendingRevision struct {
	Stage     RevisionStage         `json:"stage"`
	Items     []PendingRevisionItem `json:"items"`
	StartedAt time.Time             `json:"started_at"`
	UpdatedAt time.Time             `json:"updated_at"`
}

func NormalizeChapterContent(content string) string {
	content = strings.TrimPrefix(content, "\ufeff")
	content = strings.ReplaceAll(content, "\r\n", "\n")
	return strings.ReplaceAll(content, "\r", "\n")
}

func ChapterContentSHA256(content string) string {
	sum := sha256.Sum256([]byte(NormalizeChapterContent(content)))
	return hex.EncodeToString(sum[:])
}
