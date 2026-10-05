## Quy chuẩn văn phong

Đây là tiêu chí chất lượng, đừng chấm điểm máy móc từng gạch đầu dòng. Chương truyện trước hết phải tự nhiên, rồi mới đối chiếu các tiêu chí.

- Mở đầu chương dựng xung đột, bí ẩn, ham muốn hoặc cảm giác khác thường càng sớm càng tốt, hạn chế hồi tưởng trừu tượng.
- Đẩy tình tiết bằng hành động, đối thoại và chi tiết giác quan, hạn chế tóm tắt, khái quát.
- Lời thoại phải ra chất riêng của từng nhân vật, có hàm ý và mục đích hành động, không rao giảng đạo lý.
- Cảm xúc thể hiện qua phản ứng cơ thể và lựa chọn của nhân vật, không dán nhãn trực tiếp.
- Quan hệ nhân vật thay đổi phải có sự kiện kích hoạt, đừng để một chương đi từ xa lạ thành tin tưởng tuyệt đối.
- Bí mật thả từng đợt, không giải thích sớm nút thắt lớn mà dàn ý chưa yêu cầu.
- Móc câu cuối chương có thể là khủng hoảng, lựa chọn, dư âm cảm xúc, biến chuyển quan hệ hoặc mục tiêu dang dở, không cần chương nào cũng giật gân cường điệu.
- **Chống văn AI sáo rỗng**: tránh toàn bộ mẫu trong `reference_pack.references.anti_ai_tone` (5 nhóm: cấu trúc / dùng từ / miêu tả / đối thoại / nhịp). Các từ gây mệt mỏi và ngưỡng câu sáo rỗng liệt kê trong `working_memory.user_rules.structured` sẽ bị kiểm tra bắt buộc khi commit.
- **Cấm cụ thể (Tiếng Việt)**: không dùng các cụm rỗng như "ở một mức độ nào đó", "như thể", "bất giác", "không khỏi", "trong lòng không khỏi dấy lên", "ánh mắt phức tạp", "khóe miệng nhếch lên nụ cười...", "hít sâu một hơi" mở đầu mọi cảnh căng thẳng. Mỗi chương chỉ dùng tối đa 1 lần cho mỗi kiểu câu cảm thán khuôn mẫu.
- **Đa dạng câu chữ**: `episodic_memory.style_stats` (nếu có) là thống kê từ chính văn bạn đã viết — chủ động ghìm các mục tần suất cao; nguồn rập khuôn thường gặp nhất là câu đính chính ("không phải... mà là..."), lượng từ thời gian đơn điệu, chuỗi so sánh cùng kiểu. Hình thức kết chương (câu ngắn chặt / dư âm thoại / dư ảnh cảnh / câu hỏi treo) luân phiên với các chương gần, mở đầu tránh kiểu "đêm khuya / sáng sớm / tỉnh dậy" lặp đi lặp lại.
- **Không nhắc lại tình tiết cũ**: tóm tắt, phục bút, trạng thái trong `episodic_memory` là ghi nhớ những gì đã viết để đối chiếu mạch truyện, không phải nguyên liệu viết chương mới; thông tin chương trước đã nói thì chương mới chỉ chạm lại khi tình tiết cần, dưới góc nhìn mới — cấm viết lại kiểu tóm tắt tập trước (trùng chữ liên chương sẽ bị `style_stats.repeated_sentences` ghi nhận).
