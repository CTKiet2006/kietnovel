// Package narrative 是模型面对的故事语言（D66）：章用章号，卷与故事弧用序号，实体用
// 名称，事实用主体+谓词。文档 ID 只属于宿主——渲染时换成故事标签，提交时由这里解析
// 回补丁；模型既看不到也不书写任何内部标识。
package narrative

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/voocel/ainovel-cli/internal/domain/model"
)

// Content 是构建故事索引所需的权威文档。
type Content struct {
	Plan       []model.PlanNode
	Entities   []model.Entity
	Canon      []model.CanonFact
	Manuscript []model.ManuscriptChapter
}

// Story 是某个 Revision 的故事索引：序号、标签与自然键查找都以它为准。
type Story struct {
	content     Content
	ordered     map[model.PlanNodeKind][]model.PlanNode // 卷、弧、章各自按唯一口径排序
	number      map[string]int                          // 计划节点 ID → 所在种类的序号
	plans       map[string]model.PlanNode
	written     map[string]model.ManuscriptChapter // 章节计划 ID → 正文
	manuscripts map[string]model.ManuscriptChapter // 正文 ID → 正文
	entities    map[string]model.Entity
	facts       map[string]model.CanonFact
	parent      *Story // Apply 的基线：被删除文档的标签回退到这里
}

func New(content Content) *Story {
	s := &Story{
		content: content, ordered: make(map[model.PlanNodeKind][]model.PlanNode),
		number: make(map[string]int), plans: make(map[string]model.PlanNode),
		written: make(map[string]model.ManuscriptChapter), manuscripts: make(map[string]model.ManuscriptChapter),
		entities: make(map[string]model.Entity), facts: make(map[string]model.CanonFact),
	}
	for _, node := range content.Plan {
		s.plans[node.ID] = node
	}
	for _, kind := range []model.PlanNodeKind{model.PlanVolume, model.PlanArc, model.PlanChapter} {
		s.ordered[kind] = model.PlanNodesInOrder(content.Plan, kind)
		for index, node := range s.ordered[kind] {
			s.number[node.ID] = index + 1
		}
	}
	for _, chapter := range content.Manuscript {
		s.manuscripts[chapter.ID] = chapter
		s.written[chapter.PlanNodeID] = chapter
	}
	for _, entity := range content.Entities {
		s.entities[entity.ID] = entity
	}
	for _, fact := range content.Canon {
		s.facts[fact.ID] = fact
	}
	return s
}

// ChapterNumber 返回正文或章节计划所在的章号；规划期（空 ID）与未知 ID 返回 0。
func (s *Story) chapterNumberOf(id string) int {
	if chapter, ok := s.manuscripts[id]; ok {
		id = chapter.PlanNodeID
	}
	if node, ok := s.plans[id]; ok && node.Kind == model.PlanChapter {
		return s.number[id]
	}
	return 0
}

// chapterPlan 按章号取章节计划。
func (s *Story) chapterPlan(number int) (model.PlanNode, error) {
	chapters := s.ordered[model.PlanChapter]
	if number < 1 || number > len(chapters) {
		return model.PlanNode{}, fmt.Errorf("第 %d 章不存在（全书已规划 %d 章）: %w", number, len(chapters), model.ErrNotFound)
	}
	return chapters[number-1], nil
}

// Manuscript 按章号取已写正文。
func (s *Story) manuscript(number int) (model.ManuscriptChapter, error) {
	plan, err := s.chapterPlan(number)
	if err != nil {
		return model.ManuscriptChapter{}, err
	}
	chapter, ok := s.written[plan.ID]
	if !ok {
		return model.ManuscriptChapter{}, fmt.Errorf("第 %d 章还没有正文: %w", number, model.ErrNotFound)
	}
	return chapter, nil
}

// entity 按名称解析实体：本名优先，其次别名；别名被多个实体共用时报歧义。
func (s *Story) entity(name string) (model.Entity, error) {
	var aliased []model.Entity
	for _, entity := range s.content.Entities {
		if entity.Name == name {
			return entity, nil
		}
		if slices.Contains(entity.Aliases, name) {
			aliased = append(aliased, entity)
		}
	}
	switch len(aliased) {
	case 0:
		return model.Entity{}, fmt.Errorf("实体「%s」不存在: %w", name, model.ErrNotFound)
	case 1:
		return aliased[0], nil
	default:
		return model.Entity{}, fmt.Errorf("「%s」同时是%s的别名，请用本名: %w", name, entityNames(aliased), model.ErrInvalid)
	}
}

// fact 按自然键找事实（D61/D66）：state/world_rule/foreshadow 按主体+谓词全书唯一；
// 事件与关系按来源章+主体+谓词。
func (s *Story) fact(subjectID, predicate, sourceChapterID string) (model.CanonFact, bool) {
	kind, _ := model.CanonKindOf(predicate)
	probe := model.CanonFact{Kind: kind, SubjectID: subjectID, Predicate: predicate}
	_, keyed := probe.ConceptKey()
	for _, fact := range s.content.Canon {
		if fact.SubjectID == subjectID && fact.Predicate == predicate && (keyed || fact.SourceChapterID == sourceChapterID) {
			return fact, true
		}
	}
	return model.CanonFact{}, false
}

// Apply 返回应用补丁后的故事；标签查不到的文档（被删除的）回退到当前故事。
func (s *Story) Apply(patches []model.Patch) (*Story, error) {
	plan := byID(s.content.Plan, func(v model.PlanNode) string { return v.ID })
	entities := byID(s.content.Entities, func(v model.Entity) string { return v.ID })
	canon := byID(s.content.Canon, func(v model.CanonFact) string { return v.ID })
	manuscript := byID(s.content.Manuscript, func(v model.ManuscriptChapter) string { return v.ID })
	for _, patch := range patches {
		var err error
		switch patch.Document.Kind {
		case model.DocumentPlan:
			err = applyPatch(plan, patch)
		case model.DocumentEntity:
			err = applyPatch(entities, patch)
		case model.DocumentCanon:
			err = applyPatch(canon, patch)
		case model.DocumentManuscript:
			err = applyPatch(manuscript, patch)
		}
		if err != nil {
			return nil, fmt.Errorf("apply %s: %w", patch.Document.Key(), err)
		}
	}
	next := New(Content{Plan: values(plan), Entities: values(entities), Canon: values(canon), Manuscript: values(manuscript)})
	next.parent = s
	return next, nil
}

func byID[T any](items []T, id func(T) string) map[string]T {
	result := make(map[string]T, len(items))
	for _, item := range items {
		result[id(item)] = item
	}
	return result
}

func applyPatch[T any](documents map[string]T, patch model.Patch) error {
	if patch.Operation != model.PatchPut {
		delete(documents, patch.Document.ID)
		return nil
	}
	var value T
	if err := json.Unmarshal(patch.Content, &value); err != nil {
		return err
	}
	documents[patch.Document.ID] = value
	return nil
}

// values 按 ID 排序输出，保证由补丁构建的故事与输入顺序无关。
func values[T any](documents map[string]T) []T {
	keys := make([]string, 0, len(documents))
	for key := range documents {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	result := make([]T, 0, len(keys))
	for _, key := range keys {
		result = append(result, documents[key])
	}
	return result
}

// idAllocator 给新文档分配确定性 ID：同一前缀取现有最大序号的下一个。同一基线上的
// 同一提交总得到相同 ID，重试幂等。
type idAllocator struct {
	taken map[string]map[string]bool // 前缀 → 已占用 ID
	next  map[string]int
}

func newIDAllocator() *idAllocator {
	return &idAllocator{taken: make(map[string]map[string]bool), next: make(map[string]int)}
}

// reserve 登记某前缀下已占用的 ID。
func (a *idAllocator) reserve(prefix string, ids ...string) {
	if a.taken[prefix] == nil {
		a.taken[prefix] = make(map[string]bool)
	}
	for _, id := range ids {
		a.taken[prefix][id] = true
		if number, err := strconv.Atoi(strings.TrimPrefix(id, prefix)); err == nil && strings.HasPrefix(id, prefix) && number > a.next[prefix] {
			a.next[prefix] = number
		}
	}
}

func (a *idAllocator) allocate(prefix string) string {
	a.reserve(prefix)
	for {
		a.next[prefix]++
		if id := prefix + strconv.Itoa(a.next[prefix]); !a.taken[prefix][id] {
			a.taken[prefix][id] = true
			return id
		}
	}
}

// 新文档 ID 前缀：计划节点共用一个 ID 空间，卷、弧、章的前缀互不相同；正文与其章节
// 计划同 ID。
const (
	volumePrefix  = "v"
	arcPrefix     = "a"
	chapterPrefix = "c"
	entityPrefix  = "e"
	factPrefix    = "f"
)

func planPrefix(kind model.PlanNodeKind) string {
	switch kind {
	case model.PlanVolume:
		return volumePrefix
	case model.PlanArc:
		return arcPrefix
	default:
		return chapterPrefix
	}
}

func (s *Story) allocator() *idAllocator {
	a := newIDAllocator()
	for _, prefix := range []string{volumePrefix, arcPrefix, chapterPrefix} {
		for _, node := range s.content.Plan {
			a.reserve(prefix, node.ID)
		}
		for _, chapter := range s.content.Manuscript {
			a.reserve(prefix, chapter.ID)
		}
	}
	for _, entity := range s.content.Entities {
		a.reserve(entityPrefix, entity.ID)
	}
	for _, fact := range s.content.Canon {
		a.reserve(factPrefix, fact.ID)
	}
	return a
}

func entityNames(entities []model.Entity) string {
	names := make([]string, 0, len(entities))
	for _, entity := range entities {
		names = append(names, "「"+entity.Name+"」")
	}
	return strings.Join(names, "、")
}
