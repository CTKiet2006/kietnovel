package i18n

// diagCatalogZh là bản dịch tiếng Trung cho chuỗi của package internal/diag.
// Xem catalog_diag_en.go để biết vì sao tách riêng.
//
// Khoá PHẢI chép nguyên văn từ catalog_diag_en.go, kể cả dấu chấm tiếng Việt.
// TestCatalogDoiKhopNhau bắt lệch khoá; TestMoiChuoiTDeuCoBanDich bắt khoá
// không khớp với literal trong mã nguồn.
var diagCatalogZh = map[string]string{
	// rules_flow.go
	"Hàng đợi viết lại chứa chương chưa hoàn thành: [%s]": "返工队列包含未完成章节：[%s]",
	"Bất biến trạng thái bị hỏng. Hãy dừng chạy rồi sửa meta/progress.json, gỡ các chương chưa hoàn thành khỏi pending_rewrites; nếu hàng đợi rỗng thì đổi flow thành writing và xoá rewrite_reason.": "状态不变量已损坏。请停止运行后编辑 meta/progress.json，移除 pending_rewrites 中未完成的章节；若队列为空，将 flow 改为 writing 并清空 rewrite_reason。",
	"Chương chờ viết lại: [%s]": "待改写章节：[%s]",
	"Kiểm tra tiêu chuẩn duyệt của Editor có quá khắt khe không, hoặc prompt viết lại của Writer có hiệu lực không.": "检查 Editor 的评审标准是否过严，或 Writer 的改写 prompt 是否有效。",
	"Khi một chương thất bại lặp lại, engine sẽ tự gỡ khỏi hàng đợi và tiếp tục sáng tác, không cần dọn thủ công.":   "某章返工反复失败时，引擎会自动将其移出队列并继续后续创作，无需人工清理。",
	"Có chỉ dẫn hướng chưa được dùng": "存在未消费的转向指令",
	"Chỉ dẫn này đã được lưu nhưng không được bước định đoạn can thiệp dùng đến. Kiểm tra logic khôi phục khi bị gián đoạn, hoặc gửi lại để ghi đè.": "该 steer 已被持久化，但未被干预裁定流程消费。请检查中断恢复逻辑，或重新提交以覆盖。",
	"Giai đoạn/luồng không khớp: phase=%s, flow=%s":                                                                "阶段/流程状态不匹配：phase=%s, flow=%s",
	"phase=%s không được có flow khác trạng thái ban đầu là %s":                                                    "phase=%s 不应出现非初始 flow=%s",
	"Máy trạng thái có thể đã hỏng, cần tự kiểm tra các trường phase và flow trong meta/progress.json.":            "状态机可能已损坏，需手动检查 meta/progress.json 中的 phase 与 flow 字段。",
	"Chương bị thiếu số: thiếu [%s]":                                                                               "章节跳号：缺少 [%s]",
	"commit_chapter có thể đã bị gián đoạn. Kiểm tra meta/pending_commit.json xem có bản ghi chưa hoàn tất không.": "commit_chapter 可能中途中断。请检查 meta/pending_commit.json 是否存在未完成的提交。",

	// rules_planning.go
	"%s(ch%d đã gieo, đã qua %d chương)":                              "%s（ch%d 埋下，已过 %d 章）",
	"Câu tình tiết đứng yên: %d mục quá %d chương chưa được đẩy tiếp": "伏笔停滞：%d 条超过 %d 章未推进",
	"Nhắc tình tiết trong novel_context có thể chưa nạp được, hoặc prompt Writer thiếu chỉ dẫn đẩy tiếp. Kiểm tra foreshadow_ledger và logic nạp ngữ cảnh.": "novel_context 的伏笔提醒可能未加载，或 Writer prompt 缺少推进伏笔的指引。请检查 foreshadow_ledger 与上下文注入逻辑。",
	"Chế độ dài hạn thiếu la bàn": "长篇模式缺少指南针",
	"Architect nên tạo compass lúc lập kế hoạch ban đầu. Kiểm tra architect-long.md có lệnh tạo compass không.": "Architect 应在初始规划时创建 compass。请检查 architect-long.md 是否包含 compass 创建指令。",
	"La bàn đã %d chương chưa cập nhật": "指南针已 %d 章未更新",
	"Architect nên cập nhật compass tại ranh giới cung/tập. Kiểm tra architect-long.md có lệnh cập nhật compass không.":                                                                         "Architect 应在弧/卷边界更新 compass。请检查 architect-long.md 是否包含 compass 更新指令。",
	"Dàn ý đã cạn: đã xong %d chương >= đã lên kế hoạch %d chương":                                                                                                                              "大纲耗尽：已完成 %d 章 >= 已规划 %d 章",
	"Tín hiệu bung cung/mở tập mới có thể chưa kích hoạt. Kiểm tra chiến lược chốt phía Host và logic khôi phục, xác nhận dò biên cung, expand_next_arc hoặc append_volume có chạy đúng không.": "展开新弧/新卷的信号可能未触发。请检查宿主侧的提交策略与恢复逻辑，确认弧边界检测、expand_next_arc 或 append_volume 是否正常执行。",
	"Thiếu tóm tắt: %d chương không có tóm tắt":                                                                                                                                                 "缺少摘要：%d 章无摘要",
	"Tóm tắt là mấu chốt của tính liên tục ngữ cảnh. Kiểm tra logic ghi tóm tắt trong commit_chapter có chạy đúng không.":                                                                       "摘要是上下文连续性的关键。请检查 commit_chapter 的摘要写入逻辑是否正常。",

	// rules_quality.go
	"Chiều [%s] điểm thấp kéo dài (trung bình %.0f)": "维度 [%s] 持续低分（均值 %.0f）",
	"Tổng %d lần đánh giá, điểm trung bình %.1f":     "共 %d 次评审，均分 %.1f",
	"Kiểm tra hướng dẫn về %s trong prompt Writer có rõ không, hoặc tiêu chuẩn chấm %s trong prompt Editor có hợp lý không.": "检查 Writer prompt 中关于 %s 的指引是否清晰，或 Editor prompt 的 %s 评分标准是否合理。",
	"Tỉ lệ thực hiện hợp đồng thấp (%.0f%% chưa đạt)":                                                                        "合同履约率低（%.0f%% 未达成）",
	"Chưa đạt: [%s], tổng %d/%d": "未达成：[%s]，共 %d/%d",
	"Có thể Writer chưa đọc contract, hoặc contract.required_beats quá khắt khe. Kiểm tra sự phối hợp giữa plan_chapter và writer.md.":                                                            "可能是 Writer 未读取 contract，或 contract 的 required_beats 过于激进。请检查 plan_chapter 与 writer.md 的配合。",
	"Móc cuối chương liên tục yếu (liên tiếp %d chương)":                                                                                                                                          "章末钩子连续偏弱（连续 %d 章）",
	"Kiểm tra việc thực hiện hook_goal trong writer.md có rõ không, cần thì nêu rõ ham muốn đọc tiếp của chương trong plan_chapter, và hiệu chỉnh tiêu chuẩn nêu bằng chứng cho hook của Editor.": "检查 writer.md 中 hook_goal 的执行是否清晰，必要时在 plan_chapter 中明确本章的追读欲望，并校准 Editor 对 hook 的举证标准。",
	"ch%d(%d payoff)": "ch%d（%d 项 payoff）",
	"Chưa hoàn thành ở các chương: [%s], tổng %d/%d": "未兑现章节：[%s]，共 %d/%d",
	"Kiểm tra payoff_points của plan_chapter có quá nhiều hoặc quá rỗng không, đảm bảo Writer hoàn thành rõ ràng trong văn bản chứ không chỉ bày đặt.": "检查 plan_chapter 的 payoff_points 是否过多或过空，确保 Writer 在正文中明确兑现，而不只是铺垫。",
	"Tỉ lệ hoàn thành điểm tình tiết hơi thấp (%.0f%% chưa đạt)":                                                                                       "爽点/情节点兑现率偏低（%.0f%% 未达成）",
	"Tỉ lệ viết lại quá cao (%d/%d = %.0f%%)":                                                                                                          "改写率过高（%d/%d = %.0f%%）",
	"Tổng %d lần đánh giá, %d lần rewrite":                                                                                                             "共 %d 次评审，%d 次 rewrite",
	"Writer liên tục cho ra nội dung dưới ngưỡng của Editor. Kiểm tra tiêu chuẩn chất lượng trong prompt Writer đã khớp với tiêu chuẩn duyệt của Editor chưa.": "Writer 持续产出低于 Editor 阈值的内容。请检查 Writer prompt 的质量标准是否与 Editor 的评审标准对齐。",
	"ch%d(%d chữ, %.0f%%)": "ch%d（%d 字，%.0f%%）",
	"Số chữ của chương bất thường (trung bình %d chữ)": "章节字数异常（均值 %d 字）",
	"Chương quá ngắn có thể là output bị cắt (giới hạn token), chương quá dài có thể nuốt cửa sổ ngữ cảnh. Kiểm tra cấu hình max_tokens của model.": "极短章节可能是输出被截断（token 限制），极长章节可能占用过多上下文窗口。请检查模型的最大 token 配置。",

	// rules_context.go
	"%s(chưa từng xuất hiện trong tóm tắt)":             "%s（从未出现在摘要中）",
	"%s(xuất hiện lần cuối ở ch%d, đã vắng %d chương)":  "%s（最后出现于 ch%d，已缺席 %d 章）",
	"Nhân vật biến mất: %d nhân vật chính vắng mặt lâu": "角色消失：%d 个核心角色长期缺席",
	"Có thể Writer đã mất dấu nhân vật này. Cân nhắc gửi chỉ dẫn can thiệp trực tiếp ở ô nhập để đưa nhân vật trở lại, hoặc hạ tier của họ trong characters.json.": "可能 Writer 已丢失对该角色的追踪。可考虑直接在输入框提交干预指令以重新引入该角色，或在 characters.json 中下调其 tier。",
	"Dòng thời gian trống": "时间线为空",
	"Việc trích xuất dòng thời gian trong commit_chapter có thể chưa chạy. Kiểm tra output của Writer có trường timeline không.":                                           "commit_chapter 的时间线提取可能未生效。请检查 Writer 输出是否包含 timeline 字段。",
	"Thiếu dòng thời gian: %d chương không có sự kiện":                                                                                                                     "时间线缺口：%d 章无事件记录",
	"Việc trích xuất dòng thời gian trong commit_chapter có thể hỏng một phần. Kiểm tra định dạng trường timeline trong output của Writer.":                                "commit_chapter 的时间线提取可能部分失效。请检查 Writer 输出中 timeline 字段的格式。",
	"Dữ liệu quan hệ đứng yên: lần cập nhật gần nhất ở chương %d":                                                                                                          "关系数据停滞：最新更新在第 %d 章",
	"Việc cập nhật quan hệ trong commit_chapter có thể đã ngừng, hoặc quan hệ trong truyện thực sự không thay đổi. Kiểm tra trường relationships trong output của Writer.": "commit_chapter 的关系更新可能已停止，或故事关系确实没有变化。请检查 Writer 输出中的 relationships 字段。",

	// runtime_rules.go
	"Công cụ báo lỗi giống nhau lặp lại": "工具反复报同一错误",
	"Cùng một công cụ gần đây trả về cùng một lỗi, thường do tham số của model không hợp lệ hoặc hợp đồng công cụ lệch; kiểm tra xác thực tool của agentcore và quy ước tham số trong prompt (xem #34).": "近端同一工具反复返回同一错误，多为模型参数不合规或与工具契约不符；请检查 agentcore 的工具校验与 prompt 的参数约定（参见 #34）。",
	"Tham số lặp lại không phân tích được": "参数反复无法解析",
	"Tham số model gửi sang không phân tích được mà vẫn thử lại; xem agentcore có ép kiểu lỏng cho loại đó không (xem #34).": "模型发来的参数无法解析却不断重试；请看 agentcore 是否对该类型做了宽松强转（参见 #34）。",
	"Checkpoint đứng ở cùng một step": "checkpoint 停滞在同一 step",
	"Liên tục dừng ở `%s` ×%d":        "连续停在 `%s` ×%d",
	"Cùng một step bị ghi lặp mà không tiến; kết hợp với chữ ký lặp ở trên để xác định sub-agent nào bị kẹt.": "同一 step 反复写入却不推进；结合上面的重复签名即可定位是哪个子代理卡住。",
	"Luồng bị ngắt thường xuyên (stream_idle)": "流式中断频发（stream_idle）",
	"Phía trên lâu không trả token nên watchdog giết nhầm; với model suy nghĩ chậm hãy tăng streamIdleTimeout, hoặc kiểm tra độ ổn định kết nối của provider (xem #32).": "上游长时间不吐 token，被 watchdog 误杀；对慢思考模型请调大 streamIdleTimeout，或排查 provider 连接稳定性（参见 #32）。",

	// diag.go / planner.go / runtime.go
	"Nạp dữ liệu thất bại: %s": "工件加载失败：%s",
	"File có thể hỏng hoặc thiếu quyền, nên kết quả của các quy tắc chẩn đoán liên quan có thể không đầy đủ.": "文件可能已损坏或权限不足，相关诊断规则的结果可能不完整。",
	"Sửa lỗi máy trạng thái":                                             "状态机异常修复",
	"Máy trạng thái bất thường: ":                                        "状态机异常：",
	". Hãy kiểm tra và sửa phase/flow của progress trước khi chạy tiếp.": "。请先检查并修正 progress 的 phase/flow 状态，再继续运行。",
	"Xử lý dàn ý đã cạn":                                                 "大纲耗尽处理",
	"Số chương đã xong đạt giới hạn đã lên kế hoạch. Hãy gọi Architect bung cung kế tiếp hoặc thêm tập mới trước khi viết tiếp.": "已完成章节数达到已规划上限。请优先调用 Architect 展开下一弧或追加新卷，再继续写作。",
	"Xử lý chỉ dẫn người dùng chưa dùng": "消费未处理的用户干预",
	"Còn chỉ dẫn của người dùng chưa được dùng. Hãy xử lý pending steer trước rồi mới tiếp tục việc đang dở.": "存在未消费的用户干预指令，请优先处理 pending steer 后再继续当前任务。",
	" (phần cuối)": "（尾部）",

	// export.go — headings and labels of meta/diag-export.md
	"Sinh lúc":                      "生成时间",
	"Môi trường":                    "环境",
	"Giai đoạn":                     "阶段",
	"Chương":                        "章节",
	"Số chữ":                        "字数",
	"Kế hoạch":                      "规划",
	"Phát hiện chẩn đoán (runtime)": "诊断发现（运行时）",
	"Không phát hiện bất thường runtime.": "未发现运行时异常。",
	"Bằng chứng:":            "证据：",
	"Tín hiệu runtime":       "运行时信号",
	"Step hiện tại:":         "当前 step：",
	"Kẹt: liên tục dừng tại": "卡住：连续停在",
	"Chữ ký tần suất cao (cửa sổ gần đây ≥3 lần, gồm cả lặp tool bình thường, chỉ để tham khảo):": "高频签名（近端窗口 ≥3 次，含正常重复工具，仅供参考）：",
	"Sinh lặp cùng một đoạn văn bản (cùng sha):":                                                  "反复生成同段文本（同 sha）：",
	"Phân loại lỗi trong log:": "日志错误分类：",
	"Log":                      "日志",
	"chặn":                     "拦截",
	"Không có tín hiệu bất thường runtime nào rõ ràng.": "无明显运行时异常信号。",
	"Đuôi khung hành vi (%d mục)":                       "行为骨架尾巴（末 %d 条）",
	"(không có nhật ký phiên)":                          "（无会话记录）",
	"Tự kiểm tra che thông tin":                         "脱敏自检",
	"Số khối văn bản đã che:":                           "打码文本块",
	"chỗ":                                               "处",
	"Văn bản truyện lọt ra ngoài:":                      "正文出包",
	"Nguồn dữ liệu:":                                    "数据源：",
	"Đã che thông tin nhạy cảm: văn bản truyện / prompt / suy nghĩ đã bị gỡ bỏ, chỉ giữ lại khung hành vi. Có thể dán thẳng vào issue.": "已脱敏：小说正文 / prompt / 思考已移除，仅保留行为骨架。可直接贴到 issue。",
}
