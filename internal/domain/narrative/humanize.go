package narrative

import (
	"strings"

	"github.com/voocel/ainovel-cli/internal/domain/model"
)

// Humanize 把宿主错误里用引号括起的内部 ID 换成故事标签（D66），保留错误链：领域
// 校验按文档 ID 报错，模型只认章号和名称。Apply 得到的故事同时认得被删除的文档。
func (s *Story) Humanize(err error) error {
	if err == nil {
		return nil
	}
	var pairs []string
	for story := s; story != nil; story = story.parent {
		pairs = append(pairs, story.labelPairs()...)
	}
	message := strings.NewReplacer(pairs...).Replace(err.Error())
	if message == err.Error() {
		return err
	}
	return humanized{message: message, err: err}
}

// labelPairs 为每份文档给出 "id" 与 "kind:id" 两种引用写法的替换。
func (s *Story) labelPairs() []string {
	var pairs []string
	add := func(ref model.DocumentRef, bare string) {
		if label, ok := s.label(ref); ok {
			pairs = append(pairs, `"`+ref.ID+`"`, bare, `"`+ref.Key()+`"`, label)
		}
	}
	for _, node := range s.content.Plan {
		add(model.DocumentRef{Kind: model.DocumentPlan, ID: node.ID}, s.planLabel(node))
	}
	for _, chapter := range s.content.Manuscript {
		add(model.DocumentRef{Kind: model.DocumentManuscript, ID: chapter.ID}, s.planLabelByID(chapter.PlanNodeID))
	}
	for _, entity := range s.content.Entities {
		add(model.DocumentRef{Kind: model.DocumentEntity, ID: entity.ID}, "「"+entity.Name+"」")
	}
	for _, fact := range s.content.Canon {
		add(model.DocumentRef{Kind: model.DocumentCanon, ID: fact.ID}, s.factLabel(fact))
	}
	return pairs
}

type humanized struct {
	message string
	err     error
}

func (e humanized) Error() string { return e.message }
func (e humanized) Unwrap() error { return e.err }
