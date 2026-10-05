# 观测手册

跑长篇小说时，怎么知道各项机制是不是真的在工作？

本文档不是把 diag 规则照抄一遍，而是面向**实际运行**：你跑到第 N 章了，应该打开哪个文件、看哪个字段、判断健康还是异常。

---

## 1. 通用排查流程

```
1. /diag                       # 自动诊断，看 Findings 区
2. cd output/{novel}/meta/     # 直接 cat 关键工件
3. tail decisions.jsonl                # 看最近 Arbiter 裁定
4. ls -lt sessions/agents/             # 定位最近 Worker 会话后再 tail
```

`/diag` 覆盖不到的事实（包括本文档列出的"待补诊断"项），需要 step 2-4 手工查。

### 报 issue：脱敏诊断导出

每次 `/diag` 都会额外写出 `output/{novel}/meta/diag-export.md`——一份**已脱敏**的诊断（小说正文 / prompt / 思考已移除，仅保留行为骨架：工具名、错误串、重复次数、phase/flow、卡住的 step、日志错误分类）。遇到死循环 / 中断类问题，把这个文件贴到 GitHub issue 即可，维护者据此定位，无需用户的 `output/` 数据。

---

## 2. 关键工件速查表

按"出问题时最常见排查路径"排序：

| 工件 | 路径 | 看什么 | 健康 | 不健康 |
|---|---|---|---|---|
| 进度 | `meta/progress.json` | `phase` / `flow` / `completed_chapters` | phase 单调前进，flow 在合法集合内 | phase 倒退 / flow 卡在某状态 |
| 指南针 | `meta/compass.json` | `last_updated` 与最新章节差距 | gap < 15 章 | gap > 15 章（CompassDrift 命中） |
| 配角视图 | `meta/chapter_records/*.json` | `facts.characters` / `facts.cast_intros` | 有名字的次要角色稳定复用 | 同一角色反复换名 |
| 伏笔台账 | `meta/foreshadow.json` | `status="planted"` 的最长停滞章数 | < 章数/3 | > 章数/3（StaleForeshadow 命中） |
| 大纲 | `meta/layered_outline.json` | 当前卷剩余未写章数 | 提前 1-2 章已展开 | 写到当前章但下一章无 outline（OutlineExhausted） |
| 角色档案 | `meta/characters.json` | 是否能在最近 N 章摘要里找到 core/important 角色 | 都能找到 | 缺席（GhostCharacter 命中） |
| 检查点 | `meta/checkpoints.jsonl` | 最近一行的 `step` 是否对应 progress | 一致 | 不一致（崩溃恢复未自愈） |
| 裁定审计 | `meta/decisions.jsonl` | 最近若干条裁定的 facts/decision | 分诊准确、动作合理 | 同类干预反复裁定失败 |

---

## 3. 指南针（compass）观测

**修复时间**：2026-05-08（commit `fix: update_compass 工具自动填 last_updated`）

### 看什么

```bash
cat output/{novel}/meta/compass.json
```

字段语义：
- `ending_direction`：终局方向（应该和 `premise.md` "终局方向"段一致）
- `open_threads`：活跃长线（每卷边界由 architect 增删）
- `estimated_scale`：预估规模（如"4-6 卷"，每卷边界更新）
- `last_updated`：**工具自动填**为更新时的最大已完成章号（不再依赖 LLM 自填）

### 健康度判断

| 信号 | 判断 |
|---|---|
| `last_updated` 在 `[latest-15, latest]` 范围 | 健康 |
| `last_updated` 滞后 latest 超过 15 章 | architect 没在弧/卷边界更新——查 architect-long.md prompt |
| `last_updated == 0` | **本次修复前的脏数据**，下次 update_compass 会自愈 |
| `ending_direction` 和 premise.md "终局方向"段对不上 | architect 偷偷改了用户意图——记录下来，决定要不要冻结字段（设计议题，见 todo.md） |

### 怎么验证修复有效

跑长篇前后对比：
- **修复前**：跑 30+ 章后 `compass.last_updated` 大概率是 `0` 或某个早期章号
- **修复后**：每次 architect 调 `update_compass`，`last_updated` 都被工具层覆盖为当前 latest

---

## 4. 配角视图观测

配角视图由已完成章节的接纳记录实时投影，不再维护独立的 `cast_ledger.json`。`facts.characters` 决定角色在哪些章节出现，`facts.cast_intros` 提供最早的非空简介；`characters.json` 中的核心角色与别名会自动排除。

跑 5 章后，查看最近的 Writer 会话：

```bash
tail -50 output/{novel}/meta/sessions/agents/writer-*.jsonl
```

如果第 1 章引入“老周”、第 5 章再次使用，后一次 `novel_context` 返回的 `episodic_memory.recent_cast` 应包含老周及其首次简介。看到了但正文仍换名或定位漂移，属于 Writer 没有消费上下文；视图缺失则检查对应 `meta/chapter_records/*.json` 的 `facts.characters` 与 `facts.cast_intros`。

---

## 5. Writer 是否在按预期工作

跑长篇时最关心的是 **Writer 真的在按 prompt 行事吗**。最直接的观测是 session log：

```bash
ls output/{novel}/meta/sessions/agents/    # 每个子代理一份 jsonl
tail -50 output/{novel}/meta/sessions/agents/writer-*.jsonl
```

看几个特定行为：

| 期望行为 | 在 jsonl 中体现 |
|---|---|
| Writer 看了 recent_cast | novel_context 工具返回值里 `episodic_memory.recent_cast` 字段非空 |
| Writer 在 commit_chapter 填了 cast_intros | tool_call 参数 `cast_intros` 数组非空（仅在引入新角色的章节） |
| Writer 用了相关章节推荐 | `read_chapter` 调用次数 > 1（默认 1 次，超过说明回查了） |
| Writer 没违反工具顺序 | tool_call 序列严格 `novel_context → read_chapter → plan_chapter → draft_chapter → check_consistency → commit_chapter` |

如果 jsonl 里看到 Writer 多次空调 novel_context、或 commit_chapter 之后又调其他工具——是 prompt 没收住。

---

## 6. 长跑场景红线

跑 100+ 章长篇时，下面任何一条命中就该停下来排查：

- [ ] CompassDrift 命中且持续 2 个弧未消除
- [ ] 同一角色出现疑似多名（"老李" / "李掌柜" 共存）
- [ ] Writer 写新章时不读 recent_cast 中已有的旧角色（重复发明）
- [ ] Worker session 中出现连续 ≥ 5 次空调 novel_context
- [ ] 任意章节 commit 后 `meta/checkpoints.jsonl` 没有对应 `commit_chapter` step

---

## 7. 文档维护规范

**新增事实层工件时（新建一个 `meta/*.json` / `meta/*.jsonl`），同步：**

1. 在本文档 §2 加一行速查
2. 如果工件需要专项观测（不是简单的"存在/不存在"判断），加 §X 专题段
3. 如果想要自动诊断，在 `internal/diag/snapshot.go::Load` 中加载，并在 `internal/diag/rules_*.go` 加规则

**不要：**
- 不要把 `internal/diag/` 里所有规则照抄到本文档（那是规则参考，不是观测手册）
- 不要为每个机制都写诊断规则——阈值靠拍脑袋会错，先观察再补
