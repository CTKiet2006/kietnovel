# kietnovel

> **中文** · [English](README.en.md) · [Tiếng Việt](README.md)

> Originally inspired by and initially based on AINovel-CLI by voocel. KietNovel is now independently developed — an AI engine for writing full-length novels, with a trilingual interface (Vietnamese / English / Chinese).

半自动 AI 长篇小说创作工具：Engine 负责把一整本书从头跑完，模型只在真正需要判断的地方被调用 —— Engine 按决策表调度 3 个各自独立的 Agent（Architect / Writer / Editor），语义仲裁者 Arbiter 只在必要时才被唤醒。从一个创意念头，到一部完整小说。

## 越南语本地化与多语言特性

- 🌐 **多语言 TUI 界面（vi / en / zh）**：从首次安装的 Setup Wizard、欢迎界面、状态栏、实时 Activity 流，到 Provider/Model 管理表（`/config`、`/model`），再到错误提示与快捷键 —— 全部随所选语言切换，用词地道自然。运行途中用 `/language` 即可换语言。
- ✍️ **可选的创作语言**：通过 `"language": "vi"` / `"en"` / `"zh"` 用越南语（默认）、英语或简体中文写作。prompt 集合保持原版不变（已被反复验证有效），只更换文风层以及强制输出语言的指令。
- 🔑 **直接调用云端 API**：支持 OpenRouter、DeepSeek、Gemini、Anthropic、OpenAI。一个 API key 就能开写，无需 Docker。
- ✍️ **拒绝 AI 套话的文风**：内置越南语小说写作规范（`assets/voice.md`）和一套反套话素材（`assets/references/anti-ai-tone.md`），并针对越南语列出具体禁用短语 —— 「ở một mức độ nào đó」「như thể」「bất giác」「không khỏi」…… 让文字生动、干脆、有厚度。
- 🚀 **全面同步最新 upstream**：多 Agent 架构、2 层滚动规划（Rolling planning）、4 级上下文压缩、step 级恢复点，以及全部 14 个 slash commands。

## 🛡️ 已核查的安全性

| 项目 | 结果 |
|---|---|
| Dependency chain | 仅 10 个直接依赖，全部来自公开源。`go mod verify` + `GOSUMDB=sum.golang.org` → 每个哈希都与 Google 官方 checksum 数据库一致 |
| `govulncheck` | **No vulnerabilities found**（含标准库） |
| 全部网络请求 | 只发往你自己配置的 `provider`。除此之外仅有：版本检查（只读）和从 OpenRouter 获取模型价格 |
| API key | 只存在于发往 provider 的 header 里。不写日志、不落盘、不发往任何别处 |
| Telemetry / analytics | 没有。LLM 层零 stdlib 之外依赖 |
| Obfuscation | 没有 `unsafe`、没有 `go:linkname`、没有零宽字符、没有隐藏的 `func init()`、没有 base64 块 |
| 命令执行 | 只执行你在 `notify.command` 中**自行配置**的命令，以及自动更新自身 binary（带 SHA256 校验） |
| 读取的环境变量 | 只有一个：`NOVEL_DIR`。不读取任何其他隐藏环境变量 |

有一点需要知道：这是一个 **agent framework** —— 模型被授予读写文件、执行 shell 的 tool。如果你输入的正文里含 prompt injection，那就是一条执行路径。这是所有 agent 工具的固有设计，不是 backdoor。

## 📑 目录

- [系统要求](#1-系统要求)
- [安装与快速开始](#2-安装与快速开始)
- [语言选项 (vi / en / zh)](#3-语言选项-vi--en--zh)
- [AI Provider 配置 (LLM)](#4-ai-provider-配置-llm)
- [使用指南与 TUI 命令表](#5-使用指南与-tui-命令表)
- [管理多部独立小说](#6-管理多部独立小说)
- [高级功能](#7-高级功能)
- [技术架构与工作原理](#8-技术架构与工作原理)
- [输出目录结构](#9-输出目录结构)
- [自定义文风与个人规则](#10-自定义文风与个人规则)
- [Tech Stack & License](#11-tech-stack--license)

## 1. 系统要求

- **Go ≥ 1.25**，用于从源码构建。无需 Docker。
- 来自某个 LLM provider 的 API key：OpenRouter、DeepSeek、Gemini、Anthropic 或 OpenAI。

> **为什么不建议跑本地模型？** 这个工具需要非常大的 context（默认 200k token，85% 时开始压缩），并且**每一章要调用 4-6 次 LLM** —— Architect 做规划、Writer 写初稿、Editor 做 7 维评估，然后 commit。本地模型撑不住：14B 模型要达到 64k context 需要约 40GB 内存，在 CPU 上只有 2-5 tok/s，写一章要几个小时。请用云端 API。

## 2. 安装与快速开始

无需 Docker。三步：

```powershell
# 1. Cài (một lệnh, không cần cài Go)
irm https://raw.githubusercontent.com/CTKiet2006/kietnovel/v1.2.4/scripts/install-windows.ps1 | iex

# 2. Mở terminal mới, rồi chạy
kietnovel

# 3. Lần đầu: Setup Wizard hỏi Provider, API key, Model và ngôn ngữ (vi / en / zh)
```

安装包约 5.7 MB。脚本会自动下载与你的机器架构（x86_64 / arm64）匹配的预编译版本，
校验 SHA-256，解压到 `%LOCALAPPDATA%\kietnovel\bin`，并把该目录加入用户 PATH。

**更新：** 再跑一次上面的命令即可。请先退出 `kietnovel` —— Windows 不允许覆盖正在运行的文件。

<details>
<summary>其他方式</summary>

**已装 Go ≥ 1.25** —— 不用 binary，直接从源码构建：

```powershell
go install github.com/CTKiet2006/kietnovel/cmd/kietnovel@latest
```

明显更慢：要下载约 26 MB 依赖再编译（16 线程机器上 25 秒，配置弱一些的机器要几分钟）。

**从源码安装**（想改代码时）：

```powershell
git clone https://github.com/CTKiet2006/kietnovel.git
cd kietnovel
powershell -ExecutionPolicy Bypass -File scripts\install.ps1
```

</details>

### 数据位置

| 类型 | 路径 |
|---|---|
| 配置 | `~/.kietnovel/config.json`（由 Wizard 自动创建） |
| 仅当前目录生效的配置 | `./.kietnovel/config.json`（优先级更高） |
| 个人写作规则 | `~/.kietnovel/rules/*.md` 或 `./.kietnovel/rules/*.md` |
| 正在写的书 | `./output/novel/` —— 或设置 `NOVEL_DIR`（见第 6 节） |

直接运行 binary 时**没有** `config/` 目录。如果 `--headless` 提示 *"headless không hỗ trợ thiết lập lần đầu"*（不支持首次设置），先打开一次 TUI 完成配置。

## 3. 语言选项 (vi / en / zh)

在配置文件 `~/.kietnovel/config.json` 里指定 `"language"` 字段即可。这一个选择同时决定
**两件事**：TUI 界面语言和创作语言。

- `"language": "vi"`（默认）：界面全越南语，包括术语、文风规范、世界观提示。
- `"language": "en"`：英文界面，小说内容用自然的英语生成，达到母语小说水准。
- `"language": "zh"`：中文界面，小说内容用简体中文生成（如果你写中文作品，或之后想发到同样的平台，很合适）。

### 工作原理

prompt 集合保持作者的原版，用中文写成 —— 其质量已经过验证，因此原样保留。创作语言在两处控制：

| 控制点 | `vi`（默认） | `en` | `zh` |
|---|---|---|---|
| 文风层（`voice`） | `assets/voice.md` - 越南语小说写作规范 | `assets/voice_en.md` - 英语母语小说文风规范 | `assets/voice_zh.md` - 中文原版 |
| 输出指令 | 注入 Architect/Writer/Editor：*全部产出必须用自然、地道的越南语写作* | 同样注入，改用英语 | 不注入 —— protocol 本身就是中文 |

本质就是：一句强制语言指令 + 一份对应的文风规范，而不是维护两份容易走偏的 prompt。

### 如何切换语言

- 安装时：Setup Wizard 一上来就问你选哪种语言。
- 运行中：输入 `/language` 查看当前语言，或输入 `/language en`
  （同样接受 `vi`、`zh`）来切换。选项会写进配置，之后启动一直沿用。

界面立即切换。但**创作语言**只在启动时加载一次，所以要到下次打开才完全生效 ——
切换之后应用会马上提醒你这一点。

## 4. AI Provider 配置 (LLM)

配置文件：`~/.kietnovel/config.json`（Setup Wizard 会替你建好）。在 wizard 里选好 provider
就够了 —— 本节只在你想定动手改时用得上。

| Provider | 建议的 `model` | 备注 |
|---|---|---|
| DeepSeek | `deepseek-chat` | 最便宜，建议先试它 |
| OpenRouter | `anthropic/claude-3.5-sonnet` | 一个 key 通吃多家 |
| Gemini | `gemini-2.5-pro` | context 长 |
| Anthropic | `claude-3.5-sonnet` | |
| OpenAI | `gpt-4o` | |

通用结构：

```json
{
  "language": "vi",
  "provider": "deepseek",
  "model": "deepseek-chat",
  "providers": {
    "deepseek": { "api_key": "YOUR_API_KEY" }
  },
  "context_window": 64000,
  "style": "default"
}
```

留空时 `context_window` 会从模型自动探测。`reasoning_effort`：
`off / low / medium / high / xhigh / max`。想加更多 provider 并当作 fallback：

```json
{
  "roles": {
    "writer": {
      "provider": "openrouter",
      "model": "anthropic/claude-3.5-sonnet",
      "fallbacks": ["deepseek", "gemini"]
    }
  }
}
```

## 5. 使用指南与 TUI 命令表

输入 `kietnovel` 进入 TUI。在欢迎界面：

- `Tab` —— 在**快速开始**（输入一句话，AI 自己列纲再写）和**共创**
  （逐步对话，先敲定世界观/人物再动笔）之间切换。
- `Enter` —— 开始。`/` —— 查命令。`Ctrl+C` 连按两次 —— 保存状态并退出。

### 控制命令列表（Slash Commands）

在 TUI 中输入 `/` 打开命令选择面板：

| 命令 | 说明 |
|------|-------|
| `/help` | 打开帮助面板，查看命令列表和快捷键 |
| `/model` | 切换模型或推理（reasoning/thinking）强度 |
| `/config` | 管理 Provider、Model ID、API Key、Base URL、Context Window |
| `/diag` | 查看关于健康度、进度和小说质量的完整诊断报告 |
| `/review [on\|off]` | 开关逐章验收模式（每写完一章就停下，等你确认） |
| `/next` | 批准继续写下一章（处于验收模式时） |
| `/start <文件>` | 读取外部大纲/创意文件，用来开一部新书 |
| `/import <文件>` | 导入外部小说，让 AI 分析并续写 |
| `/reopen <目录>` | 作品完成后续写新的一卷 |
| `/cocreate` | 暂停并进入下一阶段的共创模式 |
| `/simulate` | 分析 `./simulate` 里的样本文风并加以模拟 |
| `/importsim <file>` | 从 json 文件导入文风模拟档案 |
| `/sync` | 把你对章节文件的手工修改同步回系统 |
| `/export` | 把作品导出为完整文本文件（.txt 或 .epub） |
| `/language [vi\|en\|zh]` | 查看或切换界面语言；选项会保存到配置里 |

### 实时干预（Steer）

AI 正在写的时候，你可以随时直接把修改意见敲进下方的输入框 —— 不用暂停，也不用重启：

```text
❯ Cho nhân vật phụ A hy sinh ở cuối chương này để tạo bước ngoặt cảm xúc lớn
```

按下 Enter 后，Arbiter 会自动评估影响范围，并调度 Writer/Editor 更新剧情线。

### 无界面运行模式（Headless）

适合在 VPS、服务器或 CI 上无人值守地跑：

```bash
# Bắt đầu truyện mới
kietnovel --headless --prompt "Tiểu thuyết huyền nghi đô thị phá án"
# Viết tiếp truyện đang dở trong thư mục hiện tại
kietnovel --headless
```

## 6. 管理多部独立小说

默认输出到 `./output/novel/`。要同时写多部书而互不干扰，运行前设置环境变量
`NOVEL_DIR`：

```powershell
# Bộ truyện 1
$env:NOVEL_DIR = ".\novels\tien-hiep-ky"
kietnovel
# Bộ truyện 2
$env:NOVEL_DIR = ".\novels\do-thi-di-nang"
kietnovel
```

每部书都有自己独立的文风（`style/`）、checkpoint 和进度，不会互相串味。

每部书的输出目录结构：

```text
novels/<tên-truyện>/output/novel/
├── chapters/            # Các chương hoàn chỉnh đã duyệt (.md)
├── drafts/              # Bản nháp và dàn ý chi tiết từng chương
├── reviews/             # Báo cáo đánh giá của Editor
├── summaries/           # Tóm tắt từng Cung và Tập
├── premise.md           # Ý tưởng & tiền đề cốt truyện
├── characters.md        # Hồ sơ thiết lập nhân vật
├── world_rules.md       # Thiết lập quy tắc thế giới
└── meta/                # Checkpoint, tiến độ, nhật ký token
```

> 每个 `NOVEL_DIR` 值就是一部独立的书。不设 `NOVEL_DIR` 时，就按工作目录使用
> `./output/novel`。

## 7. 高级功能

### 小说诊断（/diag）

在 TUI 里输入 `/diag`，系统会从 4 个维度自动检查整本书：

- **进度**：发现重写死循环、卡住的干预指令、章号跳变。
- **质量**：跟踪评分、剧情遵循度、异常的章节长度。
- **规划**：检查被遗忘的伏笔/未收的线、枯竭的大纲、缺失的摘要。
- **上下文**：角色失忆、时间线断裂。

### 文风模拟（/simulate）

把样本文本（你喜欢的作者的小说）放进 `simulate/` 目录，然后输入 `/simulate`。AI 会分析节奏、用词和句式结构，生成一份模拟档案并套用到你的小说上。

### 同步手动修改（/sync）

如果你直接打开 `chapters/05.md` 改了句子，在 TUI 里输入 `/sync`。系统会借助 SHA-256 重新扫描，并自动刷新摘要、角色状态和伏笔记录，同时不打断写作流程。

### 导入外部小说（/import）

输入 `/import ./truyen_cu.txt`。系统会：

1. 识别每一章的边界。
2. 抽取人物、世界观和事件设定。
3. 汇总出大纲，随时可以无缝续写新章节。

### 导出完整小说（/export TXT/EPUB）

在 TUI 里输入 `/export`，或直接带参数：

```text
/export ./xuat_ban/truyen_full.txt
/export ./xuat_ban/truyen_ebook.epub
/export from=1 to=50 ./tap_1.epub
```

## 8. 技术架构与工作原理

### 多 Agent 架构

```text
┌─────────────────────────────────────────────────┐
│              Host / Engine (Xác định)            │
│  Đọc Store → Route → Chạy Worker → Lặp chu trình │
│  Khởi động / Can thiệp / Xử lý kẹt → Arbiter     │
└────┬──────────┬──────────┬─────────────┬────────┘
     │          │          │             │
 ┌───▼────┐ ┌───▼───┐ ┌────▼────┐   ┌────▼────┐
 │Architect│ │Writer │ │ Editor  │   │ Arbiter │
 │(LLM Vòng)│ │(LLM Vòng)│ │(LLM Vòng)│   │(LLM Hàm)│
 └───┬────┘ └───┬───┘ └────┬────┘   └─────────┘
     └──────────┼──────────┘
                │ Gọi Tools (IO + Checkpoint)
┌───────────────▼─────────────────────────────────┐
│                   Store                         │
│  Tiến độ / Checkpoints / Dàn ý / Bản thảo / ... │
└─────────────────────────────────────────────────┘
```

| 角色 | 职责 | 使用的 tool |
|---------|-------------|-----------------|
| **Arbiter** | 语义支点：选择 planner、分派干预、化解卡死 | 无（单次 LLM 调用，返回结构性决策） |
| **Architect** | 架构师：生成书名、摘要、前提、大纲、人物档案、世界规则 | `novel_context`、`save_book`、`save_foundation` |
| **Writer** | 执笔者：自主规划章节、写初稿、自检并交付 | `novel_context`、`read_chapter`、`plan_chapter`、`draft_chapter`、`check_consistency`、`commit_chapter` |
| **Editor** | 编辑：读取初稿，做 7 维质量评估，撰写 arc/volume 摘要 | `novel_context`、`read_chapter`、`save_review`、`save_arc_summary`、`save_volume_summary` |

### 双层滚动规划（Rolling Planning）

不给上百章一次性定死大纲 —— 那只会让故事越写越空 —— kietnovel 先搭出 2 卷的骨架，
外加第一个 arc 的详细大纲。当一个 arc 快要写完时，由 Editor 先做总结，然后 Architect 才
根据剧情实际走向展开下一个 arc。

### 4 级上下文管理与压缩

要写出 500+ 章的长篇而不会撑爆 context window：

1. **ToolResultMicrocompact** —— 清理旧 tool 调用留下的中间结果。
2. **LightTrim** —— 砍掉已经不需要的长段文本。
3. **StoreSummaryCompact** —— 用 Store 里已存的摘要替换旧消息（不额外消耗 token）。
4. **FullSummary** —— 用专门的 prompt 压缩叙事上下文。

### Editor 的 7 维质量评估

每写完一章，Editor 都会按 7 条标准给出严格判定（每条评语都必须引用原句作为依据）：

1. 设定的一致性（Consistency）
2. 人物行为与性格（Character Behavior）
3. 剧情节奏（Pacing）
4. 行文连贯与场景切换（Narrative Flow）
5. 伏笔的埋设（Foreshadowing）
6. 章末留住读者的钩子（Hooks）
7. 文学美感（Aesthetic Quality）：描写细节、艺术手法、对白语言的区分度、遣词质量、情绪感染力。

### Step 级断点恢复

每个 tool 执行成功后，系统会立刻写入一个 checkpoint（`meta/checkpoints.jsonl`）。如果遇到断电、关机、掉网或按下 Ctrl+C：

- 重新打开时，系统会读取 `progress.json` 和最近的 checkpoint。
- 自动从中断的那一步精确续上（例如："第 7 章初稿已完成，继续 check_consistency"）。

## 9. 输出目录结构

全部数据都存放在 `output` 目录下：

```text
output/novel/
├── book.md             # Tên sách và tóm tắt giới thiệu
├── chapters/           # Bản thảo hoàn chỉnh từng chương (.md)
│   ├── 01.md
│   └── ...
├── summaries/          # Tóm tắt từng chương, cung, tập (JSON)
├── drafts/             # Bản nháp từng chương
├── reviews/            # Báo cáo đánh giá chi tiết của Editor
├── timeline.jsonl      # Nhật ký dòng thời gian
├── premise.md          # Tiền đề cốt truyện
├── outline.json        # Dàn ý chi tiết các chương
├── layered_outline.json# Dàn ý phân tầng nhiều tập
├── characters.json     # Hồ sơ nhân vật
├── world_rules.json    # Quy tắc thế giới
└── meta/
    ├── book.json       # Metadata tác phẩm
    ├── compass.json    # La bàn định hướng dài hạn
    ├── progress.json   # Trạng thái tiến độ hiện tại
    ├── foreshadow.json # Sổ tay quản lý phục bút
    └── checkpoints.jsonl # Checkpoint khôi phục từng bước
```

## 10. 自定义文风与个人规则

**添加你自己的规则（不需要改代码）**

在 `~/.kietnovel/rules/` 或 `./.kietnovel/rules/` 目录下新建任意 `.md` 文件，用自然语言
写就行：

```text
"Nhân vật chính quyết đoán, không thánh mẫu"
"Tăng cường miêu tả cảm giác cơ thể và khung cảnh xung quanh"
"Mỗi chương khoảng 3000 từ"
"Không dùng các từ sáo rỗng như: 'ở một mức độ nào đó', 'như thể', 'bất giác'"
```

系统会自动把这些要求汇总进该作品的审查规则集。

## 11. Tech Stack & License

- **语言**：Go（性能高，对 concurrency 和 I/O 控制严格）
- **Agent Core**：[agentcore](https://github.com/voocel/agentcore)（tool-calling + streaming）
- **LLM Interface**：[litellm](https://github.com/voocel/litellm)
- **TUI 框架**：Bubble Tea 与 Lip Gloss
- **prompt 组织方式**：Markdown 文件用 `//go:embed` 嵌入 binary，启动时按语言加载 —— 选择 prompt 集合不需要联网
- **依赖**：10 个直接依赖，全部来自公开源。`govulncheck` 干净，LLM 层没有 stdlib 之外的依赖

### License

Apache License 2.0 —— 见 [LICENSE](LICENSE)。kietnovel 的新增贡献：
Copyright 2026 KietNovel contributors.

---

## Acknowledgments

*Built on the shoulders of giants:*

Originally inspired by and initially based on **AINovel-CLI by voocel**. KietNovel
is now independently developed — new architecture, new features, ongoing
development.

- **[ainovel-cli](https://github.com/voocel/ainovel-cli)** — where it all started. The
  multi-agent engine, the rolling plan, the checkpointing, the TUI commands and the
  prompt set all came from here, and the prompts are still the originals, untouched
  and in the author's own words.
- **[agentcore](https://github.com/voocel/agentcore)** — the tool-calling and
  streaming runtime the agent layer is built on.
- **[litellm](https://github.com/voocel/litellm)** — multi-provider LLM interface.
- **[Bubble Tea](https://github.com/charmbracelet/bubbletea)**,
  **[Lip Gloss](https://github.com/charmbracelet/lipgloss)**,
  **[Bubbles](https://github.com/charmbracelet/bubbles)** — the TUI toolkit.
