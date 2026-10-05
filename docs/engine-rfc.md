# Step 2 RFC:Engine 直接运行 Worker(七道必答题答案)

> 状态:定稿(2026-07-12)。基于对 host/observer/subagent/usage/cocreate 的代码侦察。
> 结论:全部七题有低风险答案,进入实施。关联:docs/engine-arbiter.md。

## 1. Worker 执行面：程序化调用 subagent.Runner

> 后记(2026-07-22)：agentcore 已把类型化执行与模型工具协议拆开。`Runner.Run` 是宿主入口；
> `Runner.AsTool()` 仅供需要让 LLM 自主派发 subagent 的宿主使用。AINovel 只依赖 Runner。

`Runner.Run(agent, task)` 每次启动一个完整 `agentcore.AgentLoop`。Engine 直接调用它，build.go 的
**全部装配原样生效**：角色模型+failover、prompt cache key(#seq 每次自增)、ThinkingLevel、
UsageRecorder/SessionLogger(OnMessage)、Writer ContextManagerFactory、RestorePack、StopGuardFactory、
StopAfterTools。类型化结果与错误链直接返回，不经过 JSON 编解码或工具结果嗅探。

**事件投影**:子代理进度中继读的是 **ctx 里的 ToolProgress 回调**(`agentcore.ReportToolProgress(ctx,...)`)。
Engine 以 `agentcore.WithToolProgress(ctx, relay)` 调 `Runner.Run`,中继照常工作;relay 把 ProgressPayload 合成
`EventToolExecUpdate` 喂给现有 `observer.handleToolUpdate`——observer 的 worker 侧处理(TOOL 行/流式正文/
thinking/retry/context)**复用率 ~95%**。DISPATCH 行改由 Engine 直接发起/收尾(新增 observer 两个入口)。
Coordinator 左栏叙述流消失,由 Engine 叙述事件替代。

**/model 与推理强度**:模型切换经 ModelSet swap(configs 持 failover wrapper,原机制);推理强度经
`runner.SetThinkingLevel`(applyThinking 保留,删 coordinator 分支)。

## 2. Engine 生命周期

单 goroutine 串行循环;`ctx` cancel = 暂停/中止(传播进 worker loop,checkpoint 保证无损);
Resume/Continue = 起新循环。单 Worker 串行由循环结构天然保证。预算/停靠点哨兵在每轮边界由
Engine 直接调用(替代事件订阅与 FlowBoundaryHook)。

## 3. 状态提交协议 → 串行使其近乎消失

Engine 每轮 spawn 前才 `LoadState+Route`,指令永远基于最新事实——Route 指令无 TOCTOU,无需 Expect 对账。
Expect 快照仅用于 **Arbiter 决策的 dispatch**(咨询与执行之间隔着 worker 运行):边界执行前比对
{Phase, QueueHead},不符 → 丢弃 + 以新事实重询。前置校验(原 Gate 职责)成为 Engine 普通代码:
phase=complete 不派发;writer 目标章未展开 → 改派 architect_long expand(确定性,无需教学文案)。
干预的控制态动作(hold/reopen/dispatch)进 Engine 队列边界提交;answer/rules 即时。

## 4. 错误分类学(确定性先行)

- retryable(网络/限流/stream-idle):subagent 内部 MaxRetries=7 已就近消化,不出循环
- worker 返回 error(escalate/hard_stop/工具硬错):同一指令 Engine 重试 1 次 → 仍败 → Arbiter
  `worker_failure` 咨询(retry/reroute/abort)→ abort 或 Arbiter 自身失败 → 暂停 + notify
- 参数错/未知 agent 等确定性错误:直接暂停 + notify(代码 bug,重试无意义)

## 5. 僵局协议

每轮记录指令键 `Agent+Task`。上一轮执行后 Route 仍产生同键，说明任务后置条件未满足，`repeat++`；指令改变则清零。Worker 内部 `plan/draft/edit` 等中间 checkpoint 不算 Engine 级推进。
repeat==3 → Arbiter `deadlock` 咨询;Arbiter 建议 retry **不清零**;repeat==5 → 硬熔断:暂停 + notify。
(Coordinator 时代"不设阈值"依赖其自主性;确定性 Engine 必须有限界。)

## 6. 崩溃语义 → 免费

不需要判断"上个 Worker 是否产生有效事实":工具层 checkpoint+digest 幂等,Route 从 store 重算,
重复派发安全。agentcore 的模型流重试不会跨越工具执行边界。恢复 = 直接进循环。PendingSteer 在循环启动前作为
干预走 Arbiter。

## 7. 原型验收

端到端集成测试(fake ChatModel):规划→补齐→写章→弧末评审/摘要→展开→完本 全链路;干预分诊落 store;
暂停/恢复;僵局熔断;usage 记录;observer 事件形状(DISPATCH/TOOL 行、流式 delta)。加上既有的
60k Route 规格、agentcore 契约、editor 流测试作为回归网。

## 完成期总结(设计决定)

完本总结改为**确定性生成**:store 已有全部事实(章节摘要/角色/伏笔台账/字数),Engine 直接渲染报告,
不再花一次 LLM 调用产出仪式性文本。原 coordinator 的 LLM 总结取消(engine-arbiter.md §三:总结非裁定)。
