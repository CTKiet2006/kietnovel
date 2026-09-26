package narrative

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/voocel/ainovel-cli/internal/domain/model"
)

// Submission 是模型用故事语言写的提交（D66）：不含任何文档 ID。正文不在这里，
// 由宿主按工作稿键与版本装配后随 drafts 传入。
type Submission struct {
	Intent   *model.Intent  `json:"intent,omitempty"`
	Compass  *model.Compass `json:"compass,omitempty"`
	Volumes  []VolumeEdit   `json:"volumes,omitempty"`
	Arcs     []ArcEdit      `json:"arcs,omitempty"`
	Chapters []ChapterEdit  `json:"chapters,omitempty"`
	Entities []EntityEdit   `json:"entities,omitempty"`
	Facts    []FactEdit     `json:"facts,omitempty"`
	Confirm  []FactRef      `json:"confirm_facts,omitempty"`
	Remove   []FactRef      `json:"remove_facts,omitempty"`
}

// 计划编辑按序号定位：已有序号是修改（空字段保持原值），紧接末尾的序号是新增。
type VolumeEdit struct {
	Volume  int    `json:"volume"`
	Title   string `json:"title,omitempty"`
	Summary string `json:"summary,omitempty"`
}

type ArcEdit struct {
	Arc     int    `json:"arc"`
	Volume  int    `json:"volume,omitempty"`
	Title   string `json:"title,omitempty"`
	Summary string `json:"summary,omitempty"`
}

type ChapterEdit struct {
	Chapter int    `json:"chapter"`
	Arc     int    `json:"arc,omitempty"`
	Title   string `json:"title,omitempty"`
	Summary string `json:"summary,omitempty"`
}

// EntityEdit 按本名定位：已有实体合并别名，否则新建。
type EntityEdit struct {
	Name    string           `json:"name"`
	Kind    model.EntityKind `json:"kind"`
	Aliases []string         `json:"aliases,omitempty"`
}

// FactEdit 写入一条事实；种类由谓词前缀决定。Chapter 是来源章，单章任务由宿主填。
type FactEdit struct {
	Subject          string          `json:"subject"`
	Predicate        string          `json:"predicate"`
	Value            json.RawMessage `json:"value"`
	Chapter          int             `json:"chapter,omitempty"`
	EffectiveChapter int             `json:"effective_chapter,omitempty"`
	Resolved         bool            `json:"resolved,omitempty"`
}

// FactRef 指称一条已有事实：主体+谓词；事件与关系另按来源章区分。
type FactRef struct {
	Subject   string `json:"subject"`
	Predicate string `json:"predicate"`
	Chapter   int    `json:"chapter,omitempty"`
}

// Resolve 把故事语言提交解析成补丁：新文档由宿主分配 ID，已有文档按自然键定位，
// 更新事实的 old_value 一律取自基线。跨文档不变量仍由 model.ValidateChange 执行。
func (s *Story) Resolve(input model.TaskInput, submission Submission, drafts []model.ManuscriptChapter) ([]model.Patch, error) {
	r := &resolver{
		story: s, ids: s.allocator(), created: make(map[model.PlanNodeKind][]string),
		names: make(map[string]string), touched: make(map[string]bool), chapters: make(map[int]string),
	}
	for _, chapter := range drafts {
		r.scope = append(r.scope, chapter.ID)
		r.chapters[chapter.Number] = chapter.ID
		if err := r.put(model.DocumentManuscript, chapter.ID, chapter); err != nil {
			return nil, err
		}
	}
	if canon, ok := input.(*model.ReviseCanonInput); ok {
		r.scope = append(r.scope, canon.ChapterID)
	}
	if submission.Intent != nil {
		if err := r.put(model.DocumentIntent, model.SingletonDocumentID, submission.Intent); err != nil {
			return nil, err
		}
	}
	if submission.Compass != nil {
		if err := r.put(model.DocumentCompass, model.SingletonDocumentID, submission.Compass); err != nil {
			return nil, err
		}
	}
	steps := []func() error{
		func() error { return r.plan(model.PlanVolume, "", volumeEdits(submission.Volumes)) },
		func() error { return r.plan(model.PlanArc, model.PlanVolume, arcEdits(submission.Arcs)) },
		func() error { return r.plan(model.PlanChapter, model.PlanArc, chapterEdits(submission.Chapters)) },
		func() error { return r.entities(submission.Entities) },
		func() error { return r.facts(submission.Facts) },
		func() error { return r.references(submission.Confirm, true) },
		func() error { return r.references(submission.Remove, false) },
	}
	for _, step := range steps {
		if err := step(); err != nil {
			return nil, err
		}
	}
	return r.patches, nil
}

type resolver struct {
	story    *Story
	ids      *idAllocator
	patches  []model.Patch
	created  map[model.PlanNodeKind][]string // 本次新增的计划节点，按序号
	names    map[string]string               // 本次提交的实体名称与别名 → ID
	touched  map[string]bool                 // 本次已处理的事实
	scope    []string                        // 事实可以来源的正文章节
	chapters map[int]string                  // 本次工作稿的章号 → 正文 ID
}

func (r *resolver) put(kind model.DocumentKind, id string, value any) error {
	content, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode %s %q: %w", kind, id, err)
	}
	r.patches = append(r.patches, model.Patch{Document: model.DocumentRef{Kind: kind, ID: id}, Operation: model.PatchPut, Content: content})
	return nil
}

type planEdit struct {
	number, parent int
	title, summary string
}

func volumeEdits(edits []VolumeEdit) []planEdit {
	result := make([]planEdit, len(edits))
	for i, edit := range edits {
		result[i] = planEdit{number: edit.Volume, title: edit.Title, summary: edit.Summary}
	}
	return result
}

func arcEdits(edits []ArcEdit) []planEdit {
	result := make([]planEdit, len(edits))
	for i, edit := range edits {
		result[i] = planEdit{number: edit.Arc, parent: edit.Volume, title: edit.Title, summary: edit.Summary}
	}
	return result
}

func chapterEdits(edits []ChapterEdit) []planEdit {
	result := make([]planEdit, len(edits))
	for i, edit := range edits {
		result[i] = planEdit{number: edit.Chapter, parent: edit.Arc, title: edit.Title, summary: edit.Summary}
	}
	return result
}

// plan 处理一种计划节点：已有序号就地修改，新序号必须紧接末尾连续编号，排在已有节点之后。
func (r *resolver) plan(kind, parentKind model.PlanNodeKind, edits []planEdit) error {
	existing := r.story.ordered[kind]
	slices.SortFunc(edits, func(left, right planEdit) int { return left.number - right.number })
	order := 0
	for _, node := range existing {
		order = max(order, node.Order)
	}
	name := kindName(kind)
	for i, edit := range edits {
		if i > 0 && edits[i-1].number == edit.number {
			return fmt.Errorf("第 %d 个%s提交了两次: %w", edit.number, name, model.ErrInvalid)
		}
		var node model.PlanNode
		switch next := len(existing) + len(r.created[kind]) + 1; {
		case edit.number >= 1 && edit.number <= len(existing):
			node = existing[edit.number-1]
			node.Title, node.Summary = cmp.Or(edit.title, node.Title), cmp.Or(edit.summary, node.Summary)
		case edit.number == next:
			if strings.TrimSpace(edit.title) == "" || strings.TrimSpace(edit.summary) == "" || (parentKind != "" && edit.parent == 0) {
				return fmt.Errorf("新增的第 %d 个%s需要 title、summary 和所属的%s序号: %w", edit.number, name, kindName(parentKind), model.ErrInvalid)
			}
			node = model.PlanNode{ID: r.ids.allocate(planPrefix(kind)), Kind: kind, Order: order + edit.number - len(existing), Title: edit.title, Summary: edit.summary}
			r.created[kind] = append(r.created[kind], node.ID)
		default:
			return fmt.Errorf("第 %d 个%s不存在；已有 %d 个，新增的%s从第 %d 个起连续编号: %w",
				edit.number, name, len(existing), name, next, model.ErrInvalid)
		}
		if edit.parent != 0 {
			parent, err := r.planID(parentKind, edit.parent)
			if err != nil {
				return err
			}
			node.ParentID = parent
		}
		if err := r.put(model.DocumentPlan, node.ID, node); err != nil {
			return err
		}
	}
	return nil
}

// planID 按序号找计划节点：已有的与本次新增的。
func (r *resolver) planID(kind model.PlanNodeKind, number int) (string, error) {
	existing := r.story.ordered[kind]
	switch {
	case number >= 1 && number <= len(existing):
		return existing[number-1].ID, nil
	case number > len(existing) && number <= len(existing)+len(r.created[kind]):
		return r.created[kind][number-len(existing)-1], nil
	}
	return "", fmt.Errorf("第 %d 个%s不存在: %w", number, kindName(kind), model.ErrInvalid)
}

func (r *resolver) entities(edits []EntityEdit) error {
	for _, edit := range edits {
		name := strings.TrimSpace(edit.Name)
		if name == "" {
			return fmt.Errorf("实体需要 name: %w", model.ErrInvalid)
		}
		if _, dup := r.names[name]; dup {
			return fmt.Errorf("实体「%s」提交了两次: %w", name, model.ErrInvalid)
		}
		entity, err := r.story.entity(name)
		changed := true
		switch {
		case err == nil && entity.Name != name:
			return fmt.Errorf("「%s」是「%s」的别名；修改已有实体时用它的本名: %w", name, entity.Name, model.ErrInvalid)
		case err == nil:
			aliases := slices.Clone(entity.Aliases)
			for _, alias := range edit.Aliases {
				if alias != entity.Name && !slices.Contains(aliases, alias) {
					aliases = append(aliases, alias)
				}
			}
			changed = edit.Kind != entity.Kind || len(aliases) != len(entity.Aliases)
			entity.Kind, entity.Aliases = edit.Kind, aliases
		case errors.Is(err, model.ErrNotFound):
			entity = model.Entity{ID: r.ids.allocate(entityPrefix), Kind: edit.Kind, Name: name, Aliases: edit.Aliases}
		default:
			return err
		}
		for _, known := range append([]string{entity.Name}, entity.Aliases...) {
			r.names[known] = entity.ID
		}
		// 没有变化就不写：锁定的实体被原样引用不算改动。
		if changed {
			if err := r.put(model.DocumentEntity, entity.ID, entity); err != nil {
				return err
			}
		}
	}
	return nil
}

// subject 解析事实主体：本次提交的实体优先，其次故事里已有的实体。
func (r *resolver) subject(name string) (string, error) {
	if id, ok := r.names[name]; ok {
		return id, nil
	}
	entity, err := r.story.entity(name)
	if errors.Is(err, model.ErrNotFound) {
		return "", fmt.Errorf("实体「%s」不存在；新的人物、地点、物品或组织先在 entities 里建立: %w", name, model.ErrInvalid)
	}
	return entity.ID, err
}

// source 解析来源章：给了章号必须是本次改写的章，没给且只有一章时就是它；规划期事实没有来源章。
func (r *resolver) source(number int) (string, error) {
	if number != 0 {
		for _, id := range r.scope {
			if r.chapterNumber(id) == number {
				return id, nil
			}
		}
		return "", fmt.Errorf("事实只能来源于本次提交的章节 %v，不能是第 %d 章: %w", r.scopeNumbers(), number, model.ErrInvalid)
	}
	switch len(r.scope) {
	case 0:
		return "", nil
	case 1:
		return r.scope[0], nil
	default:
		return "", fmt.Errorf("本次改写了多章 %v，每条事实都要用 chapter 指明来源章: %w", r.scopeNumbers(), model.ErrInvalid)
	}
}

func (r *resolver) chapterNumber(id string) int {
	for number, chapterID := range r.chapters {
		if chapterID == id {
			return number
		}
	}
	return r.story.chapterNumberOf(id)
}

func (r *resolver) scopeNumbers() []int {
	numbers := make([]int, 0, len(r.scope))
	for _, id := range r.scope {
		numbers = append(numbers, r.chapterNumber(id))
	}
	slices.Sort(numbers)
	return numbers
}

// chapterID 按章号找正文：本次工作稿优先，其次已写正文。
func (r *resolver) chapterID(number int) (string, error) {
	if id, ok := r.chapters[number]; ok {
		return id, nil
	}
	chapter, err := r.story.manuscript(number)
	return chapter.ID, err
}

func (r *resolver) facts(edits []FactEdit) error {
	for _, edit := range edits {
		kind, ok := model.CanonKindOf(edit.Predicate)
		if !ok {
			return fmt.Errorf("谓词「%s」必须以 event.、state.、relation.、rule. 或 foreshadow. 开头: %w", edit.Predicate, model.ErrInvalid)
		}
		if len(edit.Value) == 0 {
			return fmt.Errorf("事实「%s」%s 缺少 value: %w", edit.Subject, edit.Predicate, model.ErrInvalid)
		}
		subject, err := r.subject(edit.Subject)
		if err != nil {
			return err
		}
		source, err := r.source(edit.Chapter)
		if err != nil {
			return err
		}
		fact := model.CanonFact{
			Kind: kind, SubjectID: subject, Predicate: edit.Predicate, Value: edit.Value,
			SourceChapterID: source, Resolved: edit.Resolved,
		}
		if edit.EffectiveChapter != 0 {
			if fact.EffectiveChapterID, err = r.chapterID(edit.EffectiveChapter); err != nil {
				return err
			}
			if fact.EffectiveChapterID == source {
				fact.EffectiveChapterID = ""
			}
		}
		if previous, exists := r.story.fact(subject, edit.Predicate, source); exists {
			fact.ID, fact.PreviousValue, fact.DependsOn = previous.ID, previous.Value, previous.DependsOn
		} else {
			fact.ID = r.ids.allocate(factPrefix)
		}
		if err := r.touch(fact, edit.Subject); err != nil {
			return err
		}
		if err := r.put(model.DocumentCanon, fact.ID, fact); err != nil {
			return err
		}
	}
	return nil
}

// references 处理确认（原样重申报）与删除。
func (r *resolver) references(refs []FactRef, confirm bool) error {
	for _, ref := range refs {
		subject, err := r.subject(ref.Subject)
		if err != nil {
			return err
		}
		source, err := r.source(ref.Chapter)
		if err != nil {
			return err
		}
		fact, exists := r.story.fact(subject, ref.Predicate, source)
		if !exists {
			return fmt.Errorf("没有这条事实：「%s」%s: %w", ref.Subject, ref.Predicate, model.ErrInvalid)
		}
		if err := r.touch(fact, ref.Subject); err != nil {
			return err
		}
		if !confirm {
			r.patches = append(r.patches, model.Patch{Document: model.DocumentRef{Kind: model.DocumentCanon, ID: fact.ID}, Operation: model.PatchDelete})
			continue
		}
		fact.PreviousValue = fact.Value
		if err := r.put(model.DocumentCanon, fact.ID, fact); err != nil {
			return err
		}
	}
	return nil
}

func (r *resolver) touch(fact model.CanonFact, subject string) error {
	if r.touched[fact.ID] {
		return fmt.Errorf("事实「%s」%s 在一次提交里只能出现一次（facts、confirm_facts、remove_facts 合计）；同一章里同一主体的同一谓词只记一条: %w",
			subject, fact.Predicate, model.ErrInvalid)
	}
	r.touched[fact.ID] = true
	return nil
}
