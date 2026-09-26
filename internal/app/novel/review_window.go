package novel

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"
	"strings"

	projectdoc "github.com/voocel/ainovel-cli/internal/app/project"
	"github.com/voocel/ainovel-cli/internal/domain/model"
)

// 窗口审阅（D62）：审阅按窗口推进，每章由包含它的最新有效裁定覆盖；要求按作用域
// 三态核验，未到期如实 pending，由作用域末章所在窗口兑现。

// requirement 是一项要核验的要求：用户要求、意图条目或收官要求。
type requirement struct {
	model.Requirement
	scope     string // 用户要求的作用域（故事语言，全书为空），扩窗时随原文交给规划安排落点
	covers    func(number int) bool
	end       int  // 作用域末章；0 表示尚未确定
	universal bool // 禁止项：每个窗口都必须给出结论
	finale    bool // 收官要求：落点就是末章，扩窗无需安排
}

// requirements 列出当前要核验的全部要求：意图条目、收官要求在前，用户要求按 ID。
// final 是全书章数，0 表示尚未收官（D63）：全书作用域的末章随之未定，照样逐窗投递。
// dropped 是作用域在全书之内没有章节的用户要求：它们无从兑现，由调用方决定如何处理。
func requirements(project projectdoc.Snapshot, final int) (list []requirement, dropped []model.Directive) {
	plans := model.ChapterPlansInOrder(project.Plan)
	story := storyOf(project)
	whole := func(int) bool { return true }
	add := func(r requirement) {
		if strings.TrimSpace(r.Text) != "" {
			list = append(list, r)
		}
	}
	for i, text := range project.Intent.Required {
		add(requirement{Requirement: model.Requirement{ID: "intent:required:" + strconv.Itoa(i), Text: text}, covers: whole, end: final})
	}
	for i, text := range project.Intent.Forbidden {
		add(requirement{Requirement: model.Requirement{ID: "intent:forbidden:" + strconv.Itoa(i), Text: text}, covers: whole, end: final, universal: true})
	}
	if final > 0 {
		add(finaleRequirement(project, final))
	}
	for _, directive := range model.ActiveDirectives(project.Directives) {
		// 覆盖判定到全书末章（未收官时到蓝图末章）：已规划的章节按祖先链匹配，蓝图之外
		// 只有章号。推导器保证收官后蓝图不超过全书章数。
		known := max(final, len(plans))
		scope, last := make(map[int]bool, known), 0
		for n := 1; n <= known; n++ {
			var planID string
			if n <= len(plans) {
				planID = plans[n-1].ID
			}
			if directive.Covers(directiveTarget(project, n, planID)) {
				scope[n], last = true, n
			}
		}
		covers := func(n int) bool { return scope[n] }
		end := last
		switch {
		case directive.PlanScoped() && (final == 0 || len(plans) < final):
			end = 0 // 蓝图未到末章，节点下还可能挂入后续章节
		case final == 0:
			end = directive.BoundedEnd() // 章号区间自带末章，其余作用域随全书章数未定
		case end == 0:
			dropped = append(dropped, directive)
			continue
		}
		var scopeLabel string
		if directive.Scope != model.DirectiveScopeProject {
			scopeLabel = story.DescribeScope(directive)
		}
		add(requirement{Requirement: model.Requirement{ID: "directive:" + directive.ID, Text: directive.Text}, scope: scopeLabel, covers: covers, end: end})
	}
	return list, dropped
}

// finalePrefix 是收官要求 ID 的前缀，后缀是终局与伏笔集合的摘要。
const finalePrefix = "book:finale:"

// finaleRequirement 是收官要求（D63）：终局方向加当前未回收的伏笔，由末章所在窗口
// 下结论——这是对"草草收尾"的确定性拦截，宿主给清单，审阅判断结局是否收束。
// ID 带清单摘要：之后新增或回收伏笔，旧的"满足"不再算数。
func finaleRequirement(project projectdoc.Snapshot, final int) requirement {
	ending := project.Intent.EndingDirection
	if strings.TrimSpace(ending) == "" && project.Compass != nil {
		ending = project.Compass.Ending
	}
	var text, identity strings.Builder
	if strings.TrimSpace(ending) != "" {
		fmt.Fprintf(&text, "全书在第 %d 章收官，结局落到终局方向：%s", final, ending)
	}
	fmt.Fprintf(&identity, "%s\x00", ending)
	var threads []string
	story := storyOf(project)
	for _, fact := range project.Canon {
		if fact.Kind == model.CanonForeshadow && !fact.Resolved {
			threads = append(threads, story.FactSummary(fact))
			fmt.Fprintf(&identity, "%s\x00", fact.ID)
		}
	}
	if len(threads) > 0 {
		if text.Len() == 0 {
			fmt.Fprintf(&text, "全书在第 %d 章收官", final)
		}
		fmt.Fprintf(&text, "；以下伏笔尚未回收，结局须回收或明确交代：%s", strings.Join(threads, "、"))
	} else if text.Len() == 0 {
		return requirement{}
	}
	return requirement{
		Requirement: model.Requirement{ID: finalePrefix + model.Digest([]byte(identity.String()))[:8], Text: text.String()},
		covers:      func(n int) bool { return n == final },
		end:         final,
		finale:      true,
	}
}

// CurrentVerdicts 返回每章当前的裁定：包含该章的有效裁定里最新的一份（CreatedAt 优先，
// 相同则 Key 决胜）。协调器与工作台共用，两处口径一致。
func CurrentVerdicts(verdicts []StoredVerdict) map[string]*StoredVerdict {
	current := make(map[string]*StoredVerdict)
	for index := range verdicts {
		candidate := &verdicts[index]
		for _, id := range candidate.Verdict.ChapterIDs {
			best := current[id]
			if best == nil || candidate.CreatedAt.After(best.CreatedAt) ||
				(candidate.CreatedAt.Equal(best.CreatedAt) && candidate.Key > best.Key) {
				current[id] = candidate
			}
		}
	}
	return current
}

// reviewLedger 是审阅闸门的推导视图：已写章节前缀、每章当前裁定与要求集合。
type reviewLedger struct {
	chapters  []model.ManuscriptChapter // 按计划顺序连续已写的章节，下标+1 即章号
	numbers   map[string]int
	current   map[string]*StoredVerdict
	effective map[string]model.ReviewVerdict // 裁定 Key → 生效形态
	reqs      []requirement
	dropped   []model.Directive // 作用域落在全书之外的用户要求
}

func newReviewLedger(project projectdoc.Snapshot, final int, verdicts []StoredVerdict) reviewLedger {
	ledger := reviewLedger{
		numbers: make(map[string]int), current: CurrentVerdicts(verdicts),
		effective: make(map[string]model.ReviewVerdict, len(verdicts)),
	}
	ledger.reqs, ledger.dropped = requirements(project, final)
	for _, stored := range verdicts {
		ledger.effective[stored.Key] = stored.Effective()
	}
	written := manuscriptsByPlanNode(project.Manuscript)
	prefix := true
	for index, plan := range model.ChapterPlansInOrder(project.Plan) {
		chapter, ok := written[plan.ID]
		if !ok {
			prefix = false
			continue
		}
		ledger.numbers[chapter.ID] = index + 1
		if prefix {
			ledger.chapters = append(ledger.chapters, chapter)
		}
	}
	return ledger
}

// gate 推导第 1..through 章的审阅闸门：按章节顺序，未覆盖的章节起一个窗口（至多
// window 章）送审，当前裁定 blocked 则返回它待重写；全部通过后，作用域已在其中收尾
// 却未兑现的要求，交给覆盖其末章的窗口再审一次（此时必须给出结论）。都没有表示闸门已过。
func (l reviewLedger) gate(through, window int) (review []string, blocked *model.ReviewVerdict) {
	chapters := l.chapters[:min(through, len(l.chapters))]
	for i, chapter := range chapters {
		stored := l.current[chapter.ID]
		if stored == nil {
			for _, next := range chapters[i:min(i+window, len(chapters))] {
				if l.current[next.ID] != nil {
					break
				}
				review = append(review, next.ID)
			}
			return review, nil
		}
		if verdict := l.effective[stored.Key]; verdict.Status != model.ReviewPass {
			return nil, &verdict
		}
	}
	due := slices.Clone(l.reqs)
	slices.SortFunc(due, func(a, b requirement) int { return cmp.Or(cmp.Compare(a.end, b.end), strings.Compare(a.ID, b.ID)) })
	for _, r := range due {
		if r.end != 0 && r.end <= len(chapters) && !l.satisfied(r) {
			return l.current[chapters[r.end-1].ID].Verdict.ChapterIDs, nil
		}
	}
	return nil, nil
}

// window 装配一个窗口的审阅要求：窗口内任一章在作用域内即投递。禁止项每窗必须下结论；
// 其余要求在作用域末章所在窗口、且尚未兑现时必须下结论——已在别处兑现的不强迫末窗重判。
func (l reviewLedger) window(chapters []string) []model.Requirement {
	var list []model.Requirement
	for _, r := range l.reqs {
		hit, closes := false, false
		for _, id := range chapters {
			number := l.numbers[id]
			hit = hit || r.covers(number)
			closes = closes || (r.end != 0 && r.end == number)
		}
		if !hit {
			continue
		}
		item := r.Requirement
		item.Settle = r.universal || (closes && !l.satisfied(r))
		list = append(list, item)
	}
	return list
}

// satisfied 是单调证据：任一有效裁定在作用域内判过满足即成立——基线成立说明它审过的
// 正文未变，结论持续有效，同一 Revision 内只增不减。
func (l reviewLedger) satisfied(r requirement) bool {
	for _, verdict := range l.effective {
		if verdict.CheckStatus(r.ID) != model.CheckSatisfied {
			continue
		}
		for _, id := range verdict.ChapterIDs {
			if number, ok := l.numbers[id]; ok && r.covers(number) {
				return true
			}
		}
	}
	return false
}

// concluded 报告第 number 章是否已作为全书结局通过审阅：它的当前生效裁定判收官要求满足。
// 在它之后扩窗就是续写（D63）。
func (l reviewLedger) concluded(number int) bool {
	if number < 1 || number > len(l.chapters) {
		return false
	}
	stored := l.current[l.chapters[number-1].ID]
	if stored == nil {
		return false
	}
	for _, check := range l.effective[stored.Key].Checks {
		if strings.HasPrefix(check.ID, finalePrefix) && check.Status == model.CheckSatisfied {
			return true
		}
	}
	return false
}

// pending 列出扩窗时仍待兑现的要求：未满足、作用域越过已覆盖章节；禁止项与收官要求
// 除外（收官由罗盘与上下文里的未回收伏笔引导，不重复占用预算）。用户要求附带作用域，
// 规划据此安排落点、定收官章数时不让它落空。
func (l reviewLedger) pending(covered int) []string {
	var texts []string
	for _, r := range l.reqs {
		if r.universal || r.finale || (r.end != 0 && r.end <= covered) || l.satisfied(r) {
			continue
		}
		if r.scope != "" {
			texts = append(texts, fmt.Sprintf("%s（作用域：%s）", r.Text, r.scope))
			continue
		}
		texts = append(texts, r.Text)
	}
	return texts
}

// reviewOperationID 让任务 ID 完整标识输入：窗口、Revision 与要求（含是否必须下结论）。
// 同 ID 即同一任务，重复派发由内核判为卡住，而不会复用旧输入。
func reviewOperationID(runID string, chapters []string, revision model.Revision, requirements []model.Requirement) string {
	var identity strings.Builder
	for _, id := range chapters {
		fmt.Fprintf(&identity, "%s\x00", id)
	}
	for _, r := range requirements {
		fmt.Fprintf(&identity, "%s\x00%t\x00", r.ID, r.Settle)
	}
	return runQuickID(runID, "review", chapters[0], "r"+strconv.FormatInt(int64(revision), 10), model.Digest([]byte(identity.String()))[:8])
}
