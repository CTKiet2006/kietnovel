package sp

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/CTKiet2006/kietnovel/internal/domain"
	storepkg "github.com/CTKiet2006/kietnovel/internal/store"
	"github.com/voocel/agentcore"
)

// fakeAdvisorModel là ChatModel giả cho test: trả lời theo kịch bản, ghi lại
// những gì service gửi để assert "không tools, không mutation".
// Mọi field đọc/ghi qua mutex vì Generate chạy goroutine khác với goroutine
// poll trong test — đọc trực tiếp là data race dưới -race.
type fakeAdvisorModel struct {
	mu            sync.Mutex
	answer        string
	usage         agentcore.Usage
	err           error
	calls         int
	sawTools      bool
	blockUntil    chan struct{}
	wrapCancelErr bool
	// noUsage: trả Usage nil — mô phỏng provider không báo usage.
	noUsage bool
}

func (m *fakeAdvisorModel) numCalls() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls
}

func (m *fakeAdvisorModel) sawToolsLocked() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sawTools
}

func (m *fakeAdvisorModel) Generate(ctx context.Context, msgs []agentcore.Message, tools []agentcore.ToolSpec, _ ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	m.mu.Lock()
	m.calls++
	if len(tools) > 0 {
		m.sawTools = true
	}
	blockUntil, wrapCancelErr := m.blockUntil, m.wrapCancelErr
	fakeErr, answer, usage := m.err, m.answer, m.usage
	fakeNoUsage := m.noUsage
	m.mu.Unlock()
	_ = msgs
	if blockUntil != nil {
		select {
		case <-blockUntil:
		case <-ctx.Done():
			if wrapCancelErr {
				return nil, errors.New("provider client: " + ctx.Err().Error())
			}
			return nil, ctx.Err()
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if fakeErr != nil {
		return nil, fakeErr
	}
	msg := agentcore.Message{
		Role:    agentcore.RoleAssistant,
		Content: []agentcore.ContentBlock{{Type: agentcore.ContentText, Text: answer}},
	}
	if !fakeNoUsage {
		u := usage
		msg.Usage = &u
	}
	return &agentcore.LLMResponse{Message: msg}, nil
}

func (m *fakeAdvisorModel) GenerateStream(ctx context.Context, _ []agentcore.Message, _ []agentcore.ToolSpec, _ ...agentcore.CallOption) (<-chan agentcore.StreamEvent, error) {
	ch := make(chan agentcore.StreamEvent)
	close(ch)
	return ch, ctx.Err()
}

func (m *fakeAdvisorModel) SupportsTools() bool { return false }

func startWriting(t *testing.T, st *storepkg.Store) {
	t.Helper()
	p, err := st.Progress.Load()
	if err != nil || p == nil {
		t.Fatalf("load progress: %v", err)
	}
	p.Phase = domain.PhaseWriting
	if err := st.Progress.Save(p); err != nil {
		t.Fatal(err)
	}
}

func newTestStore(t *testing.T) *storepkg.Store {
	t.Helper()
	st := storepkg.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init(0); err != nil {
		t.Fatal(err)
	}
	return st
}

// 1. snapshot đọc được khi không có mutation.
func TestSnapshotDocDuocKhiYen(t *testing.T) {
	st := newTestStore(t)
	snap, err := BuildSnapshot(st)
	if err != nil {
		t.Fatalf("BuildSnapshot: %v", err)
	}
	if snap.ProgressDigest == "" {
		t.Error("thiếu ProgressDigest")
	}
	if snap.CapturedAt.IsZero() {
		t.Error("thiếu CapturedAt")
	}
	if got := snap.Block("progress"); got == nil {
		t.Error("thiếu block progress")
	}
}

// 2. mỗi ContextBlock có stable source ID: không rỗng, duy nhất.
func TestMoiBlockCoSourceIDOnDinh(t *testing.T) {
	st := newTestStore(t)
	startWriting(t, st)
	if err := st.Progress.StartChapter(2); err != nil {
		t.Fatal(err)
	}
	snap, err := BuildSnapshot(st)
	if err != nil {
		t.Fatalf("BuildSnapshot: %v", err)
	}
	seen := map[string]bool{}
	for _, b := range snap.Blocks {
		if strings.TrimSpace(b.ID) == "" {
			t.Errorf("block Kind=%q thiếu ID", b.Kind)
		}
		if strings.TrimSpace(b.Kind) == "" {
			t.Errorf("block ID=%q thiếu Kind", b.ID)
		}
		if seen[b.ID] {
			t.Errorf("trùng ID %q", b.ID)
		}
		seen[b.ID] = true
	}
}

// 3. anchor nhạy với thay đổi progress: đổi chapter thì digest phải đổi.
// (Chứng minh anchor so đúng thứ quyết định story position, không phải timestamp.)
func TestAnchorNhayVoiThayDoiProgress(t *testing.T) {
	st := newTestStore(t)
	startWriting(t, st)
	a, err := BuildSnapshot(st)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.StartChapter(3); err != nil {
		t.Fatal(err)
	}
	b, err := BuildSnapshot(st)
	if err != nil {
		t.Fatal(err)
	}
	if a.ProgressDigest == b.ProgressDigest {
		t.Error("đổi chapter mà digest không đổi — anchor mù")
	}
}

// 3b. concurrent: Writer commit liên tục trong lúc build — không panic, không treo,
// kết quả luôn là snapshot hợp lệ HOẶC lỗi rõ ràng, không bao giờ nửa vời im lặng.
func TestSnapshotChiuDuocWriterConcurrent(t *testing.T) {
	st := newTestStore(t)
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 1; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			_ = st.Progress.StartChapter(i%5 + 1)
		}
	}()
	defer func() { close(stop); wg.Wait() }()

	for i := 0; i < 20; i++ {
		snap, err := BuildSnapshot(st)
		if err != nil {
			// Lỗi hợp lệ duy nhất: story đang thay đổi.
			if !strings.Contains(err.Error(), "đang thay đổi") {
				t.Fatalf("lỗi lạ: %v", err)
			}
			continue
		}
		// Snapshot hợp lệ: digest phải khớp nội dung blocks.
		if snap.ProgressDigest == "" || len(snap.Blocks) == 0 {
			t.Fatal("snapshot rỗng mà không báo lỗi")
		}
	}
}

// 4. store chưa init (Progress.Load lỗi) → lỗi rõ ràng, không phải "đang thay đổi".
func TestSnapshotLoiRoRangKhiStoreHong(t *testing.T) {
	st := storepkg.NewStore(t.TempDir())
	_, err := BuildSnapshot(st)
	if err == nil {
		t.Fatal("store chưa init phải lỗi")
	}
	if strings.Contains(err.Error(), "đang thay đổi") {
		t.Errorf("lỗi sai loại: %v", err)
	}
}

// 5. snapshot không chứa live Writer stream: nháp đang viết dở không được lọt vào.
func TestSnapshotKhongChuaDraftDangViet(t *testing.T) {
	st := newTestStore(t)
	secret := "doan-van-dang-stream-chua-commit-xyz"
	if err := st.Drafts.SaveDraft(1, secret); err != nil {
		t.Fatal(err)
	}
	snap, err := BuildSnapshot(st)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range snap.Blocks {
		if strings.Contains(b.Content, secret) {
			t.Errorf("block %q chứa nội dung draft chưa commit", b.ID)
		}
		if b.Kind == "draft" || b.Kind == "stream" {
			t.Errorf("block Kind=%q không được tồn tại trong snapshot", b.Kind)
		}
	}
}

// 6. prompt contract chứa các rule fact/inference/unknown + read-only.
func TestPromptContractDuRule(t *testing.T) {
	sys, _ := RenderPrompt(StorySnapshot{Blocks: []ContextBlock{{ID: "progress", Kind: "progress", Content: "x"}}}, "hỏi gì?", "vi")
	for _, must := range []string{
		"[FACT]", "[INFERENCE]", "[OPTION]", "[UNKNOWN]",
		"Không tạo canon", "Không sửa truyện", "gọi tool", "quyết định thay",
		"không biết",
	} {
		if !strings.Contains(sys, must) {
			t.Errorf("contract thiếu %q", must)
		}
	}
}

// RenderPrompt không cần LLM và phải giữ source ID trong user prompt.
func TestRenderPromptGiuSourceID(t *testing.T) {
	snap := StorySnapshot{ProgressDigest: "abc", Blocks: []ContextBlock{
		{ID: "outline:chapter:5", Kind: "outline", Content: "nội dung"},
	}}
	_, user := RenderPrompt(snap, "Ngọc nên làm gì?", "vi")
	if !strings.Contains(user, "[outline:chapter:5]") {
		t.Error("user prompt mất source ID")
	}
	if !strings.Contains(user, "Ngọc nên làm gì?") {
		t.Error("user prompt mất câu hỏi")
	}
}

// 7. fake model trả answer → service trả Result đúng, kèm digest và usage.
func TestServiceTraResultDung(t *testing.T) {
	st := newTestStore(t)
	fake := &fakeAdvisorModel{
		answer: "[FACT progress] chương 1 chưa viết.",
		usage:  agentcore.Usage{Input: 100, Output: 20},
	}
	var recorded []agentcore.Usage
	svc := NewService(Deps{
		Store: st, Model: fake, Provider: "p", ModelName: "m",
		RecordUsage: func(u agentcore.Usage) { recorded = append(recorded, u) },
		AuditDir:    t.TempDir(),
	})
	res, err := svc.Ask(context.Background(), Request{Mode: ModeAsk, Question: "Tình hình?"})
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if !strings.Contains(res.Answer, "[FACT progress]") {
		t.Errorf("answer sai: %q", res.Answer)
	}
	if res.SnapshotDigest == "" || res.Provider != "p" || res.Model != "m" {
		t.Errorf("Result thiếu trường: %+v", res)
	}
	if res.InputTokens != 100 || res.OutputTokens != 20 {
		t.Errorf("usage sai: %+v", res)
	}
	if len(recorded) != 1 {
		t.Fatalf("accounting phải ghi đúng 1 lần, được %d", len(recorded))
	}
	if fake.numCalls() != 1 {
		t.Errorf("phải gọi model đúng 1 lần, được %d", fake.numCalls())
	}
	if fake.sawToolsLocked() {
		t.Error("advisor không được kèm tools")
	}
}

// Câu hỏi rỗng (ask) và mode lạ phải bị từ chối trước khi chạm Store/model.
// soi/gợi ý không cần question (tự xác định context từ story state).
func TestServiceValidateTruoc(t *testing.T) {
	svc := NewService(Deps{})
	if _, err := svc.Ask(context.Background(), Request{Mode: ModeAsk}); err == nil {
		t.Error("ask rỗng phải lỗi")
	}
	if _, err := svc.Ask(context.Background(), Request{Mode: "x-la"}); err == nil {
		t.Error("mode lạ phải từ chối rõ")
	}
}

// P4: cả 3 mode được service chấp nhận; soi/gợi ý không cần question.
func TestServiceChapNhanBaMode(t *testing.T) {
	st := newTestStore(t)
	for _, req := range []Request{
		{Mode: ModeAsk, Question: "Hỏi?"},
		{Mode: ModeInspect},
		{Mode: ModeSuggest},
	} {
		svc := NewService(Deps{
			Store:    st,
			Model:    &fakeAdvisorModel{answer: "ok", usage: agentcore.Usage{Input: 1, Output: 1}},
			AuditDir: t.TempDir(),
		})
		res, err := svc.Ask(context.Background(), req)
		if err != nil {
			t.Errorf("%s lỗi: %v", req.Mode, err)
			continue
		}
		if res.Answer != "ok" {
			t.Errorf("%s answer sai", req.Mode)
		}
	}
}

// P4: audit ghi mode + context metrics (chars/blocks/truncated), digest giữ nguyên ý nghĩa.
func TestServiceAuditGhiContextMetrics(t *testing.T) {
	st := newTestStore(t)
	dir := t.TempDir()
	svc := NewService(Deps{
		Store:    st,
		Model:    &fakeAdvisorModel{answer: "ok", usage: agentcore.Usage{Input: 10, Output: 5}},
		AuditDir: dir,
	})
	res, err := svc.Ask(context.Background(), Request{Mode: ModeInspect})
	if err != nil {
		t.Fatal(err)
	}
	if res.SnapshotDigest == "" {
		t.Error("thiếu digest")
	}
	data, err := os.ReadFile(filepath.Join(dir, "sp.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var e map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(data))), &e); err != nil {
		t.Fatal(err)
	}
	if e["mode"] != "inspect" {
		t.Errorf("mode=%v", e["mode"])
	}
	if _, ok := e["context_chars"]; !ok {
		t.Error("thiếu context_chars")
	}
	if _, ok := e["context_blocks"]; !ok {
		t.Error("thiếu context_blocks")
	}
	if _, ok := e["context_truncated"]; !ok {
		t.Error("thiếu context_truncated")
	}
	if _, ok := e["snapshot_digest"]; !ok {
		t.Error("thiếu snapshot_digest")
	}
}

// 8. service không mutate Store: hash toàn bộ file trước/sau phải giống nhau.
func TestServiceKhongMutateStore(t *testing.T) {
	dir := t.TempDir()
	st := storepkg.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init(0); err != nil {
		t.Fatal(err)
	}
	before := hashDir(t, dir)

	svc := NewService(Deps{
		Store:    st,
		Model:    &fakeAdvisorModel{answer: "ok", usage: agentcore.Usage{Input: 1, Output: 1}},
		AuditDir: t.TempDir(), // audit ra ngoài, không vào story dir
	})
	if _, err := svc.Ask(context.Background(), Request{Mode: ModeAsk, Question: "Có gì mới?"}); err != nil {
		t.Fatal(err)
	}
	after := hashDir(t, dir)
	if before != after {
		t.Error("Ask làm thay đổi file trong story dir — advisor chỉ được đọc")
	}
}

func hashDir(t *testing.T, dir string) string {
	t.Helper()
	h := sha256.New()
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		h.Write([]byte(p))
		h.Write(data)
		return nil
	})
	return fmt.Sprintf("%x", h.Sum(nil))
}

// Generate trả lỗi provider bọc ngoài trong lúc ctx bị hủy giữa flight → phải
// về context.Canceled, vì caller dựa vào errors.Is để phân biệt cancel với lỗi
// thật. Model thật khi ctx hủy giữa flight không đảm bảo trả đúng ctx.Err().
func TestServiceLoiBocVanVeCanceled(t *testing.T) {
	st := newTestStore(t)
	fake := &fakeAdvisorModel{
		answer:        "x",
		blockUntil:    make(chan struct{}),
		wrapCancelErr: true,
	}
	svc := NewService(Deps{Store: st, Model: fake, AuditDir: t.TempDir()})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := svc.Ask(ctx, Request{Mode: ModeAsk, Question: "Hỏi?"})
		done <- err
	}()
	// Đợi fake vào tới Generate rồi hủy giữa flight.
	deadline := time.Now().Add(10 * time.Second)
	for fake.numCalls() == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("phải context.Canceled, được %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Ask treo sau cancel")
	}
}

// TestIdentityDungChungChoResultUsageAudit — case review chỉ ra:
// primary -> 429, fallback chạy, Usage rỗng identity.
// Trước fix: Result đúng fallback (Host sửa sau), nhưng audit đã ghi primary và
// accounting cũng nhận identity rỗng → perModel sai.
// Sau fix: ResolveIdentity trong Service, cả ba cùng fallback.
func TestIdentityDungChungChoResultUsageAudit(t *testing.T) {
	st := newTestStore(t)
	fake := &fakeAdvisorModel{
		answer: "[FACT progress] ok",
		// Usage KHÔNG có provider/model — đúng tình huống review nêu.
		usage: agentcore.Usage{Input: 50, Output: 10},
	}
	var recorded []agentcore.Usage
	auditDir := t.TempDir()
	svc := NewService(Deps{
		Store: st, Model: fake,
		Provider: "openrouter", ModelName: "advisor-model",
		// Giả lập failover đã chạy fallback (LastTarget của failoverModel thật).
		ResolveIdentity: func() (string, string) { return "openrouter", "fallback-model" },
		RecordUsage:     func(u agentcore.Usage) { recorded = append(recorded, u) },
		AuditDir:        auditDir,
	})
	res, err := svc.Ask(context.Background(), Request{Mode: ModeAsk, Question: "Ai?"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Provider != "openrouter" || res.Model != "fallback-model" {
		t.Errorf("Result sai: %q/%q", res.Provider, res.Model)
	}
	if len(recorded) != 1 {
		t.Fatalf("recorded %d lần", len(recorded))
	}
	if recorded[0].Provider != "openrouter" || recorded[0].Model != "fallback-model" {
		t.Errorf("accounting sai: %+v", recorded[0])
	}
	data, err := os.ReadFile(filepath.Join(auditDir, "sp.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "fallback-model") {
		t.Errorf("audit không ghi fallback: %s", data)
	}
	if strings.Contains(string(data), "advisor-model") {
		t.Errorf("audit lọt primary: %s", data)
	}
}

// TestIdentityLuaThieu truong hop review chi ra:
// Usage.Provider="fallback" nhung Usage.Model="" (thieu mot field).
// Truoc fix: gate "ca hai trung primary" cho qua vi Model rong != Model resolve?
// Khong — gate cu so res (da overlay Provider) voi deps: Provider da thanh
// fallback nen gate false, khong hoi LastTarget cho Model → Model giu resolve
// (sai neu fallback ten khac). Sau fix: actual truoc (fallback/fallback-model),
// Usage de tung field.
func TestIdentityLuaThieu(t *testing.T) {
	st := newTestStore(t)
	var recorded []agentcore.Usage
	auditDir := t.TempDir()
	svc := NewService(Deps{
		Store: st,
		Model: &fakeAdvisorModel{
			answer: "x",
			usage:  agentcore.Usage{Input: 5, Output: 1, Provider: "fallback-prov"},
		},
		Provider: "openrouter", ModelName: "advisor-model",
		ResolveIdentity: func() (string, string) { return "fallback-prov", "fallback-model" },
		RecordUsage:     func(u agentcore.Usage) { recorded = append(recorded, u) },
		AuditDir:        auditDir,
	})
	res, err := svc.Ask(context.Background(), Request{Mode: ModeAsk, Question: "Ai?"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Provider != "fallback-prov" || res.Model != "fallback-model" {
		t.Errorf("Result sai: %q/%q", res.Provider, res.Model)
	}
	if recorded[0].Provider != "fallback-prov" || recorded[0].Model != "fallback-model" {
		t.Errorf("accounting sai: %+v", recorded[0])
	}
	data, _ := os.ReadFile(filepath.Join(auditDir, "sp.jsonl"))
	if !strings.Contains(string(data), "fallback-model") {
		t.Errorf("audit sai: %s", data)
	}
}

// TestIdentityUsageNil — Usage nil hoan toan: actual attempt van phai len Result
// va audit (accounting khong co gi de ghi).
func TestIdentityUsageNil(t *testing.T) {
	st := newTestStore(t)
	svc := NewService(Deps{
		Store:    st,
		Model:    &fakeAdvisorModel{answer: "x", noUsage: true},
		Provider: "openrouter", ModelName: "advisor-model",
		ResolveIdentity: func() (string, string) { return "fallback-prov", "fallback-model" },
		AuditDir:        t.TempDir(),
	})
	res, err := svc.Ask(context.Background(), Request{Mode: ModeAsk, Question: "Ai?"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Provider != "fallback-prov" || res.Model != "fallback-model" {
		t.Errorf("Result sai: %q/%q", res.Provider, res.Model)
	}
}

// TestMissingUsageCoDiagnostic — model trả lời nhưng Usage nil thì hook
// OnMissingUsage phải chạy (host bật flagMissingUsage). Không thì usage = 0
// mà im lặng, tưởng request miễn phí.
func TestMissingUsageCoDiagnostic(t *testing.T) {
	st := newTestStore(t)
	called := 0
	svc := NewService(Deps{
		Store:    st,
		Model:    &fakeAdvisorModel{answer: "x", noUsage: true},
		Provider: "openrouter", ModelName: "advisor-model",
		OnMissingUsage: func() { called++ },
		AuditDir:       t.TempDir(),
	})
	if _, err := svc.Ask(context.Background(), Request{Mode: ModeAsk, Question: "Ai?"}); err != nil {
		t.Fatal(err)
	}
	if called != 1 {
		t.Errorf("OnMissingUsage gọi %d lần, mong 1", called)
	}

	// Có usage thì hook không chạy.
	called = 0
	svc2 := NewService(Deps{
		Store:    st,
		Model:    &fakeAdvisorModel{answer: "x", usage: agentcore.Usage{Input: 1, Output: 1}},
		Provider: "openrouter", ModelName: "advisor-model",
		OnMissingUsage: func() { called++ },
		AuditDir:       t.TempDir(),
	})
	if _, err := svc2.Ask(context.Background(), Request{Mode: ModeAsk, Question: "Ai?"}); err != nil {
		t.Fatal(err)
	}
	if called != 0 {
		t.Errorf("có usage mà hook vẫn chạy %d lần", called)
	}
}
func TestIdentityGiuResolveKhiKhongCoNguon(t *testing.T) {
	st := newTestStore(t)
	svc := NewService(Deps{
		Store:    st,
		Model:    &fakeAdvisorModel{answer: "x", usage: agentcore.Usage{Input: 1, Output: 1}},
		Provider: "openrouter", ModelName: "advisor-model",
		ResolveIdentity: func() (string, string) { return "", "" },
		AuditDir:        t.TempDir(),
	})
	res, err := svc.Ask(context.Background(), Request{Mode: ModeAsk, Question: "Ai?"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Provider != "openrouter" || res.Model != "advisor-model" {
		t.Errorf("phải giữ resolve, được %q/%q", res.Provider, res.Model)
	}
}

// TestAuditGhiCaLoiVaCancel — trước fix, hỏi hỏng hoặc Esc thì không có audit
// record nào ("hỏi lúc 20:35 mà sao không thấy log"). Giờ mọi đường về sau khi
// request bắt đầu đều ghi status/error.
func TestAuditGhiCaLoiVaCancel(t *testing.T) {
	readEntries := func(dir string) []map[string]any {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(dir, "sp.jsonl"))
		if err != nil {
			t.Fatal(err)
		}
		var out []map[string]any
		for _, ln := range strings.Split(strings.TrimSpace(string(data)), "\n") {
			var e map[string]any
			if err := json.Unmarshal([]byte(ln), &e); err != nil {
				t.Fatal(err)
			}
			out = append(out, e)
		}
		return out
	}

	// Lỗi provider.
	dirErr := t.TempDir()
	svcErr := NewService(Deps{
		Store:    newTestStore(t),
		Model:    &fakeAdvisorModel{err: errors.New("provider sập")},
		AuditDir: dirErr,
	})
	if _, err := svcErr.Ask(context.Background(), Request{Mode: ModeAsk, Question: "Hỏi?"}); err == nil {
		t.Fatal("phải lỗi")
	}
	entries := readEntries(dirErr)
	if len(entries) != 1 || entries[0]["status"] != "error" {
		t.Fatalf("audit lỗi sai: %+v", entries)
	}
	if !strings.Contains(entries[0]["error"].(string), "provider sập") {
		t.Errorf("audit thiếu message lỗi: %+v", entries[0])
	}
	if entries[0]["question"] != "Hỏi?" {
		t.Errorf("audit lỗi phải giữ question để tra: %+v", entries[0])
	}

	// Cancel giữa flight.
	dirCancel := t.TempDir()
	block := make(chan struct{})
	svcCancel := NewService(Deps{
		Store:    newTestStore(t),
		Model:    &fakeAdvisorModel{answer: "x", blockUntil: block},
		AuditDir: dirCancel,
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := svcCancel.Ask(ctx, Request{Mode: ModeAsk, Question: "Đợi?"})
		done <- err
	}()
	time.Sleep(100 * time.Millisecond)
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("phải Canceled, được %v", err)
	}
	entries = readEntries(dirCancel)
	if len(entries) != 1 || entries[0]["status"] != "canceled" {
		t.Fatalf("audit cancel sai: %+v", entries)
	}
}

func TestServiceCancelTraVe(t *testing.T) {
	st := newTestStore(t)
	fake := &fakeAdvisorModel{answer: "x", blockUntil: make(chan struct{})}
	svc := NewService(Deps{Store: st, Model: fake, AuditDir: t.TempDir()})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := svc.Ask(ctx, Request{Mode: ModeAsk, Question: "Đợi?"})
		done <- err
	}()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("phải trả context.Canceled, được %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Ask treo sau cancel")
	}
}
