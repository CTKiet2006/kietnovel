package host

import (
	"context"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/CTKiet2006/kietnovel/internal/bootstrap"
	"github.com/CTKiet2006/kietnovel/internal/sp"
	"github.com/voocel/agentcore"
)

// storyPartnerTimeout chặn trên cho một lượt hỏi advisor.
//
// Chỉ chống treo vô hạn, không liên quan độ dài câu trả lời (không có output
// token cap ở phase này). Hằng số đặt tên để dễ đổi/test thay vì số chôn trong code.
const storyPartnerTimeout = 2 * time.Minute

// AskStoryPartner chạy một lượt hỏi advisor trên runtime thật.
//
// Luồng: resolve advisor model (failover) → sp.Service → accounting sidecar →
// audit logs/sp. Không pause Engine, không interMu, không exclusive slot, không
// chạm lifecycle — Engine đang viết thì /sp vẫn chạy song song.
//
// Single-flight: mỗi thời điểm một request advisor. Request mới hủy request cũ
// qua context riêng của sidecar; ctx của caller vẫn được tôn trọng (cancel từ
// phía nào cũng về context.Canceled).
func (h *Host) AskStoryPartner(ctx context.Context, req sp.Request) (sp.Result, error) {
	gen, spCtx, cancel := h.startStoryPartnerCall(ctx)
	defer h.finishStoryPartnerCall(gen, cancel)

	model := h.resolveAdvisor()
	prov, name, _ := h.models.CurrentSelection("advisor")

	svc := sp.NewService(sp.Deps{
		Store:     h.store,
		Model:     model,
		Provider:  prov,
		ModelName: name,
		RecordUsage: func(u agentcore.Usage) {
			// Sidecar, KHÔNG phải Record thường: tiền advisor không được cộng
			// vào overall, không gọi onCost, không abort Engine.
			h.usage.RecordSidecar("advisor", u, u.Provider, u.Model)
		},
		AuditDir: filepath.Join(h.store.Dir(), "logs", "sp"),
	})
	return svc.Ask(spCtx, req)
}

// resolveAdvisor lấy advisor model qua failover thật.
//
// resolveAdvisorModel là seam để test inject fake model (không gọi mạng).
// Production luôn nil → đi đúng đường ForRoleWithFailover, không retry riêng,
// failover chỉ log theo cơ chế hiện tại.
func (h *Host) resolveAdvisor() agentcore.ChatModel {
	if h.resolveAdvisorModel != nil {
		return h.resolveAdvisorModel()
	}
	return h.models.ForRoleWithFailover("advisor", func(ev bootstrap.FailoverEvent) {
		slog.Warn("Chuyển provider cho advisor", "module", "storypartner",
			"reason", ev.Reason,
			"from", ev.FromProvider+"/"+ev.FromModel,
			"to", ev.ToProvider+"/"+ev.ToModel, "err", ev.Err)
	})
}

// CancelStoryPartner hủy advisor request đang chạy, nếu có. Không có request thì
// không làm gì. Không bao giờ động vào Engine.
func (h *Host) CancelStoryPartner() {
	h.spMu.Lock()
	defer h.spMu.Unlock()
	if h.spCancel != nil {
		h.spCancel()
		h.spCancel = nil
	}
}

func (h *Host) startStoryPartnerCall(ctx context.Context) (int, context.Context, context.CancelFunc) {
	h.spMu.Lock()
	defer h.spMu.Unlock()
	if h.spCancel != nil {
		h.spCancel()
	}
	spCtx, cancel := context.WithTimeout(ctx, storyPartnerTimeout)
	h.spCancel = cancel
	h.spGen++
	return h.spGen, spCtx, cancel
}

// finishStoryPartnerCall dọn cancel của chính lượt mình. So bằng generation vì
// func không so sánh được: lượt mới đã thay spCancel thì không được xóa của nó.
func (h *Host) finishStoryPartnerCall(gen int, cancel context.CancelFunc) {
	_ = cancel
	h.spMu.Lock()
	defer h.spMu.Unlock()
	if h.spGen == gen {
		h.spCancel = nil
	}
}
