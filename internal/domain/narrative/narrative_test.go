package narrative

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/voocel/ainovel-cli/internal/domain/model"
)

func fixture() *Story {
	chapter := func(id string, number int) model.ManuscriptChapter {
		return model.ManuscriptChapter{ID: id, PlanNodeID: id, Number: number, Title: fmt.Sprintf("第%d章题", number), Author: model.AuthorAI,
			Blocks: []model.ManuscriptBlock{{ID: "b1", Text: "山风很冷。"}, {ID: "b2", Text: "他走进山门。"}}}
	}
	return New(Content{
		Plan: []model.PlanNode{
			{ID: "v1", Kind: model.PlanVolume, Order: 1, Title: "入门", Summary: "进入宗门"},
			{ID: "a1", Kind: model.PlanArc, ParentID: "v1", Order: 1, Title: "试炼", Summary: "外门试炼"},
			{ID: "c1", Kind: model.PlanChapter, ParentID: "a1", Order: 1, Title: "山门", Summary: "拜师"},
			{ID: "c2", Kind: model.PlanChapter, ParentID: "a1", Order: 2, Title: "立誓", Summary: "立誓"},
			{ID: "c3", Kind: model.PlanChapter, ParentID: "a1", Order: 3, Title: "下山", Summary: "下山"},
		},
		Entities: []model.Entity{
			{ID: "e1", Kind: model.EntityCharacter, Name: "林凡", Aliases: []string{"小凡"}},
			{ID: "e2", Kind: model.EntityCharacter, Name: "苏晴"},
		},
		Canon: []model.CanonFact{
			{ID: "f1", Kind: model.CanonState, SubjectID: "e1", Predicate: "state.realm", Value: json.RawMessage(`"炼气"`), SourceChapterID: "c1"},
			{ID: "f2", Kind: model.CanonEvent, SubjectID: "e1", Predicate: "event.oath", Value: json.RawMessage(`"立誓入宗"`), SourceChapterID: "c2"},
			{ID: "f3", Kind: model.CanonWorldRule, SubjectID: "e2", Predicate: "rule.spirit_cost", Value: json.RawMessage(`"施法耗灵"`)},
		},
		Manuscript: []model.ManuscriptChapter{chapter("c1", 1), chapter("c2", 2)},
	})
}

// internalID 匹配本测试夹具与宿主分配的内部 ID 形状。
var internalID = regexp.MustCompile(`"[vacef]\d+"`)

func patchIDs(patches []model.Patch) []string {
	ids := make([]string, 0, len(patches))
	for _, patch := range patches {
		ids = append(ids, patch.Document.Key()+"/"+string(patch.Operation))
	}
	return ids
}

func decodePatch[T any](t *testing.T, patches []model.Patch, key string) T {
	t.Helper()
	var value T
	for _, patch := range patches {
		if patch.Document.Key() == key {
			if err := json.Unmarshal(patch.Content, &value); err != nil {
				t.Fatalf("decode %s: %v", key, err)
			}
			return value
		}
	}
	t.Fatalf("missing patch %s in %v", key, patchIDs(patches))
	return value
}

// 规划：已有序号就地修改，新增序号紧接末尾，ID 与顺序由宿主给。
func TestResolvePlanEditsAssignIDsAndKeepNumbering(t *testing.T) {
	story := fixture()
	patches, err := story.Resolve(&model.RevisePlanInput{}, Submission{
		Arcs: []ArcEdit{{Arc: 2, Volume: 1, Title: "下山", Summary: "历练"}},
		Chapters: []ChapterEdit{
			{Chapter: 5, Arc: 2, Title: "夜雨", Summary: "遇袭"},
			{Chapter: 4, Arc: 2, Title: "出山", Summary: "告别"},
			{Chapter: 3, Summary: "改为悄然下山"},
		},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	arc := decodePatch[model.PlanNode](t, patches, "plan:a2")
	fourth := decodePatch[model.PlanNode](t, patches, "plan:c4")
	fifth := decodePatch[model.PlanNode](t, patches, "plan:c5")
	third := decodePatch[model.PlanNode](t, patches, "plan:c3")
	if arc.ParentID != "v1" || fourth.ParentID != "a2" || fourth.Order != 4 || fifth.Order != 5 || fourth.Title != "出山" {
		t.Fatalf("new nodes = %+v %+v %+v", arc, fourth, fifth)
	}
	if third.Title != "下山" || third.Summary != "改为悄然下山" || third.ParentID != "a1" {
		t.Fatalf("update must keep unspecified fields: %+v", third)
	}
	projected, err := story.Apply(patches)
	if err != nil || projected.chapterNumberOf("c5") != 5 {
		t.Fatalf("projected numbering = %d, %v", projected.chapterNumberOf("c5"), err)
	}

	for name, edits := range map[string][]ChapterEdit{
		"gap":       {{Chapter: 5, Arc: 1, Title: "t", Summary: "s"}},
		"duplicate": {{Chapter: 3, Title: "a"}, {Chapter: 3, Title: "b"}},
		"no parent": {{Chapter: 4, Title: "t", Summary: "s"}},
	} {
		if _, err := story.Resolve(&model.RevisePlanInput{}, Submission{Chapters: edits}, nil); !errors.Is(err, model.ErrInvalid) {
			t.Fatalf("%s: error = %v", name, err)
		}
	}
}

// 写新章：事实按自然键并入已有节点，old_value 取自基线；事件按章追加；新实体可当场引用。
func TestResolveChapterSubmissionUsesNaturalKeys(t *testing.T) {
	story := fixture()
	input := &model.WriteChapterInput{ChapterPlanID: "c3", ChapterNumber: 3}
	draft, err := story.Draft(input, ChapterDraft{Title: "下山", Blocks: []model.ManuscriptBlock{{ID: "b1", Text: "下山了。"}}})
	if err != nil || draft.ID != "c3" || draft.PlanNodeID != "c3" || draft.Number != 3 || draft.Author != model.AuthorAI {
		t.Fatalf("draft identity = %+v, %v", draft, err)
	}
	patches, err := story.Resolve(input, Submission{
		Entities: []EntityEdit{{Name: "黑风寨", Kind: model.EntityOrganization}},
		Facts: []FactEdit{
			{Subject: "小凡", Predicate: "state.realm", Value: json.RawMessage(`"筑基"`)},
			{Subject: "林凡", Predicate: "event.oath", Value: json.RawMessage(`"再次立誓"`)},
			{Subject: "黑风寨", Predicate: "state.hostile", Value: json.RawMessage(`true`), EffectiveChapter: 2},
		},
	}, []model.ManuscriptChapter{draft})
	if err != nil {
		t.Fatal(err)
	}
	realm := decodePatch[model.CanonFact](t, patches, "canon:f1")
	if realm.SourceChapterID != "c3" || string(realm.PreviousValue) != `"炼气"` || realm.Kind != model.CanonState {
		t.Fatalf("keyed update = %+v", realm)
	}
	oath := decodePatch[model.CanonFact](t, patches, "canon:f4")
	if oath.SourceChapterID != "c3" || len(oath.PreviousValue) != 0 || oath.Kind != model.CanonEvent {
		t.Fatalf("events append per chapter: %+v", oath)
	}
	hostile := decodePatch[model.CanonFact](t, patches, "canon:f5")
	if hostile.SubjectID != "e3" || hostile.EffectiveChapterID != "c2" {
		t.Fatalf("new entity fact = %+v", hostile)
	}
	decodePatch[model.ManuscriptChapter](t, patches, "manuscript:c3")

	for name, submission := range map[string]Submission{
		"unknown subject": {Facts: []FactEdit{{Subject: "路人", Predicate: "state.mood", Value: json.RawMessage(`"好"`)}}},
		"bad predicate":   {Facts: []FactEdit{{Subject: "林凡", Predicate: "mood", Value: json.RawMessage(`"好"`)}}},
		"twice":           {Facts: []FactEdit{{Subject: "林凡", Predicate: "state.realm", Value: json.RawMessage(`"a"`)}, {Subject: "小凡", Predicate: "state.realm", Value: json.RawMessage(`"b"`)}}},
		"foreign chapter": {Facts: []FactEdit{{Subject: "林凡", Predicate: "state.realm", Value: json.RawMessage(`"a"`), Chapter: 1}}},
		"alias rename":    {Entities: []EntityEdit{{Name: "小凡", Kind: model.EntityCharacter}}},
	} {
		if _, err := story.Resolve(input, submission, []model.ManuscriptChapter{draft}); !errors.Is(err, model.ErrInvalid) {
			t.Fatalf("%s: error = %v", name, err)
		}
	}
}

// 重写：确认复制基线、删除按自然键定位，同一事实不得既确认又改。
func TestResolveRedeclarationByNaturalKey(t *testing.T) {
	story := fixture()
	input := &model.RewriteChapterInput{ChapterID: "c2", ChapterPlanID: "c2", ChapterNumber: 2, Findings: []string{"节奏拖沓"}}
	draft, err := story.Draft(input, ChapterDraft{Title: "立誓", Blocks: []model.ManuscriptBlock{{ID: "b1", Text: "他立誓。"}}})
	if err != nil {
		t.Fatal(err)
	}
	patches, err := story.Resolve(input, Submission{
		Confirm: []FactRef{{Subject: "林凡", Predicate: "event.oath"}},
		Remove:  []FactRef{{Subject: "苏晴", Predicate: "rule.spirit_cost"}},
	}, []model.ManuscriptChapter{draft})
	if err != nil {
		t.Fatal(err)
	}
	if got := patchIDs(patches); !slices.Equal(got, []string{"manuscript:c2/put", "canon:f2/put", "canon:f3/delete"}) {
		t.Fatalf("patches = %v", got)
	}
	if oath := decodePatch[model.CanonFact](t, patches, "canon:f2"); string(oath.PreviousValue) != string(oath.Value) {
		t.Fatalf("confirmation must copy the baseline: %+v", oath)
	}
	if _, err := story.Resolve(input, Submission{
		Facts:   []FactEdit{{Subject: "林凡", Predicate: "event.oath", Value: json.RawMessage(`"改"`)}},
		Confirm: []FactRef{{Subject: "林凡", Predicate: "event.oath"}},
	}, []model.ManuscriptChapter{draft}); !errors.Is(err, model.ErrInvalid) {
		t.Fatalf("confirm and update of one fact: %v", err)
	}
	if _, err := story.Resolve(input, Submission{Confirm: []FactRef{{Subject: "林凡", Predicate: "event.missing"}}}, []model.ManuscriptChapter{draft}); !errors.Is(err, model.ErrInvalid) {
		t.Fatalf("missing fact: %v", err)
	}
}

// 多章改写：工作稿按章号落到任务章节上，事实必须指明来源章。
func TestAffectedRewriteResolvesChaptersByNumber(t *testing.T) {
	story := fixture()
	input := &model.RewriteAffectedInput{ChapterIDs: []string{"c1", "c2"}, BaseRevision: 3, ResolutionProposalID: "p", Reason: "设定变更"}
	if _, err := story.Draft(input, ChapterDraft{Number: 3, Title: "t", Blocks: []model.ManuscriptBlock{{ID: "b", Text: "x"}}}); err == nil {
		t.Fatal("chapter outside the task accepted")
	}
	first, err := story.Draft(input, ChapterDraft{Number: 1, Title: "山门", Blocks: []model.ManuscriptBlock{{ID: "b", Text: "x"}}})
	if err != nil || first.ID != "c1" {
		t.Fatalf("draft = %+v, %v", first, err)
	}
	second, _ := story.Draft(input, ChapterDraft{Number: 2, Title: "立誓", Blocks: []model.ManuscriptBlock{{ID: "b", Text: "y"}}})
	drafts := []model.ManuscriptChapter{first, second}
	if _, err := story.Resolve(input, Submission{Facts: []FactEdit{{Subject: "林凡", Predicate: "event.fall", Value: json.RawMessage(`"跌落"`)}}}, drafts); !errors.Is(err, model.ErrInvalid) {
		t.Fatalf("multi-chapter fact without chapter: %v", err)
	}
	patches, err := story.Resolve(input, Submission{
		Facts:   []FactEdit{{Subject: "林凡", Predicate: "event.fall", Value: json.RawMessage(`"跌落"`), Chapter: 1}},
		Confirm: []FactRef{{Subject: "林凡", Predicate: "event.oath", Chapter: 2}},
	}, drafts)
	if err != nil {
		t.Fatal(err)
	}
	if fall := decodePatch[model.CanonFact](t, patches, "canon:f4"); fall.SourceChapterID != "c1" {
		t.Fatalf("fall = %+v", fall)
	}
}

// 模型读到的任务、上下文片段与回查结果里没有任何内部 ID。
func TestModelFacingViewsCarryNoInternalIDs(t *testing.T) {
	story := fixture()
	directive := model.Directive{ID: "d1", Scope: model.DirectiveScopePlanNode("a1"), Text: "多写心理", Status: model.DirectiveActive}
	tasks := map[model.OperationKind]any{
		model.OperationRewriteChapter: model.RewriteChapterInput{ChapterID: "c2", ChapterPlanID: "c2", ChapterNumber: 2, Findings: []string{"拖沓"}, Directives: []model.Directive{directive}},
		model.OperationReviseCanon:    model.ReviseCanonInput{ChapterID: "c2", FactIDs: []string{"f2"}, Reason: "核验"},
		model.OperationReviewRange: model.ReviewRangeInput{ChapterIDs: []string{"c2", "c1"}, Reviewed: []string{"c1"},
			PriorFindings: []model.ReviewFinding{{ChapterID: "c2", Severity: model.FindingBlocking, Note: "拖沓"}},
			Basis: model.EvidenceBasis{Documents: []model.DocumentBasis{
				{Ref: model.DocumentRef{Kind: model.DocumentManuscript, ID: "c1"}, Revision: 2},
			}}},
		model.OperationRewriteAffected: model.RewriteAffectedInput{ChapterIDs: []string{"c1"}, BaseRevision: 3, ResolutionProposalID: "p-c1", Reason: "设定变更"},
	}
	var rendered []string
	for kind, input := range tasks {
		raw, _ := json.Marshal(input)
		view, err := story.Task(kind, raw)
		if err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
		if kind == model.OperationReviewRange &&
			!strings.Contains(string(view), `"reviewed":[1],"prior_findings":[{"chapter":2,"severity":"blocking","note":"拖沓"}]`) {
			t.Fatalf("review view = %s", view)
		}
		rendered = append(rendered, string(view))
	}
	ownership, err := story.Ownership([]model.OwnershipRule{
		{Target: model.DocumentRef{Kind: model.DocumentManuscript, ID: "c1"}, Control: model.ControlLocked},
		{Target: model.DocumentRef{Kind: model.DocumentEntity, ID: "e1"}, Control: model.ControlGuided, Guidance: []string{"保持隐忍"}},
		{Target: model.DocumentRef{Kind: model.DocumentPlan, ID: "gone"}, Control: model.ControlLocked},
	})
	if err != nil || len(ownership) != 2 {
		t.Fatalf("ownership = %+v, %v", ownership, err)
	}
	for _, query := range []Query{{Chapter: 2}, {Arc: 1}, {Volume: 1}, {Entity: "小凡"}} {
		view, err := story.Read(query)
		if err != nil {
			t.Fatalf("read %+v: %v", query, err)
		}
		payload, _ := json.Marshal(view)
		rendered = append(rendered, string(payload))
	}
	for _, value := range []any{ownership, story.Outline([]string{"c2"}), story.Facts([]string{"f1", "f2", "f3"}), story.Texts([]string{"c1"})} {
		payload, _ := json.Marshal(value)
		rendered = append(rendered, string(payload))
	}
	all := strings.Join(rendered, "\n")
	if leaked := internalID.FindAllString(all, -1); len(leaked) > 0 {
		t.Fatalf("internal ids leaked %v in:\n%s", leaked, all)
	}
	for _, want := range []string{`"chapter":2`, `"chapters":[1,2]`, `第 1 个故事弧《试炼》`, `第 1 章《第1章题》的正文`, `"pending_facts"`} {
		if !strings.Contains(all, want) {
			t.Fatalf("missing %s in:\n%s", want, all)
		}
	}
}

// 错误里的内部 ID 换成故事标签，错误链不变；新提交的文档也认得。
func TestHumanizeTranslatesIDsAndKeepsErrorChain(t *testing.T) {
	story := fixture()
	projected, err := story.Apply([]model.Patch{{Document: model.DocumentRef{Kind: model.DocumentCanon, ID: "f2"}, Operation: model.PatchDelete}})
	if err != nil {
		t.Fatal(err)
	}
	cause := fmt.Errorf(`rewritten chapter "c2" must redeclare canon "f2", see "entity:e1": %w`, model.ErrStructuralConflict)
	got := projected.Humanize(cause)
	if !errors.Is(got, model.ErrStructuralConflict) || internalID.MatchString(got.Error()) ||
		!strings.Contains(got.Error(), "第 2 章《立誓》") || !strings.Contains(got.Error(), "「林凡」event.oath（第 2 章）") {
		t.Fatalf("humanized = %v", got)
	}
}

func TestFindingsLandOnChaptersInRange(t *testing.T) {
	story := fixture()
	input := model.ReviewRangeInput{ChapterIDs: []string{"c2"}}
	findings, err := story.Findings(input, []Finding{{Chapter: 2, Severity: "note", Note: "好"}})
	if err != nil || findings[0].ChapterID != "c2" {
		t.Fatalf("findings = %+v, %v", findings, err)
	}
	if _, err := story.Findings(input, []Finding{{Chapter: 1, Severity: "note", Note: "越界"}}); !errors.Is(err, model.ErrInvalid) {
		t.Fatalf("out of range finding: %v", err)
	}
	// 已审且正文未变的章只作上下文（D68）：阻塞必须链接被违反的要求，报错用故事语言。
	recheck := model.ReviewRangeInput{ChapterIDs: []string{"c1", "c2"}, Reviewed: []string{"c1"}}
	_, err = story.Findings(recheck, []Finding{{Chapter: 1, Severity: "blocking", Note: "旧问题"}})
	if humanized := story.Humanize(err); !errors.Is(err, model.ErrInvalid) || internalID.MatchString(humanized.Error()) || !strings.Contains(humanized.Error(), "第 1 章") {
		t.Fatalf("blocking on reviewed chapter: %v", humanized)
	}
	for _, finding := range []Finding{
		{Chapter: 1, Severity: "note", Note: "旧问题"},
		{Chapter: 1, Severity: "blocking", Note: "违反要求", Requirement: "directive:d1"},
		{Chapter: 2, Severity: "blocking", Note: "改动章的问题"},
	} {
		if _, err := story.Findings(recheck, []Finding{finding}); err != nil {
			t.Fatalf("finding %+v: %v", finding, err)
		}
	}
}
