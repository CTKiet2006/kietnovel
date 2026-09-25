package novel

import (
	"fmt"

	projectdoc "github.com/voocel/ainovel-cli/internal/app/project"
	"github.com/voocel/ainovel-cli/internal/domain/model"
)

// Length 是作品的篇幅口径（D63）：用户固定的章数、AI 的故事罗盘，以及由二者推出的
// 全书章数。推导、续跑与工作台共用这一条规则。
type Length struct {
	Fixed   int            `json:"fixed,omitempty"`   // 用户固定的全书章数；0 表示交给 AI
	Compass *model.Compass `json:"compass,omitempty"` // AI 给出的篇幅上限、终局与收官承诺
	// Final 是全书章数：固定时取 Fixed，否则取仍有效的收官承诺；0 表示尚未确定（开放期）。
	Final int `json:"final,omitempty"`
}

// LengthOf 按最近一轮创作的篇幅设定推出作品篇幅；没有运行时篇幅交给 AI。
func LengthOf(project projectdoc.Snapshot, run *model.CreationRun) Length {
	fixed := 0
	if run != nil {
		if goal, err := model.DecodeNovelGoal(run.Goal); err == nil {
			fixed = goal.TargetChapters
		}
	}
	return lengthOf(project, fixed)
}

func lengthOf(project projectdoc.Snapshot, fixed int) Length {
	length := Length{Fixed: fixed, Compass: project.Compass, Final: fixed}
	if fixed == 0 {
		length.Final = CommittedFinal(project.Compass, len(model.ChapterPlansInOrder(project.Plan)))
	}
	return length
}

// CommittedFinal 是 AI 收官承诺的生效值：不少于已规划章数才有效，更小视为过期（蓝图已被
// 改动越过它），按尚未收官处理，返回 0。推导器与作品库共用这一条规则。
func CommittedFinal(compass *model.Compass, planned int) int {
	if compass == nil || compass.Final < planned {
		return 0
	}
	return compass.Final
}

// extendTo 是扩窗请求的章节总数：全书章数已知时到末章为止；开放期按窗口扩展并以
// 篇幅上限封顶——已越过上限时仍请求多一章，由提交边界逼 AI 收官或申请上调上限。
func (l Length) extendTo(covered, window int) int {
	switch {
	case l.Final > 0:
		return min(covered+window, l.Final)
	case l.Compass == nil:
		return covered + window
	default:
		return max(covered+1, min(covered+window, l.Compass.ScaleMax))
	}
}

// developTo 是首次规划请求的章节数：全书章数已知时不超过它。
func (l Length) developTo(window int) int {
	if l.Final > 0 {
		return min(window, l.Final)
	}
	return window
}

// planningGoal 按篇幅阶段说明规划要对罗盘做什么。
func (l Length) planningGoal() string {
	switch {
	case l.Fixed > 0:
		return fmt.Sprintf("全书固定 %d 章：规划到第 %d 章时收束主线，把未回收的伏笔分配进收官章节", l.Fixed, l.Fixed)
	case l.Compass == nil:
		return "篇幅由你决定：本次必须给出故事罗盘 compass（按题材与故事容量定篇幅上限 scale_max 与终局方向 ending）；故事撑不满请求数量时可直接声明收官 final"
	case l.Compass.Final > 0 && l.Final == 0:
		return fmt.Sprintf("篇幅由你决定（当前上限 %d 章）：罗盘的收官承诺 %d 章已少于蓝图章数而失效，本次必须修订 compass.final（不少于已规划章数）或撤回收官（省略 final）", l.Compass.ScaleMax, l.Compass.Final)
	case l.Final > 0:
		return fmt.Sprintf("已承诺全书 %d 章收官：规划到第 %d 章时收束主线，把未回收的伏笔分配进收官章节；确需调整就修订 compass.final", l.Final, l.Final)
	default:
		return fmt.Sprintf("篇幅由你决定（当前上限 %d 章）：按故事进展修订 compass；故事已到收束点或临近上限时声明收官 final（全书章数，不少于已规划章数），并把未回收的伏笔分配进收官章节；确需更长可上调 scale_max（需用户同意）", l.Compass.ScaleMax)
	}
}

// planInputID 是规划任务 ID 中标识篇幅输入的一段。
func planInputID(requested, fixed int) string {
	return fmt.Sprintf("r%d:f%d", requested, fixed)
}
