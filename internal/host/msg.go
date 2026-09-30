package host

import (
	"fmt"
	"strings"
)

// Msg là thông điệp hiển thị CHƯA dịch, đi kèm qua ranh giới host → TUI.
//
// Key là nguồn tiếng Việt theo đúng quy ước của internal/i18n (chuỗi tiếng Việt
// là nguồn sự thật), thường ở dạng format string. Args là tham số sẽ điền vào
// lúc hiển thị.
//
// Vì sao không dựng sẵn ở host rồi gửi chuỗi đã điền: "Khôi phục: chương %d" và
// "Khôi phục: chương 3" là hai chuỗi khác nhau, mà bảng dịch chỉ có thể chứa mẫu
// chứa %d. Điền số ở host thì TUI không còn gì để dịch, và mọi ngôn ngữ đều hiện
// tiếng Việt — đúng cái lỗi "vá triệu chứng" mà Msg này loại bỏ.
//
// TUI dịch Key trước rồi mới điền Args (i18n.Tf), y hệt cách TUI vẫn làm với
// chuỗi của riêng nó, nên một thông điệp hiện đúng ở cả vi/en/zh.
type Msg struct {
	Key  string
	Args []any
}

// String dựng bản tiếng Việt. Dùng cho log và cho code cũ còn cần chuỗi thô;
// phía hiển thị thì gọi i18n.Tf(Key, Args...) để dịch.
func (m Msg) String() string {
	if m.Key == "" {
		return ""
	}
	if len(m.Args) == 0 {
		return m.Key
	}
	return fmt.Sprintf(m.Key, m.Args...)
}

// Empty báo thông điệp rỗng, để TUI bỏ qua thay vì vẽ một dòng trống.
func (m Msg) Empty() bool { return strings.TrimSpace(m.Key) == "" }

// Ptr trả con trỏ tới bản sao, để gán vào Event.SummaryMsg (*Msg).
// Dùng khi thông điệp được tính tại chỗ gọi: cần địa chỉ ổn định chứ không
// phải con trỏ tới biến cục bộ.
func Ptr(m Msg) *Msg {
	if m.Empty() {
		return nil
	}
	return &m
}
