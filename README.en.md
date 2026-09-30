# kietnovel

> **English** · [Tiếng Việt](README.md) · [中文](README.zh.md)

> Originally inspired by and initially based on AINovel-CLI by voocel. KietNovel is now independently developed — an AI engine for writing full-length novels, with a trilingual interface (Vietnamese / English / Chinese).

A semi-automatic AI novel-writing tool: the Engine runs a whole book end to end, and the model is only called where judgement is actually required — the Engine orchestrates 3 autonomous agents, Architect / Writer / Editor, driven by a decision table, while the semantic Arbiter only wakes up when needed. From a single spark of an idea to a finished novel.

## Highlights Of The Vietnamese Localization & Multilingual Support

- 🌐 **Multilingual TUI interface (vi / en / zh)**: From the initial Setup Wizard, the welcome screen, the status bar, the live Activity stream and the Provider/Model management table (`/config`, `/model`) through error messages and keybindings — everything follows the selected language, idiomatically and naturally. Switch mid-session with `/language`.
- ✍️ **Optional writing language**: Write in Vietnamese (default), English or Simplified Chinese via `"language": "vi"` / `"en"` / `"zh"`. The prompt set stays the original, byte for byte (proven in practice); only the voice layer and the forced output-language instruction change.
- 🔑 **Runs straight against cloud APIs**: Supports OpenRouter, DeepSeek, Gemini, Anthropic, OpenAI. One API key and you can write — no Docker needed.
- ✍️ **A voice that fights empty AI prose**: ships with a Vietnamese novel-writing style guide (`assets/voice.md`) and an anti-AI-tone kit (`assets/references/anti-ai-tone.md`) listing concrete banned phrases for Vietnamese — "ở một mức độ nào đó", "như thể", "bất giác", "không khỏi"... — keeping the prose vivid, terse and deep.
- 🚀 **Fully in sync with the latest upstream**: Multi-Agent architecture, 2-tier rolling planning, 4-level context compression, step-level recovery points, and the complete set of 14 slash commands.

## 🛡️ Security Verified

| Area | Result |
|---|---|
| Dependency chain | Only 10 direct deps, all from public sources. `go mod verify` + `GOSUMDB=sum.golang.org` → every hash matches Google's official checksum database |
| `govulncheck` | **No vulnerabilities found** (including the standard library) |
| Every network request | Goes only to the `provider` you configured. Besides that: a version check (read-only) and model pricing from OpenRouter |
| API key | Lives only in the header sent to your provider. Never logged, never written to a file, never sent anywhere else |
| Telemetry / analytics | None. Zero dependencies outside stdlib at the LLM layer |
| Obfuscation | No `unsafe`, no `go:linkname`, no zero-width characters, no hidden `func init()`, no base64 blobs |
| Command execution | Runs only the command **you configured** in `notify.command`, plus the self-update of the binary (with SHA256 verification) |
| Environment variables read | Exactly one: `NOVEL_DIR`. No other hidden environment variable is read |

One thing worth knowing: this is an **agent framework** — the model is given tools to read/write files and run shell commands. If the story content you feed in contains a prompt injection, that is an execution path. This is by design in every agent tool; it is not a backdoor.

## 📑 Table Of Contents

- [System Requirements](#1-system-requirements)
- [Installation & Quick Start](#2-installation--quick-start)
- [Language Option (vi / en / zh)](#3-language-option-vi--en--zh)
- [AI Provider Configuration (LLM)](#4-ai-provider-configuration-llm)
- [Usage Guide & TUI Command Reference](#5-usage-guide--tui-command-reference)
- [Managing Multiple Independent Novels](#6-managing-multiple-independent-novels)
- [Advanced Features](#7-advanced-features)
- [Technical Architecture & How It Works](#8-technical-architecture--how-it-works)
- [Output Directory Structure](#9-output-directory-structure)
- [Custom Voice & Personal Rules](#10-custom-voice--personal-rules)
- [Tech Stack & License](#11-tech-stack--license)

## 1. System Requirements

- **Go ≥ 1.25** to build from source. No Docker required.
- An API key from an LLM provider: OpenRouter, DeepSeek, Gemini, Anthropic or OpenAI.

> **Why not recommend running a local model?** This tool needs a very large context (200k tokens by default, compressed at 85%) and calls the LLM **4-6 times per chapter** — Architect plans, Writer drafts, Editor scores 7 dimensions, then it commits. Local models don't cut it: a 14B model needs ~40GB RAM to reach 64k context, and on CPU it runs at 2-5 tok/s, so a single chapter takes hours. Use a cloud API.

## 2. Installation & Quick Start

No Docker. Three steps:

```powershell
# 1. Cài (một lệnh, không cần cài Go)
irm https://raw.githubusercontent.com/CTKiet2006/kietnovel/v1.3.3/scripts/install-windows.ps1 | iex

# 2. Mở terminal mới, rồi chạy
kietnovel

# 3. Lần đầu: Setup Wizard hỏi Provider, API key, Model và ngôn ngữ (vi / en / zh)
```

The install is about 5.7 MB. The script downloads the prebuilt binary matching your architecture (x86_64 / arm64),
verifies SHA-256, extracts it into `%LOCALAPPDATA%\kietnovel\bin` and adds that directory to the
user PATH.

**Updating:** run the same command again. Quit `kietnovel` first — Windows cannot overwrite a
running file.

**Downgrading to an older version:**

```powershell
powershell -ExecutionPolicy Bypass -File scripts\install-windows.ps1 -Version v1.3.2
```

No Go? Download the script first, then run it:

```powershell
irm https://raw.githubusercontent.com/CTKiet2006/kietnovel/v1.3.3/scripts/install-windows.ps1 -OutFile ki.ps1
.\ki.ps1 -Version v1.3.2
```

Available: v1.3.4, v1.3.3, v1.3.2, v1.2.5, v1.2.4, v1.2.3, v1.2.2, v1.2.1, v1.1.0.

<details>
<summary>Other options</summary>

**No Go yet** — install it first, then use option 2 below:

```powershell
winget install GoLang.Go
```

Or grab the installer directly: <https://go.dev/dl/> → pick the Windows `.msi`. After
installing, open a new terminal so `go` is on your PATH.

**Option 2 — have Go ≥ 1.25**, build from source:

```powershell
go install github.com/CTKiet2006/kietnovel/cmd/kietnovel@latest
```

Noticeably slower: you have to download ~26 MB of dependencies and then compile (25 seconds on a
16-thread machine, a few minutes on a weaker one).

**From source** (if you want to change the code):

```powershell
git clone https://github.com/CTKiet2006/kietnovel.git
cd kietnovel
powershell -ExecutionPolicy Bypass -File scripts\install.ps1
```

</details>

### Data locations

| Kind | Path |
|---|---|
| Config | `~/.kietnovel/config.json` (created by the Wizard) |
| Config scoped to the current directory | `./.kietnovel/config.json` (higher priority) |
| Personal writing rules | `~/.kietnovel/rules/*.md` or `./.kietnovel/rules/*.md` |
| Novel being written | `./output/novel/` — or set `NOVEL_DIR` (section 6) |

Running the binary directly means there is **no** `config/` directory. If `--headless` reports
*"headless không hỗ trợ thiết lập lần đầu"* (it cannot do first-time setup), open the TUI once to
complete the configuration.

## 3. Language Option (vi / en / zh)

In the config file `~/.kietnovel/config.json`, you can set the `"language"` field. A single choice
governs **both** the TUI interface language and the writing language.

- `"language": "vi"` (Default): entirely Vietnamese — terminology, style guide, world indicators.
- `"language": "en"`: English interface, story content generated in natural English at native novel quality.
- `"language": "zh"`: Chinese interface, story content generated in Simplified Chinese (a good fit if you write Chinese, or if you later want to publish on the same platform).

### How it works

The prompt set stays exactly as its author wrote it, in Chinese — its quality is proven, so it is
kept intact. The writing language is controlled in two places:

| Point | `vi` (default) | `en` | `zh` |
|---|---|---|---|
| Voice layer (`voice`) | `assets/voice.md` - Vietnamese novel-writing style guide | `assets/voice_en.md` - native English novel style guide | `assets/voice_zh.md` - the Chinese original |
| Output instruction | Injected into Architect/Writer/Editor: *all output must be written in natural, idiomatic Vietnamese* | Injected the same way, in English | Not injected - the protocol is already Chinese |

In short: one language-forcing instruction plus the matching style guide, instead of maintaining
two parallel prompt sets that drift apart.

### How to switch

- At install time: the Setup Wizard asks for the language right at the start.
- While running: type `/language` to see the current language, or `/language en`
  (also accepts `vi`, `zh`) to switch. The choice is written to the config and kept for
  later runs.

The interface switches immediately. The **writing language** is loaded once at startup, so it
takes full effect from the next launch — the app reminds you of this right after you switch.

## 4. AI Provider Configuration (LLM)

Config file: `~/.kietnovel/config.json` (the Setup Wizard creates it for you). Picking a provider in
the wizard is all it takes — this section is only for when you want to edit it by hand.

| Provider | Suggested `model` | Notes |
|---|---|---|
| DeepSeek | `deepseek-chat` | Cheapest, try it first |
| OpenRouter | `anthropic/claude-3.5-sonnet` | One key, many vendors |
| Gemini | `gemini-2.5-pro` | Long context |
| Anthropic | `claude-3.5-sonnet` | |
| OpenAI | `gpt-4o` | |

Common structure:

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

`context_window` is auto-detected from the model if left blank. `reasoning_effort`:
`off / low / medium / high / xhigh / max`. Add more providers and use them as fallbacks:

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

## 5. Usage Guide & TUI Command Reference

Type `kietnovel` to enter the TUI. On the welcome screen:

- `Tab` — switch between **Quick Start** (type one line, the AI outlines then writes) and
  **Co-Create** (exchange steps to lock down the world/characters before writing).
- `Enter` — start. `/` — find a command. `Ctrl+C` twice — save state and quit.

### Control Command List (Slash Commands)

Type `/` in the TUI to open the command picker:

| Command | Description |
|------|-------|
| `/help` | Open the help sheet listing the commands and keybindings |
| `/model` | Switch model or reasoning effort level |
| `/config` | Manage provider, model ID, API key, base URL, context window |
| `/diag` | See a full diagnostic report on health, progress and story quality |
| `/review [on\|off]` | Toggle per-chapter review mode (stops after each chapter so you can approve) |
| `/next` | Approve writing the next chapter (while in review mode) |
| `/start <file>` | Read an external outline / idea file to start a new novel |
| `/import <file>` | Import an external novel for the AI to analyze and continue |
| `/reopen <dir>` | Start a new volume after the work is finished |
| `/cocreate` | Pause and enter co-create mode for the next stage |
| `/simulate` | Analyze the sample files in `./simulate` to simulate a voice |
| `/importsim <file>` | Import a style-simulation profile from a json file |
| `/sync` | Sync your manual edits on the chapter files back into the system |
| `/export` | Export the work as one complete text file (.txt or .epub) |
| `/language [vi\|en\|zh]` | View or change the interface language; the choice is saved to the config |

### Real-time Steering

While the AI is writing, you can type correction ideas straight into the input box below at any
time — no need to pause or restart:

```text
❯ Cho nhân vật phụ A hy sinh ở cuối chương này để tạo bước ngoặt cảm xúc lớn
```

After you press Enter, the Arbiter automatically assesses the blast radius and directs
Writer/Editor to update the plot.

### Headless Mode

For unattended runs on a VPS, server or CI:

```bash
# Bắt đầu truyện mới
kietnovel --headless --prompt "Tiểu thuyết huyền nghi đô thị phá án"
# Viết tiếp truyện đang dở trong thư mục hiện tại
kietnovel --headless
```

## 6. Managing Multiple Independent Novels

Output goes to `./output/novel/` by default. To write several novels without collisions, set the
`NOVEL_DIR` environment variable before running:

```powershell
# Bộ truyện 1
$env:NOVEL_DIR = ".\novels\tien-hiep-ky"
kietnovel
# Bộ truyện 2
$env:NOVEL_DIR = ".\novels\do-thi-di-nang"
kietnovel
```

Each novel has its own voice (`style/`), checkpoints and progress; nothing bleeds across.

Output directory layout of each novel:

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

> Each `NOVEL_DIR` value is a separate novel. Without `NOVEL_DIR` it uses
> `./output/novel` relative to the working directory.

## 7. Advanced Features

### Story Diagnostics (/diag)

Type `/diag` in the TUI and the system automatically checks the whole novel along 4 dimensions:

- **Progress**: detects rewrite loops, stuck steering instructions, chapter number jumps.
- **Quality**: tracks review scores, plot adherence rate, abnormal chapter lengths.
- **Planning**: checks forgotten plot knots and loose threads, exhausted outlines, missing summaries.
- **Context**: lost characters, broken timeline continuity.

### Voice Simulation (/simulate)

Drop sample files (novels by authors you like) into the `simulate/` directory, then type
`/simulate`. The AI analyzes cadence, diction, sentence structure and builds a simulation profile
to apply to your novel.

### Sync Manual Edits (/sync)

If you open `chapters/05.md` and fix the wording directly, type `/sync` in the TUI. The system
re-scans with SHA-256 and automatically updates summaries, character states and foreshadowing
without breaking the writing process.

### Import An External Novel (/import)

Type `/import ./truyen_cu.txt`. The system will:

1. Detect the boundary of each chapter.
2. Extract the character, world and event setup.
3. Consolidate an outline and be ready to continue with new chapters seamlessly.

### Export The Finished Novel (/export TXT/EPUB)

Type `/export` in the TUI or pass arguments:

```text
/export ./xuat_ban/truyen_full.txt
/export ./xuat_ban/truyen_ebook.epub
/export from=1 to=50 ./tap_1.epub
```

## 8. Technical Architecture & How It Works

### Multi-Agent Architecture

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

| Role | Responsibility | Tools used |
|---------|-------------|-----------------|
| **Arbiter** | Semantic pivot: picks the planner, distributes steering, resolves deadlocks | None (single LLM call, returns a structural decision) |
| **Architect** | The architect: generates the title, summaries, premise, outline, character profiles, world rules | `novel_context`, `save_book`, `save_foundation` |
| **Writer** | The writer: plans the chapter autonomously, drafts, self-checks and hands off | `novel_context`, `read_chapter`, `plan_chapter`, `draft_chapter`, `check_consistency`, `commit_chapter` |
| **Editor** | The editor: reads the draft, scores quality on 7 dimensions, writes arc/volume summaries | `novel_context`, `read_chapter`, `save_review`, `save_arc_summary`, `save_volume_summary` |

### 2-Tier Rolling Planning

Instead of locking in one hard outline for a hundred chapters — which leaves the story more and
more hollow as it goes — kietnovel builds a 2-Volume skeleton and detailed outlines for the first
Arc. As an Arc nears its end, the Editor summarizes and only then does the Architect expand the
next Arc based on how the story actually turned.

### 4-Level Context Management & Compression

So that a 500+ chapter novel can be written without overflowing the context window:

1. **ToolResultMicrocompact** — clears the intermediate results of old tool calls.
2. **LightTrim** — cuts long text passages that are no longer needed.
3. **StoreSummaryCompact** — replaces old messages with summaries already stored in the Store (costs no tokens).
4. **FullSummary** — a dedicated prompt that summarizes the narrative context.

### The Editor's 7-Dimension Quality Review

After every chapter, the Editor delivers a strict verdict on 7 criteria (every comment must quote
the sentence as evidence):

1. Consistency of the setup (Consistency)
2. Character behavior & personality (Character Behavior)
3. Plot pacing (Pacing)
4. Narrative coherence & scene transitions (Narrative Flow)
5. Foreshadowing set-up (Foreshadowing)
6. The end-of-chapter hook that keeps the reader (Hooks)
7. Literary aesthetic quality (Aesthetic Quality): descriptive detail, artistic devices, distinct voices in dialogue, quality of word choice, emotional power.

### Step-level Recovery

Every time a tool executes successfully, the system immediately writes a checkpoint
(`meta/checkpoints.jsonl`). If you lose power, shut down, drop the network or press Ctrl+C:

- When you open it again, the system reads `progress.json` and the nearest checkpoint.
- It resumes automatically from exactly the step where it stopped (e.g. "chapter 7 draft done, continuing with the check_consistency step").

## 9. Output Directory Structure

All data is stored in the `output` directory:

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

## 10. Custom Voice & Personal Rules

**Add your own rules (no code changes needed)**

Create any `.md` file in the `~/.kietnovel/rules/` or `./.kietnovel/rules/` directory and write in
plain language:

```text
"Nhân vật chính quyết đoán, không thánh mẫu"
"Tăng cường miêu tả cảm giác cơ thể và khung cảnh xung quanh"
"Mỗi chương khoảng 3000 từ"
"Không dùng các từ sáo rỗng như: 'ở một mức độ nào đó', 'như thể', 'bất giác'"
```

The system automatically consolidates these requirements into the work's rule-checking set.

## 11. Tech Stack & License

- **Language**: Go (high performance, tight control over concurrency and I/O)
- **Agent Core**: [agentcore](https://github.com/voocel/agentcore) (tool-calling + streaming)
- **LLM Interface**: [litellm](https://github.com/voocel/litellm)
- **TUI Framework**: Bubble Tea & Lip Gloss
- **Prompt structure**: Markdown files embedded into the binary (`//go:embed`), loaded by language at startup — no network needed to select a prompt set
- **Dependencies**: 10 direct packages, all from public sources. `govulncheck` clean, no dependency outside stdlib at the LLM layer

### License

Apache License 2.0 — see [LICENSE](LICENSE). New contributions to kietnovel:
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
