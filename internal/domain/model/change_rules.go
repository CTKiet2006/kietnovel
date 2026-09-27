package model

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// 创作领域的变更规则（§0.5 / D64）：跨文档不变量与任务提交契约只住在这里。Change
// Engine 负责载入、应用补丁与通用结构（内容校验、依赖、只追加、工件），然后调用
// ValidateChange；工具边界、执行收尾与用户批准三条路径因此执行同一份规则。

// DocumentSet 是某个 Revision（或提案应用后）的全部权威文档，按 DocumentRef.Key() 索引。
type DocumentSet map[string]DocumentVersion

// ChangeCheck 是一次变更校验的输入：Task 是提案所属任务（用户直接变更为 nil），
// Base 是提案基线，Projected 是应用补丁后的状态。
type ChangeCheck struct {
	Proposal  Proposal
	Task      *Operation
	Base      DocumentSet
	Projected DocumentSet
}

// ValidateChange 执行创作领域的全部跨文档不变量与任务提交契约。违例返回
// ErrStructuralConflict（任务契约违例为 ErrInvalid，模型可在工具边界自纠）。
func ValidateChange(check ChangeCheck) error {
	if check.Proposal.Target.Kind != AuthorityProject {
		return validateAssetState(check.Proposal.Target, check.Projected)
	}
	base, err := storyOf(check.Base, false)
	if err != nil {
		return err
	}
	projected, err := storyOf(check.Projected, true)
	if err != nil {
		return err
	}
	if err := validateCanonChanges(base, projected, check.Proposal); err != nil {
		return err
	}
	if err := validateMachineAuthorship(base, projected, check.Proposal); err != nil {
		return err
	}
	if err := projected.validateStructure(); err != nil {
		return err
	}
	if check.Task == nil {
		return nil
	}
	return validateTaskSubmission(*check.Task, base, projected, check.Proposal.Patches)
}

// story 是一次校验看到的作品状态，每份文档只解码一次。
type story struct {
	plans    map[string]PlanNode
	chapters map[string]ManuscriptChapter
	canon    map[string]CanonFact
	entities map[string]Entity
	compass  *Compass
}

// storyOf 解码校验需要的文档；基线不需要正文时跳过（正文是全书最大的部分）。
func storyOf(set DocumentSet, chapters bool) (story, error) {
	s := story{
		plans: make(map[string]PlanNode), chapters: make(map[string]ManuscriptChapter),
		canon: make(map[string]CanonFact), entities: make(map[string]Entity),
	}
	for _, document := range set {
		var err error
		switch document.Document.Kind {
		case DocumentPlan:
			err = decodeInto(s.plans, document)
		case DocumentCanon:
			err = decodeInto(s.canon, document)
		case DocumentEntity:
			err = decodeInto(s.entities, document)
		case DocumentManuscript:
			if chapters {
				err = decodeInto(s.chapters, document)
			}
		case DocumentCompass:
			s.compass = new(Compass)
			err = json.Unmarshal(document.Content, s.compass)
		}
		if err != nil {
			return story{}, fmt.Errorf("decode %s: %w", document.Document.Key(), err)
		}
	}
	return s, nil
}

func decodeInto[T any](into map[string]T, document DocumentVersion) error {
	var value T
	if err := json.Unmarshal(document.Content, &value); err != nil {
		return err
	}
	into[document.Document.ID] = value
	return nil
}

// validateStructure 是蓝图、正文与事实之间的引用结构；依赖存在性与环由 Change Engine
// 的通用依赖图保证。
func (s story) validateStructure() error {
	for _, node := range s.plans {
		if node.ParentID == "" {
			continue
		}
		if parent, ok := s.plans[node.ParentID]; !ok || !validPlanParent(node.Kind, parent.Kind) {
			return fmt.Errorf("plan node %q of kind %s cannot have %s parent: %w", node.ID, node.Kind, parent.Kind, ErrStructuralConflict)
		}
	}
	for _, chapter := range s.chapters {
		if plan, ok := s.plans[chapter.PlanNodeID]; !ok || plan.Kind != PlanChapter {
			return fmt.Errorf("chapter %q references missing chapter plan %q: %w", chapter.ID, chapter.PlanNodeID, ErrStructuralConflict)
		}
	}
	for _, fact := range s.canon {
		for _, chapterID := range []string{fact.SourceChapterID, fact.EffectiveChapterID} {
			if _, ok := s.chapters[chapterID]; chapterID != "" && !ok {
				return fmt.Errorf("canon %q references missing chapter %q: %w", fact.ID, chapterID, ErrStructuralConflict)
			}
		}
	}
	return nil
}

func validPlanParent(child, parent PlanNodeKind) bool {
	return child == PlanArc && parent == PlanVolume ||
		child == PlanChapter && parent == PlanArc ||
		child == PlanBeat && parent == PlanChapter
}

// validateCanonChanges 一次遍历执行 Canon 的全部规则。对所有作者：
//   - 连续性：已有事实的身份（种类、主体、谓词）不变，更新必须带与现值一致的 old_value；
//     新事实不得声明 old_value；
//   - 非事件事实按主体+谓词只有一个节点（D61）。
//
// 对 AI/扩展提案另有 D41 规则（用户提案由用户为内容背书）：
//  1. 带正文的提案里，每条事实 put 的来源章必须是本提案的正文之一；
//  2. 每个正文 put 至少一条来源事实，且必须重申报 base 中全部来源于该章的事实；
//  3. 带正文的提案只能改动来源章在本提案正文集合内的事件与伏笔回收——跨章只追加；
//  4. 状态类事实更新的生效位置不得早于现值——插叙不覆盖当前状态，应记录为事件。
func validateCanonChanges(base, projected story, proposal Proposal) error {
	machine := proposal.Author.Machine()
	chapters := manuscriptPuts(proposal.Patches)
	owners := make(map[CanonKey][]string)
	for _, fact := range projected.canon {
		if key, keyed := fact.ConceptKey(); keyed {
			owners[key] = append(owners[key], fact.ID)
		}
	}
	covered, touched := make(map[string]struct{}), make(map[string]struct{})
	for _, patch := range proposal.Patches {
		if patch.Document.Kind != DocumentCanon {
			continue
		}
		id := patch.Document.ID
		touched[id] = struct{}{}
		previous, existed := base.canon[id]
		ownChapter := func(chapterID string) bool {
			_, own := chapters[chapterID]
			return len(chapters) == 0 || own
		}
		if machine && existed && previous.IsEvent() && !ownChapter(previous.SourceChapterID) {
			return fmt.Errorf("event %q belongs to chapter %q outside this proposal; events are append-only across chapters: %w",
				previous.ID, previous.SourceChapterID, ErrStructuralConflict)
		}
		if patch.Operation != PatchPut {
			continue
		}
		next := projected.canon[id]
		if err := validateCanonContinuity(previous, existed, next); err != nil {
			return err
		}
		if key, keyed := next.ConceptKey(); keyed {
			if others := slices.DeleteFunc(slices.Clone(owners[key]), func(owner string) bool { return owner == id }); len(others) > 0 {
				slices.Sort(others)
				return fmt.Errorf("canon %q duplicates %s of %q; one subject+predicate is one fact, update %q instead: %w",
					id, key, others[0], others[0], ErrStructuralConflict)
			}
		}
		if !machine {
			continue
		}
		if !ownChapter(next.SourceChapterID) {
			return fmt.Errorf("canon %q must be sourced from a chapter in the same proposal, not %q: %w", id, next.SourceChapterID, ErrStructuralConflict)
		}
		covered[next.SourceChapterID] = struct{}{}
		if existed && !next.IsEvent() &&
			projected.chapters[next.EffectiveChapter()].Number < projected.chapters[previous.EffectiveChapter()].Number {
			return fmt.Errorf("canon %q (%s/%s) is already current as of %q, later than %q; an earlier chapter cannot override it, record flashbacks as events: %w",
				id, next.SubjectID, next.Predicate, previous.EffectiveChapter(), next.EffectiveChapter(), ErrStructuralConflict)
		}
		if existed && previous.Resolved && !next.Resolved && !ownChapter(previous.SourceChapterID) {
			return fmt.Errorf("foreshadow %q was resolved in %q; start a new thread with a new predicate instead of reopening it: %w",
				id, previous.SourceChapterID, ErrStructuralConflict)
		}
	}
	if !machine {
		return nil
	}
	for chapterID := range chapters {
		if _, ok := covered[chapterID]; !ok {
			return fmt.Errorf("AI manuscript %q requires a Canon Delta in the same Proposal: %w", chapterID, ErrStructuralConflict)
		}
	}
	for id, fact := range base.canon {
		if _, rewritten := chapters[fact.SourceChapterID]; rewritten {
			if _, ok := touched[id]; !ok {
				return fmt.Errorf("rewritten chapter %q must redeclare canon %q (confirm, update or delete): %w",
					fact.SourceChapterID, id, ErrStructuralConflict)
			}
		}
	}
	return nil
}

func validateCanonContinuity(previous CanonFact, existed bool, next CanonFact) error {
	if !existed {
		if len(next.PreviousValue) != 0 {
			return fmt.Errorf("new canon %q cannot declare old_value: %w", next.ID, ErrStructuralConflict)
		}
		return nil
	}
	if next.Kind != previous.Kind || next.SubjectID != previous.SubjectID || next.Predicate != previous.Predicate {
		return fmt.Errorf("canon %q identity cannot change; create a new stable fact instead: %w", next.ID, ErrStructuralConflict)
	}
	if len(next.PreviousValue) == 0 {
		return fmt.Errorf("updated canon %q requires old_value: %w", next.ID, ErrStructuralConflict)
	}
	same, err := sameJSONValue(next.PreviousValue, previous.Value)
	if err != nil {
		return err
	}
	if !same {
		return fmt.Errorf("canon %q old_value does not match the previous new_value: %w", next.ID, ErrStructuralConflict)
	}
	return nil
}

// validateMachineAuthorship 是 AI/扩展提案的署名与实体规则：章节必须署自己的名、不得
// 冒认用户（D34）；新实体不得与已有实体的名称或别名相同——有界上下文里看不到旧实体时
// 不另建同名主体（D61）。用户提案由用户背书，不受此限。
func validateMachineAuthorship(base, projected story, proposal Proposal) error {
	if !proposal.Author.Machine() {
		return nil
	}
	names := make(map[string]string)
	for _, entity := range base.entities {
		for _, name := range append([]string{entity.Name}, entity.Aliases...) {
			if owner, taken := names[name]; !taken || entity.ID < owner {
				names[name] = entity.ID
			}
		}
	}
	for _, patch := range proposal.Patches {
		if patch.Operation != PatchPut {
			continue
		}
		switch patch.Document.Kind {
		case DocumentManuscript:
			if chapter := projected.chapters[patch.Document.ID]; chapter.Author != proposal.Author.Kind {
				return fmt.Errorf("chapter %q author %s does not match proposal author %s: %w",
					chapter.ID, chapter.Author, proposal.Author.Kind, ErrStructuralConflict)
			}
		case DocumentEntity:
			if _, exists := base.entities[patch.Document.ID]; exists {
				continue
			}
			entity := projected.entities[patch.Document.ID]
			if owner, taken := names[entity.Name]; taken {
				return fmt.Errorf("entity %q name %q is already used by %q; reference %q instead of creating a new entity: %w",
					entity.ID, entity.Name, owner, owner, ErrStructuralConflict)
			}
		}
	}
	return nil
}

// validateTaskSubmission 是任务提交契约：提案只能完成它所属任务要求的事。
//   - 篇幅只由规划任务改变（§6.3 D63）：非规划任务不得改罗盘、不得增删章节节点；固定
//     篇幅时 AI 不得改罗盘。规划任务展开多少章由它决定（D67），边界只守进展与上界：
//     至少追加一章（或以现有章数收官），固定篇幅不超过全书章数，AI 定篇幅时罗盘必填、
//     收官后不超过收官章数、未收官时不得触及篇幅上限。
//   - 章节任务只写它的目标章节，正文满足任务携带要求的字数约束（S13）。
//   - 事实核验任务只动 Canon，确认或删除全部待核验事实（D41）。
func validateTaskSubmission(task Operation, base, projected story, patches []Patch) error {
	input, err := DecodeTaskInput(task.Kind, task.Input)
	if err != nil {
		return err
	}
	if err := validateBlueprint(input, base, projected, patches); err != nil {
		return err
	}
	switch input := input.(type) {
	case *WriteChapterInput:
		written := manuscriptPuts(patches)
		for id := range written {
			if projected.chapters[id].PlanNodeID != input.ChapterPlanID {
				return fmt.Errorf("chapter %q does not implement the requested plan %q: %w", id, input.ChapterPlanID, ErrInvalid)
			}
		}
		if err := requireManuscripts(patches, len(written) == 1, "one new chapter for plan "+input.ChapterPlanID); err != nil {
			return err
		}
	case *RewriteChapterInput:
		if err := requireManuscripts(patches, sameKeys(manuscriptPuts(patches), input.ChapterID), "chapter "+input.ChapterID); err != nil {
			return err
		}
	case *RewriteAffectedInput:
		if input.BaseRevision != task.Snapshot.BaseRevision {
			return fmt.Errorf("affected rewrite input does not match its execution snapshot: %w", ErrInvalid)
		}
		if err := requireManuscripts(patches, sameKeys(manuscriptPuts(patches), input.ChapterIDs...), fmt.Sprintf("chapters %v", input.ChapterIDs)); err != nil {
			return err
		}
	case *ReviseCanonInput:
		return validateCanonRevision(*input, projected, patches)
	}
	for id := range manuscriptPuts(patches) {
		if err := checkWordCounts(TaskDirectives(input), projected.chapters[id]); err != nil {
			return err
		}
	}
	return nil
}

func validateBlueprint(input TaskInput, base, projected story, patches []Patch) error {
	fixed, planning := planRequest(input)
	for _, patch := range patches {
		switch {
		case patch.Document.Kind == DocumentCompass && !planning:
			return fmt.Errorf("only planning tasks may change the compass: %w", ErrInvalid)
		case patch.Document.Kind == DocumentCompass && fixed > 0:
			return fmt.Errorf("the user fixed the length at %d chapters; leave the compass unchanged: %w", fixed, ErrInvalid)
		}
	}
	existing, chapters, compass := countChapters(base.plans), countChapters(projected.plans), projected.compass
	switch {
	case !planning:
		if chapters != existing {
			return fmt.Errorf("only planning tasks may add or remove chapter nodes (%d → %d): %w", existing, chapters, ErrInvalid)
		}
	case fixed > 0:
		if chapters <= existing || chapters > fixed {
			return fmt.Errorf("plan has %d chapter nodes: append at least one chapter after the %d planned, up to the fixed %d: %w", chapters, existing, fixed, ErrInvalid)
		}
	case compass == nil:
		return fmt.Errorf("the blueprint requires a compass when the AI decides the length: %w", ErrInvalid)
	case compass.Final > 0:
		if compass.Final < existing {
			return fmt.Errorf("compass final %d is below the %d planned chapters: %w", compass.Final, existing, ErrInvalid)
		}
		if chapters > compass.Final {
			return fmt.Errorf("plan has %d chapter nodes, beyond the final chapter %d: %w", chapters, compass.Final, ErrInvalid)
		}
		if chapters == existing && chapters != compass.Final {
			return noProgress(existing)
		}
	case max(chapters, existing+1) >= compass.ScaleMax:
		if existing > compass.ScaleMax {
			return fmt.Errorf("the %d planned chapters already exceed the compass scale_max %d: raise scale_max for user approval: %w", existing, compass.ScaleMax, ErrInvalid)
		}
		return fmt.Errorf("plan reaches the compass scale_max %d: declare the final chapter count (not below the %d planned chapters), or raise scale_max for user approval: %w", compass.ScaleMax, existing, ErrInvalid)
	case chapters <= existing:
		return noProgress(existing)
	}
	return nil
}

func noProgress(existing int) error {
	return fmt.Errorf("plan adds no chapter: append at least one chapter, or declare the final chapter count as the %d planned: %w", existing, ErrInvalid)
}

// planRequest 取规划任务的篇幅约束：fixed > 0 表示用户固定全书章数；非规划任务 planning=false。
func planRequest(input TaskInput) (fixed int, planning bool) {
	switch input := input.(type) {
	case *DevelopPlanInput:
		return input.FixedChapters, true
	case *RevisePlanInput:
		return input.FixedChapters, true
	default:
		return 0, false
	}
}

func countChapters(nodes map[string]PlanNode) int {
	count := 0
	for _, node := range nodes {
		if node.Kind == PlanChapter {
			count++
		}
	}
	return count
}

// requireManuscripts：章节任务的正文补丁只能是目标章节的 put，且恰好覆盖目标。
func requireManuscripts(patches []Patch, covers bool, target string) error {
	for _, patch := range patches {
		if patch.Document.Kind == DocumentManuscript && patch.Operation != PatchPut {
			return fmt.Errorf("chapter tasks cannot delete chapter %q: %w", patch.Document.ID, ErrInvalid)
		}
	}
	if !covers {
		return fmt.Errorf("submission must write exactly %s: %w", target, ErrInvalid)
	}
	return nil
}

func manuscriptPuts(patches []Patch) map[string]struct{} {
	ids := make(map[string]struct{})
	for _, patch := range patches {
		if patch.Document.Kind == DocumentManuscript && patch.Operation == PatchPut {
			ids[patch.Document.ID] = struct{}{}
		}
	}
	return ids
}

func sameKeys(set map[string]struct{}, ids ...string) bool {
	if len(set) != len(ids) {
		return false
	}
	for _, id := range ids {
		if _, ok := set[id]; !ok {
			return false
		}
	}
	return true
}

// checkWordCounts 是量化要求的确定性校验（§4.9 / S13）：字数按各 block 正文字符数
// 累加、不含标题；越界连同实际值与区间原样回给模型自纠。
func checkWordCounts(directives []Directive, chapter ManuscriptChapter) error {
	words := chapter.Runes()
	for _, directive := range directives {
		low, high, ok := directive.Constraints.Bounds()
		if !ok {
			continue
		}
		if words < low || (high > 0 && words > high) {
			return fmt.Errorf("chapter %q has %d characters, the requirement 「%s」 needs %d-%d: %w",
				chapter.ID, words, directive.Text, low, high, ErrInvalid)
		}
	}
	return nil
}

// validateCanonRevision 是事实核验任务的契约（D41）：只允许 canon 补丁，每条 put 的
// 来源章是任务章节，任务列出的待核验事实全部被确认（put）或删除，且该章至少留一条事实。
func validateCanonRevision(input ReviseCanonInput, projected story, patches []Patch) error {
	touched := make(map[string]struct{}, len(patches))
	declared := 0
	for _, patch := range patches {
		if patch.Document.Kind != DocumentCanon {
			return fmt.Errorf("canon revision may only change canon facts, got %s: %w", patch.Document.Key(), ErrInvalid)
		}
		touched[patch.Document.ID] = struct{}{}
		if patch.Operation != PatchPut {
			continue
		}
		if fact := projected.canon[patch.Document.ID]; fact.SourceChapterID != input.ChapterID {
			return fmt.Errorf("canon %q must be sourced from chapter %q: %w", fact.ID, input.ChapterID, ErrInvalid)
		}
		declared++
	}
	for _, id := range input.FactIDs {
		if _, ok := touched[id]; !ok {
			return fmt.Errorf("canon %q is pending verification and must be confirmed, updated or deleted: %w", id, ErrInvalid)
		}
	}
	if declared == 0 {
		return fmt.Errorf("chapter %q needs at least one canon fact: %w", input.ChapterID, ErrInvalid)
	}
	return nil
}

func validateAssetState(target AuthorityTarget, state DocumentSet) error {
	if len(state) > 1 {
		return fmt.Errorf("%s authority may contain only one root document: %w", target.Kind, ErrStructuralConflict)
	}
	for _, document := range state {
		switch target.Kind {
		case AuthorityProfile:
			var profile CreatorProfile
			if err := json.Unmarshal(document.Content, &profile); err != nil {
				return err
			}
			if profile.ID != target.ID || profile.Scope != target.Scope {
				return fmt.Errorf("creator profile does not match authority target: %w", ErrStructuralConflict)
			}
		case AuthorityPack:
			var pack PackManifest
			if err := json.Unmarshal(document.Content, &pack); err != nil {
				return err
			}
			if pack.ID != target.ID {
				return fmt.Errorf("pack does not match authority target: %w", ErrStructuralConflict)
			}
		}
	}
	return nil
}

func sameJSONValue(left, right json.RawMessage) (bool, error) {
	canonical := func(raw json.RawMessage) ([]byte, error) {
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		var value any
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		return json.Marshal(value)
	}
	leftValue, err := canonical(left)
	if err != nil {
		return false, fmt.Errorf("decode canon old_value: %w", err)
	}
	rightValue, err := canonical(right)
	if err != nil {
		return false, fmt.Errorf("decode previous canon new_value: %w", err)
	}
	return bytes.Equal(leftValue, rightValue), nil
}

// NormalizeSubmission 是宿主对执行器提交的规范化（D40）：正文的故事依赖由宿主按本章
// Canon Delta 的主体写入，执行器自行声明的 depends_on 不作数。工具边界与执行收尾共用。
func NormalizeSubmission(patches []Patch) ([]Patch, error) {
	subjects := make(map[string][]DocumentRef)
	for _, patch := range patches {
		if patch.Document.Kind != DocumentCanon || patch.Operation != PatchPut {
			continue
		}
		var fact CanonFact
		if err := DecodeStrict(patch.Content, &fact); err != nil {
			return nil, fmt.Errorf("decode canon delta %q: %w", patch.Document.ID, err)
		}
		if fact.SourceChapterID != "" {
			subjects[fact.SourceChapterID] = append(subjects[fact.SourceChapterID], DocumentRef{Kind: DocumentEntity, ID: fact.SubjectID})
		}
	}
	bound := slices.Clone(patches)
	for i, patch := range bound {
		if patch.Document.Kind != DocumentManuscript || patch.Operation != PatchPut {
			continue
		}
		var chapter ManuscriptChapter
		if err := DecodeStrict(patch.Content, &chapter); err != nil {
			return nil, fmt.Errorf("decode chapter %q: %w", patch.Document.ID, err)
		}
		dependencies := subjects[chapter.ID]
		slices.SortFunc(dependencies, func(a, b DocumentRef) int { return strings.Compare(a.Key(), b.Key()) })
		chapter.DependsOn = slices.CompactFunc(dependencies, func(a, b DocumentRef) bool { return a.Key() == b.Key() })
		content, err := json.Marshal(chapter)
		if err != nil {
			return nil, fmt.Errorf("encode chapter %q: %w", patch.Document.ID, err)
		}
		bound[i].Content = content
	}
	return bound, nil
}
