# 文风层(Voice Layer)设计

> 状态:设计定稿 v2(2026-07-12 吸收外部评审:补覆盖语义、路径语义、拼装顺序、eval 入口、评测统计协议),**可实施**。
> 优先级:先于控制面演进(docs/engine-arbiter.md)——AI 味是活跃的用户痛点。

## 一、背景与问题定义

用户反馈生成内容"AI 味重"。排查后结论:**问题不是文风知识与流程耦合过深,而是迭代回路断在两处**:

1. **改一次要重编译**——文风语义资产(anti-ai-tone.md、writer.md 写作标准、styles/*.md)全部 `go:embed`,调一个措辞就要重新构建发版;
2. **没有文风专用的度量回路**——改完只能靠人读感觉,无客观前后对照,优化变成玄学。

## 二、现状盘点(文风相关资产共五层)

| 层 | 位置 | 现状 | 用户可调 |
|----|------|------|---------|
| 语义判据 | `assets/references/anti-ai-tone.md` | writer 规避 + editor 举证共用,结构/用词/描写/对话/节奏五类 | ❌ 内嵌 |
| 写作标准 | `assets/prompts/writer.md` §写作标准 | 与执行协议混在同一内嵌文件 | ❌ |
| 风格预设 | `assets/styles/*.md`(4 个) | cfg.Style 单点选择,追加到 writer prompt | ❌ 且不能新增 |
| 机械规则 | `internal/rules` | 疲劳词/禁用语/字数,commit 强制检查 | ✅ 已有三层覆盖(注意:其"项目级"绑定 **cwd**,见 3.4) |
| 运行时偏好 | Arbiter `rules` 动作 | 自然语言 → 结构化,跨重启生效 | ✅ |

另有两件关键基建:**stylestat**(全书级句式 tic 统计,喂回 writer 作"口头禅镜像",纯代码零幻觉)和 **eval 的 `OverridePrompt`**(prompt A/B 基建已存在)。

结论:机械层的可调性和度量原料已就绪,缺口集中在**语义层不可覆盖**与**度量回路未对准文风**。

## 三、设计

### 3.1 核心原则

**把"怎么写"(文风)从"怎么协作"(协议)里拆出来:前者数据化、可覆盖;后者保持编译内嵌。**

### 3.2 writer.md 拆分:占位符原位回填

writer.md 的写作标准节位于文件**中部**(执行协议之后、配角连续性之前),不能简单尾接。采用占位符方案:

- `writer.md`(协议,内嵌):保留执行协议 / 断点续跑 / 重写与打磨 / 章节契约 / 用户偏好机制说明 / **字数节全部(含写法建议)** / 配角连续性 / commit 参数;原写作标准节位置替换为**单一** `{{VOICE}}` 占位符
- `voice.md`(文风,可覆盖):写作标准全节(去 AI 味 / 句式多样性 / 前情不复述)

字数写法建议留在协议文件(2026-07-12 评审采纳):它与字数契约的执行强耦合,拆出去需要第二个占位符,把 Voice 变成多片段格式——为一段极少有人想覆盖的技巧文本不值得;用户对字数的偏好走 user_rules。文件名保持 `writer.md` 不改(eval `OverridePrompt` 以文件名为键,改名徒增接线)。

**拼装顺序必须与现状逐字节兼容**。现状为 `writer.md → simulationGuidance → style`(assets/load.go:84 + agents/build.go:247),故唯一组装函数为:

```go
// 生产、eval、测试唯一入口;{{VOICE}} 原位回填保证拆分无损
func BuildWriterPrompt(protocolTemplate, voice, simulationGuidance, style string) string
// = replace(protocolTemplate, "{{VOICE}}", voice) + simulationGuidance + style
```

先例教训:`WithSimulationGuidance` 注释记录过"baseline 带包装、variant 不带 → A/B 不等价"的坑;组装路径分叉是同类事故的温床,故收敛为单函数。

### 3.3 覆盖模型:逐资产语义(不含糊)

| 资产 | 覆盖语义 | 理由 |
|------|---------|------|
| `voice.md` | **追加**:内置保留,全局/本书作为标记段追加 | 整文件替换会让用户永久停留在旧版内置;常见诉求是微调而非重写 |
| `anti-ai-tone.md` | **追加**(同上) | 常见诉求是补判据;想推翻内置判据的用户属极少数,不为其设计 |
| `styles/<name>.md` | **同名整文件替换**;新文件名即新增风格 | 风格是整体声音,两个风格合并无意义 |
| `genres/<name>/style-references.md` | 同名整文件替换;自定义 style 无 reference 时**允许缺省,不回退 default**(错误参照比没有更糟) | 同上 |
| user_rules | 运行时最高优先级(现状不动) | — |

追加语义的组装带显式边界标记:

```
## 项目默认文风
...
## 用户全局文风覆盖(以下要求优先于项目默认)
...
## 本书文风覆盖(以下要求优先于以上全部)
...
```

**诚实边界**:追加语义下"后者胜"是给 LLM 的优先级指示,不是机械保证——文风是建议性内容,可接受;需要机械保证的约束走 rules 层(那里是真覆盖)。此边界写入用户文档。

`arc-templates.md` 属规划平面(塑造故事结构而非声音),**不入 v1 白名单**,记录待议。

### 3.4 路径语义:本书级绑定 outputDir,不绑 cwd

```
本书级   <outputDir>/style/     >   全局   ~/.ainovel/style/   >   内置默认(embed 兜底)
```

- 绑定 outputDir 使 Voice **随书走**:换目录恢复同一本书加载同一份文风;Docker/headless/TUI 路径解析一致;多书共享 cwd 时互不串扰
- `assets.Load` 签名显式接收解析根(outputDir),**内部不读 cwd**
- 注意与 rules 层的差异:rules 的 `./.ainovel/rules` 绑定 cwd(internal/rules/loader.go 既有约定,本设计不动它);用户文档明确两者语义不同——rules 是"项目级",voice 是"本书级"

用户目录完整结构:

```
<outputDir>/style/            (~/.ainovel/style/ 同构)
  voice.md                    追加段
  anti-ai-tone.md             追加段
  styles/
    xianxia.md                新增或同名替换
  genres/
    xianxia/
      style-references.md     可选
```

style 名即文件名,校验 `[a-z0-9-]+`,拒绝路径字符。

### 3.5 为什么开放给用户是安全的

协议不变量全部住在**事实层**:draft 先于 check、commit 强制机械规则检查、字数越界拦截、checkpoint 幂等——不住提示词里。用户把 voice.md 改得再离谱,守卫与工具前置条件照常生效,最坏结果是文笔难看,状态机坏不了。

### 3.6 生效时机与 eval 入口

- v1 启动时解析,**重启生效**(断点恢复精确到步骤,重启成本近乎零;热重载不做)
- eval 增加 **voice 独立 variant 入口**(如 `Bundle.OverrideVoice(raw)`),内部走 `BuildWriterPrompt` 同一路径——禁止通过覆盖完整 writer.md 做文风 A/B(会连带协议,且 baseline/variant 协议可能不等)

## 四、度量回路:文风评测集

```
改 voice/anti-ai-tone
  → 文风评测集(固定用例,eval voice-variant A/B)      ← 唯一新增
  → stylestat 指标对比(确定性硬指标)
  + LLM judge 按 anti-ai-tone 判据逐项举证打分(初期仅报告,不作 hard gate)
```

统计协议(固定输入只保证**可比较**,不保证可复现):

- baseline/variant 锁定同一模型与推理参数
- 每用例重复 N≥3 次,报告均值、方差与原始样本
- judge 盲评(不暴露 baseline/variant 身份)
- 用例覆盖题材 × 章型(开篇/日常推进/高潮/收束)

## 五、明确不做(防过度设计)

- 不开放协议提示词给最终用户(`OverridePrompt` 保留为 eval 内部能力)
- 不做运行中热重载
- 不把 stylestat 正则模式开放为用户配置(机械层扩展入口已有:rules 的 fatigue_words/forbidden_phrases)
- 不做风格市场/分享机制(拷贝 style 目录即天然可分享)
- arc-templates 不入 v1 白名单

## 六、实施步骤与验收

1. writer.md 拆分(`{{VOICE}}` 占位)+ `BuildWriterPrompt` 唯一组装函数
2. 三层解析器:`assets.Load(outputDir, style)` + 逐资产语义(3.3 表)+ styles 枚举合并;单测覆盖优先级/缺省兜底/追加边界标记
3. eval `OverrideVoice` 入口
4. 用户文档:目录结构、逐资产语义、rules 与 voice 的路径语义差异、示例
5. 文风评测集(可后置为独立任务)

**验收标准**:① 无任何覆盖文件时,`BuildWriterPrompt` 产出与拆分前**逐字节一致**;② 三层优先级与追加/替换语义有表驱动单测;③ 新增 `styles/xianxia.md` 后 `style: xianxia` 即放即用;④ eval voice A/B 与生产同组装路径(有测试证明);⑤ 全量测试与 sim 回归绿。

## 七、与控制面演进的关系

完全正交(内容平面 vs 控制平面),无实施依赖。约定顺序:**文风层 → 文风评测集 → Engine/Arbiter(按其文档 §八 决议推进)**。
