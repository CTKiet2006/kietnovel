# kietnovel

> **Tiếng Việt** · [English](README.en.md) · [中文](README.zh.md)

> Originally inspired by and initially based on AINovel-CLI by voocel. KietNovel is now independently developed — an AI engine for writing full-length novels, with a trilingual interface (Vietnamese / English / Chinese).

Công cụ sáng tác tiểu thuyết AI bán tự động: Engine xác định chạy trọn một bộ truyện, model chỉ được gọi đúng chỗ cần phán đoán — Engine điều phối 3 agent tự chủ Architect / Writer / Editor theo bảng quyết định, Arbiter ngữ nghĩa chỉ thức dậy khi cần. Từ một câu ý tưởng tới tiểu thuyết hoàn chỉnh.

## Điểm Nổi Bật Của Bản Việt Hóa & Đa Ngôn Ngữ

- 🌐 **Giao diện TUI đa ngôn ngữ (vi / en / zh)**: Từ Setup Wizard cài đặt ban đầu, màn hình chào mừng, thanh trạng thái, Activity stream trực tiếp, bảng quản lý Provider/Model (`/config`, `/model`) đến thông báo lỗi và phím tắt — tất cả dịch theo ngôn ngữ đang chọn, chuẩn mực và tự nhiên. Đổi giữa chừng bằng lệnh `/language`.
- ✍️ **Tùy chọn ngôn ngữ sáng tác truyện**: Viết truyện bằng Tiếng Việt (mặc định), Tiếng Anh hay Tiếng Trung giản thuận qua `"language": "vi"` / `"en"` / `"zh"`. Bộ prompt giữ nguyên bản gốc (đã kiểm chứng), chỉ đổi lớp văn phong kèm chỉ dẫn buộc ngôn ngữ đầu ra.
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

Không cần Docker. Ba bước:

```powershell
# 1. Cài (một lệnh, không cần cài Go)
irm https://raw.githubusercontent.com/CTKiet2006/kietnovel/v1.3.3/scripts/install-windows.ps1 | iex

# 2. Mở terminal mới, rồi chạy
kietnovel

# 3. Lần đầu: Setup Wizard hỏi Provider, API key, Model và ngôn ngữ (vi / en / zh)
```

Cài trong khoảng 5.7 MB. Script tự tải bản dựng sẵn đúng kiến trúc máy (x86_64 / arm64),
kiểm tra SHA-256, giải nén vào `%LOCALAPPDATA%\kietnovel\bin` và thêm thư mục đó vào
user PATH.

**Cập nhật:** chạy lại lệnh trên. Thoát `kietnovel` trước — Windows không cho ghi đè
file đang chạy.

**Hạ về bản cũ:**

```powershell
powershell -ExecutionPolicy Bypass -File scripts\install-windows.ps1 -Version v1.3.2
```

Nếu không có Go, tải script rồi chạy:

```powershell
irm https://raw.githubusercontent.com/CTKiet2006/kietnovel/v1.3.3/scripts/install-windows.ps1 -OutFile ki.ps1
.\ki.ps1 -Version v1.3.2
```

Bản hiện có: v1.3.4, v1.3.3, v1.3.2, v1.2.5, v1.2.4, v1.2.3, v1.2.2, v1.2.1, v1.1.0.

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

### Cách hoạt động

Bộ prompt giữ nguyên bản gốc của tác giả, viết bằng tiếng Trung — đã được kiểm chứng về chất lượng nên giữ nguyên. Ngôn ngữ sáng tác được điều khiển ở hai điểm:

| Điểm | `vi` (mặc định) | `en` | `zh` |
|---|---|---|---|
| Lớp văn phong (`voice`) | `assets/voice.md` - quy chuẩn nhà văn tiểu thuyết Tiếng Việt | `assets/voice_en.md` - quy chuẩn tiểu thuyết tiếng Anh bản ngữ | `assets/voice_zh.md` - bản gốc tiếng Trung |
| Chỉ dẫn đầu ra | Gắn vào Architect/Writer/Editor: *toàn bộ sản phẩm phải viết bằng Tiếng Việt tự nhiên, đúng chuẩn văn phong* | Gắn tương tự, bằng tiếng Anh | Không gắn - protocol vốn đã là tiếng Trung |

Bản chất: một câu lệnh buộc ngôn ngữ + một bộ quy chuẩn văn phong tương ứng, thay vì
duy trì hai bản prompt song song dễ lệch nội dung.

### Cách đổi ngôn ngữ

- Lúc cài đặt: Setup Wizard hỏi ngôn ngữ ngay từ đầu.
- Khi đang chạy: gõ `/language` để xem ngôn ngữ hiện tại, hoặc `/language en`
  (cũng nhận `vi`, `zh`) để đổi. Lựa chọn được ghi vào cấu hình và giữ cho các
  lần khởi động sau.

Giao diện đổi ngay. Riêng **ngôn ngữ sáng tác** được nạp một lần lúc khởi động, nên
có hiệu lực đầy đủ từ lần mở kế tiếp - ứng dụng sẽ nhắc việc này ngay sau khi đổi.


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
| `/language [vi\|en\|zh]` | Xem hoặc đổi ngôn ngữ giao diện; lựa chọn được lưu vào cấu hình |

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
