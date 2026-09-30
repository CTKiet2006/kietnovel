package bootstrap

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// key dựng một tea.KeyMsg từ tên phím, giống hệt thứ bubbletea nhận từ terminal.
func key(name string) tea.KeyMsg {
	switch name {
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(name)}
}

// TestSetupSelectArrowKeys là test chốt hồi quy: wizard phải lên/xuống được.
// Không có test này thì lỗi mũi tên hỏng lọt xuống bản phát hành.
func TestSetupSelectArrowKeys(t *testing.T) {
	m := setupSelectModel{title: "test", items: setupProviders}
	if len(m.items) < 3 {
		t.Fatalf("cần ít nhất 3 provider để test, có %d", len(m.items))
	}
	if m.cursor != 0 {
		t.Fatalf("cursor ban đầu = %d, mong 0", m.cursor)
	}

	// Xuống 2 lần.
	for i := 1; i <= 2; i++ {
		next, _ := m.Update(key("down"))
		m = next.(setupSelectModel)
		if m.cursor != i {
			t.Fatalf("sau %d lần nhấn down: cursor = %d, mong %d", i, m.cursor, i)
		}
	}

	// Lên 1 lần.
	next, _ := m.Update(key("up"))
	m = next.(setupSelectModel)
	if m.cursor != 1 {
		t.Fatalf("sau khi nhấn up: cursor = %d, mong 1", m.cursor)
	}
}

// TestSetupSelectBounds khoá hai dau cua danh sach: len tren het thi dung lai,
// xuong duoi het thi dung lai. Khong duoc tran cursor ra ngoai roi pan index.
func TestSetupSelectBounds(t *testing.T) {
	m := setupSelectModel{title: "test", items: setupProviders}

	// Lên khi đã ở đầu.
	for i := 0; i < 3; i++ {
		next, _ := m.Update(key("up"))
		m = next.(setupSelectModel)
	}
	if m.cursor != 0 {
		t.Fatalf("nhấn up nhiều lần ở đầu: cursor = %d, mong 0", m.cursor)
	}

	// Xuống nhiều lần ở cuối.
	for i := 0; i < len(setupProviders)+5; i++ {
		next, _ := m.Update(key("down"))
		m = next.(setupSelectModel)
	}
	if m.cursor != len(setupProviders)-1 {
		t.Fatalf("nhấn down nhiều lần: cursor = %d, mong %d", m.cursor, len(setupProviders)-1)
	}

	// Cursor hợp lệ để index vào items, không panic.
	_ = m.items[m.cursor]
}

// TestSetupSelectJupyterPhim chứng minh phím k/j (vim) cũng chạy, vì đây là
// đường dự phòng khi mũi tên không tới được terminal.
func TestSetupSelectJupyterPhim(t *testing.T) {
	m := setupSelectModel{title: "test", items: setupProviders}

	next, _ := m.Update(key("j"))
	m = next.(setupSelectModel)
	if m.cursor != 1 {
		t.Fatalf("nhấn j: cursor = %d, mong 1", m.cursor)
	}

	next, _ = m.Update(key("k"))
	m = next.(setupSelectModel)
	if m.cursor != 0 {
		t.Fatalf("nhấn k: cursor = %d, mong 0", m.cursor)
	}
}

// TestSetupSelectEnterXongVaHuy bao hai phim ket thuc.
func TestSetupSelectEnterXongVaHuy(t *testing.T) {
	m := setupSelectModel{title: "test", items: setupProviders}

	next, cmd := m.Update(key("enter"))
	if cmd == nil {
		t.Fatal("Enter phải trả lệnh quit")
	}
	if next.(setupSelectModel).cancelled {
		t.Fatal("Enter không được đánh dấu hủy")
	}

	m2 := setupSelectModel{title: "test", items: setupProviders}
	// Esc cần hai lần, xem TestEscMotLanKhongHuy.
	armed, _ := m2.Update(key("esc"))
	next2, cmd2 := armed.Update(key("esc"))
	if cmd2 == nil {
		t.Fatal("Esc lần hai phải trả lệnh quit")
	}
	if !next2.(setupSelectModel).cancelled {
		t.Fatal("Esc lần hai phải đánh dấu hủy")
	}
}

// TestEscMotLanKhongHuy la test chong loai bug nguy hiem nhat cua wizard.
//
// Muoi ten tren Windows gui chuoi escape "\x1b[A" / "\x1b[B". Neu terminal gui
// cham, bubbletea co the tra ve mot KeyEsc doc le. Truoc day Esc huy ngay, nen
// nguoi dung chi can bam mui ten la mat toan bo phan thiet lap da nhap API key.
// Bay Esc phai bam hai lan moi huy.
func TestEscMotLanKhongHuy(t *testing.T) {
	m := setupSelectModel{title: "test", items: setupProviders}

	// Một Esc đứng lẻ (mô phỏng escape bị cắt) chỉ được vũ trang, chưa hủy.
	next, cmd := m.Update(key("esc"))
	m = next.(setupSelectModel)
	if cmd != nil {
		t.Fatal("một Esc đứng lẻ không được thoát chương trình")
	}
	if m.cancelled {
		t.Fatal("một Esc đứng lẻ không được đánh dấu hủy")
	}
	if !m.escArmed {
		t.Fatal("sau một Esc phải ở trạng thái chờ xác nhận")
	}

	// Esc lần hai mới hủy thật.
	next2, cmd2 := m.Update(key("esc"))
	m2 := next2.(setupSelectModel)
	if cmd2 == nil {
		t.Fatal("Esc lần hai phải thoát chương trình")
	}
	if !m2.cancelled {
		t.Fatal("Esc lần hai phải đánh dấu hủy")
	}
}

// TestPhimKhacBoVoTrangThaiChoEsc: sau khi Esc lần một, bấm phím khác thì phải bỏ
// vũ trang, để nếu người dùng định hủy rồi đổi ý thì lần Esc sau tính từ đầu.
func TestPhimKhacBoVoTrangThaiChoEsc(t *testing.T) {
	m := setupSelectModel{title: "test", items: setupProviders}

	next, _ := m.Update(key("esc"))
	m = next.(setupSelectModel)
	if !m.escArmed {
		t.Fatal("phải vũ trang sau Esc đầu tiên")
	}

	next2, _ := m.Update(key("down"))
	m2 := next2.(setupSelectModel)
	if m2.escArmed {
		t.Fatal("bấm phím khác phải bỏ trạng thái chờ hủy")
	}

	// Vũ trang bị bỏ nên Esc tiếp theo lại chỉ chờ, chưa hủy.
	next3, cmd3 := m2.Update(key("esc"))
	m3 := next3.(setupSelectModel)
	if cmd3 != nil || m3.cancelled {
		t.Fatal("Esc sau khi đã bỏ vũ trang phải chỉ chờ, không hủy")
	}
}

// TestCtrlCHuyNgay: Ctrl+C là chuỗi phím riêng biệt, không bị cắt nên vẫn hủy
// ngay lập tức, không cần bấm hai lần.
func TestCtrlCHuyNgay(t *testing.T) {
	m := setupSelectModel{title: "test", items: setupProviders}
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil {
		t.Fatal("Ctrl+C phải thoát ngay")
	}
	if !next.(setupSelectModel).cancelled {
		t.Fatal("Ctrl+C phải đánh dấu hủy")
	}

	mi := setupInputModel{label: "t"}
	ni, cmi := mi.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmi == nil || !ni.(setupInputModel).cancelled {
		t.Fatal("Ctrl+C ở ô nhập phải hủy ngay")
	}
}

// TestEscMotLanKhongHuyOInput: bảo vệ phần nhập text, nơi người dùng đang gõ
// API key — mất dở dang tốn nhiều công hơn hủy ở bước chọn.
func TestEscMotLanKhongHuyOInput(t *testing.T) {
	m := setupInputModel{label: "t", value: "sk-secret"}

	next, cmd := m.Update(key("esc"))
	m = next.(setupInputModel)
	if cmd != nil || m.cancelled {
		t.Fatal("một Esc đứng lẻ không được xóa nội dung đang nhập")
	}
	if m.value != "sk-secret" {
		t.Fatalf("giá trị đã nhập bị mất: %q", m.value)
	}

	next2, cmd2 := m.Update(key("esc"))
	if cmd2 == nil || !next2.(setupInputModel).cancelled {
		t.Fatal("Esc lần hai ở ô nhập mới hủy")
	}
}
