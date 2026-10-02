package i18n

// diagCatalogEn là bản dịch tiếng Anh cho chuỗi của package internal/diag
// (báo cáo chẩn đoán + file xuất meta/diag-export.md).
//
// Tách riêng thay vì nhồi vào catalog_en.go: đây là nhóm chuỗi của một package
// riêng, có vòng đời và người duy trì khác với chuỗi TUI. Trước khi tách, toàn bộ
// 90 chuỗi của diag viết cứng tiếng Trung nên truyện tiếng Việt thì báo cáo
// chẩn đoán hiện ra tiếng Trung.
var diagCatalogEn = map[string]string{
	// rules_flow.go
	"Hàng đợi viết lại chứa chương chưa hoàn thành: [%s]": "Rewrite queue contains unfinished chapters: [%s]",
	"Bất biến trạng thái bị hỏng. Hãy dừng chạy rồi sửa meta/progress.json, gỡ các chương chưa hoàn thành khỏi pending_rewrites; nếu hàng đợi rỗng thì đổi flow thành writing và xoá rewrite_reason.": "A state invariant is broken. Stop, then edit meta/progress.json and remove unfinished chapters from pending_rewrites; if the queue is empty, set flow to writing and clear rewrite_reason.",
	"Chương chờ viết lại: [%s]": "Chapters pending rewrite: [%s]",
	"Kiểm tra tiêu chuẩn duyệt của Editor có quá khắt khe không, hoặc prompt viết lại của Writer có hiệu lực không.": "Check whether Editor's review criteria are too strict, or Writer's rewrite prompt is ineffective.",
	"Khi một chương thất bại lặp lại, engine sẽ tự gỡ khỏi hàng đợi và tiếp tục sáng tác, không cần dọn thủ công.":   "When a chapter keeps failing, the engine drops it from the queue and keeps writing; no manual cleanup needed.",
	"Có chỉ dẫn hướng chưa được dùng": "A steering instruction was never consumed",
	"Chỉ dẫn này đã được lưu nhưng không được bước định đoạn can thiệp dùng đến. Kiểm tra logic khôi phục khi bị gián đoạn, hoặc gửi lại để ghi đè.": "This steer was persisted but never consumed by the intervention arbiter. Check the interrupt-recovery logic, or resubmit to overwrite it.",
	"Giai đoạn/luồng không khớp: phase=%s, flow=%s":                                                                "Phase/flow mismatch: phase=%s, flow=%s",
	"phase=%s không được có flow khác trạng thái ban đầu là %s":                                                    "phase=%s must not have a non-initial flow of %s",
	"Máy trạng thái có thể đã hỏng, cần tự kiểm tra các trường phase và flow trong meta/progress.json.":            "The state machine may be corrupted; inspect the phase and flow fields in meta/progress.json by hand.",
	"Chương bị thiếu số: thiếu [%s]":                                                                               "Chapter numbering has gaps: missing [%s]",
	"commit_chapter có thể đã bị gián đoạn. Kiểm tra meta/pending_commit.json xem có bản ghi chưa hoàn tất không.": "commit_chapter may have been interrupted. Check meta/pending_commit.json for an unfinished commit.",

	// rules_planning.go
	"%s(ch%d đã gieo, đã qua %d chương)":                              "%s(planted at ch%d, %d chapters ago)",
	"Câu tình tiết đứng yên: %d mục quá %d chương chưa được đẩy tiếp": "Foreshadowing stalled: %d items went %d chapters without progress",
	"Nhắc tình tiết trong novel_context có thể chưa nạp được, hoặc prompt Writer thiếu chỉ dẫn đẩy tiếp. Kiểm tra foreshadow_ledger và logic nạp ngữ cảnh.": "Foreshadow reminders in novel_context may not be loading, or Writer's prompt lacks guidance to advance them. Check foreshadow_ledger and the context injection logic.",
	"Chế độ dài hạn thiếu la bàn": "Long-form mode has no compass",
	"Architect nên tạo compass lúc lập kế hoạch ban đầu. Kiểm tra architect-long.md có lệnh tạo compass không.": "Architect should create a compass during initial planning. Check whether architect-long.md contains the compass instruction.",
	"La bàn đã %d chương chưa cập nhật": "Compass not updated for %d chapters",
	"Architect nên cập nhật compass tại ranh giới cung/tập. Kiểm tra architect-long.md có lệnh cập nhật compass không.":                                                                         "Architect should update the compass at arc/volume boundaries. Check architect-long.md for the update instruction.",
	"Dàn ý đã cạn: đã xong %d chương >= đã lên kế hoạch %d chương":                                                                                                                              "Outline exhausted: %d chapters done >= %d planned",
	"Tín hiệu bung cung/mở tập mới có thể chưa kích hoạt. Kiểm tra chiến lược chốt phía Host và logic khôi phục, xác nhận dò biên cung, expand_next_arc hoặc append_volume có chạy đúng không.": "The expand-arc/new-volume signal may not have fired. Check the host-side commit strategy and recovery logic, and confirm arc-boundary detection, expand_next_arc or append_volume actually run.",
	"Thiếu tóm tắt: %d chương không có tóm tắt":                                                                                                                                                 "Missing summaries: %d chapters have none",
	"Tóm tắt là mấu chốt của tính liên tục ngữ cảnh. Kiểm tra logic ghi tóm tắt trong commit_chapter có chạy đúng không.":                                                                       "Summaries are the backbone of context continuity. Check whether commit_chapter writes them correctly.",

	// rules_quality.go
	"Chiều [%s] điểm thấp kéo dài (trung bình %.0f)": "Dimension [%s] scores low over time (avg %.0f)",
	"Tổng %d lần đánh giá, điểm trung bình %.1f":     "%d reviews total, average %.1f",
	"Kiểm tra hướng dẫn về %s trong prompt Writer có rõ không, hoặc tiêu chuẩn chấm %s trong prompt Editor có hợp lý không.": "Check whether Writer's prompt explains %s clearly, or whether Editor's scoring criteria for %s are reasonable.",
	"Tỉ lệ thực hiện hợp đồng thấp (%.0f%% chưa đạt)":                                                                        "Contract fulfilment rate is low (%.0f%% missed)",
	"Chưa đạt: [%s], tổng %d/%d": "Missed: [%s], total %d/%d",
	"Có thể Writer chưa đọc contract, hoặc contract.required_beats quá khắt khe. Kiểm tra sự phối hợp giữa plan_chapter và writer.md.":                                                            "Writer may not be reading the contract, or contract.required_beats is too aggressive. Check how plan_chapter and writer.md fit together.",
	"Móc cuối chương liên tục yếu (liên tiếp %d chương)":                                                                                                                                          "Chapter-end hooks stay weak (%d chapters in a row)",
	"Kiểm tra việc thực hiện hook_goal trong writer.md có rõ không, cần thì nêu rõ ham muốn đọc tiếp của chương trong plan_chapter, và hiệu chỉnh tiêu chuẩn nêu bằng chứng cho hook của Editor.": "Check whether hook_goal is executed clearly in writer.md, spell out the chapter's pull-to-read in plan_chapter if needed, and calibrate Editor's evidence standard for hooks.",
	"ch%d(%d payoff)": "ch%d(%d payoff)",
	"Chưa hoàn thành ở các chương: [%s], tổng %d/%d": "Unfulfilled in chapters: [%s], total %d/%d",
	"Kiểm tra payoff_points của plan_chapter có quá nhiều hoặc quá rỗng không, đảm bảo Writer hoàn thành rõ ràng trong văn bản chứ không chỉ bày đặt.": "Check whether plan_chapter's payoff_points are too many or too empty, and make sure Writer delivers them in the prose rather than only setting them up.",
	"Tỉ lệ hoàn thành điểm tình tiết hơi thấp (%.0f%% chưa đạt)":                                                                                       "Payoff fulfilment rate is low (%.0f%% missed)",
	"Tỉ lệ viết lại quá cao (%d/%d = %.0f%%)":                                                                                                          "Rewrite rate too high (%d/%d = %.0f%%)",
	"Tổng %d lần đánh giá, %d lần rewrite":                                                                                                             "%d reviews total, %d rewrites",
	"Writer liên tục cho ra nội dung dưới ngưỡng của Editor. Kiểm tra tiêu chuẩn chất lượng trong prompt Writer đã khớp với tiêu chuẩn duyệt của Editor chưa.": "Writer keeps producing content below Editor's threshold. Check whether Writer's quality bar is aligned with Editor's review standard.",
	"ch%d(%d chữ, %.0f%%)": "ch%d(%d words, %.0f%%)",
	"Số chữ của chương bất thường (trung bình %d chữ)": "Chapter word count is abnormal (avg %d words)",
	"Chương quá ngắn có thể là output bị cắt (giới hạn token), chương quá dài có thể nuốt cửa sổ ngữ cảnh. Kiểm tra cấu hình max_tokens của model.": "Very short chapters may be truncated output (token limit); very long ones may eat the context window. Check the model's max_tokens setting.",

	// rules_context.go
	"%s(chưa từng xuất hiện trong tóm tắt)":             "%s(never appeared in any summary)",
	"%s(xuất hiện lần cuối ở ch%d, đã vắng %d chương)":  "%s(last seen at ch%d, absent for %d chapters)",
	"Nhân vật biến mất: %d nhân vật chính vắng mặt lâu": "Characters vanished: %d core characters absent for a long time",
	"Có thể Writer đã mất dấu nhân vật này. Cân nhắc gửi chỉ dẫn can thiệp trực tiếp ở ô nhập để đưa nhân vật trở lại, hoặc hạ tier của họ trong characters.json.": "Writer may have lost track of this character. Consider steering it back via the input box, or demote its tier in characters.json.",
	"Dòng thời gian trống": "Timeline is empty",
	"Việc trích xuất dòng thời gian trong commit_chapter có thể chưa chạy. Kiểm tra output của Writer có trường timeline không.":                                           "Timeline extraction in commit_chapter may not be running. Check whether Writer's output has a timeline field.",
	"Thiếu dòng thời gian: %d chương không có sự kiện":                                                                                                                     "Timeline gaps: %d chapters have no event",
	"Việc trích xuất dòng thời gian trong commit_chapter có thể hỏng một phần. Kiểm tra định dạng trường timeline trong output của Writer.":                                "Timeline extraction in commit_chapter may be partially broken. Check the timeline field format in Writer's output.",
	"Dữ liệu quan hệ đứng yên: lần cập nhật gần nhất ở chương %d":                                                                                                          "Relationship data stalled: last updated at chapter %d",
	"Việc cập nhật quan hệ trong commit_chapter có thể đã ngừng, hoặc quan hệ trong truyện thực sự không thay đổi. Kiểm tra trường relationships trong output của Writer.": "Relationship updates in commit_chapter may have stopped, or the relationships genuinely did not change. Check the relationships field in Writer's output.",

	// runtime_rules.go
	"Công cụ báo lỗi giống nhau lặp lại": "The same tool error keeps repeating",
	"Cùng một công cụ gần đây trả về cùng một lỗi, thường do tham số của model không hợp lệ hoặc hợp đồng công cụ lệch; kiểm tra xác thực tool của agentcore và quy ước tham số trong prompt (xem #34).": "The same tool returns the same error repeatedly, usually because the model's arguments are invalid or violate the tool contract; check agentcore's tool validation and the prompt's argument conventions (see #34).",
	"Tham số lặp lại không phân tích được": "Arguments keep failing to parse",
	"Tham số model gửi sang không phân tích được mà vẫn thử lại; xem agentcore có ép kiểu lỏng cho loại đó không (xem #34).": "The model's arguments fail to parse but it retries anyway; check whether agentcore coerces that type leniently (see #34).",
	"Checkpoint đứng ở cùng một step": "Checkpoint stuck on the same step",
	"Liên tục dừng ở `%s` ×%d":        "Stopped at `%s` ×%d in a row",
	"Cùng một step bị ghi lặp mà không tiến; kết hợp với chữ ký lặp ở trên để xác định sub-agent nào bị kẹt.": "The same step is written repeatedly without advancing; combine with the repeated signatures above to find which sub-agent is stuck.",
	"Luồng bị ngắt thường xuyên (stream_idle)": "Streams are frequently interrupted (stream_idle)",
	"Phía trên lâu không trả token nên watchdog giết nhầm; với model suy nghĩ chậm hãy tăng streamIdleTimeout, hoặc kiểm tra độ ổn định kết nối của provider (xem #32).": "Upstream stopped sending tokens so the watchdog killed it wrongly; raise streamIdleTimeout for slow-thinking models, or check provider connection stability (see #32).",

	// diag.go / planner.go / runtime.go
	"Nạp dữ liệu thất bại: %s": "Failed to load data: %s",
	"File có thể hỏng hoặc thiếu quyền, nên kết quả của các quy tắc chẩn đoán liên quan có thể không đầy đủ.": "The file may be corrupt or unreadable, so related diagnostic results may be incomplete.",
	"Sửa lỗi máy trạng thái":                                             "Fix state machine",
	"Máy trạng thái bất thường: ":                                        "State machine anomaly: ",
	". Hãy kiểm tra và sửa phase/flow của progress trước khi chạy tiếp.": ". Check and fix progress phase/flow before running again.",
	"Xử lý dàn ý đã cạn":                                                 "Handle exhausted outline",
	"Số chương đã xong đạt giới hạn đã lên kế hoạch. Hãy gọi Architect bung cung kế tiếp hoặc thêm tập mới trước khi viết tiếp.": "Completed chapters reached the planned limit. Call Architect to expand the next arc or append a volume before continuing.",
	"Xử lý chỉ dẫn người dùng chưa dùng": "Handle unconsumed user steer",
	"Còn chỉ dẫn của người dùng chưa được dùng. Hãy xử lý pending steer trước rồi mới tiếp tục việc đang dở.": "A user steer is still unconsumed. Handle the pending steer before continuing the current task.",
	" (phần cuối)": " (tail)",

	// export.go — headings and labels of meta/diag-export.md
	"Sinh lúc":                      "Generated at",
	"Môi trường":                    "Environment",
	"Giai đoạn":                     "Phase",
	"Chương":                        "Chapters",
	"Số chữ":                        "Words",
	"Kế hoạch":                      "Planning",
	"Phát hiện chẩn đoán (runtime)": "Diagnostic findings (runtime)",
	"Không phát hiện bất thường runtime.": "No runtime anomalies found.",
	"Bằng chứng:":            "Evidence:",
	"Tín hiệu runtime":       "Runtime signals",
	"Step hiện tại:":         "Current step:",
	"Kẹt: liên tục dừng tại": "Stuck: repeatedly stopped at",
	"Chữ ký tần suất cao (cửa sổ gần đây ≥3 lần, gồm cả lặp tool bình thường, chỉ để tham khảo):": "High-frequency signatures (≥3 times in the recent window, including normal tool repeats; for reference only):",
	"Sinh lặp cùng một đoạn văn bản (cùng sha):":                                                  "Same text generated repeatedly (same sha):",
	"Phân loại lỗi trong log:": "Log error categories:",
	"Log":                      "Log",
	"chặn":                     "blocks",
	"Không có tín hiệu bất thường runtime nào rõ ràng.": "No obvious runtime anomaly signals.",
	"Đuôi khung hành vi (%d mục)":                       "Behaviour skeleton tail (%d entries)",
	"(không có nhật ký phiên)":                          "(no session log)",
	"Tự kiểm tra che thông tin":                         "Redaction self-check",
	"Số khối văn bản đã che:":                           "Redacted text blocks:",
	"chỗ":                                               "places",
	"Văn bản truyện lọt ra ngoài:":                      "Story text leaked:",
	"Nguồn dữ liệu:":                                    "Data sources:",
	"Đã che thông tin nhạy cảm: văn bản truyện / prompt / suy nghĩ đã bị gỡ bỏ, chỉ giữ lại khung hành vi. Có thể dán thẳng vào issue.": "Redacted: story text / prompts / thinking removed, only the behaviour skeleton kept. Safe to paste into an issue.",
}
