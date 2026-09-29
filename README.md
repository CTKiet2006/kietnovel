# kietnovel

> Engine sáng tác tiểu thuyết dài tập bằng AI, Việt hóa toàn diện. Fork từ [voocel/ainovel-cli](https://github.com/voocel/ainovel-cli) — giữ nguyên bộ prompt gốc đã kiểm chứng.

Công cụ sáng tác tiểu thuyết AI bán tự động: Engine xác định chạy trọn một bộ truyện, model chỉ được gọi đúng chỗ cần phán đoán — Engine điều phối 3 agent tự chủ Architect / Writer / Editor theo bảng quyết định, Arbiter ngữ nghĩa chỉ thức dậy khi cần. Từ một câu ý tưởng tới tiểu thuyết hoàn chỉnh.

## Điểm Nổi Bật Của Bản Việt Hóa & Đa Ngôn Ngữ

- 🇻🇳 **Giao diện TUI Việt hóa 100%**: Từ Setup Wizard cài đặt ban đầu, màn hình chào mừng, thanh trạng thái, Activity stream trực tiếp, bảng quản lý Provider/Model (`/config`, `/model`) đến thông báo lỗi và phím tắt đều được dịch sang Tiếng Việt chuẩn mực, tự nhiên.
- 🌐 **Tùy chọn Ngôn ngữ sáng tác truyện**: Viết truyện bằng Tiếng Việt (mặc định) hoặc Tiếng Trung nguyên bản qua `"language": "vi"` / `"language": "zh"`. Bộ prompt giữ nguyên bản gốc (đã kiểm chứng), chỉ đổi lớp văn phong kèm chỉ dẫn buộc ngôn ngữ đầu ra.
- 🔑 **Chạy thẳng với API cloud**: Hỗ trợ OpenRouter, DeepSeek, Gemini, Anthropic, OpenAI. Một API key là viết được ngay, không cần Docker.
- ✍️ **Văn phong chống AI sáo rỗng**: kèm quy chuẩn hành văn tiểu thuyết Tiếng Việt (`assets/voice.md`) và bộ chống sáo rỗng (`assets/references/anti-ai-tone.md`) với danh sách cụm cấm cụ thể cho tiếng Việt — "ở một mức độ nào đó", "như thể", "bất giác", "không khỏi"... giúp hành văn sống động, gãy gọn, có chiều sâu.
- 🚀 **Đồng bộ toàn diện Upstream mới nhất**: Kiến trúc Đa Agent, quy hoạch cuộn 2 tầng (Rolling planning), nén ngữ cảnh 4 cấp, điểm phục hồi step-level, và toàn bộ 14 lệnh slash commands.

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

Yêu cầu duy nhất: **Go ≥ 1.25**. Không cần Docker.

**Cách 1 — script cài (giống cách bạn cài `agy` / `hermes`, lệnh nằm trên PATH ở mọi nơi):**

```powershell
git clone https://github.com/CTKiet2006/kietnovel.git
cd kietnovel
powershell -ExecutionPolicy Bypass -File scripts\install.ps1
```

Script build, chép binary vào `%LOCALAPPDATA%\kietnovel\bin\` và tự thêm thư mục đó vào user PATH. Mở terminal mới là gõ `kietnovel` ở bất kỳ đâu cũng chạy.

**Cách 2 — chạy tại chỗ, không cài (chỉ dùng được trong thư mục repo):**

```powershell
cd kietnovel
go build -o kietnovel.exe ./cmd/ainovel-cli
.\kietnovel.exe
```

**Cập nhật sau khi có thay đổi mới:** chạy lại `scripts\install.ps1` (thoát `kietnovel` trước nếu đang mở — Windows không cho ghi đè file đang chạy).

Lần đầu chạy, hệ thống tự bật **Setup Wizard** tiếng Việt để chọn Provider, nhập API Key/Base URL, chọn Model và Ngôn ngữ sáng tác. Xong là viết được ngay.

### Vị trí dữ liệu

| Loại | Đường dẫn |
|---|---|
| Cấu hình | `~/.ainovel/config.json` (Wizard tự tạo) |
| Cấu hình riêng cho thư mục hiện tại | `./.ainovel/config.json` (ưu tiên cao hơn) |
| Quy tắc viết cá nhân | `~/.ainovel/rules/*.md` hoặc `./.ainovel/rules/*.md` |
| Truyện đang viết | `./output/novel/` — hoặc đặt `NOVEL_DIR` (mục 6) |

⚠️ Chạy binary trực tiếp thì **không** có thư mục `config/`. File cấu hình nằm trong home dir của bạn. Nếu chạy `--headless` mà báo *"headless không hỗ trợ thiết lập lần đầu"*, hãy mở TUI một lần để hoàn tất cấu hình.

### Chạy nhiều bộ truyện

Đặt biến môi trường `NOVEL_DIR` trước khi chạy:

```powershell
$env:NOVEL_DIR = ".\novels\tien-hiep-ky"
kietnovel
```

## 3. Tùy Chọn Ngôn Ngữ Sáng Tác

Trong file cấu hình `~/.ainovel/config.json`, bạn có thể chỉ định trường `"language"`:

- `"language": "vi"` (Mặc định): Toàn bộ dàn ý, nhân vật, bối cảnh thế giới, quy chuẩn văn phong chống AI và nội dung từng chương sẽ được sinh ra bằng Tiếng Việt tự nhiên, mượt mà.
- `"language": "zh"`: Nội dung truyện được sinh ra bằng Tiếng Trung nguyên bản (phù hợp nếu bạn viết truyện Trung hoặc muốn dùng công cụ dịch sau).

### Cách hoạt động

Prompt hệ thống giữ nguyên bản gốc của upstream (tiếng Trung, đã được kiểm chứng và tinh chỉnh) — **không dịch lại** để tránh mất chất lượng. Ngôn ngữ sáng tác được điều khiển ở hai điểm:

| Điểm | `vi` (mặc định) | `zh` |
|---|---|---|
| Lớp văn phong (`voice`) | `assets/voice.md` — quy chuẩn hành văn tiểu thuyết Tiếng Việt | `assets/voice_zh.md` — bản gốc tiếng Trung |
| Chỉ dẫn đầu ra | Gắn vào Architect/Writer/Editor: *toàn bộ sản phẩm phải viết bằng Tiếng Việt tự nhiên, đúng chuẩn văn phong* | Không gắn — protocol vốn đã là tiếng Trung |

Bản chất: một câu lệnh buộc ngôn ngữ + bộ quy chuẩn văn phong tương ứng, thay vì duy trì hai bản prompt song song dễ lệch nội dung.

💡 Ghi chú: Bảng điều khiển TUI, thanh trạng thái, menu và các thông báo lỗi luôn hiển thị 100% bằng Tiếng Việt, kể cả khi `language` là `zh`.

## 4. Cấu Hình Nhà Cung Cấp AI (LLM)

File cấu hình: `~/.ainovel/config.json`. Lần chạy đầu tiên Setup Wizard sẽ tạo file này cho bạn — dưới đây là các ví dụ tham khảo.

### DeepSeek (rẻ nhất, nên thử trước)

```json
{
  "language": "vi",
  "provider": "deepseek",
  "model": "deepseek-chat",
  "providers": {
    "deepseek": {
      "api_key": "YOUR_DEEPSEEK_API_KEY"
    }
  },
  "context_window": 64000,
  "style": "default"
}
```

### OpenRouter

```json
{
  "language": "vi",
  "provider": "openrouter",
  "model": "anthropic/claude-3.5-sonnet",
  "providers": {
    "openrouter": {
      "api_key": "sk-or-v1-YOUR_OPENROUTER_API_KEY"
    }
  },
  "context_window": 128000,
  "reasoning_effort": "off",
  "style": "default"
}
```

Google Gemini:

```json
{
  "language": "vi",
  "provider": "gemini",
  "model": "gemini-2.5-pro",
  "providers": {
    "gemini": {
      "api_key": "YOUR_GEMINI_API_KEY"
    }
  },
  "context_window": 1000000,
  "style": "default"
}
```

### Phối hợp nhiều Model theo vai trò

Hệ thống cho phép gán model mạnh làm Biên tập / Kiến trúc sư và model rẻ, nhanh làm Người viết — cắt đáng kể chi phí:

```json
{
  "language": "vi",
  "provider": "deepseek",
  "model": "deepseek-chat",
  "providers": {
    "deepseek": { "api_key": "YOUR_DEEPSEEK_API_KEY" },
    "openrouter": { "api_key": "sk-or-v1-YOUR_KEY" }
  },
  "roles": {
    "architect": { "provider": "openrouter", "model": "anthropic/claude-3.5-sonnet" },
    "editor": { "provider": "openrouter", "model": "anthropic/claude-3.5-sonnet" },
    "writer": { "provider": "deepseek", "model": "deepseek-chat" }
  },
  "context_window": 128000,
  "style": "default"
}
```

## 5. Hướng Dẫn Sử Dụng & Bảng Lệnh TUI

### Khởi động TUI & Chế độ sáng tác

Chạy lệnh:

```bash
kietnovel
```

Tại màn hình chào mừng:

- Phím `Tab`: Chuyển đổi giữa 2 chế độ:
  - **Bắt đầu nhanh**: Nhập 1 câu tóm tắt ý tưởng (ví dụ: "Tiểu thuyết tiên hiệp phàm nhân, nhân vật chính cơ trí, quyết đoán"), AI tự động lập dàn ý và sáng tác ngay.
  - **Đồng sáng tác (Co-create)**: AI sẽ trao đổi cùng bạn từng bước để làm rõ thiết lập thế giới, nhân vật, cốt truyện trước khi viết.
- Phím `Enter`: Bắt đầu quá trình sáng tác.
- Phím `/`: Mở thanh tìm kiếm và thực thi lệnh nhanh (Slash Commands).
- `Ctrl+C` 2 lần: Lưu an toàn toàn bộ trạng thái và thoát ra.

### Danh sách Lệnh Điều Khiển (Slash Commands)

Khi đang ở trong giao diện TUI, bạn có thể gõ `/` để mở bảng chọn lệnh:

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

### Can thiệp thời gian thực (Steer)

Trong lúc AI đang viết, bạn có thể nhập trực tiếp ý kiến sửa đổi vào ô nhập liệu bên dưới bất cứ lúc nào mà không cần tạm dừng hay khởi động lại:

```text
❯ Cho nhân vật phụ A hy sinh ở cuối chương này để tạo bước ngoặt cảm xúc lớn
```

Sau khi nhấn Enter, Arbiter sẽ tự động đánh giá phạm vi ảnh hưởng và điều phối Writer/Editor cập nhật mạch truyện.

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

Mỗi bộ truyện có văn phong (`style/`), checkpoint và tiến độ riêng, không lẫn nhau. Bỏ `NOVEL_DIR` thì dùng `./output/novel` theo thư mục làm việc như bản gốc.

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

> Mỗi giá trị `NOVEL_DIR` là một bộ truyện riêng: văn phong (`style/`), checkpoint và tiến độ đi theo thư mục truyện, không lẫn nhau. Không đặt `NOVEL_DIR` thì dùng `./output/novel` theo thư mục làm việc như bản gốc.

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

Khác với các công cụ AI thông thường lên dàn ý cứng một lần cho cả trăm chương khiến câu chuyện càng về sau càng rỗng và loãng, kietnovel áp dụng cơ chế La bàn (Compass) + Tầm nhìn cuộn:

- Ban đầu chỉ dựng khung 2 Tập (Volume) và dàn ý chi tiết cho Cung đầu tiên (Arc 1).
- Khi viết gần hết một Cung, Editor đánh giá tổng kết và Architect mới tiếp tục mở rộng Cung tiếp theo dựa trên diễn biến thực tế của truyện.

### Quản lý & Nén ngữ cảnh 4 cấp

Để viết được truyện dài 500+ chương mà không bị tràn context window hay mất trí nhớ, hệ thống sử dụng đường ống nén 4 cấp:

1. **ToolResultMicrocompact**: Dọn dẹp kết quả trung gian của các lệnh tool cũ.
2. **LightTrim**: Cắt tỉa các đoạn text dài không còn cần thiết.
3. **StoreSummaryCompact**: Thay thế tin nhắn cũ bằng bản tóm tắt đã lưu trong Store (0 tốn LLM token).
4. **FullSummary**: Dùng prompt chuyên dụng tóm tắt ngữ cảnh tự sự (giữ vững trạng thái nhân vật, manh mối phục bút).

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

Tạo file `.md` bất kỳ trong thư mục `~/.ainovel/rules/` hoặc `./.ainovel/rules/` và viết bằng ngôn ngữ tự nhiên:

```text
"Nhân vật chính quyết đoán, không thánh mẫu"
"Tăng cường miêu tả cảm giác cơ thể và khung cảnh xung quanh"
"Mỗi chương khoảng 3000 từ"
"Không dùng các từ sáo rỗng như: 'ở một mức độ nào đó', 'như thể', 'bất giác'"
```

Hệ thống sẽ tự động tổng hợp các yêu cầu này vào bộ quy tắc kiểm duyệt của tác phẩm.

## 11. Tech Stack & License

- **Ngôn ngữ**: Go (Hiệu năng cao, kiểm soát chặt chẽ concurrency và I/O)
- **Agent Core**: agentcore (Tool-calling + Streaming)
- **LLM Interface**: litellm
- **TUI Framework**: Bubble Tea & Lip Gloss
- **Cấu trúc prompt**: file Markdown embed vào binary (`//go:embed`), nạp theo ngôn ngữ lúc khởi động — không cần mạng để chọn bộ prompt
- **Dependency**: 10 gói trực tiếp, tất cả từ nguồn công khai. `govulncheck` sạch, không có dependency ngoài stdlib ở tầng LLM

### License

Dự án được phân phối dưới giấy phép mã nguồn mở MIT License (kế thừa từ upstream). Bản Việt hóa và phát triển bởi cộng đồng. Chúc bạn có những tác phẩm tuyệt vời!

---

## Ghi nhận nguồn

| Thành phần | Nguồn |
|---|---|
| Kiến trúc Engine, đa agent, quy hoạch cuộn, checkpoint, toàn bộ lệnh TUI, **và toàn bộ bộ prompt/tài liệu tham chiếu** | [voocel/ainovel-cli](https://github.com/voocel/ainovel-cli) — MIT |
| Việt hóa TUI, `NOVEL_DIR`, quy chuẩn văn phong tiếng Việt (`assets/voice.md`), cơ chế `language`, README | repo này |

Bộ prompt giữ nguyên bản gốc của upstream: đã được kiểm chứng về chất lượng, dịch lại chỉ làm tăng rủi ro lệch nghĩa. Việt hóa tập trung ở tầng giao diện và tầng văn phong — nơi thực sự quyết định trải nghiệm và chất lượng đầu ra.

Cải tiến chung nên gửi PR về upstream. Repo này tập trung vào Việt hóa và trải nghiệm người dùng Việt.
