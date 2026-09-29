# Mẫu Lập Kế Hoạch Đề Cương

Mẫu này không nhằm ép tất cả tác phẩm vào một độ dài cố định, mà giúp xác định cấp độ tác phẩm trước, rồi mới chọn độ chi tiết của đề cương.

## Bước 1: Xác định cấp độ độ dài tác phẩm

### Truyện ngắn / Đơn tập

- Phù hợp: xung đột đơn, mục tiêu đơn, ít nhân vật, kết thúc tập trung
- Độ dài tham khảo: 8-25 chương
- Định dạng đề xuất: `outline` dạng phẳng

### Truyện vừa / Đa giai đoạn

- Phù hợp: có nâng cấp theo giai đoạn, nhiều tuyến phụ, quan hệ nhân vật thay đổi
- Độ dài tham khảo: 25-60 chương
- Định dạng đề xuất: `outline` dạng phẳng hoặc phân lớp nhẹ

### Truyện dài kỳ / Kiểu web novel

- Phù hợp: thể loại tự nhiên có không gian nâng cấp liên tục, căng thẳng quan hệ dài hạn, nhiều mục tiêu giai đoạn, thế giới có thể mở rộng, bí ẩn dài hạn hoặc tuyến tăng trưởng dài hạn
- Độ dài tham khảo: 80-200+ chương
- Định dạng đề xuất: `layered_outline` phân lớp

## Bước 2: Xác định có cần dùng đề cương phân lớp không

Chỉ cần thỏa mãn bất kỳ 2 điều kiện dưới đây là ưu tiên dùng `layered_outline`:

- Thế giới quan cần được mở ra từng bước, thay vì giải thích hết một lần
- Sự trưởng thành của nhân vật chính không phải một bước nhảy vọt mà là nâng cấp nhiều giai đoạn
- Quan hệ nhân vật liên tục thay đổi qua nhiều giai đoạn
- Ở giữa truyện và cuối truyện tồn tại các loại mâu thuẫn chính khác nhau
- Cần nhiều lần chuyển đổi bản đồ / thế lực / danh tính / mục tiêu
- Thể loại rõ ràng giống tiểu thuyết thương mại kỳ dài hơn là truyện đơn tập

## Bước 3: Với truyện dài, không nên làm ngay "danh sách chương toàn bộ cuốn sách"

Thứ tự lập kế hoạch truyện dài đề xuất là:

1. Điểm bán và sự khác biệt của tác phẩm
2. Động lực truyện dài hạn
3. Chủ đề và nâng cấp cấp tập
4. Mục tiêu cung truyện và bước ngoặt giai đoạn
5. Sự kiện và điểm móc cấp chương

Những sai lầm thường gặp:

- Viết trước 20 chương tóm tắt, rồi cố kéo dài
- Mỗi tập lặp lại "gặp địch - mạnh hơn - đổi bản đồ"
- Chỉ nâng cấp tuyến chính, không nâng cấp tuyến quan hệ
- Giai đoạn đầu đã tiêu hết mọi bí mật lớn, giữa và cuối truyện chỉ có thể lặp lại công thức

## Mẫu Đề Cương Phẳng (Truyện ngắn / vừa)

```json
[
  {
    "chapter": 1,
    "title": "Tiêu đề chương",
    "core_event": "Sự kiện cốt lõi của chương này",
    "conflict": "Ai/cơ chế nào trực tiếp chống lại mục tiêu",
    "stakes": "Cái giá cụ thể nếu thất bại, trì hoãn hoặc bị lộ",
    "turn": "Phát hiện/phản đòn khiến kế hoạch phải đổi",
    "payoff": "Lời hứa cũ được trả trong chương",
    "consequence": "Tình thế mới không thể quay về như đầu chương",
    "hook": "Điểm móc cuối chương",
    "scenes": ["Mục tiêu → lực cản → lựa chọn → tình thế đổi", "Nấc áp lực tiếp theo sinh từ cảnh trước"]
  }
]
```

## Mẫu Đề Cương Phân Lớp (Truyện dài - Cuộn mở hai lớp tập-cung)

Lập kế hoạch ban đầu dùng cuộn hai lớp: 2 tập đầu có khung cung truyện, các tập còn lại là tập khung; cung đầu tiên có chương chi tiết.

```json
[
  {
    "index": 1,
    "title": "Tiêu đề Tập 1",
    "theme": "Mâu thuẫn / chủ đề cốt lõi mới thêm vào trong tập này",
    "arcs": [
      {
        "index": 1,
        "title": "Cung thứ nhất (đã mở rộng)",
        "goal": "Mục tiêu cục bộ, lực cản và bước ngoặt",
        "chapters": [
          {"chapter": 1, "title": "Tiêu đề chương", "core_event": "Sự kiện cốt lõi", "conflict": "Lực cản trực tiếp", "stakes": "Cái giá", "turn": "Bước ngoặt", "payoff": "Lời hứa được trả", "consequence": "Tình thế mới", "hook": "Điểm móc sinh từ hậu quả", "scenes": ["Mục tiêu → lực cản → lựa chọn → tình thế đổi", "Áp lực tăng từ hệ quả cảnh trước"]}
        ]
      },
      {
        "index": 2,
        "title": "Cung thứ hai (cung khung)",
        "goal": "Tóm tắt mục tiêu của cung này",
        "estimated_chapters": 12,
        "chapters": []
      }
    ]
  },
  {
    "index": 2,
    "title": "Tiêu đề Tập 2",
    "theme": "Chủ đề tập 2",
    "arcs": [
      {"index": 1, "title": "Tiêu đề cung", "goal": "Mục tiêu cung", "estimated_chapters": 15, "chapters": []},
      {"index": 2, "title": "Tiêu đề cung", "goal": "Mục tiêu cung", "estimated_chapters": 10, "chapters": []}
    ]
  },
  {
    "index": 3,
    "title": "Tiêu đề Tập 3 (tập khung)",
    "theme": "Hướng chủ đề tập 3",
    "estimated_chapters": 60,
    "arcs": []
  }
]
```

- Mở rộng cấp cung: khi viết tiến đến cung khung, Kiến trúc sư sẽ mở rộng các chương chi tiết của cung đó
- Mở rộng cấp tập: khi viết tiến đến tập khung, Kiến trúc sư sẽ mở rộng cấu trúc cung của tập đó + các chương của cung đầu tiên

## Danh Sách Kiểm Tra Chất Lượng Cấp Tập (Truyện dài)

Mỗi tập đều cần trả lời:

- Tập này bổ sung thông tin thế giới gì mới?
- Tập này nâng cấp mâu thuẫn cốt lõi gì?
- Tập này giúp nhân vật chính đạt được gì, và mất đi gì?
- Tập này thay đổi quan hệ nhân vật chính như thế nào?
- Sau khi tập này kết thúc, tại sao câu chuyện bắt buộc phải tiếp tục sang tập tiếp theo?

## Danh Sách Kiểm Tra Chất Lượng Cấp Cung Truyện (Truyện dài)

Mỗi cung truyện đều cần trả lời:

- Mục tiêu rõ ràng của cung này là gì?
- Lực cản đến từ ai, quy tắc gì, cái giá gì?
- Bước ngoặt là gì?
- Sau khi cung này kết thúc, những trạng thái nào đã thay đổi không thể đảo ngược?

## Danh Sách Kiểm Tra Chất Lượng Cấp Chương

- Mỗi chương phải phục vụ mục tiêu của cung truyện chứa nó
- Mỗi chương phải chứa một sự kiện đẩy truyện tiến không thể xóa bỏ
- Điểm móc cần đa dạng, không nên chỉ dựa vào một kiểu "phát hiện bí mật"
- Các chương đầu truyện không được chỉ "giới thiệu thế giới", mà phải đồng thời thúc đẩy nhân vật và xung đột
- Mỗi chương có mục tiêu tức thời, cái giá, ít nhất hai nấc áp lực, một bước ngoặt đổi quyết định và một hậu quả còn hiệu lực
- Mỗi chương trả ít nhất một lời hứa cũ trước khi mở câu hỏi mới; không tích lũy bí ẩn vô hạn để giả tạo lực kéo
