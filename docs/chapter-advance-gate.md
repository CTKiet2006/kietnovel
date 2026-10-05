# Chapter Advance Gate

> 状态：已实现  
> 日期：2026-07-14  
> 解决：逐章验收、干预后安全暂停、崩溃恢复下的精确章节许可

## 1. 为什么需要它

长篇自动创作的核心风险不是多消耗一次调用，而是用户审读期间系统继续写入新章，并把建立在旧剧情上的摘要、角色状态和大纲反馈折入后续事实源。删除多写的一章并不能自动撤销这些派生状态，用户会因此失去对创作过程的信任。

项目仍以“给出目标后持续自主完成”为默认定位，所以不把每章确认变成全局默认。系统提供两种明确政策：

- `auto`：默认模式，持续自主推进；
- `review`：用户主动选择的逐章验收模式，每个正向新章都需要一次精确许可。

这不是把工作流交还给 Coordinator LLM。何时需要用户确认是用户政策；下一步确定性流程仍由 Route 推导；是否需要一次性停下来验收某次干预结果，才由 Arbiter 做语义判断。

## 2. 边界划分

| 问题 | 归属 | 原因 |
|---|---|---|
| 当前是否为逐章验收模式 | RunMeta / Host | 用户持久运行意图 |
| 哪一章已获许可 | RunMeta / Gate | 可验证、可恢复的机械事实 |
| 下一步运行哪个 Worker | `flow.Route` | 从创作事实纯函数推导 |
| 指令是否开始一个正向新章 | `flow.StartsForwardChapter` | 类型化机械判断 |
| “改完让我看看”是否需要暂停 | Arbiter | 自然语言语义判断 |
| 暂停何时触发 | `ChapterAdvanceGate` | 对一次性意图做确定性执行 |
| 预算是否允许继续 | `BudgetSentinel` | 独立 Host 政策 |

`AdvanceMode`、章节许可和一次性 hold 不进入 Route 决策表，也不允许模型修改。Route 的创作状态机与逐章验收政策保持正交。

## 3. 最小状态模型

`meta/run.json` 中只增加三项运行意图：

```go
type RunMeta struct {
	AdvanceMode          ChapterAdvanceMode `json:"advance_mode"`
	AdvancePermitChapter int                `json:"advance_permit_chapter,omitempty"`
	AdvanceHold          *AdvanceHold       `json:"advance_hold,omitempty"`
}

const (
	ChapterAdvanceAuto   ChapterAdvanceMode = "auto"
	ChapterAdvanceReview ChapterAdvanceMode = "review"
)

const (
	AdvanceHoldAtBoundary           AdvanceHoldAfter = "boundary"
	AdvanceHoldAfterRewritesDrained AdvanceHoldAfter = "rewrites_drained"
	AdvanceHoldAtChapter            AdvanceHoldAfter = "chapter"
)

type AdvanceHold struct {
	After         AdvanceHoldAfter `json:"after"`
	TargetChapter int              `json:"target_chapter,omitempty"`
	Reason        string           `json:"reason"`
}
```

没有通用 PolicyEngine、条件数组、许可队列、过期时间或策略版本。章节推进只保留一个持久模式、一个精确许可和一个类型化的一次性 hold。

### 3.1 不变量

1. `AdvanceMode` 只能为 `auto` 或 `review`；未知值返回 `UnsupportedAdvanceModeError`。
2. 未知模式不得启动 Host，也不得改写 RunMeta。
3. `auto` 下许可必须为 `0`。
4. `review` 下许可只能为 `0` 或一个正整数章节号。
5. 同目标重复授权幂等，不同目标不得覆盖在途许可。
6. 许可仅约束“开始尚未完成的正向新章”；规划、评审、返工、打磨和提交恢复不受阻断。
7. 许可与章节号绑定，不与某次进程运行或某次 Worker 调用绑定。
8. 只有目标章已经进入 `CompletedChapters`、对应 `PendingCommit` 已清空、且存在该章 `commit` checkpoint 时，许可才算稳定消费。
9. 目标章已完成但缺 commit checkpoint 属于状态损坏：显式报错并暂停，不猜测修复。
10. 未完成许可必须等于 `Progress.NextChapter()`。`PendingRewrites` 不改变 `NextChapter()`，所以返工与在途正向许可可以机械共存。
11. `AdvanceHold` 只能使用 `boundary`、`rewrites_drained` 或 `chapter`，且必须携带非空原因；`chapter` 必须携带正数目标章节，其他条件禁止携带。
12. hold 与许可使用 compare-and-clear；状态被新动作替换时不得误清。
13. 目标章节 hold 在 `review` 模式下构成一次性区间授权；暂停后原有逐章验收政策保持不变。

## 4. Store API

RunMetaStore 提供窄而类型化的原子操作：

```go
SetAdvanceMode(mode domain.ChapterAdvanceMode) error
GrantAdvancePermit(chapter int) error
ClearAdvancePermit(chapter int) error
SetAdvanceHold(hold domain.AdvanceHold) error
ClearAdvanceHold(expected domain.AdvanceHold) error
```

- 切回 `auto` 时在同一写锁内清除章节许可，但不清除另一条用户干预产生的 hold；
- 授权只在 `review` 下合法；
- 清除操作只消费调用方刚读取的同一目标；
- RunMeta 初始化时缺省模式为 `auto`，并保留已落盘的模式、许可和 hold。

项目当前没有需要迁移的历史数据，因此实现不包含旧字段读取、双写或降级分支。

## 5. 纯函数语义

### 5.1 正向新章识别

```go
func StartsForwardChapter(
	inst *Instruction,
	progress *domain.Progress,
	pending *domain.PendingCommit,
) bool
```

只有以下条件同时成立才返回 true：

- Worker 是 `writer`；
- phase 为 `writing`；
- 没有 `PendingCommit`；
- 没有返工队列；
- 没有 `InProgressChapter`；
- 目标章等于 `NextChapter()`。

判断只读类型化字段，不解析 Task 或 Reason 文案。

### 5.2 一次性 hold

`ResolveAdvanceHold` 根据 hold 与 Progress 返回：

- `keep`：条件尚未满足；
- `consume`：完本态只需清理意图；
- `consume-and-stop`：清理意图并暂停。

`boundary` 在当前 Worker 边界触发；`rewrites_drained` 等返工队列排空后触发；`chapter` 在目标章进入完成列表、`PendingCommit` 清空且 commit checkpoint 存在后触发。未知条件和缺失事实直接报错。

## 6. ChapterAdvanceGate

Gate 是除预算外唯一的创作前进政策组件，职责只有两项：

1. 在循环边界解析和消费一次性 hold；
2. 在 writer 派发前检查逐章许可，并在边界对账许可是否稳定消费。

Engine 顺序为：

```text
提交待处理干预
→ Gate 边界检查
→ Route / 取 Arbiter 派单
→ precheck
→ Gate 派发许可检查
→ Worker
→ Budget 边界检查
→ Gate 边界检查
→ 下一轮
```

`auto && hold == nil` 时，边界检查读取 RunMeta 后立即返回，不读取 Progress、PendingCommit 或 checkpoint。

### 6.1 hold + dispatch

Arbiter 可以把“重写第 3 章，改完让我看”裁成：

```json
{
  "hold": {
    "after": "rewrites_drained",
    "reason": "重写完成后等待用户验收"
  },
  "dispatch": {
    "agent": "editor",
    "task": "复核第 3 章并按结果建立返工队列"
  }
}
```

这组动作必须先执行配对派单，让 Editor 建立返工事实，再由 Gate 判断队列是否排空。Engine 将“本次派单延后 Gate”与该条内存指令绑定，取走指令时一并清除；普通 Arbiter 派单不能绕过 Gate。

### 6.2 permit 与返工

完本 `reopen` 仅能发生在 `complete`，而 `/next` 仅能发生在 `writing`，两者机械互斥。写作期已经存在的 `PendingRewrites` 不改变最大已完成章节，因此许可仍与同一个 `NextChapter()` 对齐；返工 Worker 可运行，但不会消费正向许可。

## 7. 崩溃恢复

章节提交是多步 saga，许可不能用“下一次 run 可写一章”的布尔值表示。恢复时 Gate 依据三类事实对账：

| 事实窗口 | Gate 行为 |
|---|---|
| 目标章未完成、无 PendingCommit | 保留许可，允许开始/恢复该章 |
| PendingCommit 属于目标章 | 保留许可，让提交恢复完成 |
| 目标章完成、PendingCommit 清空、commit checkpoint 存在 | 消费许可 |
| 目标章完成但 checkpoint 缺失 | 报错并暂停 |
| 许可指向非 NextChapter 的未完成章 | 报错并暂停 |

因此进程在草稿、状态写入、进度标记或信号写入任一窗口崩溃，都不会把同一个许可错误用于下一章。

## 8. Arbiter

干预 schema 使用 `AdvanceHoldOp`：

```go
type AdvanceHoldOp struct {
	Cancel        bool                    `json:"cancel,omitempty"`
	After         domain.AdvanceHoldAfter `json:"after,omitempty"`
	TargetChapter int                     `json:"target_chapter,omitempty"`
	Reason        string                  `json:"reason,omitempty"`
}
```

规则：

- 显式“先停一下”使用 `boundary`；
- `auto` 下“修改已写章节，改完让我验收”使用 `rewrites_drained`；
- “写到第 N 章”使用 `chapter`，与“全书共 N 章”的篇幅调整严格区分；
- `review` 已经逐章停，不重复制造同义 hold；
- `review` 下的目标章节 hold 是用户显式签署的一次性批量授权；
- “继续”可以取消现有 hold，但不能签发章节许可；
- 切模式只能使用 `/review on|off`，放行只能使用 `/next`。

Engine 直接调用 RunMetaStore 应用结构化动作，不把它伪装成 LLM Tool。

## 9. 用户接口

### 9.1 `/review on|off`

- `/review on`：立即持久化逐章验收政策；若 Worker 正在运行，当前工作完成后在下一次正向新章前停下；
- `/review off`：切回自动推进并原子清除许可；不会隐式启动已经暂停的 Engine，事件会明确提示用户输入继续指令。

### 9.2 `/next`

仅在以下条件同时成立时可用：

- Engine 未运行；
- 非阶段共创；
- 模式为 `review`；
- 没有待处理 hold；
- 预算允许；
- phase 为 `writing`。

命令给 `NextChapter()` 签发精确许可并启动 Engine。通知会明确：该章提交后，必要的评审及弧/卷结构维护仍会完成，然后再次等待放行。

### 9.3 状态展示

`UISnapshot` 是 TUI 的唯一事实源，包含：

- `AdvanceMode`；
- `AdvancePermitChapter`；
- `HasAdvanceHold`；
- `AdvanceHoldReason`。

侧栏展示自动/逐章验收状态和已放行章节；等待时输入框提示“输入修改意见，或 `/next` 放行下一章”。通知 kind 为 `advance_gate`。

## 10. 验证

测试覆盖：

- RunMeta 模式、许可、hold 的原子状态转换与 compare-and-clear；
- 未知模式显式失败且不改写 RunMeta；
- 正向新章与返工/恢复的纯函数识别；
- hold 的 boundary、返工未排空、返工排空与完本语义；
- 无许可阻断、精确许可放行、错章许可报错；
- PendingCommit 期间许可保留，稳定 commit 后消费；
- 完成标记与 checkpoint 冲突时暂停；
- permit 与 PendingRewrites 交错不误报；
- Engine 端到端证明一个许可恰好只稳定一个新章节；
- Gate 已标记暂停但旧 Engine goroutine 尚在退出时，`/next` 明确拒绝重入，稍后重试按同章许可幂等恢复；
- hold-only、hold+dispatch 和退出竞态回归。

## 11. 明确不做

- 不让模型决定运行模式或签发许可；
- 不修改 Route 以适配用户确认策略；
- 不把返工、规划、评审和结构维护都变成逐步确认；
- 不增加通用 PolicyEngine、StopCondition 列表或策略 DSL；
- 不提供预授权多章或许可队列；
- 不保留旧暂停模型、兼容字段、迁移 DTO 或双写链路；
- 不为未知未来模式静默降级。

未来若出现新的、重复验证的自治边界需求，再基于证据扩展模式；当前低反悔成本就是未来兼容性。
