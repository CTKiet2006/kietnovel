## Tiêu chuẩn viết tiểu thuyết (Voice Standard)

Đây là các tiêu chí chất lượng, không phải danh sách kiểm tra để điểm danh cứng nhắc. Chương truyện trước tiên phải tự nhiên thành lập, sau đó mới đến việc các hạng mục đầy đủ.

- Mở đầu nhanh chóng thiết lập xung đột, hồi hộp, khao khát hoặc cảm giác bất thường — ít dùng hồi tưởng trừu tượng.
- Dùng hành động, đối thoại, chi tiết cảm quan để thúc đẩy cốt truyện — ít dùng tóm tắt và khái quát.
- Đối thoại nhân vật phải có sự khác biệt danh tính, ẩn ý và mục đích hành động — không thuyết giáo.
- Thể hiện cảm xúc qua phản ứng cơ thể và lựa chọn hành vi — không dán nhãn cảm xúc trực tiếp.
- Thay đổi quan hệ phải có sự kiện kích hoạt — không nhảy vọt từ xa lạ sang tin tưởng tuyệt đối trong một chương.
- Bí mật phân kỳ giải phóng — không giải thích trước những bí ẩn lớn mà đại cương chưa yêu cầu.
- Điểm móc cuối chương có thể là nguy cơ, lựa chọn, dư âm cảm xúc, thay đổi quan hệ hoặc mục tiêu chưa hoàn thành — không nhất thiết mỗi chương đều phải làm hồi hộp phóng đại.
- **Văn phong tự nhiên như người thật viết**: Câu văn phải có hơi thở — nhịp co giãn theo cảm xúc của cảnh, từ ngữ đời thường đúng bối cảnh, các ý nối nhau bằng dòng chú ý và suy nghĩ của nhân vật chứ không phải liệt kê thông tin. Tránh văn khô khan kiểu dịch convert hay báo cáo. Giữ kết cấu đoạn văn hoàn chỉnh, đan xen hành động, cảm quan, nội tâm và đối thoại; tránh xé nhỏ văn bản thành các đoạn vụn 1-2 câu khiến mạch truyện đứt gãy.
- **Chống văn phong AI**: Khi viết, tránh tất cả các mẫu được liệt kê trong `reference_pack.references.anti_ai_tone` (năm loại: cấu trúc/dùng từ/miêu tả/đối thoại/nhịp điệu). Ngưỡng từ sáo rỗng và cụm từ cấm có thể liệt kê cơ học nằm trong `working_memory.user_rules.structured` — bắt buộc kiểm tra khi lưu chương.
- **Đa dạng cú pháp**: `episodic_memory.style_stats` (nếu có) là thống kê của hệ thống về văn bản bạn đã viết — tấm gương phản chiếu các cụm từ quen miệng của chính bạn. Chương này chủ động giảm các mục có tần suất cao; nguồn cứng hóa phổ biến nhất là câu chỉnh lý ("không phải… mà là…"), từ chỉ thời lượng đơn điệu và ẩn dụ so sánh cùng loại liên tiếp. Hình thức kết thúc chương (câu ngắn chặt đứt/dư âm đối thoại/ảnh hưởng cảnh tượng/câu hỏi hồi hộp) luân phiên với các chương gần đây; tránh mở đầu kiểu "đêm/sáng sớm/thức dậy" mỗi chương.
- **Không tóm lại tình tiết cũ**: Tóm tắt, phục bút, trạng thái trong `episodic_memory` là ghi chú đối chiếu của những gì đã viết vào chính văn — không phải tư liệu chờ viết của chương này; thông tin đã trình bày ở chương trước, chương mới chỉ chạm đến từ góc nhìn mới khi cốt truyện cần, cấm viết lại kiểu tiền đề (chép lại nguyên văn xuyên chương sẽ bị `style_stats.repeated_sentences` ghi lại).
