package tui

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/CTKiet2006/kietnovel/internal/bootstrap"
	"github.com/CTKiet2006/kietnovel/internal/host"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

func hubFieldIDs(fields []hubField) []string {
	ids := make([]string, len(fields))
	for i, f := range fields {
		ids[i] = f.id
	}
	return ids
}

func hubFieldIndex(fields []hubField, id string) int {
	for i, field := range fields {
		if field.id == id {
			return i
		}
	}
	return -1
}

// Chọn Provider đã có phải vào hub chi tiết (xem thông tin rồi chỉnh từng mục), thay vì nhảy thẳng vào "đổi giao thức".
func TestSelectingProviderOpensHub(t *testing.T) {
	st := &modelConfigState{editModelIdx: -1}
	st.applyProviderChoice(configProviderChoice{existing: &host.ProviderSnapshot{
		Name: "openrouter", BaseURL: "u", HasAPIKey: true,
		Models: []bootstrap.ModelConfig{{Name: "m"}},
	}})
	if st.step != configStepHub {
		t.Fatalf("Chọn Provider đã có phải vào hub, được step=%d", st.step)
	}
	ids := hubFieldIDs(st.hubFields())
	// Provider dựng sẵn (type rỗng) không bày nhiễu Giao thức/Endpoint, nhưng giữ key/models/save.
	if slices.Contains(ids, "protocol") || slices.Contains(ids, "api") {
		t.Fatalf("Hub provider dựng sẵn không nên có giao thức/Endpoint, được %v", ids)
	}
	for _, want := range []string{"key", "baseurl", "models", "save"} {
		if !slices.Contains(ids, want) {
			t.Fatalf("hub thiếu %q, được %v", want, ids)
		}
	}
}

// Chỉ hub Provider tùy chỉnh (giao thức openai tường minh) mới hiện giao thức và Endpoint.
func TestCustomProviderHubShowsProtocolAndEndpoint(t *testing.T) {
	st := &modelConfigState{editModelIdx: -1}
	st.applyProviderChoice(configProviderChoice{existing: &host.ProviderSnapshot{
		Name: "proxy", Type: "openai", API: "responses", HasAPIKey: true,
		Models: []bootstrap.ModelConfig{{Name: "m"}},
	}})
	ids := hubFieldIDs(st.hubFields())
	if !slices.Contains(ids, "protocol") || !slices.Contains(ids, "api") {
		t.Fatalf("Hub provider openai tùy chỉnh phải có giao thức/Endpoint, được %v", ids)
	}
}

// Esc quay từng cấp: sửa dòng trong hub → hub → danh sách Provider → đóng.
func TestEscapeBackHierarchy(t *testing.T) {
	st := &modelConfigState{step: configStepHub, provider: "proxy"}
	st.beginInlineEdit("baseurl")
	m := Model{modelConfig: st}
	m.handleModelConfigKey(tea.KeyMsg{Type: tea.KeyEsc})
	if st.step != configStepHub || st.editingField != "" {
		t.Fatalf("Esc khi sửa dòng phải ở lại hub và hủy nhập, được step=%d field=%q", st.step, st.editingField)
	}
	if got, ok := st.escapeBack(); !ok || got != configStepProvider {
		t.Fatalf("Esc ở hub phải về danh sách, được %d,%v", got, ok)
	}
	st.step = configStepProvider
	if _, ok := st.escapeBack(); ok {
		t.Fatal("Esc ở danh sách phải đóng cả panel")
	}
}

func TestModelListAddsAndEditsInPlace(t *testing.T) {
	st := &modelConfigState{step: configStepModels, editModelIdx: -1,
		models: []bootstrap.ModelConfig{{Name: "m1"}}, modelOrigins: []string{"m1"}}
	st.cursor = len(st.models)
	m := Model{modelConfig: st}
	m.handleModelConfigKey(tea.KeyMsg{Type: tea.KeyEnter})
	if st.step != configStepModels || st.editingField != configModelNameField || !st.addingModel {
		t.Fatalf("Thêm mới phải sửa dòng ngay trong danh sách, step=%d field=%q adding=%v", st.step, st.editingField, st.addingModel)
	}
	st.input.SetValue("  m2  ")
	m.handleModelConfigKey(tea.KeyMsg{Type: tea.KeyEnter})
	if st.editingField != configModelWindowField || st.models[1].Name != "m2" {
		t.Fatalf("Đặt tên xong phải vào luôn cột window cùng dòng, field=%q models=%#v", st.editingField, st.models)
	}
	st.input.SetValue("128K")
	m.handleModelConfigKey(tea.KeyMsg{Type: tea.KeyEnter})
	if st.editingField != "" || st.addingModel || st.models[1].ContextWindow != 128000 {
		t.Fatalf("Model mới chưa xong trong cùng trang: %#v", st)
	}
}

func TestModelListEditsSelectedCellAndCancels(t *testing.T) {
	st := &modelConfigState{step: configStepModels, editModelIdx: -1,
		models: []bootstrap.ModelConfig{{Name: "m1", ContextWindow: 1000}}, modelOrigins: []string{"m1"}}
	m := Model{modelConfig: st}
	m.handleModelConfigKey(tea.KeyMsg{Type: tea.KeyRight})
	m.handleModelConfigKey(tea.KeyMsg{Type: tea.KeyEnter})
	if st.editingField != configModelWindowField || st.step != configStepModels {
		t.Fatalf("Enter ở cột phải phải sửa window ngay trong dòng, step=%d field=%q", st.step, st.editingField)
	}
	st.input.SetValue("200K")
	m.handleModelConfigKey(tea.KeyMsg{Type: tea.KeyEsc})
	if st.editingField != "" || st.models[0].ContextWindow != 1000 {
		t.Fatalf("Esc phải hủy ô hiện tại và không đổi giá trị: %#v", st.models[0])
	}
}

func TestModelRenameProducesExplicitDraftAndReferenceNotice(t *testing.T) {
	st := &modelConfigState{
		step: configStepModels, provider: "proxy", models: []bootstrap.ModelConfig{{Name: "old"}},
		modelOrigins: []string{"old"}, snapshot: host.ModelConfigurationSnapshot{
			References: map[string][]string{"proxy\x00old": {"default", "writer fallback[0]"}},
		},
	}
	m := Model{modelConfig: st}
	m.handleModelConfigKey(tea.KeyMsg{Type: tea.KeyEnter})
	st.input.SetValue("renamed")
	m.handleModelConfigKey(tea.KeyMsg{Type: tea.KeyEnter})
	draft := st.draft()
	if len(draft.Renames) != 1 || draft.Renames[0] != (host.ModelRename{From: "old", To: "renamed"}) {
		t.Fatalf("Đổi tên model phải giữ quan hệ danh tính tường minh, renames=%#v", draft.Renames)
	}
	if !strings.Contains(st.message, "cập nhật tham chiếu") || !strings.Contains(st.message, "default") {
		t.Fatalf("Đổi tên model có tham chiếu phải báo rõ hành vi lưu, message=%q", st.message)
	}
}

func TestModelListRendersEditableColumnsAndReferences(t *testing.T) {
	st := &modelConfigState{
		step: configStepModels, provider: "proxy", models: []bootstrap.ModelConfig{{Name: "deepseek-chat", ContextWindow: 128000}},
		modelOrigins: []string{"deepseek-chat"}, snapshot: host.ModelConfigurationSnapshot{
			References: map[string][]string{"proxy\x00deepseek-chat": {"default"}},
		},
	}
	plain := ansi.Strip(renderModelConfigModal(120, st))
	for _, want := range []string{"ID model", "Context window", "Tham chiếu", "deepseek-chat", "128K", "default", "+ Thêm model"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("Bảng model một trang thiếu %q:\n%s", want, plain)
		}
	}
}

func TestModelNameInlineEditorKeepsModalRowsIntact(t *testing.T) {
	oldProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(oldProfile) })
	st := &modelConfigState{
		step: configStepModels, provider: "deepseek",
		models:       []bootstrap.ModelConfig{{Name: "deepseek-v4-pro"}, {Name: "deepseek-v4-flash"}},
		modelOrigins: []string{"deepseek-v4-pro", "deepseek-v4-flash"},
	}
	st.beginModelEdit(0, 0)
	view := renderModelConfigModal(120, st)
	lines := strings.Split(view, "\n")
	for i, line := range lines {
		if width := lipgloss.Width(line); width != 72 {
			t.Fatalf("Sửa tên model trong dòng làm vỡ rộng dòng %d: width=%d line=%q\n%s", i, width, ansi.Strip(line), ansi.Strip(view))
		}
	}
	if len(lines) != 7 {
		t.Fatalf("Sửa trong dòng không được sinh xuống dòng vật lý, được %d dòng:\n%s", len(lines), ansi.Strip(view))
	}
}

func TestRenamedReferencedModelStillCannotBeDeleted(t *testing.T) {
	st := &modelConfigState{
		provider: "proxy", currentModel: "old", models: []bootstrap.ModelConfig{{Name: "renamed"}},
		modelOrigins: []string{"old"}, snapshot: host.ModelConfigurationSnapshot{
			References: map[string][]string{"proxy\x00old": {"default"}},
		},
	}
	if st.deleteModel(0) || len(st.models) != 1 || !strings.Contains(st.message, "đang dùng") {
		t.Fatalf("Đổi tên chưa lưu vẫn phải chặn xóa theo danh tính gốc, models=%#v message=%q", st.models, st.message)
	}
}

func TestCancellingNewModelNameRemovesTemporaryRow(t *testing.T) {
	st := &modelConfigState{step: configStepModels, models: []bootstrap.ModelConfig{{Name: "m1"}}, modelOrigins: []string{"m1"}}
	st.cursor = 1
	m := Model{modelConfig: st}
	m.handleModelConfigKey(tea.KeyMsg{Type: tea.KeyEnter})
	m.handleModelConfigKey(tea.KeyMsg{Type: tea.KeyEsc})
	if len(st.models) != 1 || len(st.modelOrigins) != 1 || st.cursor != 1 {
		t.Fatalf("Hủy thêm mới phải dọn dòng tạm, models=%#v origins=%#v cursor=%d", st.models, st.modelOrigins, st.cursor)
	}
}

func TestParseContextWindowInput(t *testing.T) {
	cases := map[string]int{
		"": 0, "0": 0, "auto": 0, "128K": 128000, "1M": 1000000,
		"1.5m": 1500000, "200000": 200000,
	}
	for input, want := range cases {
		got, err := parseContextWindowInput(input)
		if err != nil || got != want {
			t.Errorf("parseContextWindowInput(%q) = %d, %v; want %d", input, got, err, want)
		}
	}
	for _, input := range []string{"-1", "abc", "0.5"} {
		if _, err := parseContextWindowInput(input); err == nil {
			t.Errorf("parseContextWindowInput(%q) should fail", input)
		}
	}
}

func TestModelConfigModalDoesNotRenderAPIKey(t *testing.T) {
	state := &modelConfigState{step: configStepHub, provider: "proxy", apiKeyOptional: true}
	state.beginInlineEdit("key")
	state.input.SetValue("sk-super-secret")
	view := renderModelConfigModal(120, state)
	if strings.Contains(view, "sk-super-secret") {
		t.Fatal("API key leaked into rendered modal")
	}
}

func TestProviderHubEditsAPIKeyInlineAndTrims(t *testing.T) {
	state := &modelConfigState{step: configStepHub, provider: "proxy", existing: true,
		hasAPIKey: true, apiKeyHint: "sk-o******7890", apiKeyOptional: true, apiKeyAction: host.APIKeyKeep}
	state.cursor = hubFieldIndex(state.hubFields(), "key")
	m := Model{modelConfig: state}
	m.handleModelConfigKey(tea.KeyMsg{Type: tea.KeyEnter})
	if state.step != configStepHub || state.editingField != "key" {
		t.Fatalf("API Key phải sửa ngay trên dòng hub, được step=%d field=%q", state.step, state.editingField)
	}
	state.input.SetValue("  sk-new-secret-1234567890  ")
	m.handleModelConfigKey(tea.KeyMsg{Type: tea.KeyEnter})
	if state.editingField != "" || state.apiKey != "sk-new-secret-1234567890" || state.apiKeyAction != host.APIKeyReplace {
		t.Fatalf("Kết quả commit API Key trong dòng sai: field=%q key=%q action=%q", state.editingField, state.apiKey, state.apiKeyAction)
	}
	if got := state.keyStatus(); got != "sk-n******7890" {
		t.Fatalf("API Key mới phải hiện gợi ý che, được %q", got)
	}
}

func TestProviderHubEditsBaseURLInlineAndKeepsLongTailVisible(t *testing.T) {
	state := &modelConfigState{step: configStepHub, provider: "proxy", existing: true,
		apiKeyOptional: true, baseURL: "https://old.example/v1"}
	state.cursor = hubFieldIndex(state.hubFields(), "baseurl")
	m := Model{modelConfig: state}
	m.handleModelConfigKey(tea.KeyMsg{Type: tea.KeyEnter})
	if state.editingField != "baseurl" || state.input.Value() != "https://old.example/v1" {
		t.Fatalf("Base URL phải sửa ngay dòng gốc với giá trị điền sẵn, field=%q value=%q", state.editingField, state.input.Value())
	}
	state.input.SetValue("  https://example.com/a/very/long/provider/path/UNIQUE-END  ")
	state.input.CursorEnd()
	view := renderModelConfigModal(76, state)
	if !strings.Contains(view, "UNIQUE-END") {
		t.Fatalf("Base URL dài khi sửa phải hiện đuôi gần con trỏ:\n%s", view)
	}
	m.handleModelConfigKey(tea.KeyMsg{Type: tea.KeyEnter})
	if state.baseURL != "https://example.com/a/very/long/provider/path/UNIQUE-END" {
		t.Fatalf("Base URL chưa TrimSpace, được %q", state.baseURL)
	}
}

func TestSaveConfigHighlightsOnlyWhenDirty(t *testing.T) {
	state := &modelConfigState{editModelIdx: -1}
	state.applyProviderChoice(configProviderChoice{existing: &host.ProviderSnapshot{
		Name: "proxy", Type: "openai", BaseURL: "https://old.example/v1", HasAPIKey: true,
		APIKeyHint: "sk-o******7890", Models: []bootstrap.ModelConfig{{Name: "m1"}},
	}})
	if state.isDirty() {
		t.Fatal("Vừa vào Provider đã có không nên đánh dấu đã sửa")
	}
	state.baseURL = "https://new.example/v1"
	if !state.isDirty() {
		t.Fatal("Đổi Base URL xong phải đánh dấu đã sửa")
	}
	state.baseURL = "https://old.example/v1"
	if state.isDirty() {
		t.Fatal("Trả về giá trị baseline phải tự hết đánh dấu sửa")
	}
	state.beginInlineEdit("baseurl")
	state.input.SetValue("https://editing.example/v1")
	if !state.isDirty() {
		t.Fatal("Đang nhập giá trị Base URL mới phải đánh dấu sửa ngay")
	}
	state.input.SetValue(" https://old.example/v1 ")
	if state.isDirty() {
		t.Fatal("Nhập trong dòng tương đương baseline thì không báo sửa nhầm")
	}
	state.editingField = ""
	state.apiKeyAction = host.APIKeyReplace
	state.apiKey = "sk-new-secret"
	if !state.isDirty() {
		t.Fatal("Thay API Key xong phải đánh dấu đã sửa")
	}

	oldProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(oldProfile) })
	lines := renderProviderHubFields(state, 68)
	want := lipgloss.NewStyle().Foreground(colorSuccess).Render("Lưu cấu hình")
	found := false
	for _, line := range lines {
		if strings.Contains(line, want) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("Khi có thay đổi mục lưu phải dùng màu success, lines=%q", lines)
	}

	newProvider := &modelConfigState{}
	if !newProvider.isDirty() {
		t.Fatal("Provider mới luôn xem như thay đổi chưa lưu")
	}
}

func TestStyledBaseURLLineKeepsANSIAndFillsModalWidth(t *testing.T) {
	plain := "› Base URL  https://api.deepseek.com"
	styled := "\x1b[38;2;255;200;0m› \x1b[0m" +
		"\x1b[1;38;2;255;200;0mBase URL\x1b[0m  " +
		"\x1b[4;38;2;220;220;220mhttps://api.deepseek.com\x1b[0m"
	if got := ansi.Strip(truncateStyledWidth(styled, 56)); got != plain {
		t.Fatalf("Cắt nhận biết ANSI làm hỏng dòng nhập: %q", got)
	}

	modal := renderPaddedModalFrame(60, 3, "/config", "", []string{styled})
	lines := strings.Split(modal, "\n")
	if len(lines) != 3 || lipgloss.Width(lines[1]) != 60 {
		t.Fatalf("Dòng nhập popup chưa lấp đủ rộng cố định: width=%d\n%s", lipgloss.Width(lines[1]), modal)
	}
	if !strings.Contains(ansi.Strip(lines[1]), "https://api.deepseek.com") {
		t.Fatalf("Popup mất Base URL:\n%s", modal)
	}
}

func TestProviderHubDeleteClearsOnlyOptionalAPIKey(t *testing.T) {
	optional := &modelConfigState{step: configStepHub, provider: "proxy", providerType: "openai",
		hasAPIKey: true, apiKeyHint: "sk-o******7890", apiKeyOptional: true, apiKeyAction: host.APIKeyKeep}
	optional.cursor = hubFieldIndex(optional.hubFields(), "key")
	m := Model{modelConfig: optional}
	m.handleModelConfigKey(tea.KeyMsg{Type: tea.KeyDelete})
	if optional.apiKeyAction != host.APIKeyClear || optional.keyStatus() != "Đã xóa" {
		t.Fatalf("Delete Key tùy chọn phải đánh dấu xóa, action=%q status=%q", optional.apiKeyAction, optional.keyStatus())
	}

	required := &modelConfigState{step: configStepHub, provider: "openrouter",
		hasAPIKey: true, apiKeyHint: "sk-o******7890", apiKeyOptional: false, apiKeyAction: host.APIKeyKeep}
	required.cursor = hubFieldIndex(required.hubFields(), "key")
	m = Model{modelConfig: required}
	m.handleModelConfigKey(tea.KeyMsg{Type: tea.KeyDelete})
	if required.apiKeyAction != host.APIKeyKeep || !strings.Contains(required.message, "không thể xóa") {
		t.Fatalf("Key bắt buộc không được xóa, action=%q message=%q", required.apiKeyAction, required.message)
	}
}

func TestConfigTextInputSupportsCursorEditing(t *testing.T) {
	state := &modelConfigState{step: configStepCustomName}
	state.startTextInput("ac", "Tên Provider", false)
	state.input.SetCursor(1)
	m := Model{modelConfig: state}
	m.handleModelConfigKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}})
	if got := state.input.Value(); got != "abc" {
		t.Fatalf("Ô nhập chung phải chèn được tại con trỏ, được %q", got)
	}
}

func TestProviderHubShowsConfigPathAndConnectionAction(t *testing.T) {
	state := &modelConfigState{
		step: configStepHub, provider: "proxy", apiKeyOptional: true, currentModel: "m2",
		models:   []bootstrap.ModelConfig{{Name: "m1"}, {Name: "m2"}},
		snapshot: host.ModelConfigurationSnapshot{ConfigPath: `C:\work\.ainovel\config.json`},
	}
	fields := state.hubFields()
	idx := hubFieldIndex(fields, "test")
	if idx < 0 || fields[idx].value != "m2" {
		t.Fatalf("Kiểm tra kết nối phải ưu tiên model hiện tại, fields=%#v", fields)
	}
	view := renderModelConfigModal(120, state)
	for _, want := range []string{"Cấu hình nâng cao", "extra_body"} {
		if !strings.Contains(view, want) {
			t.Fatalf("Hub cấu hình thiếu %q:\n%s", want, view)
		}
	}
	compact := strings.NewReplacer("\r", "", "\n", "", " ", "", "│", "").Replace(view)
	if !strings.Contains(compact, `C:\work\.ainovel\config.json`) {
		t.Fatalf("Hub cấu hình chưa hiện đủ đường dẫn config:\n%s", view)
	}
}

func TestModelConfigMessageWrapKeepsErrorTail(t *testing.T) {
	state := &modelConfigState{step: configStepHub, provider: "proxy", apiKeyOptional: true,
		message: "Kết nối thất bại: " + strings.Repeat("lỗi rất dài từ phía máy chủ ", 8) + " UNIQUE-ERROR-TAIL"}
	view := renderModelConfigModal(64, state)
	compact := strings.NewReplacer("\r", "", "\n", "", " ", "", "│", "").Replace(view)
	if !strings.Contains(compact, "UNIQUE-ERROR-TAIL") {
		t.Fatalf("Lỗi dài không được cắt mất đuôi:\n%s", view)
	}
}

func TestConnectionActionStartsAsyncTestWithoutLeavingHub(t *testing.T) {
	state := &modelConfigState{step: configStepHub, provider: "proxy", providerType: "openai",
		apiKeyOptional: true, models: []bootstrap.ModelConfig{{Name: "m1"}}}
	state.cursor = hubFieldIndex(state.hubFields(), "test")
	m := Model{modelConfig: state}
	_, cmd := m.handleModelConfigKey(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil || !state.testing || state.step != configStepHub {
		t.Fatalf("Kiểm tra kết nối phải chạy async trong hub, cmd=%v testing=%v step=%d", cmd != nil, state.testing, state.step)
	}
}

func TestConnectionTestCanBeCancelled(t *testing.T) {
	cancelled := false
	state := &modelConfigState{step: configStepHub, provider: "proxy", testing: true,
		testCancel: func() { cancelled = true }}
	m := Model{modelConfig: state}
	m.handleModelConfigKey(tea.KeyMsg{Type: tea.KeyEsc})
	if !cancelled || !state.testing || state.message != "Đang hủy kiểm tra kết nối..." {
		t.Fatalf("Esc phải hủy test đang chạy và chờ kết quả, cancelled=%v testing=%v message=%q", cancelled, state.testing, state.message)
	}

	updated, _, handled := m.handleRuntimeMsg(modelConfigConnectionMsg{err: context.Canceled})
	m = updated.(Model)
	if !handled || m.modelConfig.testing || m.modelConfig.message != "Đã hủy test kết nối" {
		t.Fatalf("Kết quả hủy chưa hội tụ đúng: handled=%v testing=%v message=%q", handled, m.modelConfig.testing, m.modelConfig.message)
	}
}

func TestConfigCommandIsRegistered(t *testing.T) {
	spec, ok := commandRegistryInstance().Find("config")
	if !ok {
		t.Fatal("/config is not registered")
	}
	if spec.Usage != "/config" || !spec.AutoExecute {
		t.Fatalf("config spec = %#v", spec)
	}
}

func TestModelSwitchLabelIncludesContextWindow(t *testing.T) {
	state := modelSwitchState{models: []host.ConfiguredModel{{Name: "gpt-test", ContextWindow: 400000}}}
	if got := state.modelLabel(); got != "gpt-test · 400K" {
		t.Fatalf("modelLabel = %q", got)
	}
}

// Giống /model: /config render thành popup có khung cao theo nội dung (không còn kéo 3/4 màn hình căn giữa).
func TestModelConfigModalIsCompactOverlay(t *testing.T) {
	state := &modelConfigState{step: configStepProvider, providerChoices: []configProviderChoice{
		{label: "Sửa openrouter", existing: &host.ProviderSnapshot{Name: "openrouter"}},
		{label: "+ Thêm Provider…", add: true},
	}}
	lines := strings.Split(renderModelConfigModal(120, state), "\n")

	// 1 dòng tiêu đề + 2 lựa chọn + viền trên/dưới = 5 dòng; cao theo nội dung, không phình theo màn hình.
	if len(lines) != 5 {
		t.Fatalf("Popup gọn phải 5 dòng (cao nội dung), được %d dòng:\n%s", len(lines), strings.Join(lines, "\n"))
	}
	if !strings.Contains(lines[0], "┌") || !strings.Contains(lines[0], "/config") {
		t.Fatalf("Dòng đầu phải là viền trên có tiêu đề /config, được %q", lines[0])
	}
	if !strings.Contains(lines[len(lines)-1], "└") {
		t.Fatalf("Dòng cuối phải là viền dưới, được %q", lines[len(lines)-1])
	}
}

// Menu cấp 1 chỉ liệt kê "sửa đã có + cửa thêm mới", không trải cả danh sách Provider dựng sẵn; danh sách chỉ ở menu cấp 2.
func TestProviderMenuIsTwoLevel(t *testing.T) {
	state := &modelConfigState{snapshot: host.ModelConfigurationSnapshot{
		Providers:       []host.ProviderSnapshot{{Name: "openrouter"}, {Name: "anthropic"}},
		DefaultProvider: "openrouter",
	}}
	state.buildProviderMenus()

	// Cấp 1 = 2 sửa + 1 cửa thêm; mục cuối là "thêm", không lẫn add/preset khác.
	if len(state.providerChoices) != 3 {
		t.Fatalf("Menu cấp 1 phải 2 sửa + 1 thêm, được %d mục", len(state.providerChoices))
	}
	if !state.providerChoices[len(state.providerChoices)-1].add {
		t.Fatal("Mục cuối menu cấp 1 phải là cửa thêm Provider…")
	}
	for i, c := range state.providerChoices[:2] {
		if c.existing == nil || c.add {
			t.Fatalf("Mục %d menu cấp 1 phải là sửa Provider đã có, được %#v", i, c)
		}
	}

	// Cấp 2 = danh sách thêm được: không rỗng, và mục dựng sẵn đã cấu hình (openrouter/anthropic) không lặp lại.
	if len(state.presetChoices) == 0 {
		t.Fatal("Menu cấp 2 phải liệt kê danh sách Provider thêm được")
	}
	if len(state.presetChoices) >= len(bootstrap.ProviderPresets()) {
		t.Fatalf("Provider dựng sẵn đã cấu hình phải loại khỏi danh sách thêm, presets=%d tổng=%d",
			len(state.presetChoices), len(bootstrap.ProviderPresets()))
	}
	for _, c := range state.presetChoices {
		if c.preset != nil && (c.preset.Name == "openrouter" || c.preset.Name == "anthropic") {
			t.Fatalf("Danh sách thêm không nên chứa %q đã cấu hình", c.preset.Name)
		}
	}
}
