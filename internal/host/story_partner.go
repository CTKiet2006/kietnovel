package host

import (
	"context"
	"errors"
	"fmt"
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
	gen, spCtx, cancel, err := h.startStoryPartnerCall(ctx)
	if err != nil {
		return sp.Result{}, err
	}
	defer h.finishStoryPartnerCall(gen, cancel)

	target := h.resolveAdvisor()
	model, prov, name := target.Model, target.Provider, target.Name

	svc := sp.NewService(sp.Deps{
		Store:     h.store,
		Model:     model,
		Provider:  prov,
		ModelName: name,
		// ResolveIdentity đọc attempt thực tế từ failoverModel. Service dùng khi
		// Usage thiếu identity — để Result, accounting và audit cùng một identity,
		// thay vì Host sửa Result sau khi audit đã ghi xong.
		ResolveIdentity: func() (string, string) {
			if lt, ok := model.(interface{ LastTarget() (string, string) }); ok {
				return lt.LastTarget()
			}
			return "", ""
		},
		RecordUsage: func(u agentcore.Usage) {
			// Sidecar, KHÔNG phải Record thường: tiền advisor không được cộng
			// vào overall, không gọi onCost, không abort Engine.
			// Task theo từng request (gen tăng đơn điệu): mỗi lượt hỏi là một
			// prompt lineage mới, detector không được so B với baseline của A.
			h.usage.RecordSidecar("advisor", fmt.Sprintf("sp:%d", gen), u, u.Provider, u.Model)
		},
		OnMissingUsage: func() {
			h.usage.flagMissingUsage("advisor")
		},
		AuditDir: filepath.Join(h.store.Dir(), "logs", "sp"),
	})
	return svc.Ask(spCtx, req)
}

// resolveAdvisor lấy advisor model + identity trong một snapshot nguyên tử.
// Production đi đường ResolveRoleTarget (một RLock duy nhất cho cả object lẫn
// metadata), nên /model advisor đổi đúng giữa chừng cũng không làm object và
// metadata lệch nhau.
//
// resolveAdvisorModel là seam để test inject fake model (không gọi mạng).
// Đường test đọc identity rời qua CurrentSelection — chấp nhận được vì test
// đơn luồng, không có mutation đồng thời.
func (h *Host) resolveAdvisor() bootstrap.RoleTarget {
	if h.resolveAdvisorModel != nil {
		m := h.resolveAdvisorModel()
		p, n, _ := h.models.CurrentSelection("advisor")
		return bootstrap.RoleTarget{Model: m, Provider: p, Name: n, Explicit: true}
	}
	return h.models.ResolveRoleTarget("advisor", func(ev bootstrap.FailoverEvent) {
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

func (h *Host) startStoryPartnerCall(ctx context.Context) (int, context.Context, context.CancelFunc, error) {
	h.spMu.Lock()
	defer h.spMu.Unlock()
	// Kiểm closing và Add phải nguyên tử dưới cùng spMu: nếu check ở mutex khác
	// với Add thì Close có thể lọt vào giữa — Wait đã qua mà Add mới tới, request
	// chạy sau khi usage đã persist và lease đã thả.
	if h.spClosed {
		return 0, nil, nil, errors.New("sp: host đang đóng, không nhận request mới")
	}
	if h.spCancel != nil {
		h.spCancel()
	}
	spCtx, cancel := context.WithTimeout(ctx, storyPartnerTimeout)
	h.spCancel = cancel
	h.spGen++
	h.spWG.Add(1)
	return h.spGen, spCtx, cancel, nil
}

// finishStoryPartnerCall dọn lượt mình: gọi cancel để giải phóng timer của
// WithTimeout, rồi xóa spCancel nếu vẫn là lượt mình. So bằng generation vì
// func không so sánh được: lượt mới đã thay spCancel thì không được xóa của nó.
// Gọi cancel ở đây an toàn vì nó chỉ ảnh hưởng ctx của chính lượt này
// (idempotent, ctx đã xong thì không tác dụng gì thêm).
func (h *Host) finishStoryPartnerCall(gen int, cancel context.CancelFunc) {
	cancel()
	h.spMu.Lock()
	defer h.spMu.Unlock()
	if h.spGen == gen {
		h.spCancel = nil
	}
	h.spWG.Done()
}
