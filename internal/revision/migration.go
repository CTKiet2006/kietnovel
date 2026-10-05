package revision

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/store"
)

// MigrateLegacyBaseline 从旧版业务投影逆向生成章节记录。会话日志不是作品事实，
// 因此不参与迁移。保存前正向投影一次与原状态比对，有出入只记日志：升级不改磁盘上的
// 世界状态，也不因此拦住用户。
func MigrateLegacyBaseline(st *store.Store) error {
	progress, err := st.Progress.Load()
	if err != nil {
		return fmt.Errorf("读取进度: %w", err)
	}
	if progress == nil || len(progress.CompletedChapters) == 0 {
		return nil
	}

	chapters := slices.Clone(progress.CompletedChapters)
	slices.Sort(chapters)
	existing := make([]domain.ChapterRecord, 0, len(chapters))
	missing := make(map[int]bool)
	for _, chapter := range chapters {
		record, err := st.ChapterRecords.Load(chapter)
		if err != nil {
			return fmt.Errorf("读取第 %d 章接纳记录: %w", chapter, err)
		}
		if record != nil {
			existing = append(existing, *record)
			continue
		}
		missing[chapter] = true
	}
	if len(missing) == 0 {
		return ValidateRecords(existing)
	}

	previous, err := loadLegacyProjection(st, chapters, progress)
	if err != nil {
		return err
	}
	legacyCast, err := loadLegacyCast(st)
	if err != nil {
		return fmt.Errorf("读取配角名册: %w", err)
	}
	pending := make(map[int]*domain.ChapterRecord, len(missing))
	for _, summary := range previous.summaries {
		if !missing[summary.Chapter] {
			continue
		}
		record, err := buildLegacyRecord(st, summary)
		if err != nil {
			return err
		}
		pending[summary.Chapter] = &record
	}

	restoreLegacyFacts(existing, pending, chapters, legacyCast, &previous)
	records := slices.Clone(existing)
	for _, chapter := range chapters {
		if record := pending[chapter]; record != nil {
			records = append(records, *record)
		}
	}
	projected, err := NewProjector(st).build(records)
	if err != nil {
		return fmt.Errorf("旧版章节事实无法重放: %w", err)
	}
	if err := compareProjection(previous, projected); err != nil {
		slog.Warn("旧数据重建结果与原状态有出入，下次重建世界状态时以记录为准", "module", "migration", "err", err)
	}
	for _, chapter := range chapters {
		if record := pending[chapter]; record != nil {
			if err := st.ChapterRecords.Save(*record); err != nil {
				return fmt.Errorf("保存第 %d 章修订基线: %w", chapter, err)
			}
		}
	}
	return nil
}

func loadLegacyProjection(st *store.Store, chapters []int, progress *domain.Progress) (projection, error) {
	result := projection{
		hookHistory: progress.HookHistory, strandHistory: progress.StrandHistory,
	}
	for _, chapter := range chapters {
		summary, err := st.Summaries.LoadSummary(chapter)
		if err != nil {
			return result, fmt.Errorf("读取第 %d 章摘要: %w", chapter, err)
		}
		if summary == nil {
			slog.Warn("旧章节缺少摘要，以空事实建立基线", "module", "migration", "chapter", chapter)
			summary = &domain.ChapterSummary{}
		}
		summary.Chapter = chapter
		result.summaries = append(result.summaries, *summary)
	}
	var err error
	if result.timeline, err = st.World.LoadTimeline(); err != nil {
		return result, fmt.Errorf("读取时间线: %w", err)
	}
	if result.foreshadow, err = st.World.LoadForeshadowLedger(); err != nil {
		return result, fmt.Errorf("读取伏笔账本: %w", err)
	}
	if result.relationships, err = st.World.LoadRelationships(); err != nil {
		return result, fmt.Errorf("读取人物关系: %w", err)
	}
	if result.stateChanges, err = st.World.LoadStateChanges(); err != nil {
		return result, fmt.Errorf("读取状态变化: %w", err)
	}
	style, err := st.World.LoadAuthorRevisionStyle()
	if err != nil {
		return result, fmt.Errorf("读取用户修订风格: %w", err)
	}
	if style != nil {
		result.style = *style
	}
	result.normalizeLegacy(chapters)
	return result, nil
}

// normalizeLegacy 去掉旧版数据里不属于基线的条目：提交中途崩溃留下的未完成章引用、
// 早期版本允许的空 ID 与重复埋设。它们不影响写作，待恢复的提交会在迁移后重新写入。
func (p *projection) normalizeLegacy(chapters []int) {
	completed := func(chapter int) bool {
		_, ok := slices.BinarySearch(chapters, chapter)
		return ok
	}
	before := len(p.timeline) + len(p.stateChanges) + len(p.relationships) + len(p.foreshadow)
	p.timeline = slices.DeleteFunc(p.timeline, func(e domain.TimelineEvent) bool { return !completed(e.Chapter) })
	p.stateChanges = slices.DeleteFunc(p.stateChanges, func(c domain.StateChange) bool { return !completed(c.Chapter) })
	p.relationships = slices.DeleteFunc(p.relationships, func(r domain.RelationshipEntry) bool { return !completed(r.Chapter) })

	var ledger []domain.ForeshadowEntry
	index := make(map[string]int)
	for _, entry := range p.foreshadow {
		if entry.ID == "" || !completed(entry.PlantedAt) {
			continue
		}
		if entry.Status == "resolved" && !completed(entry.ResolvedAt) {
			entry.Status, entry.ResolvedAt = "advanced", 0
		}
		// 早期重复埋设会追加同 ID 条目，后一条才是当时的活动状态。
		if i, seen := index[entry.ID]; seen {
			ledger[i] = entry
			continue
		}
		index[entry.ID] = len(ledger)
		ledger = append(ledger, entry)
	}
	p.foreshadow = ledger
	if after := len(p.timeline) + len(p.stateChanges) + len(p.relationships) + len(p.foreshadow); after < before {
		slog.Warn("忽略不属于基线的旧数据条目", "module", "migration", "count", before-after)
	}
}

func buildLegacyRecord(st *store.Store, summary domain.ChapterSummary) (domain.ChapterRecord, error) {
	chapter := summary.Chapter
	final, err := st.Drafts.LoadChapterText(chapter)
	if err != nil {
		return domain.ChapterRecord{}, fmt.Errorf("读取第 %d 章正文: %w", chapter, err)
	}
	draft, err := st.Drafts.LoadDraft(chapter)
	if err != nil {
		return domain.ChapterRecord{}, fmt.Errorf("读取第 %d 章历史草稿: %w", chapter, err)
	}
	hasFinal, hasDraft := strings.TrimSpace(final) != "", strings.TrimSpace(draft) != ""
	content := draft
	switch {
	case !hasFinal && !hasDraft:
		content = ""
		slog.Warn("旧章节正文与草稿都缺失，以空正文建立基线，请用重写重新生成", "module", "migration", "chapter", chapter)
	case !hasDraft:
		content = final
		slog.Info("旧章节无历史草稿，以当前正文建立 legacy 基线", "module", "migration", "chapter", chapter)
	case !hasFinal:
		// 草稿就是当年提交的正文，写回是确定性还原。
		if err := st.Drafts.SaveFinalChapter(chapter, draft); err != nil {
			return domain.ChapterRecord{}, fmt.Errorf("恢复第 %d 章正文: %w", chapter, err)
		}
		slog.Warn("旧章节正文缺失，已从草稿恢复", "module", "migration", "chapter", chapter)
	case domain.NormalizeChapterContent(draft) != domain.NormalizeChapterContent(final):
		slog.Info("旧章节正文有外部修订，迁移后需执行 /sync", "module", "migration", "chapter", chapter)
	}
	content = domain.NormalizeChapterContent(content)

	return domain.ChapterRecord{
		Version: domain.ChapterRecordVersion, Chapter: chapter, Revision: 1,
		Origin: domain.ChapterOriginLegacy, Content: content,
		ContentSHA256: domain.ChapterContentSHA256(content),
		Facts: domain.ChapterFacts{
			Title: summary.Title, Summary: summary.Summary,
			Characters: slices.Clone(summary.Characters), KeyEvents: slices.Clone(summary.KeyEvents),
		},
		AcceptedAt: legacyAcceptedAt(st, chapter),
	}, nil
}

func legacyAcceptedAt(st *store.Store, chapter int) time.Time {
	if checkpoint := st.Checkpoints.LatestByStep(domain.ChapterScope(chapter), "commit"); checkpoint != nil && !checkpoint.OccurredAt.IsZero() {
		return checkpoint.OccurredAt
	}
	if info, err := os.Stat(filepath.Join(st.Dir(), "chapters", fmt.Sprintf("%02d.md", chapter))); err == nil {
		return info.ModTime()
	}
	return time.Now()
}

type legacyCastEntry struct {
	Name             string   `json:"name"`
	Aliases          []string `json:"aliases,omitempty"`
	BriefRole        string   `json:"brief_role,omitempty"`
	FirstSeenChapter int      `json:"first_seen_chapter"`
}

func loadLegacyCast(st *store.Store) ([]legacyCastEntry, error) {
	data, err := os.ReadFile(filepath.Join(st.Dir(), "meta", "cast_ledger.json"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var entries []legacyCastEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, err
	}
	return entries, nil
}

func restoreLegacyFacts(existing []domain.ChapterRecord, pending map[int]*domain.ChapterRecord, chapters []int, legacyCast []legacyCastEntry, previous *projection) {
	for chapter, record := range pending {
		record.Facts.HookType = historyAt(previous.hookHistory, chapter)
		record.Facts.DominantStrand = historyAt(previous.strandHistory, chapter)
	}
	for _, event := range previous.timeline {
		if record := pending[event.Chapter]; record != nil {
			record.Facts.TimelineEvents = append(record.Facts.TimelineEvents, event)
		}
	}
	for _, change := range previous.stateChanges {
		if record := pending[change.Chapter]; record != nil {
			record.Facts.StateChanges = append(record.Facts.StateChanges, change)
		}
	}
	for _, relation := range previous.relationships {
		if record := pending[relation.Chapter]; record != nil {
			record.Facts.RelationshipChanges = append(record.Facts.RelationshipChanges, relation)
		}
	}
	for _, entry := range legacyCast {
		record := pending[entry.FirstSeenChapter]
		if record == nil || entry.BriefRole == "" {
			continue
		}
		name := castNameInSummary(entry, record.Facts.Characters)
		if name != "" {
			record.Facts.CastIntros = append(record.Facts.CastIntros, domain.CastIntro{Name: name, BriefRole: entry.BriefRole})
		}
	}
	restoreForeshadow(existing, pending, chapters, previous)
}

// restoreForeshadow 把账本条目还原成各章的 plant/advance/resolve。还原不了的条目
// 说明已有记录与账本矛盾，丢弃并记日志，不让一条伏笔挡住升级。
func restoreForeshadow(existing []domain.ChapterRecord, pending map[int]*domain.ChapterRecord, chapters []int, previous *projection) {
	kept := previous.foreshadow[:0]
	for _, entry := range previous.foreshadow {
		if !restoreForeshadowEntry(existing, pending, chapters, entry) {
			slog.Warn("伏笔无法从旧数据还原，已忽略", "module", "migration", "id", entry.ID, "status", entry.Status)
			continue
		}
		kept = append(kept, entry)
	}
	previous.foreshadow = kept
}

func restoreForeshadowEntry(existing []domain.ChapterRecord, pending map[int]*domain.ChapterRecord, chapters []int, entry domain.ForeshadowEntry) bool {
	type placement struct {
		chapter int
		update  domain.ForeshadowUpdate
	}
	var placements []placement
	place := func(chapter int, update domain.ForeshadowUpdate) bool {
		if pending[chapter] == nil {
			return false
		}
		placements = append(placements, placement{chapter, update})
		return true
	}
	if !hasForeshadowAction(existing, pending, entry.ID, "plant") &&
		!place(entry.PlantedAt, domain.ForeshadowUpdate{ID: entry.ID, Action: "plant", Description: entry.Description}) {
		return false
	}
	switch entry.Status {
	case "planted":
	case "advanced":
		if !hasForeshadowAction(existing, pending, entry.ID, "advance") &&
			!place(firstPendingChapterAtOrAfter(chapters, pending, entry.PlantedAt), domain.ForeshadowUpdate{ID: entry.ID, Action: "advance"}) {
			return false
		}
	case "resolved":
		if !hasForeshadowAction(existing, pending, entry.ID, "resolve") &&
			!place(entry.ResolvedAt, domain.ForeshadowUpdate{ID: entry.ID, Action: "resolve"}) {
			return false
		}
	default:
		return false
	}
	// 全部落点确认后再写入，避免半还原的条目混进记录。
	for _, p := range placements {
		record := pending[p.chapter]
		record.Facts.ForeshadowUpdates = append(record.Facts.ForeshadowUpdates, p.update)
	}
	return true
}

func hasForeshadowAction(existing []domain.ChapterRecord, pending map[int]*domain.ChapterRecord, id, action string) bool {
	hasAction := func(record domain.ChapterRecord) bool {
		for _, update := range record.Facts.ForeshadowUpdates {
			if update.ID == id && update.Action == action {
				return true
			}
		}
		return false
	}
	for _, record := range existing {
		if hasAction(record) {
			return true
		}
	}
	for _, record := range pending {
		if hasAction(*record) {
			return true
		}
	}
	return false
}

func firstPendingChapterAtOrAfter(chapters []int, pending map[int]*domain.ChapterRecord, chapter int) int {
	for _, candidate := range chapters {
		if candidate >= chapter && pending[candidate] != nil {
			return candidate
		}
	}
	return 0
}

func castNameInSummary(entry legacyCastEntry, characters []string) string {
	for _, name := range characters {
		if name == entry.Name || slices.Contains(entry.Aliases, name) {
			return name
		}
	}
	return ""
}

func historyAt(history []string, chapter int) string {
	if chapter > 0 && chapter <= len(history) {
		return history[chapter-1]
	}
	return ""
}

func compareProjection(previous, projected projection) error {
	if chapter, ok := mismatchedChapter(previous.summaries, projected.summaries,
		func(a, b domain.ChapterSummary) bool {
			return a.Chapter == b.Chapter && a.Title == b.Title && a.Summary == b.Summary &&
				slices.Equal(a.Characters, b.Characters) && slices.Equal(a.KeyEvents, b.KeyEvents)
		}, func(value domain.ChapterSummary) int { return value.Chapter }); ok {
		return fmt.Errorf("第 %d 章摘要不一致", chapter)
	}
	if chapter, ok := mismatchedChapter(previous.timeline, projected.timeline,
		func(a, b domain.TimelineEvent) bool {
			return a.Chapter == b.Chapter && a.Time == b.Time && a.Event == b.Event && slices.Equal(a.Characters, b.Characters)
		}, func(value domain.TimelineEvent) int { return value.Chapter }); ok {
		return fmt.Errorf("第 %d 章时间线不一致", chapter)
	}
	if id, ok := mismatchedKey(previous.foreshadow, projected.foreshadow,
		func(value domain.ForeshadowEntry) string { return value.ID }); ok {
		return fmt.Errorf("伏笔 %q 不一致", id)
	}
	if key, ok := mismatchedKey(previous.relationships, projected.relationships,
		func(value domain.RelationshipEntry) string {
			return relationshipKey(value.CharacterA, value.CharacterB)
		}); ok {
		return fmt.Errorf("人物关系 %q 不一致", key)
	}
	if chapter, ok := mismatchedChapter(previous.stateChanges, projected.stateChanges,
		func(a, b domain.StateChange) bool { return a == b },
		func(value domain.StateChange) int { return value.Chapter }); ok {
		return fmt.Errorf("第 %d 章状态变化不一致", chapter)
	}
	if chapter := mismatchedHistory(previous.hookHistory, projected.hookHistory); chapter != 0 {
		return fmt.Errorf("第 %d 章 hook_type 不一致", chapter)
	}
	if chapter := mismatchedHistory(previous.strandHistory, projected.strandHistory); chapter != 0 {
		return fmt.Errorf("第 %d 章 dominant_strand 不一致", chapter)
	}
	if !equalStyle(previous.style, projected.style) {
		return fmt.Errorf("用户修订风格不一致")
	}
	return nil
}

func mismatchedChapter[T any](a, b []T, equal func(T, T) bool, chapter func(T) int) (int, bool) {
	limit := min(len(a), len(b))
	for i := 0; i < limit; i++ {
		if !equal(a[i], b[i]) {
			return chapter(a[i]), true
		}
	}
	if len(a) > limit {
		return chapter(a[limit]), true
	}
	if len(b) > limit {
		return chapter(b[limit]), true
	}
	return 0, false
}

func mismatchedKey[T comparable](a, b []T, key func(T) string) (string, bool) {
	right := make(map[string]T, len(b))
	for _, value := range b {
		right[key(value)] = value
	}
	for _, value := range a {
		k := key(value)
		if actual, ok := right[k]; !ok || actual != value {
			return k, true
		}
		delete(right, k)
	}
	for _, value := range b {
		if _, extra := right[key(value)]; extra {
			return key(value), true
		}
	}
	return "", false
}

func mismatchedHistory(a, b []string) int {
	a, b = trimHistory(a), trimHistory(b)
	for i := 0; i < max(len(a), len(b)); i++ {
		if historyAt(a, i+1) != historyAt(b, i+1) {
			return i + 1
		}
	}
	return 0
}

func trimHistory(values []string) []string {
	end := len(values)
	for end > 0 && values[end-1] == "" {
		end--
	}
	return values[:end]
}

func equalStyle(a, b domain.AuthorRevisionStyle) bool {
	if !slices.Equal(a.Prose, b.Prose) || !slices.Equal(a.Taboos, b.Taboos) || !a.UpdatedAt.Equal(b.UpdatedAt) || len(a.Dialogue) != len(b.Dialogue) {
		return false
	}
	for i := range a.Dialogue {
		if a.Dialogue[i].Name != b.Dialogue[i].Name || !slices.Equal(a.Dialogue[i].Rules, b.Dialogue[i].Rules) {
			return false
		}
	}
	return true
}
