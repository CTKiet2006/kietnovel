# kietnovel

> **Tiếng Việt** · [English](README.en.md) · [中文](README.zh.md)

> Originally inspired by and initially based on AINovel-CLI by voocel. KietNovel is now independently developed — an AI engine for writing full-length novels, with a trilingual interface (Vietnamese / English / Chinese).

Công cụ sáng tác tiểu thuyết AI bán tự động: Engine xác định chạy trọn một bộ truyện, model chỉ được gọi đúng chỗ cần phán đoán — Engine điều phối 3 agent tự chủ Architect / Writer / Editor theo bảng quyết định, Arbiter ngữ nghĩa chỉ thức dậy khi cần. Từ một câu ý tưởng tới tiểu thuyết hoàn chỉnh.

```text
┌─ Tổng quan ──────────────────┐┌─ Dòng sự kiện thời gian thực (Activity Stream) ───────────────┐
│ Trạng thái   Đang sáng tác   ││ 15:02:10 ⚙ WRITER (Đang viết Chương 1)                         │
│ Giai đoạn    Viết chính văn  ││   ├ novel_context (chương 1)                                   │
│ Đã xong      0 chương        ││   ├ draft_chapter (chương 1, 3.200 từ)                         │
│ Đã dàn ý     9 chương        ││   ├ check_consistency (đối chiếu phục bút & thế giới quan)     │
│ Dung lượng   0 từ            ││   └ commit_chapter (hoàn tất chương 1)                         │
│ Vai trò      WRITER (Chạy)   ││ 15:04:15 ✓ EDITOR: Đã thẩm định đạt 88/100 (Accept)            │
│ Ngữ cảnh     [████░░░] 24%   ││ 15:04:18 ⚙ ARCHITECT: Mở rộng Arc tiếp theo (expand_next_arc)   │
└──────────────────────────────┘└───────────────────────────────────────────────────────────────┘
│ ❯ Cho nhân vật chính gặp tai nạn bất ngờ ở ngã tư để kích hoạt phục bút về quá khứ...         │
└───────────────────────────────────────────────────────────────────────────────────────────────┘
```

## Điểm Nổi Bật Của Bản Việt Hóa & Đa Ngôn Ngữ (v1.6.3)

- 🌐 **Giao diện TUI đa ngôn ngữ (vi / en / zh)**: Từ Setup Wizard cài đặt ban đầu, màn hình chào mừng, thanh trạng thái, Activity stream trực tiếp, bảng quản lý Provider/Model (`/config`, `/model`) đến thông báo lỗi và phím tắt — tất cả dịch theo ngôn ngữ đang chọn, chuẩn mực và tự nhiên. Đổi giữa chừng bằng lệnh `/language`.
- ✍️ **Độc lập ngôn ngữ sáng tác triệt để (Tri-Language Isolation)**:
  - **100% Native Prompts**: Toàn bộ hệ thống prompt của Architect, Writer, Editor và Arbiter được chuyển hóa sâu sắc sang văn phong tiểu thuyết Tiếng Việt chuẩn mực, gãy gọn, giàu hình tượng, không dịch thô từ tiếng Trung.
  - **100% Dynamic Tool Schemas & Error Handling**: 16 công cụ giao tiếp với LLM (`draft_chapter`, `plan_chapter`, `commit_chapter`, `novel_context`...) tự động đồng bộ schema và thông báo lỗi bằng Tiếng Việt / Tiếng Anh / Tiếng Trung, cắt đứt hoàn toàn nguy cơ model bị "nhiễm" tiếng Trung khi suy nghĩ (thinking) hoặc khi gặp lỗi.
  - **100% Native Reference Docs**: 10 tài liệu kỹ thuật ngầm (10 kỹ thuật móc câu kịch tính, nghệ thuật Show Don't Tell, duy trì tính nhất quán, cẩm nang đối thoại, quy hoạch trường thiên...) được nạp trực tiếp bằng ngôn ngữ truyện.
  - **Đồng sáng tác (Co-create)**: Trợ lý trò chuyện định hướng ý tưởng thô và lập kế hoạch chặng tiếp theo bằng ngôn ngữ bản địa tự nhiên.
- 🔑 **Chạy thẳng với API cloud**: Hỗ trợ OpenRouter, DeepSeek, Gemini, Anthropic, OpenAI. Một API key là viết được ngay, không cần Docker.
- ✍️ **Văn phong chống AI sáo rỗng**: kèm quy chuẩn hành văn tiểu thuyết Tiếng Việt (`assets/voice.md`) và bộ chống sáo rỗng (`assets/references/anti-ai-tone_vi.md`) với danh sách cụm cấm cụ thể cho tiếng Việt — "ở một mức độ nào đó", "như thể", "bất giác", "không khỏi", "đáng chú ý là"... giúp hành văn sống động, gãy gọn, có chiều sâu.
- 🚀 **Kiến trúc Đa Agent hiện đại**: Quy hoạch cuộn 2 tầng (Rolling planning), nén ngữ cảnh 4 cấp, điểm phục hồi checkpoint step-level, và toàn bộ 14 lệnh slash commands.

## 🛡️ Đã kiểm tra bảo mật

| Hạng mục | Kết quả |
|---|---|
| Dependency chain | Chỉ 10 dep trực tiếp, tất cả từ nguồn công khai. `go mod verify` + `GOSUMDB=sum.golang.org` → mọi hash khớp cơ sở dữ liệu checksum chính thức của Google |
| `govulncheck` | **No vulnerabilities found** (bao gồm standard library) |
| Mọi request mạng | Chỉ tới `provider` bạn tự cấu hình. Ngoài ra: kiểm tra phiên bản (đọc) và lấy giá model từ OpenRouter |
| API key | Chỉ nằm trong header gửi tới provider. Không log, không ghi file, không gửi đi nơi khác |
| Telemetry / analytics | Không có. Zero dependency ngoài stdlib ở tầng LLM |
| Obfuscation | Không có `unsafe`, `go:linkname`, zero-width char, `func init()` ẩn, base64 blob |
| Thực thi lệnh | Chỉ chạy lệnh **bạn tự cấu hình** trong `notify.command`, và tự cập nhật binary (có verify SHA256) |
| Biến môi trường đọc | Đúng một biến: `NOVEL_DIR`. Không đọc biến môi trường ẩn nào khác |

Điểm cần biết: đây là **agent framework** — mô hình được cấp tool đọc/ghi file và chạy shell. Nếu nội dung truyện bạn nhập vào chứa prompt injection, đó là đường thực thi. Đây là thiết kế của mọi công cụ agent, không phải backdoor.

## 📑 Mục Lục

- [Yêu Cầu Hệ Thống](#1-yêu-cầu-hệ-thống)
- [Cài Đặt & Khởi Chạy Nhanh](#2-cài-đặt--khởi-chạy-nhanh)
- [Tùy Chọn Ngôn Ngữ Sáng Tác](#3-tùy-chọn-ngôn-ngữ-sáng-tác)
- [Cấu Hình Nhà Cung Cấp AI (LLM)](#4-cấu-hình-nhà-cung-cấp-ai-llm)
- [Hướng Dẫn Sử Dụng & Bảng Lệnh TUI](#5-hướng-dẫn-sử-dụng--bảng-lệnh-tui)
- [Quản Lý Nhiều Bộ Truyện Độc Lập](#6-quản-lý-nhiều-bộ-truyện-độc-lập)
- [Tính Năng Nâng Cao](#7-tính-năng-nâng-cao)
- [Kiến Trúc Kỹ Thuật & Nguyên Lý Hoạt Động](#8-kiến-trúc-kỹ-thuật--nguyên-lý-hoạt-động)
- [Cấu Trúc Thư Mục Đầu Ra](#9-cấu-trúc-thư-mục-đầu-ra)
- [Tùy Biến Văn Phong & Quy Tắc Cá Nhân](#10-tùy-biến-văn-phong--quy-tắc-cá-nhân)
- [Tech Stack & License](#11-tech-stack--license)

## 1. Yêu Cầu Hệ Thống

- **Go ≥ 1.25** để build từ source. Không cần Docker.
- Một API key từ nhà cung cấp LLM: OpenRouter, DeepSeek, Gemini, Anthropic hoặc OpenAI.

> **Vì sao không khuyến nghị chạy model local?** Công cụ này cần context rất lớn (mặc định 200k token, nén ở 85%) và gọi LLM **4-6 lần cho mỗi chương** — Architect lên kế hoạch, Writer viết nháp, Editor đánh giá 7 chiều, rồi commit. Model local địa phương không đáp ứng: model 14B cần ~40GB RAM để đạt 64k context, và trên CPU tốc độ 2-5 tok/s nên một chương mất hàng giờ. Hãy dùng API cloud.

## 2. Cài Đặt & Khởi Chạy Nhanh

Không cần Docker. Ba bước:

```powershell
# 1. Cài (một lệnh, không cần cài Go)
irm https://github.com/CTKiet2006/kietnovel/releases/latest/download/install-windows.ps1 | iex

# 2. Mở terminal mới, rồi chạy
kietnovel

# 3. Lần đầu: Setup Wizard hỏi Provider, API key, Model và ngôn ngữ (vi / en / zh)
```

**Phím trong Setup Wizard:** `↑`/`↓` (hoặc `k`/`j`) để chọn, `Enter` để xác nhận,
`Esc` để **quay lại bước trước** — lỡ chọn sai Provider ở bước 1 thì quay lại sửa, không
phải hủy rồi làm lại từ đầu. Riêng tại bước 1, `Esc` phải bấm hai lần mới hủy. `Ctrl+C`
hủy ngay lúc nào cũng được. Khi quay lại, phần đã nhập và vị trí con trỏ được giữ nguyên.

Cài trong khoảng 5.7 MB. Script tự tải bản dựng sẵn đúng kiến trúc máy (x86_64 / arm64),
kiểm tra SHA-256, giải nén vào `%LOCALAPPDATA%\kietnovel\bin` và thêm thư mục đó vào
user PATH.

**Cập nhật:** chạy lại lệnh trên. Thoát `kietnovel` trước — Windows không cho ghi đè
file đang chạy.

**Hạ về bản cũ:**

```powershell
powershell -ExecutionPolicy Bypass -File scripts\install-windows.ps1 -Version v1.4.0
```

Nếu không có Go, tải script rồi chạy:

```powershell
irm https://github.com/CTKiet2006/kietnovel/releases/latest/download/install-windows.ps1 -OutFile ki.ps1
.\ki.ps1 -Version v1.4.0
```

Tất cả bản phát hành: <https://github.com/CTKiet2006/kietnovel/releases>

<details>
<summary>Cách khác</summary>

**Nếu chưa có Go** — tải trước, rồi quay lại dùng cách 2:

```powershell
winget install GoLang.Go
```

Hoặc tải thẳng file cài: <https://go.dev/dl/> → chọn bản Windows `.msi`. Sau khi cài,
mở terminal mới để `go` có hiệu lực.

**Cách 2 — đã có Go ≥ 1.25**, tự build từ source:

```powershell
go install github.com/CTKiet2006/kietnovel/cmd/kietnovel@latest
```

Chậm hơn đáng kể: phải tải ~26 MB dependency rồi compile (25 giây trên máy 16 luồng,
vài phút trên máy yếu).

**Từ source** (muốn sửa code):

```powershell
git clone https://github.com/CTKiet2006/kietnovel.git
cd kietnovel
powershell -ExecutionPolicy Bypass -File scripts\install.ps1
```

</details>

### Vị trí dữ liệu

| Loại | Đường dẫn |
|---|---|
| Cấu hình | `~/.kietnovel/config.json` (Wizard tự tạo) |
| Cấu hình riêng cho thư mục hiện tại | `./.kietnovel/config.json` (ưu tiên cao hơn) |
| Quy tắc viết cá nhân | `~/.kietnovel/rules/*.md` hoặc `./.kietnovel/rules/*.md` |
| Truyện đang viết | `./output/novel/` — hoặc đặt `NOVEL_DIR` (mục 6) |

Chạy binary trực tiếp thì **không** có thư mục `config/`. Nếu `--headless` báo *"headless
không hỗ trợ thiết lập lần đầu"*, hãy mở TUI một lần để hoàn tất cấu hình.

## 3. Tùy Chọn Ngôn Ngữ (vi / en / zh)

Trong file cấu hình `~/.kietnovel/config.json`, bạn có thể chỉ định trường `"language"`.
Một lựa chọn này chi phối **cả hai**: ngôn ngữ giao diện TUI lẫn ngôn ngữ sáng tác.

- `"language": "vi"` (Mặc định): Toàn bộ tiếng Việt, thuật ngữ, quy chuẩn văn phong, chỉ báo thế giới.
- `"language": "en"`: Giao diện tiếng Anh, nội dung truyện sinh ra bằng tiếng Anh tự nhiên, đúng chuẩn tiểu thuyết bản ngữ.
- `"language": "zh"`: Giao diện tiếng Trung, nội dung truyện sinh ra bằng tiếng Trung giản thuận (phù hợp nếu bạn viết truyện Trung hoặc muốn đăng cùng cổ địch sau).

### Cách hoạt động (Tri-Language Isolation)

Từ phiên bản v1.6.3, hệ thống áp dụng kiến trúc **cô lập ngôn ngữ 4 tầng** nhằm loại bỏ triệt để hiện tượng rò rỉ ngoại ngữ và ngăn LLM bị "nhiễm" ngôn ngữ chéo trong quá trình suy nghĩ (thinking):

| Thành phần | Tiếng Việt (`vi`) | Tiếng Anh (`en`) | Tiếng Trung (`zh`) |
|---|---|---|---|
| **System Prompts** | `writer_vi.md`, `editor_vi.md`, `architect-*_vi.md` | `writer_en.md`, `editor_en.md`, `architect-*_en.md` | `writer.md`, `editor.md`, `architect-*.md` (gốc) |
| **Lớp văn phong (`voice`)** | `voice.md` + `anti-ai-tone_vi.md` | `voice_en.md` + `anti-ai-tone_en.md` | `voice_zh.md` + `anti-ai-tone.md` |
| **Tool Schemas & Lỗi** | 16 tools dịch 100% tiếng Việt cả description lẫn error | 16 tools dịch 100% tiếng Anh | 16 tools giữ nguyên tiếng Trung |
| **References ngầm** | 10 tài liệu kỹ thuật bản địa hóa Tiếng Việt | 10 tài liệu kỹ thuật Tiếng Anh | 10 tài liệu kỹ thuật bản gốc |
| **Đồng sáng tác / Co-create** | Prompt & Tóm tắt trạng thái thuần Việt | Prompt & Tóm tắt trạng thái tiếng Anh | Prompt & Tóm tắt trạng thái tiếng Trung |

### Cách đổi ngôn ngữ

- Lúc cài đặt: Setup Wizard hỏi ngôn ngữ ngay từ đầu.
- Khi đang chạy: gõ `/language` để xem ngôn ngữ hiện tại, hoặc `/language en` (hoặc `vi`, `zh`) để đổi trực tiếp.
- Mỗi bộ truyện còn được khóa ngôn ngữ độc lập trong `meta/language.json`, đảm bảo khi chuyển đổi giữa các tác phẩm khác nhau thì ngữ cảnh sáng tác vẫn luôn đồng bộ chuẩn xác.

## 4. Cấu Hình Nhà Cung Cấp AI (LLM)

File cấu hình: `~/.kietnovel/config.json` (Setup Wizard tạo sẵn). Chọn Provider trong
wizard là xong — mục này chỉ dành khi muốn tự sửa tay.

| Provider | `model` gợi ý | Ghi chú |
|---|---|---|
| DeepSeek | `deepseek-chat` | Rẻ nhất, nên thử trước |
| OpenRouter | `anthropic/claude-3.5-sonnet` | Một key, nhiều hãng |
| Gemini | `gemini-2.5-pro` | Context dài |
| Anthropic | `claude-3.5-sonnet` | |
| OpenAI | `gpt-4o` | |

Cấu trúc chung:

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

`context_window` tự dò từ model nếu bỏ trống. `reasoning_effort`:
`off / low / medium / high / xhigh / max`. Thêm nhiều provider và dùng làm fallback:

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

## 5. Hướng Dẫn Sử Dụng & Bảng Lệnh TUI

Gõ `kietnovel` để vào TUI. Tại màn hình chào:

- `Tab` — chuyển giữa **Bắt đầu nhanh** (nhập 1 câu, AI tự dàn ý rồi viết) và
  **Đồng sáng tác** (trao đổi từng bước để chốt thế giới/nhân vật trước khi viết).
- `Enter` — bắt đầu. `/` — tìm lệnh. `Ctrl+C` 2 lần — lưu trạng thái rồi thoát.

### Danh sách Lệnh Điều Khiển (Slash Commands)

Gõ `/` trong TUI để mở bảng chọn lệnh:

| Lệnh | Mô tả |
|------|-------|
| `/help` | Mở bảng trợ giúp tra cứu danh sách lệnh và phím tắt |
| `/model` | Chuyển đổi Model hoặc mức độ suy luận (reasoning/thinking) |
| `/config` | Quản lý cấu hình Provider, Model ID, API Key, Base URL, Context Window |
| `/diag` | Xem báo cáo chẩn đoán toàn diện về sức khỏe, tiến độ và chất lượng truyện |
| `/review [on\|off]` | Bật/tắt chế độ nghiệm thu từng chương (dừng lại sau mỗi chương để bạn duyệt) |
| `/next` | Phê duyệt cho phép viết chương tiếp theo (khi ở chế độ nghiệm thu) |
| `/start <tệp>` | Đọc tệp dàn ý / ý tưởng bên ngoài để bắt đầu truyện mới |
| `/import <tệp>` | Nhập tiểu thuyết từ bên ngoài vào để AI phân tích và viết tiếp |
| `/reopen <hướng>` | Viết tiếp tập mới sau khi tác phẩm đã hoàn thành |
| `/cocreate` | Tạm dừng để vào chế độ đồng sáng tác định hướng giai đoạn tiếp theo |
| `/simulate` | Phân tích các file văn mẫu trong `./simulate` để mô phỏng văn phong |
| `/importsim <file>` | Nhập hồ sơ mô phỏng văn phong từ tệp json |
| `/sync` | Đồng bộ các chỉnh sửa thủ công của bạn trên các file chương vào hệ thống |
| `/export` | Xuất tác phẩm thành file văn bản hoàn chỉnh (.txt hoặc .epub) |
| `/read [n]` | Đọc truyện ngay trong TUI — cả chương đã chốt lẫn bản nháp đang làm dở |
| `/books` | Xem danh sách truyện trong `output/`, chuyển qua lại giữa các truyện |
| `/new [tên]` | Tạo truyện mới và mở luôn để viết (tên có dấu và khoảng trắng đều được) |
| `/delete [tên]` | Xoá truyện — hai bước, bước hai phải gõ `y`, không khôi phục được |
| `/language [vi\|en\|zh]` | Xem hoặc đổi ngôn ngữ giao diện; lựa chọn được lưu vào cấu hình |

### Trực Quan Hóa Các Tính Năng UI

#### 1. Màn hình Đồng sáng tác (/cocreate)
Giúp bạn và AI cùng lên khung thế giới quan, nhân vật và hướng đi trước khi viết hoặc giữa các chặng:

```text
┌─ Đồng sáng tác ý tưởng (Co-Create) ────────────────────────────────────────────┐
│ AI: Chào bạn! Bạn muốn viết về đề tài gì? Đô thị huyền bí, tu chân, hay trinh │
│     thám hồi hộp? Nhân vật chính là người như thế nào?                         │
│                                                                                │
│ Bạn: Trinh thám tâm lý, nhân vật nữ làm ca đêm ở bến xe vắng, nghe thấy tiếng │
│      chuông điện thoại lạ từ phòng trực tầng ba.                               │
│                                                                                │
│ AI: Hướng đi rất hấp dẫn! Đang cập nhật bản thảo chỉ đạo sáng tác:             │
│     ## Chủ đề: Kinh dị tâm lý / Án mạng bí ẩn tại trạm xe đêm                 │
│     ## Nhân vật: Ngọc (nữ trực tổng đài, ít nói, tuân thủ nghiêm ngặt quy tắc) │
│                                                                                │
│  gợi ý: [1] Cài cắm thêm một bí mật gia đình  [2] Tập trung vào yếu tố giật gân│
│ [Ctrl+S]: Bắt đầu sáng tác  |  [Ctrl+C]: Lưu bản nháp  |  [Esc]: Quay lại      │
└────────────────────────────────────────────────────────────────────────────────┘
```

#### 2. Can thiệp thời gian thực (Steer)
Trong lúc AI đang viết, bạn có thể nhập trực tiếp ý kiến sửa đổi vào ô nhập liệu bên dưới bất cứ lúc nào mà không cần tạm dừng hay khởi động lại:

```text
┌────────────────────────────────────────────────────────────────────────────────┐
│ 15:02:10 ⚙ WRITER (Đang viết Chương 1)                                         │
│   ├ novel_context (chương 1)                                                   │
│   ├ draft_chapter (chương 1, 3.200 từ)                                         │
├────────────────────────────────────────────────────────────────────────────────┤
│ ❯ Cho nhân vật phụ A hy sinh ở cuối chương này để tạo bước ngoặt cảm xúc lớn   │
└────────────────────────────────────────────────────────────────────────────────┘
```
Sau khi nhấn `Enter`, Arbiter sẽ tự động đánh giá phạm vi ảnh hưởng và điều phối Writer/Editor cập nhật mạch truyện ngay lập tức.

#### 3. Đọc truyện trực tiếp trong TUI (/read)
Gõ `/read` để đọc chương hiện tại hoặc `/read 5` để đọc lại chương 5 với định dạng sạch sẽ, phân trang mượt mà ngay trong cửa sổ dòng lệnh:

```text
┌─ Đang đọc: Chương 1 - Hai giờ bốn mươi bảy (3.420 từ) ─────────────────────────┐
│ Hai giờ bốn mươi bảy phút, máy trong phòng trực reo lên.                       │
│                                                                                │
│ Tiếng chuông của đường dây nội bộ không giống tiếng chuông ngoài sảnh. Nó ngắn,│
│ hai tiếng, lặp lại ba lần, rồi ngừng. Ngọc biết mẩu đó thuộc dãy tầng ba...    │
├────────────────────────────────────────────────────────────────────────────────┤
│ [↑/↓ hoặc j/k]: Cuộn trang  |  [q hoặc Esc]: Quay lại màn hình chính           │
└────────────────────────────────────────────────────────────────────────────────┘
```

### Chế độ chạy ngầm (Headless)

Dành cho chạy tự động trên VPS, Server hoặc CI:

```bash
# Bắt đầu truyện mới
kietnovel --headless --prompt "Tiểu thuyết huyền nghi đô thị phá án"
# Viết tiếp truyện đang dở trong thư mục hiện tại
kietnovel --headless
```

## 6. Quản Lý Nhiều Bộ Truyện Độc Lập

Mặc định output lưu vào `./output/novel/`. Để viết nhiều bộ truyện mà không xung đột, đặt biến môi trường `NOVEL_DIR` trước khi chạy:

```powershell
# Bộ truyện 1
$env:NOVEL_DIR = ".\novels\tien-hiep-ky"
kietnovel
# Bộ truyện 2
$env:NOVEL_DIR = ".\novels\do-thi-di-nang"
kietnovel
```

Mỗi bộ truyện có văn phong (`style/`), checkpoint và tiến độ riêng, không lẫn nhau.

Cấu trúc thư mục đầu ra của mỗi truyện:

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

> Mỗi giá trị `NOVEL_DIR` là một bộ truyện riêng. Không đặt `NOVEL_DIR` thì dùng
> `./output/novel` theo thư mục làm việc.

## 7. Tính Năng Nâng Cao

### Chẩn đoán truyện (/diag)

Gõ `/diag` trong TUI để hệ thống tự động kiểm tra toàn bộ truyện theo 4 chiều:

- **Tiến trình**: Phát hiện vòng lặp viết lại, kẹt lệnh can thiệp, nhảy số chương.
- **Chất lượng**: Theo dõi điểm số đánh giá, tỷ lệ tuân thủ cốt truyện, độ dài chương bất thường.
- **Quy hoạch**: Kiểm tra các nút thắt/phục bút bị bỏ quên, dàn ý cạn kiệt, thiếu tóm tắt.
- **Ngữ cảnh**: Nhân vật bị mất tích, đứt gãy dòng thời gian.

### Mô phỏng văn phong (/simulate)

Đặt các file văn mẫu (truyện của tác giả bạn yêu thích) vào thư mục `simulate/` rồi gõ `/simulate`. AI sẽ phân tích nhịp điệu, cách dùng từ, cấu trúc câu và tạo hồ sơ mô phỏng để áp dụng vào truyện của bạn.

### Đồng bộ sửa đổi thủ công (/sync)

Nếu bạn mở file `chapters/05.md` ra chỉnh sửa câu chữ trực tiếp, gõ `/sync` trong TUI. Hệ thống sẽ quét qua SHA-256, tự động cập nhật lại tóm tắt, trạng thái nhân vật và phục bút mà không làm hỏng tiến trình sáng tác.

### Nhập truyện bên ngoài (/import)

Gõ `/import ./truyen_cu.txt`. Hệ thống sẽ:

1. Nhận diện ranh giới từng chương.
2. Trích xuất thiết lập nhân vật, thế giới và dòng sự kiện.
3. Tổng hợp dàn ý và sẵn sàng viết tiếp các chương mới liền mạch.

### Xuất truyện hoàn chỉnh (/export TXT/EPUB)

Gõ `/export` trong TUI hoặc chỉ định tham số:

```text
/export ./xuat_ban/truyen_full.txt
/export ./xuat_ban/truyen_ebook.epub
/export from=1 to=50 ./tap_1.epub
```

## 8. Kiến Trúc Kỹ Thuật & Nguyên Lý Hoạt Động

### Kiến trúc Đa Agent

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

| Vai trò | Trách nhiệm | Công cụ sử dụng |
|---------|-------------|-----------------|
| **Arbiter** | Trọng tài ngữ nghĩa: chọn planner, phân luồng can thiệp, giải quyết bế tắc | Không có (Gọi LLM đơn, trả về quyết định cấu trúc) |
| **Architect** | Kiến trúc sư: sinh tên truyện, tóm tắt, tiền đề, dàn ý, hồ sơ nhân vật, quy tắc thế giới | `novel_context`, `save_book`, `save_foundation` |
| **Writer** | Người viết: tự chủ lên kế hoạch chương, viết nháp, tự kiểm tra và bàn giao | `novel_context`, `read_chapter`, `plan_chapter`, `draft_chapter`, `check_consistency`, `commit_chapter` |
| **Editor** | Biên tập viên: đọc bản thảo, đánh giá chất lượng 7 chiều, lập tóm tắt cung/tập | `novel_context`, `read_chapter`, `save_review`, `save_arc_summary`, `save_volume_summary` |

### Quy hoạch cuộn 2 tầng (Rolling Planning)

Thay vì lên dàn ý cứng một lần cho cả trăm chương — khiến câu chuyện càng về sau càng
rỗng — kietnovel dựng khung 2 Tập và dàn ý chi tiết cho Cung đầu tiên. Khi viết gần
hết một Cung, Editor tổng kết rồi Architect mới mở rộng Cung tiếp theo dựa trên diễn
biến thực tế của truyện.

### Quản lý & Nén ngữ cảnh 4 cấp

Để viết được truyện dài 500+ chương mà không tràn context window:

1. **ToolResultMicrocompact** — dọn kết quả trung gian của tool cũ.
2. **LightTrim** — cắt đoạn text dài không còn cần.
3. **StoreSummaryCompact** — thay tin nhắn cũ bằng bản tóm tắt đã lưu trong Store (không tốn token).
4. **FullSummary** — prompt chuyên dụng tóm tắt ngữ cảnh tự sự.

### Đánh giá chất lượng 7 chiều của Editor

Sau mỗi chương, Editor sẽ thẩm định nghiêm ngặt theo 7 tiêu chí (mỗi nhận xét bắt buộc phải trích dẫn câu văn làm bằng chứng):

1. Tính nhất quán của thiết lập (Consistency)
2. Hành vi & Tính cách nhân vật (Character Behavior)
3. Nhịp điệu cốt truyện (Pacing)
4. Tính mạch lạc & Chuyển cảnh (Narrative Flow)
5. Gài gắm phục bút (Foreshadowing)
6. Móc câu giữ chân người đọc cuối chương (Hooks)
7. Chất lượng thẩm mỹ văn chương (Aesthetic Quality): Chi tiết miêu tả, thủ pháp nghệ thuật, sự khác biệt trong lời thoại, chất lượng từ ngữ, sức truyền cảm.

### Khôi phục điểm ngắt (Step-level Recovery)

Mỗi lần một công cụ thực thi thành công, hệ thống sẽ ghi ngay một checkpoint (`meta/checkpoints.jsonl`). Nếu bị mất điện, tắt máy, rớt mạng hay nhấn Ctrl+C:

- Khi mở lại, hệ thống đọc `progress.json` và checkpoint gần nhất.
- Tự động tiếp tục chính xác từ bước bị dừng lại (ví dụ: "Đã xong draft chương 7, tiếp tục bước check_consistency").

## 9. Cấu Trúc Thư Mục Đầu Ra

Toàn bộ dữ liệu được lưu trong thư mục `output`:

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

## 10. Tùy Biến Văn Phong & Quy Tắc Cá Nhân

**Thêm quy tắc riêng (Không cần sửa code)**

Tạo file `.md` bất kỳ trong thư mục `~/.kietnovel/rules/` hoặc `./.kietnovel/rules/` và viết bằng ngôn ngữ tự nhiên:

```text
"Nhân vật chính quyết đoán, không thánh mẫu"
"Tăng cường miêu tả cảm giác cơ thể và khung cảnh xung quanh"
"Mỗi chương khoảng 3000 từ"
"Không dùng các từ sáo rỗng như: 'ở một mức độ nào đó', 'như thể', 'bất giác'"
```

Hệ thống sẽ tự động tổng hợp các yêu cầu này vào bộ quy tắc kiểm duyệt của tác phẩm.

## 11. Tech Stack & License

- **Ngôn ngữ**: Go (Hiệu năng cao, kiểm soát chặt chẽ concurrency và I/O)
- **Agent Core**: [agentcore](https://github.com/voocel/agentcore) (Tool-calling + Streaming)
- **LLM Interface**: [litellm](https://github.com/voocel/litellm)
- **TUI Framework**: Bubble Tea & Lip Gloss
- **Cấu trúc prompt**: file Markdown embed vào binary (`//go:embed`), nạp theo ngôn ngữ lúc khởi động — không cần mạng để chọn bộ prompt
- **Dependency**: 10 gói trực tiếp, tất cả từ nguồn công khai. `govulncheck` sạch, không có dependency ngoài stdlib ở tầng LLM

### License

Apache License 2.0 — xem [LICENSE](LICENSE). Phần đóng góp mới của kietnovel:
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
