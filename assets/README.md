# Bản đồ assets

Trước khi thêm "một đoạn / một tài liệu / một quy tắc" vào hệ thống, tra bảng dưới để xác định nó thuộc đâu, rồi xem cách nối dây.

| Thư mục | Chứa gì | Ai tiêu thụ | Cách nối |
|---|---|---|---|
| `prompts/` | System prompt của Worker (writer / editor / architect×2), prompt định đoạn của Arbiter và prompt tác vụ một lần (import / simulation / revision) | `agents/build.go`, `internal/arbiter`, runner của imp / sim / revision | Trường Prompts trong `load.go`. Lưu ý: `simulation_guidance` được tiêm lúc load nên không thấy trong file md |
| `references/` | Tài liệu kiến thức viết không gắn với thể loại. Không đưa vào system prompt; `novel_context` cắt theo vai trò / chương rồi chèn vào `reference_pack` | writer / editor / architect | **Ba chỗ nối**: thêm trường vào `tools.References` + đọc ở `load.go` loadReferences + chèn ở `novel_context.go` (writerReferences / architectReferences). Thả file vào thư mục sẽ không tự động được nạp |
| `references/genres/<style>/` | Kiến thức riêng theo thể loại (style-references / arc-templates) | như trên, chỉ nạp khi `style != default` | `load.go` loadReferences |
| `rules/` | Thư mục rule cũ đã bỏ; baseline máy móc đã chuyển vào code, quy tắc người dùng đến từ snapshot ngôn ngữ tự nhiên của `~/.kietnovel/rules/*.md` / `./.kietnovel/rules/*.md` | `userrules.Service` chuẩn hóa thành `meta/user_rules.json`; `novel_context` chèn; `commit_chapter` kiểm tra | Baseline tích hợp xem `SystemDefaults()` trong `internal/rules/snapshot.go`; file `.md` của người dùng không định dạng, không YAML, được chuẩn hóa theo ngôn ngữ tự nhiên |
| `styles/<style>.md` | Chỉ dẫn văn phong theo thể loại | Ghép vào system prompt của **writer** (`agents/build.go`) | Tên file chính là giá trị của `config.style`. Cùng một khái niệm thể loại với `references/genres/<style>/` nhưng hai dạng: một là chỉ dẫn văn phong, một là tài liệu kiến thức |
| `voice.md` | Quy chuẩn hành văn cho **Tiếng Việt** (chèn vào `{{VOICE}}` của writer) | `load.go` loadVoice | Khi `language: "vi"` |
| `voice_zh.md` | Quy chuẩn hành văn bản gốc tiếng Trung | `load.go` loadVoice | Khi `language: "zh"` |

## Vì sao chỉ có một bộ prompt

Prompt hệ thống giữ nguyên tiếng Trung của upstream cho cả hai ngôn ngữ. Lý do: chúng đã được kiểm chứng và tinh chỉnh về chất lượng, còn dịch lại chỉ tăng rủi ro lệch nghĩa mà không thu được lợi ích tương xứng — ngôn ngữ đầu ra do lớp văn phong (`voice`) và một chỉ dẫn buộc đầu ra quyết định, không phải do ngôn ngữ của prompt.

Vì vậy đừng tạo `prompts/zh/` hay `prompts/vi/`. Muốn đổi văn phong thì sửa `voice*.md` hoặc đặt override ở `~/.kietnovel/style/` và `<outputDir>/style/`.

## Quyết định một nội dung mới thuộc đâu (hỏi năm câu)

1. Quy trình này **bắt buộc phải được** đảm bảo? → Không viết prompt, hãy viết ràng buộc bằng code (StopAfterTools / chốt chặn tool / Flow Router)
2. Đây là tiêu chí định đoạn? → Quy trình dạng bảng tra đặt ở `internal/flow/router.go`; phán đoán ngữ nghĩa đặt ở `prompts/arbiter-*.md`
3. Đây là chuẩn thẩm mỹ / thực thi của một vai trò? → `prompts/<role>.md`
4. Đây là quy tắc mặc định liệt kê được bằng máy (từ cấm / ngưỡng)? → `SystemDefaults()` trong `internal/rules/snapshot.go`; quy tắc tùy chỉnh của người dùng viết vào `.kietnovel/rules/*.md` và được snapshot chuẩn hóa tiêu thụ (độ dài / khối lượng là ràng buộc mềm ngữ nghĩa, đi qua preferences, không làm quy tắc máy)
5. Đây là tài liệu kiến thức viết? → `references/` (nhớ ba chỗ nối)

## Bảo đảm tính nhất quán

Các đường dẫn bao thư mà prompt tham chiếu (`working_memory.*`…) phải khớp với `novel_context`. Hình dạng tham số tool chỉ được định nghĩa trong Schema của tool; prompt chỉ bổ sung ngữ nghĩa nghiệp vụ mà Schema không diễn đạt được, không sao chép lại danh sách tham số JSON hay ví dụ về hình dạng.

Prompt có thể mô tả cách thực thi của một Worker cụ thể, nhưng định tuyến toàn cục, chuyển trạng thái và logic khôi phục chỉ lấy code làm chuẩn. Bước nào xác định được từ dữ kiện trong Store thì đưa vào Router/Tool; chỉ phán đoán cần hiểu nội dung tiểu thuyết hoặc ý định người dùng mới để lại cho model.
