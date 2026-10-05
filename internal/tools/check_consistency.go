package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/CTKiet2006/kietnovel/internal/domain"
	"github.com/CTKiet2006/kietnovel/internal/errs"
	"github.com/CTKiet2006/kietnovel/internal/store"
	"github.com/voocel/agentcore/schema"
)

// CheckConsistencyTool returns the chapter content and all state data, for the Agent to compare and judge on its own.
// A pure IO tool: it only loads data and injects no instructions.
type CheckConsistencyTool struct {
	store *store.Store
}

func NewCheckConsistencyTool(store *store.Store) *CheckConsistencyTool {
	return &CheckConsistencyTool{store: store}
}

func (t *CheckConsistencyTool) Name() string { return "check_consistency" }
func (t *CheckConsistencyTool) Description() string {
	switch toolLang(t.store) {
	case "vi":
		return "Tải bản nháp đã viết và dữ liệu đối chiếu (quy tắc thế giới, phục bút, quan hệ, bí danh, tóm tắt gần đây) để kiểm tra tính nhất quán. Bắt buộc gọi sau draft_chapter"
	case "en":
		return "Load written draft and reference data (world rules, foreshadowing, relationships, aliases, recent summaries) to verify consistency. Must be called after draft_chapter"
	default:
		return "加载已写草稿和对照数据（世界规则、伏笔、关系、别名、最近摘要），供你检查一致性。必须在 draft_chapter 之后调用"
	}
}
func (t *CheckConsistencyTool) Label() string {
	switch toolLang(t.store) {
	case "vi":
		return "Kiểm tra tính nhất quán"
	case "en":
		return "Check consistency"
	default:
		return "一致性检查"
	}
}

// A read-only tool (it only appends a checkpoint event and changes no state), so it can be scheduled concurrently.
func (t *CheckConsistencyTool) ReadOnly(_ json.RawMessage) bool        { return true }
func (t *CheckConsistencyTool) ConcurrencySafe(_ json.RawMessage) bool { return true }

func (t *CheckConsistencyTool) Schema() map[string]any {
	chapterDesc := "要检查的章节号"
	switch toolLang(t.store) {
	case "vi":
		chapterDesc = "Số chương cần kiểm tra"
	case "en":
		chapterDesc = "Chapter number to verify"
	}
	return schema.Object(
		schema.Property("chapter", schema.Int(chapterDesc)).Required(),
	)
}

func (t *CheckConsistencyTool) Execute(_ context.Context, args json.RawMessage) (json.RawMessage, error) {
	var a struct {
		Chapter int `json:"chapter"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, fmt.Errorf("invalid args: %w: %w", errs.ErrToolArgs, err)
	}
	if a.Chapter <= 0 {
		return nil, fmt.Errorf("chapter must be > 0: %w", errs.ErrToolArgs)
	}

	result := map[string]any{"chapter": a.Chapter}
	var warnings []string
	warn := func(scope string, err error) {
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("%s 读取失败: %v", scope, err))
		}
	}

	// Chapter content
	content, wordCount, err := t.store.Drafts.LoadChapterContent(a.Chapter)
	if err != nil {
		return nil, fmt.Errorf("load chapter content: %w: %w", errs.ErrStoreRead, err)
	}
	if content == "" {
		return nil, fmt.Errorf("no content found for chapter %d: %w", a.Chapter, errs.ErrToolPrecondition)
	}
	result["content"] = content
	result["word_count"] = wordCount

	// Comparison data: keep the global consistency-check data and avoid reloading the window data novel_context already provides
	if rules, err := t.store.World.LoadWorldRules(); len(rules) > 0 {
		result["world_rules"] = rules
	} else {
		warn("world_rules", err)
	}
	if foreshadow, err := t.store.World.LoadActiveForeshadow(); len(foreshadow) > 0 {
		result["foreshadow_ledger"] = foreshadow
	} else {
		warn("foreshadow_ledger", err)
	}
	if relationships, err := t.store.World.LoadRelationships(); len(relationships) > 0 {
		result["relationships"] = relationships
	} else {
		warn("relationships", err)
	}
	if chars, err := t.store.Characters.Load(); len(chars) > 0 {
		aliasMap := make(map[string]string)
		for _, c := range chars {
			for _, alias := range c.Aliases {
				aliasMap[alias] = c.Name
			}
		}
		if len(aliasMap) > 0 {
			result["alias_map"] = aliasMap
		}
	} else {
		warn("characters", err)
	}
	if summaries, err := t.store.Summaries.LoadRecentSummaries(a.Chapter, 2); len(summaries) > 0 {
		result["recent_summaries"] = summaries
	} else {
		warn("recent_summaries", err)
	}

	if _, err := t.store.Checkpoints.AppendArtifact(
		domain.ChapterScope(a.Chapter), "consistency_check",
		fmt.Sprintf("drafts/%02d.draft.md", a.Chapter),
	); err != nil {
		return nil, fmt.Errorf("checkpoint consistency check: %w", err)
	}
	if len(warnings) > 0 {
		result["status"] = "partial"
		result["_warnings"] = warnings
	}

	return json.Marshal(result)
}
