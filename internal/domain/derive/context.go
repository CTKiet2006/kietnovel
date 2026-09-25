package derive

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/voocel/ainovel-cli/internal/domain/model"
)

// StoryContextKind 是上下文的显式 schema 版本：结构演进时必须升版，
// 旧 Execution Profile 仍按其记录的版本解释，不做静默兼容。
const StoryContextKind = "story_context.v3"

// contextPolicy 是有界装配参数（§6.5）。它进入 ContextKey，改参数即换缓存键。
type contextPolicy struct {
	Window    int `json:"window"`    // 近 N 章：章节计划与来源于这些章的事件
	Lookahead int `json:"lookahead"` // 后 N 章章节计划
	Budget    int `json:"budget"`    // 故事上下文与任务合计的字符（rune）预算
}

var defaultPolicy = contextPolicy{Window: 5, Lookahead: 3, Budget: 32000}

func ContextKey(kind model.OperationKind, task json.RawMessage) (string, error) {
	if kind == "" || len(task) == 0 || !json.Valid(task) {
		return "", fmt.Errorf("context operation kind and task are required: %w", model.ErrInvalid)
	}
	decoder := json.NewDecoder(bytes.NewReader(task))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return "", err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return "", fmt.Errorf("context task must contain exactly one JSON value: %w", model.ErrInvalid)
	}
	return model.DigestJSON(struct {
		Version string              `json:"version"`
		Policy  contextPolicy       `json:"policy"`
		Kind    model.OperationKind `json:"kind"`
		Task    any                 `json:"task"`
	}{Version: StoryContextKind, Policy: defaultPolicy, Kind: kind, Task: value})
}

type ProjectContent struct {
	ID         string
	Revision   model.Revision
	Compass    *model.Compass
	Plan       []model.PlanNode
	Entities   []model.Entity
	Canon      []model.CanonFact
	Manuscript []model.ManuscriptChapter
	Ownership  []model.OwnershipRule
}

type ContextDocument struct {
	Ref     model.DocumentRef `json:"ref"`
	Content json.RawMessage   `json:"content"`
}

type ChapterIndexEntry struct {
	ID     string `json:"id"`
	Number int    `json:"number"`
	Title  string `json:"title"`
}

// ContextBudget 让裁剪可见：Used 超过 Limit 表示必选内容本身超预算；Omitted 是各类
// 文档未装配的数量，模型可凭已知 ID 用 authority_read 回查。
type ContextBudget struct {
	Limit   int                        `json:"limit"`
	Used    int                        `json:"used"`
	Omitted map[model.DocumentKind]int `json:"omitted,omitempty"`
}

// StoryContext 是按预算装配的有界视图。Intent 与 Ownership 在 project_rules 层，不重复。
type StoryContext struct {
	SchemaVersion string              `json:"schema_version"`
	ProjectID     string              `json:"project_id"`
	Revision      model.Revision      `json:"revision"`
	Documents     []ContextDocument   `json:"relevant_documents"`
	Chapters      []ChapterIndexEntry `json:"chapter_index"`
	Budget        ContextBudget       `json:"budget"`
}

func BuildStoryContext(content ProjectContent, kind model.OperationKind, task json.RawMessage) (StoryContext, error) {
	return buildStoryContext(content, kind, task, defaultPolicy)
}

// buildStoryContext 分两段装配（§6.5）：固定段大小与章数无关，照装；随作品增长的
// 当前事实与卷弧结构按优先级排序，吃剩余预算。整个流程只在一处截断。
func buildStoryContext(content ProjectContent, kind model.OperationKind, task json.RawMessage, policy contextPolicy) (StoryContext, error) {
	if strings.TrimSpace(content.ID) == "" || content.Revision <= model.InitialRevision || len(task) == 0 || !json.Valid(task) {
		return StoryContext{}, fmt.Errorf("context project, positive revision and task are required: %w", model.ErrInvalid)
	}
	for _, rule := range content.Ownership {
		if err := rule.Validate(); err != nil {
			return StoryContext{}, err
		}
	}
	input, err := model.DecodeTaskInput(kind, task)
	if err != nil {
		return StoryContext{}, err
	}
	s, err := newSelector(content, max(0, policy.Budget-utf8.RuneCount(task)))
	if err != nil {
		return StoryContext{}, err
	}
	switch input := input.(type) {
	case *model.InitializeProjectInput, *model.DevelopPlanInput, *model.RevisePlanInput:
		err = s.assemble(focus{anchor: s.firstUnwritten(), planning: true}, policy)
	case *model.WriteChapterInput:
		err = s.writing(input.ChapterPlanID, "", policy)
	case *model.RewriteChapterInput:
		err = s.writing(input.ChapterPlanID, input.ChapterID, policy)
	case *model.ReviseCanonInput:
		// 事实核验（D41）：本章正文与来源于它的事实，对着正文逐条核对。
		chapter, ok := s.manuscripts[input.ChapterID]
		if !ok {
			return StoryContext{}, fmt.Errorf("context dependency %q does not exist: %w", manuscriptRef(input.ChapterID).Key(), model.ErrInvalid)
		}
		err = s.assemble(focus{anchor: s.position[chapter.PlanNodeID], targets: s.chapterTargets(chapter.ID)}, policy)
	case *model.RewriteAffectedInput:
		var refs []model.DocumentRef
		for _, id := range input.ChapterIDs {
			refs = append(refs, s.chapterTargets(id)...)
		}
		err = s.scoped(refs)
	case *model.ReviewRangeInput:
		// 窗口审阅（D62）：窗口各章正文与来源事实是目标，锚在首章；衔接章、近期计划与
		// 事件随固定段进入，相关实体的当前事实按预算排序。
		anchor := len(s.chapters)
		var targets []model.DocumentRef
		for _, id := range input.ChapterIDs {
			if _, ok := s.manuscripts[id]; !ok {
				return StoryContext{}, fmt.Errorf("context dependency %q does not exist: %w", manuscriptRef(id).Key(), model.ErrInvalid)
			}
			anchor = min(anchor, s.chapterPosition(id))
			targets = append(targets, s.chapterTargets(id)...)
		}
		err = s.assemble(focus{anchor: anchor, targets: targets, previous: true}, policy)
	default:
		return StoryContext{}, fmt.Errorf("operation %s has no story context contract: %w", kind, model.ErrInvalid)
	}
	if err != nil {
		return StoryContext{}, err
	}
	return s.result(content), nil
}

// focus 描述锚定任务：锚点是章节计划有序下标，targets 是任务本身必须看到的文档。
type focus struct {
	anchor   int
	targets  []model.DocumentRef
	previous bool // 写作需要上一章正文衔接
	planning bool // 规划任务：锚点起的全部章节计划都是工作对象
}

type selector struct {
	documents   map[string]ContextDocument
	facts       map[string]model.CanonFact
	sourced     map[string][]string // 来源章 → 事实 ID（升序）
	entities    []model.Entity
	structure   []model.PlanNode // 卷与弧
	chapters    []model.PlanNode // 章节计划，按唯一排序口径
	position    map[string]int   // 章节计划 ID → 下标
	written     map[string]model.ManuscriptChapter
	manuscripts map[string]model.ManuscriptChapter
	ownership   []model.OwnershipRule
	selected    map[string]ContextDocument
	used, limit int
}

func newSelector(content ProjectContent, limit int) (*selector, error) {
	s := &selector{
		documents: make(map[string]ContextDocument), facts: make(map[string]model.CanonFact),
		sourced: make(map[string][]string), entities: content.Entities,
		chapters: model.ChapterPlansInOrder(content.Plan), position: make(map[string]int),
		written: make(map[string]model.ManuscriptChapter), manuscripts: make(map[string]model.ManuscriptChapter),
		ownership: content.Ownership, selected: make(map[string]ContextDocument), limit: limit,
	}
	add := func(ref model.DocumentRef, value any) error {
		payload, err := json.Marshal(value)
		if err != nil {
			return fmt.Errorf("encode context document %q: %w", ref.Key(), err)
		}
		if err := model.ValidateDocumentContent(ref, payload); err != nil {
			return err
		}
		s.documents[ref.Key()] = ContextDocument{Ref: ref, Content: payload}
		return nil
	}
	if content.Compass != nil {
		if err := add(compassRef, *content.Compass); err != nil {
			return nil, err
		}
	}
	for _, node := range content.Plan {
		if node.Kind == model.PlanVolume || node.Kind == model.PlanArc {
			s.structure = append(s.structure, node)
		}
		if err := add(model.DocumentRef{Kind: model.DocumentPlan, ID: node.ID}, node); err != nil {
			return nil, err
		}
	}
	for index, node := range s.chapters {
		s.position[node.ID] = index
	}
	for _, entity := range content.Entities {
		if err := add(model.DocumentRef{Kind: model.DocumentEntity, ID: entity.ID}, entity); err != nil {
			return nil, err
		}
	}
	for _, fact := range content.Canon {
		if err := add(canonRef(fact.ID), fact); err != nil {
			return nil, err
		}
		s.facts[fact.ID] = fact
		if fact.SourceChapterID != "" {
			s.sourced[fact.SourceChapterID] = append(s.sourced[fact.SourceChapterID], fact.ID)
		}
	}
	for _, ids := range s.sourced {
		slices.Sort(ids)
	}
	for _, chapter := range content.Manuscript {
		if err := add(manuscriptRef(chapter.ID), chapter); err != nil {
			return nil, err
		}
		s.manuscripts[chapter.ID] = chapter
		s.written[chapter.PlanNodeID] = chapter
	}
	return s, nil
}

// writing 装配写作与重写：目标章计划、上一章正文，重写时再加本章正文与来源事实。
func (s *selector) writing(planID, chapterID string, policy contextPolicy) error {
	anchor, ok := s.position[planID]
	if !ok {
		return fmt.Errorf("context dependency %q does not exist: %w", planRef(planID).Key(), model.ErrInvalid)
	}
	targets := []model.DocumentRef{planRef(planID)}
	if chapterID != "" {
		targets = append(targets, s.chapterTargets(chapterID)...)
	}
	return s.assemble(focus{anchor: anchor, targets: targets, previous: true}, policy)
}

// chapterTargets 是改动某章时必须看到的内容：正文与来源于它的全部事实——重写要
// 重申报这些事实（D41），没有 ID 就无法确认。
func (s *selector) chapterTargets(chapterID string) []model.DocumentRef {
	refs := []model.DocumentRef{manuscriptRef(chapterID)}
	for _, id := range s.sourced[chapterID] {
		refs = append(refs, canonRef(id))
	}
	return refs
}

// assemble 先照装固定段（罗盘、目标、上一章、近期窗口、后续计划），再按优先级装当前事实。
func (s *selector) assemble(f focus, policy contextPolicy) error {
	required := slices.Clone(f.targets)
	if _, ok := s.documents[compassRef.Key()]; ok {
		required = append(required, compassRef) // 篇幅与终局（D63），各任务都要知道故事往哪收
	}
	if f.previous {
		for i := f.anchor - 1; i >= 0; i-- {
			if chapter, ok := s.written[s.chapters[i].ID]; ok {
				required = append(required, manuscriptRef(chapter.ID))
				break
			}
		}
	}
	for i := max(0, f.anchor-policy.Window); i < min(f.anchor, len(s.chapters)); i++ {
		required = append(required, planRef(s.chapters[i].ID))
		if chapter, ok := s.written[s.chapters[i].ID]; ok {
			for _, id := range s.sourced[chapter.ID] {
				if s.facts[id].IsEvent() {
					required = append(required, canonRef(id))
				}
			}
		}
	}
	start, end := f.anchor+1, min(len(s.chapters), f.anchor+1+policy.Lookahead)
	if f.planning {
		start, end = f.anchor, len(s.chapters)
	}
	for i := start; i < end; i++ {
		required = append(required, planRef(s.chapters[i].ID))
	}
	for _, ref := range required {
		if err := s.require(ref); err != nil {
			return err
		}
	}
	return s.rank()
}

// scoped 装配范围由任务显式给出的受影响重写：依赖闭包加 locked/guided 目标，不裁剪。
func (s *selector) scoped(refs []model.DocumentRef) error {
	for _, rule := range s.ownership {
		if rule.Control == model.ControlLocked || rule.Control == model.ControlGuided {
			refs = append(refs, rule.Target)
		}
	}
	for _, ref := range refs {
		if err := s.require(ref); err != nil {
			return err
		}
	}
	return nil
}

// rank 是唯一的裁剪点，优先级依次为：
//  0. 约束——受保护事实、世界规则、未回收伏笔，由早到晚（越早立下越要记住）；
//  1. 相关实体的当前事实，由近到远；
//  2. 其余当前事实，由近到远；
//  3. 卷弧结构。
//
// 事件与已回收伏笔只经窗口或重写目标进入。
func (s *selector) rank() error {
	relevant := s.relevantEntities()
	guarded := make(map[string]bool)
	for _, rule := range s.ownership {
		if rule.Control == model.ControlLocked || rule.Control == model.ControlGuided {
			guarded[rule.Target.Key()] = true
		}
	}
	type candidate struct {
		ref            model.DocumentRef
		rank, position int
	}
	var candidates []candidate
	for id, fact := range s.facts {
		ref := canonRef(id)
		if _, done := s.selected[ref.Key()]; done || fact.IsEvent() || fact.Resolved {
			continue
		}
		rank := 2
		switch {
		case guarded[ref.Key()] || fact.Kind == model.CanonWorldRule || fact.Kind == model.CanonForeshadow:
			rank = 0
		case relevant[fact.SubjectID]:
			rank = 1
		}
		candidates = append(candidates, candidate{ref: ref, rank: rank, position: s.chapterPosition(fact.EffectiveChapter())})
	}
	for _, node := range s.structure {
		if _, done := s.selected[planRef(node.ID).Key()]; !done {
			candidates = append(candidates, candidate{ref: planRef(node.ID), rank: 3, position: -1})
		}
	}
	slices.SortFunc(candidates, func(left, right candidate) int {
		if left.rank != right.rank {
			return left.rank - right.rank
		}
		if left.position != right.position {
			if left.rank == 0 {
				return left.position - right.position
			}
			return right.position - left.position
		}
		return strings.Compare(left.ref.Key(), right.ref.Key())
	})
	for _, candidate := range candidates {
		additions, err := s.closure(candidate.ref)
		if err != nil {
			return err
		}
		if s.used+cost(additions) <= s.limit {
			s.commit(additions)
		}
	}
	return nil
}

// relevantEntities 是本次任务涉及的实体：已选正文由宿主绑定的依赖（该章 Canon Delta
// 的主体），以及名称或别名出现在已选计划与正文里的实体。
func (s *selector) relevantEntities() map[string]bool {
	relevant := make(map[string]bool)
	var text strings.Builder
	for _, document := range s.selected {
		switch document.Ref.Kind {
		case model.DocumentManuscript:
			chapter := s.manuscripts[document.Ref.ID]
			for _, dependency := range chapter.DependsOn {
				if dependency.Kind == model.DocumentEntity {
					relevant[dependency.ID] = true
				}
			}
			for _, block := range chapter.Blocks {
				text.WriteString(block.Text)
			}
		case model.DocumentPlan:
			text.Write(document.Content)
		}
	}
	corpus := text.String()
	for _, entity := range s.entities {
		for _, name := range append([]string{entity.Name}, entity.Aliases...) {
			if utf8.RuneCountInString(name) >= 2 && strings.Contains(corpus, name) {
				relevant[entity.ID] = true
				break
			}
		}
	}
	return relevant
}

func (s *selector) require(ref model.DocumentRef) error {
	additions, err := s.closure(ref)
	if err != nil {
		return err
	}
	s.commit(additions)
	return nil
}

// closure 返回装入 ref 需要新增的文档：它自身与尚未装入的结构依赖（计划祖先、事实主体等）。
func (s *selector) closure(ref model.DocumentRef) ([]ContextDocument, error) {
	var additions []ContextDocument
	pending := make(map[string]bool)
	var visit func(model.DocumentRef) error
	visit = func(ref model.DocumentRef) error {
		if ref.Kind == model.DocumentIntent || ref.Kind == model.DocumentOwnership {
			return nil
		}
		key := ref.Key()
		if _, done := s.selected[key]; done || pending[key] {
			return nil
		}
		document, ok := s.documents[key]
		if !ok {
			return fmt.Errorf("context dependency %q does not exist: %w", key, model.ErrInvalid)
		}
		pending[key] = true
		additions = append(additions, document)
		dependencies, err := model.DocumentDependencies(ref, document.Content)
		if err != nil {
			return err
		}
		for _, dependency := range dependencies {
			if err := visit(dependency); err != nil {
				return err
			}
		}
		return nil
	}
	return additions, visit(ref)
}

func (s *selector) commit(additions []ContextDocument) {
	for _, document := range additions {
		s.selected[document.Ref.Key()] = document
	}
	s.used += cost(additions)
}

func cost(documents []ContextDocument) int {
	total := 0
	for _, document := range documents {
		total += utf8.RuneCount(document.Content)
	}
	return total
}

// firstUnwritten 是规划任务的锚点：第一个还没有正文的章节计划。
func (s *selector) firstUnwritten() int {
	for index, node := range s.chapters {
		if _, ok := s.written[node.ID]; !ok {
			return index
		}
	}
	return len(s.chapters)
}

// chapterPosition 把正文章节换算为章节计划下标；规划期事实没有章节，视为最早。
func (s *selector) chapterPosition(chapterID string) int {
	if chapter, ok := s.manuscripts[chapterID]; ok {
		if index, ok := s.position[chapter.PlanNodeID]; ok {
			return index
		}
	}
	return -1
}

func (s *selector) result(content ProjectContent) StoryContext {
	result := StoryContext{
		SchemaVersion: StoryContextKind, ProjectID: content.ID, Revision: content.Revision,
		Budget: ContextBudget{Limit: s.limit, Used: s.used},
	}
	referenced := make(map[string]bool)
	selectedKinds := make(map[model.DocumentKind]int)
	for _, document := range s.selected {
		result.Documents = append(result.Documents, document)
		selectedKinds[document.Ref.Kind]++
		switch document.Ref.Kind {
		case model.DocumentManuscript:
			referenced[document.Ref.ID] = true
		case model.DocumentCanon:
			fact := s.facts[document.Ref.ID]
			referenced[fact.SourceChapterID], referenced[fact.EffectiveChapterID] = true, true
		case model.DocumentPlan:
			if chapter, ok := s.written[document.Ref.ID]; ok {
				referenced[chapter.ID] = true
			}
		}
	}
	slices.SortFunc(result.Documents, func(left, right ContextDocument) int {
		return strings.Compare(left.Ref.Key(), right.Ref.Key())
	})
	totalKinds := make(map[model.DocumentKind]int)
	for _, document := range s.documents {
		totalKinds[document.Ref.Kind]++
	}
	for kind, total := range totalKinds {
		if omitted := total - selectedKinds[kind]; omitted > 0 {
			if result.Budget.Omitted == nil {
				result.Budget.Omitted = make(map[model.DocumentKind]int)
			}
			result.Budget.Omitted[kind] = omitted
		}
	}
	result.Chapters = []ChapterIndexEntry{}
	for _, chapter := range content.Manuscript {
		if referenced[chapter.ID] {
			result.Chapters = append(result.Chapters, ChapterIndexEntry{ID: chapter.ID, Number: chapter.Number, Title: chapter.Title})
		}
	}
	slices.SortFunc(result.Chapters, func(left, right ChapterIndexEntry) int {
		if left.Number != right.Number {
			return left.Number - right.Number
		}
		return strings.Compare(left.ID, right.ID)
	})
	return result
}

var compassRef = model.DocumentRef{Kind: model.DocumentCompass, ID: model.SingletonDocumentID}

func planRef(id string) model.DocumentRef { return model.DocumentRef{Kind: model.DocumentPlan, ID: id} }
func canonRef(id string) model.DocumentRef {
	return model.DocumentRef{Kind: model.DocumentCanon, ID: id}
}
func manuscriptRef(id string) model.DocumentRef {
	return model.DocumentRef{Kind: model.DocumentManuscript, ID: id}
}
