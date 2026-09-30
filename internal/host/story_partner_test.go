package host

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/CTKiet2006/kietnovel/internal/bootstrap"
	"github.com/CTKiet2006/kietnovel/internal/sp"
	storepkg "github.com/CTKiet2006/kietnovel/internal/store"
	"github.com/voocel/agentcore"
)

// fakeSPModel là advisor giả cho test Host-level: không gọi mạng.
type fakeSPModel struct {
	mu       sync.Mutex
	answer   string
	usage    agentcore.Usage
	err      error
	calls    int
	sawTools bool
	block    chan struct{} // != nil thì Generate chờ tới khi đóng hoặc ctx hủy
	// blockFirstOnly: chỉ lượt gọi đầu mới chờ. Dùng cho test "B hủy A":
	// A vào chờ, B tới hủy A rồi chạy thẳng không chờ.
	blockFirstOnly bool
}

func (m *fakeSPModel) Generate(ctx context.Context, msgs []agentcore.Message, tools []agentcore.ToolSpec, _ ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	m.mu.Lock()
	m.calls++
	m.mu.Unlock()
	if len(tools) > 0 {
		m.mu.Lock()
		m.sawTools = true
		m.mu.Unlock()
	}
	if m.block != nil && (!m.blockFirstOnly || m.calls == 1) {
		select {
		case <-m.block:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return nil, m.err
	}
	return &agentcore.LLMResponse{Message: agentcore.Message{
		Role:    agentcore.RoleAssistant,
		Content: []agentcore.ContentBlock{{Type: agentcore.ContentText, Text: m.answer}},
		Usage:   &m.usage,
	}}, nil
}

func (m *fakeSPModel) GenerateStream(ctx context.Context, _ []agentcore.Message, _ []agentcore.ToolSpec, _ ...agentcore.CallOption) (<-chan agentcore.StreamEvent, error) {
	ch := make(chan agentcore.StreamEvent)
	close(ch)
	return ch, ctx.Err()
}

func (m *fakeSPModel) SupportsTools() bool { return false }

func (m *fakeSPModel) numCalls() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls
}

// newSPTestHost dựng Host đủ để chạy AskStoryPartner: store thật + ModelSet thật
// (key giả, không gọi mạng lúc tạo) + UsageTracker thật. Advisor model là fake
// qua seam, production không dùng đường này.
func newSPTestHost(t *testing.T, fake *fakeSPModel) (*Host, string) {
	t.Helper()
	base := t.TempDir()
	novelDir := filepath.Join(base, "output", "novel")
	st := storepkg.NewStore(novelDir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init(0); err != nil {
		t.Fatal(err)
	}
	cfg := bootstrap.Config{
		Provider: "openrouter", ModelName: "test-model", OutputDir: novelDir,
		Providers: map[string]bootstrap.ProviderConfig{
			"openrouter": {APIKey: "test-key", BaseURL: "https://openrouter.ai/api/v1"},
		},
	}
	ms, err := bootstrap.NewModelSet(cfg)
	if err != nil {
		t.Fatalf("NewModelSet: %v", err)
	}
	h := &Host{
		cfg:      cfg,
		store:    st,
		models:   ms,
		usage:    NewUsageTracker(nil, nil),
		events:   make(chan Event, 16),
		streamCh: make(chan string, 16),
		done:     make(chan struct{}),
		resolveAdvisorModel: func() agentcore.ChatModel {
			return fake
		},
	}
	return h, novelDir
}

func spReq(q string) sp.Request { return sp.Request{Mode: sp.ModeAsk, Question: q} }

// A. Engine đang chạy (giả lập lifecycle) + Ask → lifecycle không đổi.
func TestSPHostEngineDangChayKhongDoi(t *testing.T) {
	fake := &fakeSPModel{answer: "[FACT progress] ok", usage: agentcore.Usage{Input: 10, Output: 5}}
	h, _ := newSPTestHost(t, fake)
	h.lifecycle = lifecycleRunning

	res, err := h.AskStoryPartner(context.Background(), spReq("Tình hình?"))
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if !strings.Contains(res.Answer, "[FACT progress]") {
		t.Errorf("answer sai: %q", res.Answer)
	}
	if h.lifecycle != lifecycleRunning {
		t.Errorf("lifecycle đổi thành %q — /sp không được động vào Engine", h.lifecycle)
	}
	if fake.numCalls() != 1 {
		t.Errorf("gọi model %d lần, mong 1", fake.numCalls())
	}
	if fake.sawTools {
		t.Error("advisor không được kèm tools")
	}
}

// B. Cancel advisor đang block → context.Canceled.
func TestSPHostCancelTraVe(t *testing.T) {
	fake := &fakeSPModel{answer: "x", block: make(chan struct{})}
	h, _ := newSPTestHost(t, fake)

	done := make(chan error, 1)
	go func() {
		_, err := h.AskStoryPartner(context.Background(), spReq("Đợi?"))
		done <- err
	}()
	time.Sleep(200 * time.Millisecond)
	h.CancelStoryPartner()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("phải context.Canceled, được %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Ask treo sau CancelStoryPartner")
	}
}

// C. Request B hủy request A đang chạy; B chạy tiếp bình thường.
func TestSPHostRequestMoiHuyCu(t *testing.T) {
	fake := &fakeSPModel{answer: "b-xong", block: make(chan struct{}), blockFirstOnly: true}
	h, _ := newSPTestHost(t, fake)

	errA := make(chan error, 1)
	go func() {
		_, err := h.AskStoryPartner(context.Background(), spReq("A?"))
		errA <- err
	}()
	// Đợi A vào tới Generate và mắc ở block.
	deadline := time.Now().Add(10 * time.Second)
	for fake.numCalls() == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if fake.numCalls() == 0 {
		t.Fatal("A không vào tới Generate")
	}
	time.Sleep(100 * time.Millisecond)

	// B tới: hủy A (A đang chờ block), B chạy thẳng vì chỉ lượt đầu mới chờ.
	resB, errB := h.AskStoryPartner(context.Background(), spReq("B?"))
	if errB != nil {
		t.Fatalf("B phải xong, lỗi: %v", errB)
	}
	if !strings.Contains(resB.Answer, "b-xong") {
		t.Errorf("B sai: %q", resB.Answer)
	}
	select {
	case err := <-errA:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("A phải bị cancel, được %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("A treo")
	}
}

// D. Sidecar budget: cost lớn, overall không đổi, onCost không gọi, không abort.
func TestSPHostSidecarBudgetKhongAbort(t *testing.T) {
	fake := &fakeSPModel{
		answer: "ok",
		usage:  agentcore.Usage{Input: 1000000, Output: 500000},
	}
	h, _ := newSPTestHost(t, fake)
	var onCost []float64
	h.usage.SetOnCost(func(total float64) { onCost = append(onCost, total) })
	before, _, _, _, _ := h.usage.Totals()

	if _, err := h.AskStoryPartner(context.Background(), spReq("Tốn?")); err != nil {
		t.Fatal(err)
	}
	if len(onCost) != 0 {
		t.Fatalf("onCost gọi %d lần — BudgetSentinel có thể abort vì /sp", len(onCost))
	}
	after, _, _, _, _ := h.usage.Totals()
	if after != before {
		t.Errorf("overall đổi %v -> %v — tiền advisor lọt vào tổng Engine", before, after)
	}
	h.usage.mu.Lock()
	per := h.usage.perAgent["advisor"]
	h.usage.mu.Unlock()
	if per == nil || per.Input != 1000000 {
		t.Errorf("perAgent[advisor] thiếu/sai: %+v", per)
	}
}

// E. Model role: usage mang identity thật thì Result dùng nó.
func TestSPHostIdentityUuTienUsageThat(t *testing.T) {
	fake := &fakeSPModel{
		answer: "ok",
		usage:  agentcore.Usage{Input: 5, Output: 2, Provider: "openrouter", Model: "real-model"},
	}
	h, _ := newSPTestHost(t, fake)
	res, err := h.AskStoryPartner(context.Background(), spReq("Ai?"))
	if err != nil {
		t.Fatal(err)
	}
	if res.Provider != "openrouter" || res.Model != "real-model" {
		t.Errorf("identity sai: %q %q — phải ưu tiên usage thật", res.Provider, res.Model)
	}
}

// G. Audit: một entry ở logs/sp/sp.jsonl, không có gì trong meta/.
func TestSPHostAuditNgoaiMeta(t *testing.T) {
	fake := &fakeSPModel{answer: "ok", usage: agentcore.Usage{Input: 3, Output: 1}}
	h, novelDir := newSPTestHost(t, fake)
	if _, err := h.AskStoryPartner(context.Background(), spReq("Ghi?")); err != nil {
		t.Fatal(err)
	}
	auditPath := filepath.Join(novelDir, "logs", "sp", "sp.jsonl")
	data, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatalf("thiếu audit: %v", err)
	}
	for _, must := range []string{"Ghi?", "snapshot_digest", "chapter"} {
		if !strings.Contains(string(data), must) {
			t.Errorf("audit thiếu %q", must)
		}
	}
	// Không file audit nào trong meta/.
	_ = filepath.Walk(filepath.Join(novelDir, "meta"), func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && strings.Contains(info.Name(), "sp") {
			t.Errorf("audit lọt vào meta/: %s", p)
		}
		return nil
	})
}

// H. No mutation: hash story data trước/sau giống nhau.
func TestSPHostKhongMutateStory(t *testing.T) {
	fake := &fakeSPModel{answer: "ok", usage: agentcore.Usage{Input: 2, Output: 1}}
	h, novelDir := newSPTestHost(t, fake)
	before := hashStoryDir(t, novelDir)
	if _, err := h.AskStoryPartner(context.Background(), spReq("Đọc?")); err != nil {
		t.Fatal(err)
	}
	after := hashStoryDir(t, novelDir)
	if before != after {
		t.Error("AskStoryPartner làm đổi file truyện — sidecar chỉ được đọc")
	}
}

// hashStoryDir băm toàn bộ cây thư mục truyện, trừ logs/ (audit và log vận hành
// được phép ghi — chúng không phải story data).
func hashStoryDir(t *testing.T, dir string) string {
	t.Helper()
	h := sha256.New()
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		if rel == "logs" || strings.HasPrefix(rel, "logs"+string(filepath.Separator)) {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		h.Write([]byte(rel))
		h.Write(data)
		return nil
	})
	return fmt.Sprintf("%x", h.Sum(nil))
}
