package revision

import (
	"fmt"
	"slices"
	"strings"

	"github.com/voocel/ainovel-cli/internal/chapterfacts"
	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/store"
)

// Projector 从章节记录重建所有章节级派生状态。
type Projector struct{ store *store.Store }

func NewProjector(st *store.Store) *Projector { return &Projector{store: st} }

type projection struct {
	summaries     []domain.ChapterSummary
	timeline      []domain.TimelineEvent
	foreshadow    []domain.ForeshadowEntry
	relationships []domain.RelationshipEntry
	stateChanges  []domain.StateChange
	wordCounts    map[int]int
	totalWords    int
	hookHistory   []string
	strandHistory []string
	style         domain.AuthorRevisionStyle
}

// ValidateRecords 校验完整章节记录集能否被确定性重放，不写入任何投影。
func ValidateRecords(records []domain.ChapterRecord) error {
	records, err := prepareRecords(records)
	if err != nil {
		return err
	}
	_, _, _, _, err = projectWorld(records)
	return err
}

func prepareRecords(records []domain.ChapterRecord) ([]domain.ChapterRecord, error) {
	records = slices.Clone(records)
	slices.SortFunc(records, func(a, b domain.ChapterRecord) int { return a.Chapter - b.Chapter })
	for _, record := range records {
		// legacy 记录由旧版 store 状态重建，只受当时的合同约束；Validate 是新模型输出的合同。
		if record.Origin == domain.ChapterOriginLegacy {
			continue
		}
		if err := chapterfacts.Validate(record.Facts); err != nil {
			return nil, fmt.Errorf("第 %d 章事实无效: %w", record.Chapter, err)
		}
	}
	return records, nil
}

func (p *Projector) build(records []domain.ChapterRecord) (projection, error) {
	records, err := prepareRecords(records)
	if err != nil {
		return projection{}, err
	}

	timeline, ledger, relationships, changes, err := projectWorld(records)
	if err != nil {
		return projection{}, err
	}
	result := projection{
		timeline: timeline, foreshadow: ledger, relationships: relationships,
		stateChanges: changes,
		wordCounts:   make(map[int]int, len(records)), style: projectStyle(records),
	}
	for _, record := range records {
		facts := record.Facts
		result.summaries = append(result.summaries, domain.ChapterSummary{
			Chapter: record.Chapter, Title: facts.Title, Summary: facts.Summary,
			Characters: facts.Characters, KeyEvents: facts.KeyEvents,
		})
		count := domain.WordCount(record.Content)
		result.wordCounts[record.Chapter] = count
		result.totalWords += count
		setChapterHistory(&result.hookHistory, record.Chapter, facts.HookType)
		setChapterHistory(&result.strandHistory, record.Chapter, facts.DominantStrand)
	}
	return result, nil
}

func (p *Projector) Apply(records []domain.ChapterRecord) error {
	result, err := p.build(records)
	if err != nil {
		return err
	}

	for _, summary := range result.summaries {
		if err := p.store.Summaries.SaveSummary(summary); err != nil {
			return fmt.Errorf("保存第 %d 章摘要: %w", summary.Chapter, err)
		}
	}
	if err := p.store.World.SaveTimeline(result.timeline); err != nil {
		return fmt.Errorf("重建时间线: %w", err)
	}
	if err := p.store.World.SaveForeshadowLedger(result.foreshadow); err != nil {
		return fmt.Errorf("重建伏笔账本: %w", err)
	}
	if err := p.store.World.SaveRelationships(result.relationships); err != nil {
		return fmt.Errorf("重建人物关系: %w", err)
	}
	if err := p.store.World.SaveStateChanges(result.stateChanges); err != nil {
		return fmt.Errorf("重建状态变化: %w", err)
	}
	if err := p.updateProgress(result); err != nil {
		return err
	}
	if err := p.store.World.SaveAuthorRevisionStyle(result.style); err != nil {
		return fmt.Errorf("保存用户修订风格: %w", err)
	}
	return nil
}

func projectWorld(records []domain.ChapterRecord) ([]domain.TimelineEvent, []domain.ForeshadowEntry, []domain.RelationshipEntry, []domain.StateChange, error) {
	var timeline []domain.TimelineEvent
	var changes []domain.StateChange
	ledger := make([]domain.ForeshadowEntry, 0)
	foreshadowIndex := make(map[string]int)
	relationships := make(map[string]domain.RelationshipEntry)

	for _, record := range records {
		chapter := record.Chapter
		for _, event := range record.Facts.TimelineEvents {
			event.Chapter = chapter
			timeline = append(timeline, event)
		}
		for _, change := range record.Facts.StateChanges {
			change.Chapter = chapter
			changes = append(changes, change)
		}
		for _, relation := range record.Facts.RelationshipChanges {
			relation.Chapter = chapter
			relationships[relationshipKey(relation.CharacterA, relation.CharacterB)] = relation
		}
		for _, update := range record.Facts.ForeshadowUpdates {
			idx, exists := foreshadowIndex[update.ID]
			switch update.Action {
			case "plant":
				if strings.TrimSpace(update.ID) == "" {
					return nil, nil, nil, nil, fmt.Errorf("第 %d 章伏笔 plant 缺少 id", chapter)
				}
				if exists {
					if ledger[idx].Description == "" {
						ledger[idx].Description = update.Description
					}
					continue
				}
				foreshadowIndex[update.ID] = len(ledger)
				ledger = append(ledger, domain.ForeshadowEntry{ID: update.ID, Description: update.Description, PlantedAt: chapter, Status: "planted"})
			case "advance":
				if !exists {
					return nil, nil, nil, nil, fmt.Errorf("第 %d 章推进未知伏笔 %q", chapter, update.ID)
				}
				ledger[idx].Status = "advanced"
			case "resolve":
				if !exists {
					return nil, nil, nil, nil, fmt.Errorf("第 %d 章回收未知伏笔 %q", chapter, update.ID)
				}
				ledger[idx].Status = "resolved"
				ledger[idx].ResolvedAt = chapter
			default:
				return nil, nil, nil, nil, fmt.Errorf("第 %d 章伏笔操作非法: %q", chapter, update.Action)
			}
		}
	}

	relationList := make([]domain.RelationshipEntry, 0, len(relationships))
	for _, relation := range relationships {
		relationList = append(relationList, relation)
	}
	slices.SortFunc(relationList, func(a, b domain.RelationshipEntry) int {
		return strings.Compare(relationshipKey(a.CharacterA, a.CharacterB), relationshipKey(b.CharacterA, b.CharacterB))
	})
	return timeline, ledger, relationList, changes, nil
}

func (p *Projector) updateProgress(result projection) error {
	progress, err := p.store.Progress.Load()
	if err != nil {
		return fmt.Errorf("读取进度: %w", err)
	}
	if progress == nil {
		return fmt.Errorf("progress 未初始化")
	}
	progress.ChapterWordCounts = result.wordCounts
	progress.TotalWordCount = result.totalWords
	progress.HookHistory = result.hookHistory
	progress.StrandHistory = result.strandHistory
	if err := p.store.Progress.Save(progress); err != nil {
		return fmt.Errorf("更新章节进度投影: %w", err)
	}
	return nil
}

func projectStyle(records []domain.ChapterRecord) domain.AuthorRevisionStyle {
	style := domain.AuthorRevisionStyle{}
	style.Prose = appendUnique(style.Prose, collectStyle(records, func(s domain.StyleDelta) []string { return s.Prose })...)
	style.Taboos = appendUnique(style.Taboos, collectStyle(records, func(s domain.StyleDelta) []string { return s.Taboos })...)
	voiceIndex := make(map[string]int)
	for _, record := range records {
		if record.Origin == domain.ChapterOriginUser && record.AcceptedAt.After(style.UpdatedAt) {
			style.UpdatedAt = record.AcceptedAt
		}
		for _, voice := range record.StyleDelta.Dialogue {
			idx, exists := voiceIndex[voice.Name]
			if !exists {
				idx = len(style.Dialogue)
				voiceIndex[voice.Name] = idx
				style.Dialogue = append(style.Dialogue, domain.CharacterVoice{Name: voice.Name})
			}
			style.Dialogue[idx].Rules = appendUnique(style.Dialogue[idx].Rules, voice.Rules...)
		}
	}
	return style
}

func collectStyle(records []domain.ChapterRecord, selectItems func(domain.StyleDelta) []string) []string {
	var out []string
	for _, record := range records {
		out = append(out, selectItems(record.StyleDelta)...)
	}
	return out
}

func appendUnique(dst []string, values ...string) []string {
	seen := make(map[string]bool, len(dst)+len(values))
	for _, value := range dst {
		seen[value] = true
	}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		dst = append(dst, value)
	}
	return dst
}

func relationshipKey(a, b string) string {
	if a > b {
		a, b = b, a
	}
	return a + "|" + b
}

func setChapterHistory(history *[]string, chapter int, value string) {
	if value == "" {
		return
	}
	for len(*history) < chapter {
		*history = append(*history, "")
	}
	(*history)[chapter-1] = value
}
