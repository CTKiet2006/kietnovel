package sp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	storepkg "github.com/CTKiet2006/kietnovel/internal/store"
	"github.com/voocel/agentcore"
)

// Deps là những gì Service cần. Không có Engine, không có interMu, không có
// exclusive slot — cố tình thiếu để không thể chạm vào luồng viết dù muốn.
type Deps struct {
	Store *storepkg.Store
	// Model là ChatModel đã resolve cho role advisor (fallback về default nếu
	// chưa cấu hình, theo ModelSet.ForRoleWithFailover).
	Model agentcore.ChatModel
	// Provider/ModelName để ghi audit và accounting.
	Provider  string
	ModelName string
	// ResolveIdentity trả provider/model THỰC TẾ của attempt thành công gần nhất
	// (ví dụ LastTarget của failoverModel). Service gọi khi Usage không có
	// identity — để Result, accounting và audit cùng dùng một identity, thay vì
	// Result đúng còn audit/accounting ghi nhầm primary.
	// Nil thì bỏ qua (giữ resolve ban đầu).
	ResolveIdentity func() (provider, name string)
	// RecordUsage ghi accounting sidecar. BẮT BUỘC là đường không trigger abort
	// Engine (RecordSidecar của UsageTracker), không phải Record thường.
	RecordUsage func(u agentcore.Usage)
	// AuditDir là thư mục ghi audit, ví dụ <book>/logs/sp. Không nằm trong meta/.
	AuditDir string
}

// Service là advisor cho /sp hỏi. Một request, một response, không tool, không
// ghi truyện, không chạm Engine.
type Service struct {
	deps Deps
}

func NewService(d Deps) *Service { return &Service{deps: d} }

// Ask thực hiện một lượt hỏi. Thứ tự cố định: validate → snapshot → render →
// gọi model → accounting → audit. Bất kỳ bước nào cũng tôn trọng ctx: cancel thì
// dừng ngay và trả context.Canceled, không để lại tác dụng phụ nửa vời.
func (s *Service) Ask(ctx context.Context, req Request) (Result, error) {
	start := time.Now()
	if err := validate(req); err != nil {
		return Result{}, err
	}
	if s.deps.Store == nil {
		return Result{}, errors.New("sp: thiếu Store")
	}
	if s.deps.Model == nil {
		return Result{}, errors.New("sp: thiếu advisor model")
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}

	snap, err := BuildSnapshot(s.deps.Store)
	if err != nil {
		return Result{}, err
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}

	system, user := RenderPrompt(snap, req.Question, requestLanguage(req))
	msgs := []agentcore.Message{
		{Role: agentcore.RoleSystem, Content: textBlocks(system)},
		{Role: agentcore.RoleUser, Content: textBlocks(user)},
	}
	// Không tools: advisor chỉ đọc snapshot đã render, không gọi gì thêm.
	resp, err := s.deps.Model.Generate(ctx, msgs, nil)
	if err != nil {
		// Cancel phải về context.Canceled thay vì lỗi provider chung chung:
		// model thật khi ctx hủy giữa flight có thể trả lỗi bọc ngoài, và
		// caller (single-flight, UI) dựa vào errors.Is(err, context.Canceled).
		if ctx.Err() != nil {
			return Result{}, ctx.Err()
		}
		return Result{}, err
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}

	res := Result{
		Answer:         messageText(resp),
		SnapshotDigest: snap.ProgressDigest,
		CapturedAt:     snap.CapturedAt,
		Chapter:        snap.Chapter,
		Provider:       s.deps.Provider,
		Model:          s.deps.ModelName,
	}
	// Actual attempt TRƯỚC, Usage overlay SAU — thứ tự này là bắt buộc.
	// ResolveIdentity (LastTarget của failoverModel) cho biết attempt thành công
	// gần nhất là primary hay fallback, kể cả khi Usage rỗng identity hoặc
	// Usage nil hoàn toàn. Không kiểm tra "cả hai có trùng primary không" vì
	// Usage.Provider="fallback"/Model="" hoặc Usage==nil đều lọt qua khe đó.
	if s.deps.ResolveIdentity != nil {
		if p, n := s.deps.ResolveIdentity(); p != "" && n != "" {
			res.Provider, res.Model = p, n
		}
	}
	if resp != nil && resp.Message.Usage != nil {
		u := *resp.Message.Usage
		res.InputTokens, res.OutputTokens = u.Input, u.Output
		res.CacheRead, res.CacheWrite = u.CacheRead, u.CacheWrite
		// Usage overlay từng field: field nào có thật thì đè.
		if strings.TrimSpace(u.Provider) != "" {
			res.Provider = u.Provider
		}
		if strings.TrimSpace(u.Model) != "" {
			res.Model = u.Model
		}
		// Điền identity cuối vào usage trước khi accounting: cùng một identity
		// cho Result, accounting và audit.
		u.Provider, u.Model = res.Provider, res.Model
		if s.deps.RecordUsage != nil {
			s.deps.RecordUsage(u)
		}
	}
	writeAudit(s.deps.AuditDir, auditEntry{
		At:             start,
		Mode:           string(req.Mode),
		Question:       req.Question,
		Language:       requestLanguage(req),
		SnapshotDigest: snap.ProgressDigest,
		Chapter:        snap.Chapter,
		Provider:       res.Provider,
		Model:          res.Model,
		Answer:         res.Answer,
		InputTokens:    res.InputTokens,
		OutputTokens:   res.OutputTokens,
		Duration:       time.Since(start),
	})
	return res, nil
}

func validate(req Request) error {
	if req.Mode != ModeAsk {
		return fmt.Errorf("sp: mode %q chưa hỗ trợ ở phase 1 (chỉ có ask)", req.Mode)
	}
	if strings.TrimSpace(req.Question) == "" {
		return errors.New("sp: câu hỏi rỗng")
	}
	return nil
}

// requestLanguage chuẩn hoá ngôn ngữ trả lời: rỗng hoặc lạ thì về vi.
// Không đọc global UI state ở đây — caller capture từ trước.
func requestLanguage(req Request) string {
	switch strings.ToLower(strings.TrimSpace(req.Language)) {
	case "en":
		return "en"
	case "zh":
		return "zh"
	default:
		return "vi"
	}
}

func textBlocks(s string) []agentcore.ContentBlock {
	return []agentcore.ContentBlock{{Type: agentcore.ContentText, Text: s}}
}

func messageText(resp *agentcore.LLMResponse) string {
	if resp == nil {
		return ""
	}
	return resp.Message.TextContent()
}
