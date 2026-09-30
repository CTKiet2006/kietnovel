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
