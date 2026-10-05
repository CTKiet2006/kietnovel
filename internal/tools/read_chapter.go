package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/CTKiet2006/kietnovel/internal/store"
	"github.com/voocel/agentcore/schema"
)

// ReadChapterTool reads a chapter's raw text, so the Agent can re-read its own writing and the preceding text.
type ReadChapterTool struct {
	store *store.Store
}

func NewReadChapterTool(store *store.Store) *ReadChapterTool {
	return &ReadChapterTool{store: store}
}

func (t *ReadChapterTool) Name() string { return "read_chapter" }
func (t *ReadChapterTool) Description() string {
	switch toolLang(t.store) {
	case "vi":
		return "Đọc nguyên văn chương truyện. Có thể đọc bản cuối, bản nháp, hoặc trích xuất các phân đoạn đối thoại của nhân vật"
	case "en":
		return "Read chapter source text. Can read final prose, draft, or extract character dialogue snippets"
	default:
		return "读取章节原文。可读终稿、草稿，或提取角色对话片段"
	}
}
func (t *ReadChapterTool) Label() string {
	switch toolLang(t.store) {
	case "vi":
		return "Đọc chương"
	case "en":
		return "Read chapter"
	default:
		return "读取章节"
	}
}

// A pure read tool, so it can be scheduled concurrently (an editor reviewing often reads several chapters at once).
func (t *ReadChapterTool) ReadOnly(_ json.RawMessage) bool        { return true }
func (t *ReadChapterTool) ConcurrencySafe(_ json.RawMessage) bool { return true }

func (t *ReadChapterTool) Schema() map[string]any {
	chapterDesc := "章节号（读单章时必填）"
	fromDesc := "起始章节号（读范围时使用）"
	toDesc := "结束章节号（读范围时使用）"
	sourceDesc := "来源"
	charDesc := "角色名（提取对话片段时使用）"
	maxRunesDesc := "每章最大字符数（范围读取时截取，默认 2000）"

	switch toolLang(t.store) {
	case "vi":
		chapterDesc = "Số chương (bắt buộc khi đọc đơn chương)"
		fromDesc = "Số chương bắt đầu (khi đọc theo dải chương)"
		toDesc = "Số chương kết thúc (khi đọc theo dải chương)"
		sourceDesc = "Nguồn đọc (final hoặc draft)"
		charDesc = "Tên nhân vật (khi trích xuất các câu thoại)"
		maxRunesDesc = "Số ký tự tối đa mỗi chương (khi đọc theo dải, mặc định 2000)"
	case "en":
		chapterDesc = "Chapter number (required when reading a single chapter)"
		fromDesc = "Start chapter number (for range reading)"
		toDesc = "End chapter number (for range reading)"
		sourceDesc = "Source (final or draft)"
		charDesc = "Character name (for extracting dialogue snippets)"
		maxRunesDesc = "Max characters per chapter (for range reading, default 2000)"
	}

	return schema.Object(
		schema.Property("chapter", schema.Int(chapterDesc)),
		schema.Property("from", schema.Int(fromDesc)),
		schema.Property("to", schema.Int(toDesc)),
		schema.Property("source", schema.Enum(sourceDesc, "final", "draft")).Required(),
		schema.Property("character", schema.String(charDesc)),
		schema.Property("max_runes", schema.Int(maxRunesDesc)),
	)
}

func (t *ReadChapterTool) Execute(_ context.Context, args json.RawMessage) (json.RawMessage, error) {
	var a struct {
		Chapter   int    `json:"chapter"`
		From      int    `json:"from"`
		To        int    `json:"to"`
		Source    string `json:"source"`
		Character string `json:"character"`
		MaxRunes  int    `json:"max_runes"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, fmt.Errorf("invalid args: %w", err)
	}
	if a.Source != "final" && a.Source != "draft" {
		return nil, fmt.Errorf("source must be final or draft")
	}

	// Mode 1: extract character dialogue
	if a.Character != "" {
		var warnings []string
		warn := func(scope string, err error) {
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("%s 读取失败: %v", scope, err))
			}
		}
		chars, err := t.store.Characters.Load()
		warn("characters", err)
		var aliases []string
		for _, c := range chars {
			if c.Name == a.Character {
				aliases = c.Aliases
				break
			}
		}
		var maxCompleted int
		p, err := t.store.Progress.Load()
		warn("progress", err)
		if p != nil {
			maxCompleted = maxCompletedChapter(p.CompletedChapters)
		}
		samples, err := t.store.Drafts.ExtractDialogue(a.Character, aliases, 8, maxCompleted)
		warn("dialogue_samples", err)
		result := map[string]any{
			"character": a.Character,
			"samples":   samples,
		}
		if len(samples) == 0 {
			result["hint"] = "该角色暂无可用的已提交对话样本"
		}
		if len(warnings) > 0 {
			result["status"] = "partial"
			result["_warnings"] = warnings
		}
		return json.Marshal(result)
	}

	// Mode 2: range read
	if a.From > 0 && a.To > 0 {
		maxRunes := a.MaxRunes
		if maxRunes <= 0 {
			maxRunes = 2000
		}
		var load func(int) (string, error)
		if a.Source == "draft" {
			load = t.store.Drafts.LoadDraft
		} else {
			load = t.store.Drafts.LoadChapterText
		}
		texts := make(map[int]string)
		for ch := a.From; ch <= a.To; ch++ {
			chapter, err := load(ch)
			if err != nil {
				return nil, fmt.Errorf("load %s chapter %d: %w", a.Source, ch, err)
			}
			if chapter == "" {
				continue
			}
			runes := []rune(chapter)
			if len(runes) > maxRunes {
				chapter = string(runes[:maxRunes]) + "..."
			}
			texts[ch] = chapter
		}
		return json.Marshal(map[string]any{
			"chapters": texts,
			"from":     a.From,
			"to":       a.To,
			"source":   a.Source,
		})
	}

	// Mode 3: single-chapter read
	if a.Chapter <= 0 {
		return nil, fmt.Errorf("chapter is required")
	}

	var content string
	var err error
	switch a.Source {
	case "draft":
		content, err = t.store.Drafts.LoadDraft(a.Chapter)
	default: // final
		content, err = t.store.Drafts.LoadChapterText(a.Chapter)
	}
	if err != nil {
		return nil, fmt.Errorf("read chapter %d: %w", a.Chapter, err)
	}
	if content == "" {
		return json.Marshal(map[string]any{
			"chapter": a.Chapter,
			"source":  a.Source,
			"exists":  false,
			"hint":    "请求的来源中没有该章节；如需读取另一来源，请明确指定 source",
		})
	}

	return json.Marshal(map[string]any{
		"chapter":    a.Chapter,
		"source":     a.Source,
		"content":    content,
		"word_count": len([]rune(content)),
	})
}

// maxCompletedChapter returns the largest chapter number in the list of completed chapters.
func maxCompletedChapter(completed []int) int {
	m := 0
	for _, ch := range completed {
		if ch > m {
			m = ch
		}
	}
	return m
}
