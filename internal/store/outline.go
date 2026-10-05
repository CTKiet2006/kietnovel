package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"time"

	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/errs"
)

// ErrOutlineChapterNotFound 表示章节尚未进入当前大纲。
var ErrOutlineChapterNotFound = errors.New("outline chapter not found")

// OutlineStore 管理故事前提、大纲（扁平/分层）和指南针。
type OutlineStore struct{ io *IO }

func NewOutlineStore(io *IO) *OutlineStore { return &OutlineStore{io: io} }

// SavePremise 保存故事前提到 premise.md。
func (s *OutlineStore) SavePremise(content string) error {
	return s.io.WriteMarkdown("premise.md", content)
}

// LoadPremise 读取 premise.md。不存在时返回空字符串。
func (s *OutlineStore) LoadPremise() (string, error) {
	data, err := s.io.ReadFile("premise.md")
	if os.IsNotExist(err) {
		return "", nil
	}
	return string(data), err
}

// SaveOutline 同时保存 outline.json 和 outline.md（原子写入）。
func (s *OutlineStore) SaveOutline(entries []domain.OutlineEntry) error {
	return s.io.WithWriteLock(func() error {
		return s.saveOutlineUnlocked(entries)
	})
}

func (s *OutlineStore) saveOutlineUnlocked(entries []domain.OutlineEntry) error {
	if err := s.io.WriteJSONUnlocked("outline.json", entries); err != nil {
		return err
	}
	return s.io.WriteMarkdownUnlocked("outline.md", renderOutline(entries))
}

// LoadOutline 从 outline.json 读取结构化大纲。
func (s *OutlineStore) LoadOutline() ([]domain.OutlineEntry, error) {
	var entries []domain.OutlineEntry
	if err := s.io.ReadJSON("outline.json", &entries); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return entries, nil
}

// GetChapterOutline 获取指定章节的大纲条目。
func (s *OutlineStore) GetChapterOutline(chapter int) (*domain.OutlineEntry, error) {
	entries, err := s.LoadOutline()
	if err != nil {
		return nil, err
	}
	for i := range entries {
		if entries[i].Chapter == chapter {
			return &entries[i], nil
		}
	}
	return nil, fmt.Errorf("%w: chapter %d", ErrOutlineChapterNotFound, chapter)
}

// SaveLayeredOutline 以分层大纲为唯一来源，保存分层视图并同步重建扁平派生视图。
// 调用方不需要、也不应再单独维护 outline.json/outline.md。
func (s *OutlineStore) SaveLayeredOutline(volumes []domain.VolumeOutline) error {
	return s.io.WithWriteLock(func() error {
		assignLayeredIndexes(volumes)
		return s.saveLayeredViewsUnlocked(volumes)
	})
}

// LoadLayeredOutline 读取分层大纲。
func (s *OutlineStore) LoadLayeredOutline() ([]domain.VolumeOutline, error) {
	var volumes []domain.VolumeOutline
	if err := s.io.ReadJSON("layered_outline.json", &volumes); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	repairMissingLayeredIndexes(volumes)
	return volumes, nil
}

// ClearLayeredOutline 清理分层大纲文件。
func (s *OutlineStore) ClearLayeredOutline() error {
	return s.io.WithWriteLock(func() error {
		if err := s.io.RemoveFileUnlocked("layered_outline.json"); err != nil {
			return err
		}
		return s.io.RemoveFileUnlocked("layered_outline.md")
	})
}

// GetChapterFromLayered 从分层大纲中按全局章节号查找。
func (s *OutlineStore) GetChapterFromLayered(chapter int) (*domain.OutlineEntry, error) {
	volumes, err := s.LoadLayeredOutline()
	if err != nil {
		return nil, err
	}
	ch := 1
	for _, v := range volumes {
		for _, a := range v.Arcs {
			for i := range a.Chapters {
				if ch == chapter {
					e := a.Chapters[i]
					e.Chapter = ch
					return &e, nil
				}
				ch++
			}
		}
	}
	return nil, fmt.Errorf("%w: chapter %d in layered outline", ErrOutlineChapterNotFound, chapter)
}

// LocateChapter 根据全局章节号定位所在的卷和弧。
func (s *OutlineStore) LocateChapter(chapter int) (volume, arc int, err error) {
	volumes, err := s.LoadLayeredOutline()
	if err != nil {
		return 0, 0, err
	}
	ch := 1
	for _, v := range volumes {
		for _, a := range v.Arcs {
			for range a.Chapters {
				if ch == chapter {
					return v.Index, a.Index, nil
				}
				ch++
			}
		}
	}
	return 0, 0, fmt.Errorf("%w: chapter %d in layered outline", ErrOutlineChapterNotFound, chapter)
}

// ArcBoundary 弧边界信息。
type ArcBoundary struct {
	IsArcEnd       bool
	IsVolumeEnd    bool
	Volume         int
	Arc            int
	StartChapter   int
	EndChapter     int
	NextVolume     int
	NextArc        int
	NeedsExpansion bool
	NeedsNewVolume bool // 卷末且当前 layered_outline 没有下一卷
	nextVolumePos  int
	nextArcPos     int
}

// HasNextArc 是否还有后续弧。
func (b *ArcBoundary) HasNextArc() bool {
	return b.NextVolume > 0 || b.NextArc > 0
}

// CheckArcBoundary 检查某章是否为弧/卷的最后一章。
func (s *OutlineStore) CheckArcBoundary(chapter int) (*ArcBoundary, error) {
	volumes, err := s.LoadLayeredOutline()
	if err != nil || len(volumes) == 0 {
		return nil, err
	}
	return checkArcBoundary(volumes, chapter), nil
}

func checkArcBoundary(volumes []domain.VolumeOutline, chapter int) *ArcBoundary {
	type arcPos struct {
		volIdx, arcIdx int
		volume, arc    int
		chInArc        int
		arcLen         int
		arcStart       int
	}

	ch := 1
	var cur *arcPos
	for vi, v := range volumes {
		for ai, a := range v.Arcs {
			arcStart := ch
			for ci := range a.Chapters {
				if ch == chapter {
					cur = &arcPos{
						volIdx:   vi,
						arcIdx:   ai,
						volume:   v.Index,
						arc:      a.Index,
						chInArc:  ci,
						arcLen:   len(a.Chapters),
						arcStart: arcStart,
					}
				}
				ch++
			}
		}
	}
	if cur == nil {
		return nil
	}

	b := &ArcBoundary{
		Volume:        cur.volume,
		Arc:           cur.arc,
		StartChapter:  cur.arcStart,
		EndChapter:    cur.arcStart + cur.arcLen - 1,
		nextVolumePos: -1,
		nextArcPos:    -1,
	}

	isLastChInArc := cur.chInArc == cur.arcLen-1
	isLastArcInVol := cur.arcIdx == len(volumes[cur.volIdx].Arcs)-1

	// Next*/NeedsExpansion/NeedsNewVolume 只在弧末才有意义，否则会让协调者误以为要提前展开下一弧。
	if !isLastChInArc {
		return b
	}

	b.IsArcEnd = true
	if isLastArcInVol {
		b.IsVolumeEnd = true
	}

	found := false
	for vi := cur.volIdx; vi < len(volumes); vi++ {
		startArc := 0
		if vi == cur.volIdx {
			startArc = cur.arcIdx + 1
		}
		for ai := startArc; ai < len(volumes[vi].Arcs); ai++ {
			b.NextVolume = volumes[vi].Index
			b.NextArc = volumes[vi].Arcs[ai].Index
			b.NeedsExpansion = !volumes[vi].Arcs[ai].IsExpanded()
			b.nextVolumePos = vi
			b.nextArcPos = ai
			found = true
			break
		}
		if found {
			break
		}
	}

	if b.IsVolumeEnd && !found {
		b.NeedsNewVolume = true
	}

	return b
}

// CompletedArcBoundaries 按故事顺序返回已完成的详细弧边界。
func (s *OutlineStore) CompletedArcBoundaries(lastCompleted int) ([]ArcBoundary, error) {
	volumes, err := s.LoadLayeredOutline()
	if err != nil {
		return nil, err
	}
	chapter := 1
	var result []ArcBoundary
	for _, volume := range volumes {
		for arcIndex, arc := range volume.Arcs {
			if len(arc.Chapters) == 0 {
				continue
			}
			start := chapter
			end := start + len(arc.Chapters) - 1
			chapter = end + 1
			if end > lastCompleted {
				return result, nil
			}
			result = append(result, ArcBoundary{
				IsArcEnd: true, IsVolumeEnd: arcIndex == len(volume.Arcs)-1,
				Volume: volume.Index, Arc: arc.Index, StartChapter: start, EndChapter: end,
			})
		}
	}
	return result, nil
}

// expandArcAtUnlocked 展开故事顺序中的指定弧，不接受模型提供的结构主键。
func (s *OutlineStore) expandArcAtUnlocked(volumes []domain.VolumeOutline, volumePos, arcPos int, expansion domain.ArcExpansion) ([]domain.VolumeOutline, error) {
	if strings.TrimSpace(expansion.Title) == "" {
		return nil, fmt.Errorf("弧标题不能为空")
	}
	if strings.TrimSpace(expansion.Goal) == "" {
		return nil, fmt.Errorf("弧目标不能为空")
	}
	if len(expansion.Chapters) == 0 {
		return nil, fmt.Errorf("展开弧必须至少包含一章")
	}

	if volumePos < 0 || volumePos >= len(volumes) || arcPos < 0 || arcPos >= len(volumes[volumePos].Arcs) {
		return nil, fmt.Errorf("arc position out of range: volume=%d, arc=%d", volumePos+1, arcPos+1)
	}
	arc := &volumes[volumePos].Arcs[arcPos]
	if arc.IsExpanded() {
		current := domain.ArcExpansion{Title: arc.Title, Goal: arc.Goal, Chapters: arc.Chapters}
		if !reflect.DeepEqual(current, expansion) {
			return nil, fmt.Errorf("arc already expanded: volume=%d, arc=%d", volumePos+1, arcPos+1)
		}
	} else {
		arc.Title = expansion.Title
		arc.Goal = expansion.Goal
		arc.Chapters = expansion.Chapters
		arc.EstimatedChapters = 0
	}
	if err := s.saveLayeredViewsUnlocked(volumes); err != nil {
		return nil, err
	}
	return volumes, nil
}

// appendVolumeUnlocked 内部方法，在 Store.AppendVolume 跨域协调中调用。
func (s *OutlineStore) appendVolumeUnlocked(vol domain.VolumeOutline) ([]domain.VolumeOutline, domain.VolumeOutline, error) {
	var volumes []domain.VolumeOutline
	if err := s.io.ReadJSONUnlocked("layered_outline.json", &volumes); err != nil {
		return nil, domain.VolumeOutline{}, fmt.Errorf("load layered_outline: %w", err)
	}
	repairMissingLayeredIndexes(volumes)
	nextIndex := 1
	if len(volumes) > 0 {
		nextIndex = volumes[len(volumes)-1].Index + 1
	}
	numberVolume(&vol, nextIndex)
	// AppendVolume 的下一步还要更新 Progress。若进程在“大纲已追加、Progress
	// 未更新”之间中断，恢复会用同一持久化载荷重试；完全相同的末卷应视为幂等，
	// 让同参数重试继续补齐 Progress，而不是重复追加。
	if len(volumes) > 0 && sameVolumePlan(volumes[len(volumes)-1], vol) {
		vol = volumes[len(volumes)-1]
	} else {
		if err := validateAppendVolume(vol); err != nil {
			return nil, domain.VolumeOutline{}, err
		}
		volumes = append(volumes, vol)
	}
	// 即使末卷已存在也重写全部派生视图；上次可能恰好在 layered JSON 落盘后、
	// flat outline/Markdown 写入前中断。
	if err := s.saveLayeredViewsUnlocked(volumes); err != nil {
		return nil, domain.VolumeOutline{}, err
	}
	return volumes, vol, nil
}

// saveLayeredViewsUnlocked 以分层大纲为唯一来源，统一重建其 Markdown 与扁平派生视图。
// 调用方必须持有 OutlineStore 的写锁。
func (s *OutlineStore) saveLayeredViewsUnlocked(volumes []domain.VolumeOutline) error {
	repairMissingLayeredIndexes(volumes)
	if err := s.io.WriteJSONUnlocked("layered_outline.json", volumes); err != nil {
		return err
	}
	if err := s.io.WriteMarkdownUnlocked("layered_outline.md", renderLayeredOutline(volumes)); err != nil {
		return err
	}
	if err := s.saveOutlineUnlocked(domain.FlattenOutline(volumes)); err != nil {
		return err
	}
	return nil
}

func (s *OutlineStore) reviseFlatTailUnlocked(fromChapter int, replacement []domain.OutlineEntry) ([]domain.OutlineEntry, error) {
	var outline []domain.OutlineEntry
	if err := s.io.ReadJSONUnlocked("outline.json", &outline); err != nil {
		return nil, fmt.Errorf("load outline: %w: %w", errs.ErrStoreRead, err)
	}
	if fromChapter > len(outline)+1 {
		return nil, fmt.Errorf("from_chapter=%d 超出大纲末尾 %d: %w",
			fromChapter, len(outline), errs.ErrToolPrecondition)
	}
	updated := append([]domain.OutlineEntry(nil), outline[:fromChapter-1]...)
	updated = append(updated, replacement...)
	if len(updated) == 0 {
		return nil, fmt.Errorf("修订后大纲不能为空: %w", errs.ErrToolPrecondition)
	}
	for i := range updated {
		updated[i].Chapter = i + 1
	}
	if err := s.saveOutlineUnlocked(updated); err != nil {
		return nil, fmt.Errorf("save outline: %w: %w", errs.ErrStoreWrite, err)
	}
	return updated, nil
}

func (s *OutlineStore) reviseLayeredTailUnlocked(fromChapter int, replacement []domain.OutlineEntry) ([]domain.VolumeOutline, error) {
	var volumes []domain.VolumeOutline
	if err := s.io.ReadJSONUnlocked("layered_outline.json", &volumes); err != nil {
		return nil, fmt.Errorf("load layered_outline: %w: %w", errs.ErrStoreRead, err)
	}
	if err := reviseLayeredTail(volumes, fromChapter, replacement); err != nil {
		return nil, fmt.Errorf("%w: %w", errs.ErrToolPrecondition, err)
	}
	if err := s.saveLayeredViewsUnlocked(volumes); err != nil {
		return nil, fmt.Errorf("save layered outline: %w: %w", errs.ErrStoreWrite, err)
	}
	return volumes, nil
}

// reviseLayeredTail 替换 fromChapter 所在弧从该章起的尾段。若 fromChapter 正好
// 位于当前扁平大纲末尾之后，则追加到最后一个已展开弧。
func reviseLayeredTail(volumes []domain.VolumeOutline, fromChapter int, replacement []domain.OutlineEntry) error {
	chapter := 1
	targetVolume, targetArc, local := -1, -1, -1
	lastVolume, lastArc := -1, -1
	for vi := range volumes {
		for ai := range volumes[vi].Arcs {
			chapters := volumes[vi].Arcs[ai].Chapters
			if len(chapters) == 0 {
				continue
			}
			lastVolume, lastArc = vi, ai
			if fromChapter >= chapter && fromChapter < chapter+len(chapters) {
				targetVolume, targetArc = vi, ai
				local = fromChapter - chapter
				break
			}
			chapter += len(chapters)
		}
		if targetVolume >= 0 {
			break
		}
	}
	if targetVolume < 0 && fromChapter == chapter && lastVolume >= 0 {
		targetVolume, targetArc = lastVolume, lastArc
		local = len(volumes[lastVolume].Arcs[lastArc].Chapters)
	}
	if targetVolume < 0 {
		return fmt.Errorf("from_chapter=%d 不在已展开大纲范围内", fromChapter)
	}

	arc := &volumes[targetVolume].Arcs[targetArc]
	updated := append([]domain.OutlineEntry(nil), arc.Chapters[:local]...)
	updated = append(updated, replacement...)
	if len(updated) == 0 {
		return fmt.Errorf("修订后目标弧不能为空")
	}
	arc.Chapters = updated
	arc.EstimatedChapters = 0
	return nil
}

func validateAppendVolume(vol domain.VolumeOutline) error {
	if len(vol.Arcs) == 0 {
		return fmt.Errorf("新卷必须至少包含一个弧")
	}
	if !vol.Arcs[0].IsExpanded() {
		return fmt.Errorf("新卷的首弧必须包含详细章节")
	}
	return nil
}

func assignLayeredIndexes(volumes []domain.VolumeOutline) {
	for i := range volumes {
		numberVolume(&volumes[i], i+1)
	}
}

// repairMissingLayeredIndexes 兼容旧版本曾把可选 index 漏写为 0 的大纲。
// 正数标识保持原样，避免改动已有摘要和 checkpoint 的作用域。
func repairMissingLayeredIndexes(volumes []domain.VolumeOutline) {
	for vi := range volumes {
		if volumes[vi].Index <= 0 {
			volumes[vi].Index = vi + 1
		}
		for ai := range volumes[vi].Arcs {
			if volumes[vi].Arcs[ai].Index <= 0 {
				volumes[vi].Arcs[ai].Index = ai + 1
			}
		}
	}
}

func numberVolume(volume *domain.VolumeOutline, index int) {
	volume.Index = index
	for i := range volume.Arcs {
		volume.Arcs[i].Index = i + 1
	}
}

func sameVolumePlan(a, b domain.VolumeOutline) bool {
	numberVolume(&a, 1)
	numberVolume(&b, 1)
	return reflect.DeepEqual(a, b)
}

// SaveCompass 保存终局方向指南针。
func (s *OutlineStore) SaveCompass(compass domain.StoryCompass) error {
	if compass.EndingDirection == "" {
		return fmt.Errorf("ending_direction 不能为空")
	}
	return s.io.WriteJSON("meta/compass.json", compass)
}

// LoadCompass 读取终局方向指南针。
func (s *OutlineStore) LoadCompass() (*domain.StoryCompass, error) {
	var c domain.StoryCompass
	if err := s.io.ReadJSON("meta/compass.json", &c); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return &c, nil
}

// SaveFoundationAudit 保存 Architect 对当前基础设定版本的语义审查。
func (s *OutlineStore) SaveFoundationAudit(a domain.FoundationAudit) error {
	return s.io.WriteJSON("meta/foundation_audit.json", a)
}

// LoadFoundationAudit 读取最近一次基础设定语义审查。
func (s *OutlineStore) LoadFoundationAudit() (*domain.FoundationAudit, error) {
	var a domain.FoundationAudit
	if err := s.io.ReadJSON("meta/foundation_audit.json", &a); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return &a, nil
}

func renderLayeredOutline(volumes []domain.VolumeOutline) string {
	var b strings.Builder
	b.WriteString("# 分层大纲\n\n")
	ch := 1
	for _, v := range volumes {
		fmt.Fprintf(&b, "## 第 %d 卷：%s\n\n", v.Index, v.Title)
		fmt.Fprintf(&b, "**主题**：%s\n\n", v.Theme)
		for _, a := range v.Arcs {
			fmt.Fprintf(&b, "### 第 %d 弧：%s\n\n", a.Index, a.Title)
			fmt.Fprintf(&b, "**目标**：%s\n\n", a.Goal)
			if !a.IsExpanded() {
				fmt.Fprintf(&b, "*（待展开，预估 %d 章）*\n\n", a.EstimatedChapters)
				continue
			}
			for _, e := range a.Chapters {
				fmt.Fprintf(&b, "#### 第 %d 章：%s\n\n", ch, e.Title)
				fmt.Fprintf(&b, "**核心事件**：%s\n\n", e.CoreEvent)
				if e.Hook != "" {
					fmt.Fprintf(&b, "**钩子**：%s\n\n", e.Hook)
				}
				ch++
			}
		}
	}
	return b.String()
}

func renderOutline(entries []domain.OutlineEntry) string {
	var b strings.Builder
	b.WriteString("# 大纲\n\n")
	for _, e := range entries {
		fmt.Fprintf(&b, "## 第 %d 章：%s\n\n", e.Chapter, e.Title)
		fmt.Fprintf(&b, "**核心事件**：%s\n\n", e.CoreEvent)
		if e.Hook != "" {
			fmt.Fprintf(&b, "**钩子**：%s\n\n", e.Hook)
		}
		if len(e.Scenes) > 0 {
			b.WriteString("**场景**：\n")
			for i, sc := range e.Scenes {
				fmt.Fprintf(&b, "%d. %s\n", i+1, sc)
			}
			b.WriteString("\n")
		}
	}
	return b.String()
}

// ── Writer 大纲反馈池 ──
//
// commit_chapter 的 feedback(偏离/建议)持久化于此,architect 下次结构操作
// (expand_next_arc / append_volume / update_compass)经 novel_context 消费后清空。
// 事实闭环:工具落盘 → 上下文注入 → 结构操作即消费(docs/engine-arbiter.md 阻断1)。

// ChapterFeedback 一条带章节号的大纲反馈。
type ChapterFeedback struct {
	Chapter          int      `json:"chapter"`
	StoryChanged     bool     `json:"story_changed,omitempty"`
	ChangeSummary    string   `json:"change_summary,omitempty"`
	Deviation        string   `json:"deviation,omitempty"`
	Suggestion       string   `json:"suggestion,omitempty"`
	DownstreamIssues []string `json:"downstream_issues,omitempty"`
	At               string   `json:"at"`
}

// RequiresImmediateReview 区分外部修订影响与普通写作反馈。普通反馈留到下一次
// 自然结构操作统一吸收；外部修订可能使即将续写的大纲失效，必须先交 Architect。
func (f ChapterFeedback) RequiresImmediateReview() bool {
	return f.StoryChanged || strings.TrimSpace(f.ChangeSummary) != "" || len(f.DownstreamIssues) > 0
}

const outlineFeedbackFile = "meta/outline_feedback.jsonl"
const outlineFeedbackResolutionFile = "meta/outline_feedback_resolution.json"

// AppendOutlineFeedback 追加一条 writer 反馈。相同章节与内容视为同一事实，
// 使 commit 在 ProgressMarked 前崩溃重放时不会重复累加附属反馈。
func (s *OutlineStore) AppendOutlineFeedback(fb ChapterFeedback) error {
	return s.io.WithWriteLock(func() error {
		existing, err := s.io.ReadFileUnlocked(outlineFeedbackFile)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		currentFeedback, err := parseOutlineFeedback(existing)
		if err != nil {
			return err
		}
		for _, current := range currentFeedback {
			if current.Chapter == fb.Chapter && current.StoryChanged == fb.StoryChanged &&
				current.ChangeSummary == fb.ChangeSummary && current.Deviation == fb.Deviation &&
				current.Suggestion == fb.Suggestion && reflect.DeepEqual(current.DownstreamIssues, fb.DownstreamIssues) {
				return nil
			}
		}
		if fb.At == "" {
			fb.At = time.Now().Format(time.RFC3339)
		}
		data, err := json.Marshal(fb)
		if err != nil {
			return err
		}
		return s.io.AppendLineUnlocked(outlineFeedbackFile, append(data, '\n'))
	})
}

// LoadPendingOutlineFeedback 读取未消费的反馈(旧→新)。损坏行显式返回错误，
// 防止 Architect 在缺失部分反馈的上下文上继续结构操作并随后清空原文件。
func (s *OutlineStore) LoadPendingOutlineFeedback() ([]ChapterFeedback, error) {
	s.io.mu.RLock()
	defer s.io.mu.RUnlock()
	data, err := os.ReadFile(s.io.path(outlineFeedbackFile))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return parseOutlineFeedback(data)
}

func (s *OutlineStore) SaveOutlineFeedbackResolution(reason string, count int) error {
	return s.io.WriteJSON(outlineFeedbackResolutionFile, struct {
		Reason   string `json:"reason"`
		Resolved int    `json:"resolved"`
		At       string `json:"at"`
	}{Reason: reason, Resolved: count, At: time.Now().Format(time.RFC3339)})
}

func parseOutlineFeedback(data []byte) ([]ChapterFeedback, error) {
	var out []ChapterFeedback
	for lineNo, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var fb ChapterFeedback
		if err := json.Unmarshal([]byte(line), &fb); err != nil {
			return nil, fmt.Errorf("parse %s line %d: %w", outlineFeedbackFile, lineNo+1, err)
		}
		out = append(out, fb)
	}
	return out, nil
}

// ClearOutlineFeedback 清空反馈池(architect 结构操作成功 = 反馈已被参考)。
func (s *OutlineStore) ClearOutlineFeedback() error {
	s.io.mu.Lock()
	defer s.io.mu.Unlock()
	data, err := os.ReadFile(s.io.path(outlineFeedbackFile))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if _, err := parseOutlineFeedback(data); err != nil {
		return err
	}
	err = os.Remove(s.io.path(outlineFeedbackFile))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
