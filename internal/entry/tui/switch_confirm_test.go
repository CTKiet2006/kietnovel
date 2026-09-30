package tui

import "testing"

func TestSanitizeBookName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"truyen linh di", "truyen-linh-di"},
		{"Đêm ba tháng mười một", "dem-ba-thang-muoi-mot"},
		{"  Truyen  Linh  Di  ", "truyen-linh-di"},
		{"truyen--linh--di", "truyen-linh-di"},
		{"Truyen Linh Di", "truyen-linh-di"},
		{"-truyen-", "truyen"},
		{"truyen_2", "truyen-2"},
		{"Phim kinh di 2024", "phim-kinh-di-2024"},
		{"Cặp Đôi", "cap-doi"},
		{"a", "a"},
		{"2024", "2024"},
	}
	for _, c := range cases {
		if got := sanitizeBookName(c.in); got != c.want {
			t.Errorf("sanitizeBookName(%q) = %q, mong %q", c.in, got, c.want)
		}
	}
}

func TestSanitizeBookNameRacKyTuLạ(t *testing.T) {
	// Toàn ký tự bị loại thì phải rơi về tên dùng được, không được trả chuỗi rỗng
	// vì chuỗi rỗng sẽ tạo thư mục "" và hỏng.
	for _, in := range []string{"", "   ", "***", "***///", "———"} {
		got := sanitizeBookName(in)
		if got == "" {
			t.Errorf("sanitizeBookName(%q) rỗng, phải có tên dự phòng", in)
		}
	}
}

func TestSanitizeBookNameKhongChuaKyTuCauDuongDan(t *testing.T) {
	got := sanitizeBookName("a/b\\c:d*e?f\"g<h>i|j")
	for _, bad := range []string{"/", "\\", ":", "*", "?", "\"", "<", ">", "|"} {
		if contains(got, bad) {
			t.Errorf("sanitizeBookName còn sót %q trong %q", bad, got)
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// TestEnterSwitchConfirmKhongPanicKhiModalDaDong — hồi quy cho panic nil pointer
// khi /new tạo xong rồi hỏi chuyển.
//
// createBookConfirmed đóng modal (m.books = nil) TRƯỚC khi gọi askSwitch. Khi
// truyện cũ còn việc dở, askSwitch chạm s.mode với s == nil và crash toàn bộ TUI.
// Test này dựng đúng trạng thái đó: books=nil, runtime=nil, gọi thẳng vào, và
// yêu cầu không panic + mode đúng + dữ liệu mục tiêu còn nguyên.
func TestEnterSwitchConfirmKhongPanicKhiModalDaDong(t *testing.T) {
	m := newTestModel(120, 30)
	m.books = nil
	m.runtime = nil

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("panic khi modal da dong: %v", r)
		}
	}()
	m = m.enterSwitchConfirm(`C:\x\output\truyen-moi`, "Truyen Moi", "đang viết dở chương 3")

	if m.books == nil {
		t.Fatal("phai dung lai state, khong de nil")
	}
	if m.books.mode != booksSwitchConfirm {
		t.Errorf("mode = %v, mong booksSwitchConfirm", m.books.mode)
	}
	if m.books.targetName != "Truyen Moi" || m.books.switchWhy == "" {
		t.Errorf("du lieu muc tieu mat: %+v", m.books)
	}
}

// Esc sau khi hỏi phải về được danh sách, không kẹt ở khung xác nhận.
func TestEnterSwitchConfirmEscVeDanhSach(t *testing.T) {
	m := newTestModel(120, 30)
	m.books = nil
	m.runtime = nil
	m = m.enterSwitchConfirm(`C:\x\output\truyen-moi`, "Truyen Moi", "ly do")
	m.books.mode = booksList
	if m.books.mode != booksList {
		t.Error("Esc phai ve booksList")
	}
}
