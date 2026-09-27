package narrative

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/voocel/ainovel-cli/internal/domain/model"
)

// DirectiveView 是用户要求的故事语言形状：原话、作用域标签与字数约束。
type DirectiveView struct {
	Text        string                      `json:"text"`
	Scope       string                      `json:"scope"`
	Constraints *model.DirectiveConstraints `json:"constraints,omitempty"`
}

// Task 把任务输入渲染成故事语言（D66）：章号代替章节 ID，待核验事实给出内容，
// 证据基线、基线 Revision 与提案 ID 这类宿主簿记不进入模型视图。
func (s *Story) Task(kind model.OperationKind, raw json.RawMessage) (json.RawMessage, error) {
	input, err := model.DecodeTaskInput(kind, raw)
	if err != nil {
		return nil, err
	}
	var view any
	switch input := input.(type) {
	case *model.DevelopPlanInput:
		view = input
	case *model.RevisePlanInput:
		plan := *input
		plan.Basis = model.EvidenceBasis{}
		view = plan
	case *model.ReviseCanonInput:
		pending := make([]string, 0, len(input.FactIDs))
		for _, id := range input.FactIDs {
			if _, ok := s.facts[id]; !ok {
				return nil, fmt.Errorf("pending canon %q does not exist: %w", id, model.ErrInvalid)
			}
			pending = append(pending, id)
		}
		view = struct {
			Chapter int        `json:"chapter"`
			Pending []FactView `json:"pending_facts,omitempty"`
			Reason  string     `json:"reason"`
			Goal    string     `json:"goal,omitempty"`
		}{s.chapterNumberOf(input.ChapterID), s.Facts(pending), input.Reason, input.Goal}
	case *model.WriteChapterInput:
		view = struct {
			Chapter    int             `json:"chapter"`
			Directives []DirectiveView `json:"directives,omitempty"`
			Goal       string          `json:"goal,omitempty"`
		}{input.ChapterNumber, s.directives(input.Directives), input.Goal}
	case *model.RewriteChapterInput:
		view = struct {
			Chapter    int             `json:"chapter"`
			Findings   []string        `json:"findings"`
			Directives []DirectiveView `json:"directives,omitempty"`
			Goal       string          `json:"goal,omitempty"`
		}{input.ChapterNumber, input.Findings, s.directives(input.Directives), input.Goal}
	case *model.RewriteAffectedInput:
		view = struct {
			Chapters []int  `json:"chapters"`
			Reason   string `json:"reason"`
		}{s.chapterNumbers(input.ChapterIDs), input.Reason}
	case *model.ReviewRangeInput:
		prior := make([]Finding, 0, len(input.PriorFindings))
		for _, finding := range input.PriorFindings {
			prior = append(prior, Finding{Chapter: s.chapterNumberOf(finding.ChapterID), Severity: finding.Severity, Note: finding.Note, Requirement: finding.Requirement})
		}
		view = struct {
			Chapters      []int               `json:"chapters"`
			Reviewed      []int               `json:"reviewed,omitempty"`
			PriorFindings []Finding           `json:"prior_findings,omitempty"`
			Requirements  []model.Requirement `json:"requirements,omitempty"`
			Goal          string              `json:"goal,omitempty"`
		}{s.chapterNumbers(input.ChapterIDs), s.chapterNumbers(input.Reviewed), prior, input.Requirements, input.Goal}
	default:
		return nil, fmt.Errorf("operation %s has no story-language task view: %w", kind, model.ErrInvalid)
	}
	return json.Marshal(view)
}

func (s *Story) directives(directives []model.Directive) []DirectiveView {
	views := make([]DirectiveView, 0, len(directives))
	for _, directive := range directives {
		views = append(views, DirectiveView{Text: directive.Text, Scope: s.DescribeScope(directive), Constraints: directive.Constraints})
	}
	return views
}

// DescribeScope 用故事语言描述要求的作用域。
func (s *Story) DescribeScope(directive model.Directive) string {
	return directive.DescribeScope(s.planLabelByID)
}

func (s *Story) chapterNumbers(ids []string) []int {
	numbers := make([]int, 0, len(ids))
	for _, id := range ids {
		numbers = append(numbers, s.chapterNumberOf(id))
	}
	slices.Sort(numbers)
	return numbers
}

// RuleView 是 Ownership 的故事语言形状。
type RuleView struct {
	Target   string             `json:"target"`
	Control  model.ControlLevel `json:"control"`
	Guidance []string           `json:"guidance,omitempty"`
}

// Ownership 渲染作品的控制规则；目标已不在故事里的规则对模型没有意义，跳过——
// 锁定始终由 Change Engine 执行，不依赖模型看见它。
func (s *Story) Ownership(rules []model.OwnershipRule) ([]RuleView, error) {
	views := make([]RuleView, 0, len(rules))
	for _, rule := range rules {
		if err := rule.Validate(); err != nil {
			return nil, err
		}
		if label, ok := s.Label(rule.Target); ok {
			views = append(views, RuleView{Target: label, Control: rule.Control, Guidance: rule.Guidance})
		}
	}
	slices.SortFunc(views, func(left, right RuleView) int { return strings.Compare(left.Target, right.Target) })
	return views, nil
}

// ChapterDraft 是模型写入的章节工作稿：章的身份（ID、所属计划、章号、署名）由宿主
// 按任务填写；Number 只在一次改写多章时用来指明是哪一章。
type ChapterDraft struct {
	Number int                     `json:"number,omitempty"`
	Title  string                  `json:"title"`
	Blocks []model.ManuscriptBlock `json:"blocks"`
}

// Draft 把工作稿补全为正文文档。
func (s *Story) Draft(input model.TaskInput, draft ChapterDraft) (model.ManuscriptChapter, error) {
	chapter := model.ManuscriptChapter{Title: draft.Title, Author: model.AuthorAI, Blocks: draft.Blocks}
	switch input := input.(type) {
	case *model.WriteChapterInput:
		if existing, ok := s.manuscripts[input.ChapterPlanID]; ok && existing.PlanNodeID != input.ChapterPlanID {
			return model.ManuscriptChapter{}, fmt.Errorf("manuscript id %q is taken by another chapter: %w", input.ChapterPlanID, model.ErrStateConflict)
		}
		chapter.ID, chapter.PlanNodeID, chapter.Number = input.ChapterPlanID, input.ChapterPlanID, input.ChapterNumber
	case *model.RewriteChapterInput:
		chapter.ID, chapter.PlanNodeID, chapter.Number = input.ChapterID, input.ChapterPlanID, input.ChapterNumber
	case *model.RewriteAffectedInput:
		target, err := s.manuscript(draft.Number)
		if err != nil {
			return model.ManuscriptChapter{}, err
		}
		if !slices.Contains(input.ChapterIDs, target.ID) {
			return model.ManuscriptChapter{}, fmt.Errorf("第 %d 章不在本次要改写的章节 %v 之内: %w", draft.Number, s.chapterNumbers(input.ChapterIDs), model.ErrInvalid)
		}
		chapter.ID, chapter.PlanNodeID, chapter.Number = target.ID, target.PlanNodeID, draft.Number
	default:
		return model.ManuscriptChapter{}, fmt.Errorf("this task does not write chapters: %w", model.ErrInvalid)
	}
	return chapter, nil
}

// Finding 是模型写的审阅发现：章用章号。
type Finding struct {
	Chapter     int    `json:"chapter"`
	Severity    string `json:"severity"`
	Note        string `json:"note"`
	Requirement string `json:"requirement,omitempty"`
}

// Findings 把审阅发现落到审阅范围内的正文上。
func (s *Story) Findings(input model.ReviewRangeInput, findings []Finding) ([]model.ReviewFinding, error) {
	result := make([]model.ReviewFinding, 0, len(findings))
	for _, finding := range findings {
		chapter, err := s.manuscript(finding.Chapter)
		if err == nil && !slices.Contains(input.ChapterIDs, chapter.ID) {
			err = fmt.Errorf("第 %d 章不在本次审阅范围 %v 之内；跨章问题挂到范围内最相关的一章: %w",
				finding.Chapter, s.chapterNumbers(input.ChapterIDs), model.ErrInvalid)
		}
		if err != nil {
			return nil, err
		}
		landed := model.ReviewFinding{ChapterID: chapter.ID, Severity: finding.Severity, Note: finding.Note, Requirement: finding.Requirement}
		if err := input.AdmitsFinding(landed); err != nil {
			return nil, err
		}
		result = append(result, landed)
	}
	return result, nil
}
