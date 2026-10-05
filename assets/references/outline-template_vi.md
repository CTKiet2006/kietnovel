# Khuôn mẫu quy hoạch dàn ý (Outline Template)

Tác dụng của khuôn mẫu này là giúp bạn định hình đúng cấp độ quy mô của tác phẩm trước khi lựa chọn độ chi tiết của dàn ý.

## Bước 1: Phán đoán cấp độ trường độ của tác phẩm

### Truyện ngắn / Đơn quyển (Short-form)
- **Áp dụng**: Đơn xung đột, đơn mục tiêu, ít nhân vật, kết cục tập trung dứt điểm.
- **Quy mô tham khảo**: 8-25 chương.
- **Định dạng khuyến nghị**: Dàn ý phẳng (`outline`).

### Truyện vừa / Nhiều giai đoạn (Mid-form)
- **Áp dụng**: Có nâng cấp theo từng giai đoạn, vài tuyến phụ, quan hệ nhân vật có chuyển biến.
- **Quy mô tham khảo**: 25-60 chương.
- **Định dạng khuyến nghị**: Dàn ý phẳng (`outline`) hoặc phân tầng nhẹ.

### Trường thiên dài kỳ / Tiểu thuyết mạng (Long-form Serial)
- **Áp dụng**: Không gian mở rộng liên tục, sức căng quan hệ dài hạn, nhiều mục tiêu theo chặng, thế giới quan nhiều tầng nấc, bí ẩn dài hơi hoặc lộ trình trưởng thành kéo dài.
- **Quy mô tham khảo**: 80-200+ chương.
- **Định dạng khuyến nghị**: Dàn ý phân tầng (`layered_outline`).

## Bước 2: Khi nào bắt buộc dùng Dàn ý phân tầng (`layered_outline`)?

Chỉ cần thỏa mãn từ 2 điều kiện dưới đây, hãy ưu tiên chọn `layered_outline`:
- Thế giới quan cần hé mở từng bước qua nhiều vùng đất/thế lực.
- Sự trưởng thành của nhân vật chính trải qua nhiều giai đoạn đột phá.
- Mối quan hệ giữa các nhân vật chủ chốt liên tục biến chuyển qua từng thời kỳ.
- Giai đoạn đầu, giữa và cuối đối mặt với những loại hình mâu thuẫn chủ đạo khác nhau.
- Cần chuyển đổi bản đồ, bang phái, thân phận hoặc mục tiêu lớn nhiều lần.

## Bước 3: Nguyên tắc lập dàn ý trường thiên

Với truyện dài kỳ, tuyệt đối không lập danh sách chương dàn trải hàng trăm chương ngay từ đầu:
1. **Quy hoạch khung sườn các Quyển (Volume)**: Mỗi quyển giải quyết một xung đột chủ đạo, có cao trào quyển và móc câu chuyển quyển.
2. **Quy hoạch các Arc trong Quyển**: Mỗi arc gồm 8-15 chương, có mục tiêu rõ ràng và điểm rơi cảm xúc.
3. **Mở rộng chi tiết theo hình thức Cuộn chiếu (Rolling Expansion)**: Chỉ mở rộng chi tiết từng chương cho Arc đầu tiên (Arc 1); các Arc và Quyển sau lưu giữ ở dạng khung sườn (skeleton: `title`, `goal`, `estimated_chapters`), đợi khi viết xong Arc 1 mới dùng công cụ `expand_next_arc` để triển khai tiếp dựa trên thực tế đã viết.
