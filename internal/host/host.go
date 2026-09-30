package host

import (
	"context"
	"encoding/json"
	"fmt"
	buildversion "github.com/CTKiet2006/kietnovel/internal/version"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/CTKiet2006/kietnovel/assets"
	"github.com/CTKiet2006/kietnovel/internal/agents"
	"github.com/CTKiet2006/kietnovel/internal/agents/ctxpack"
	"github.com/CTKiet2006/kietnovel/internal/arbiter"
	"github.com/CTKiet2006/kietnovel/internal/bootstrap"
	"github.com/CTKiet2006/kietnovel/internal/domain"
	"github.com/CTKiet2006/kietnovel/internal/flow"
	"github.com/CTKiet2006/kietnovel/internal/host/exp"
	"github.com/CTKiet2006/kietnovel/internal/host/imp"
	"github.com/CTKiet2006/kietnovel/internal/host/sim"
	runtimelog "github.com/CTKiet2006/kietnovel/internal/logger"
	modelreg "github.com/CTKiet2006/kietnovel/internal/models"
	"github.com/CTKiet2006/kietnovel/internal/notify"
	"github.com/CTKiet2006/kietnovel/internal/revision"
	"github.com/CTKiet2006/kietnovel/internal/rules"
	storepkg "github.com/CTKiet2006/kietnovel/internal/store"
	"github.com/CTKiet2006/kietnovel/internal/tools"
	"github.com/CTKiet2006/kietnovel/internal/userrules"
	"github.com/CTKiet2006/kietnovel/internal/utils"
	"github.com/voocel/agentcore"
	"github.com/voocel/agentcore/subagent"
)

// Host là vỏ runtime: vòng đời / cửa vào can thiệp / chiếu sự kiện / quản lý model.
// Lập lịch và thực thi nằm ở engine (vòng lặp tất định); định đoạn ngữ nghĩa nằm ở arbiter (LLM-as-function).
type Host struct {
	cfg             bootstrap.Config
	bundle          assets.Bundle
	store           *storepkg.Store
	bookLease       *bookLease
	styleStats      *tools.StyleStatsIndex
	models          *bootstrap.ModelSet
	engine          *engine
	thinkingApplier agents.ApplyThinking // /model đổi mức suy luận thì liên kết các Worker
	writerRestore   *ctxpack.WriterRestorePack
	userRules       *userrules.Service
	observer        *observer
	usage           *UsageTracker
	usageCancel     context.CancelFunc  // Dừng autoSaveLoop và kích hoạt lần flush cuối
	budget          *BudgetSentinel     // Chính sách ngân sách; nil khi không bật (method nil-safe)
	gate            *ChapterAdvanceGate // Thành phần chính sách thống nhất cho giấy phép chương và tạm dừng một lần
	notifier        *notify.Notifier    // Cảnh báo không người trông; nil khi không bật (Send nil-safe)
	configPath      string              // Đích ghi cấu hình: /config, /model ghi vào bản đang có hiệu lực (có bản project thì ghi nó, không thì ghi bản toàn cục)
	logCleanup      func()
	fileLogErr      error

	events   chan Event
	streamCh chan string
	done     chan struct{}

	mu         sync.Mutex
	lifecycle  lifecycle
	cocreating bool   // Cờ chiếm đồng sáng tác: trong cửa sổ paused chặn can thiệp chồng nhau từ import/simulate/continue
	exclusive  string // Cờ chiếm việc nền độc quyền (import/mô phỏng/sửa chương): khác rỗng nghĩa là có việc đang chạy, chặn các cửa độc quyền chạy chồng
	// exclusiveCancel là hàm huỷ của việc độc quyền hiện tại: dừng cứng theo ngân sách hay dừng thủ công
	// đều phải dừng được việc đang đốt tiền
	// đang chạy, chứ không chỉ Engine — abortWithEvent huỷ nó khi Engine không chạy (cảnh báo ngân sách và
	// Abort thủ công dùng chung một cơ chế dừng). releaseExclusive xóa luôn cờ này.
	exclusiveCancel context.CancelFunc
	closeOnce       sync.Once
	asyncWG         sync.WaitGroup
	closing         bool

	interMu sync.Mutex // Định đoạn can thiệp tuần tự FIFO (mỗi thời điểm tối đa một lần tư vấn đang chờ)

	outputMu     sync.RWMutex
	outputClosed bool

	// runCtx ràng buộc các lệnh gọi định đoạn LLM phía Host (định đoạn khởi động/chẩn đoán can thiệp); Close huỷ,
	// tránh thoát khi vẫn còn định đoạn đang dở mà không cắt được.
	runCtx    context.Context
	runCancel context.CancelFunc
}

type lifecycle string

const (
	lifecycleIdle      lifecycle = "idle"
	lifecycleRunning   lifecycle = "running"
	lifecyclePaused    lifecycle = "paused"
	lifecycleCompleted lifecycle = "completed"
)

// userRulesBuildTimeout chặn trên cho việc chuẩn hóa quy tắc ở lúc khởi động/khôi phục. Khi đưa cả bộ dàn ý vào,
// một lần gọi
// có thể mất vài phút, nên chừa dư; quá thời gian thì đi đường degraded sẵn có, rút về raw preferences, không chặn việc tạo truyện.
const userRulesBuildTimeout = 3 * time.Minute

// New tạo Host.
func New(cfg bootstrap.Config, bundle assets.Bundle, options ...NewOption) (*Host, error) {
	cfg.FillDefaults()
	if err := cfg.ValidateBase(); err != nil {
		return nil, err
	}
	var opts newOptions
	for _, option := range options {
		if option != nil {
			option(&opts)
		}
	}

	bookLease, err := acquireBookLease(cfg.OutputDir)
	if err != nil {
		return nil, err
	}
	keepBookLease := false
	var logCleanup func()
	defer func() {
		if keepBookLease {
			return
		}
		if err := bookLease.Close(); err != nil {
			slog.Error("Không giải phóng được khoá thư mục truyện", "module", "host", "dir", cfg.OutputDir, "err", err)
		}
		if logCleanup != nil {
			logCleanup()
		}
	}()

	var fileLogErr error
	if opts.logFile != "" {
		logCleanup, fileLogErr = runtimelog.SetupFile(cfg.OutputDir, opts.logFile, opts.logAlsoStderr, opts.logAttrs...)
		if fileLogErr != nil {
			logCleanup = nil
			slog.Warn("Log file không dùng được, tiếp tục dùng log của tiến trình hiện tại", "module", "host", "file", opts.logFile, "err", fileLogErr)
		}
	}

	slog.Info("Khởi động", "module", "boot", "provider", cfg.Provider, "model", cfg.ModelName, "output", cfg.OutputDir)

	store := storepkg.NewStore(cfg.OutputDir)
	if err := store.Init(); err != nil {
		return nil, fmt.Errorf("init store: %w", err)
	}
	if err := upgradeProject(store); err != nil {
		return nil, err
	}
	// RunMeta là nguồn dữ kiện của mọi ngữ nghĩa điều khiển, phải kiểm tra xong trước khi dựng model/tác vụ nền.
	// advance mode lạ thì trả lỗi có cấu trúc ngay; cấm đoán rồi hạ cấp mà vẫn ghi đĩa.
	if err := store.RunMeta.Init(cfg.Style, cfg.Provider, cfg.ModelName); err != nil {
		return nil, fmt.Errorf("init run meta: %w", err)
	}
	// Chạy goroutine nền lấy lại metadata model (cửa sổ/giá) từ OpenRouter, cache đĩa 24h.
	modelreg.StartPricingRefresh(modelreg.DefaultRegistry(), bootstrap.DefaultConfigDir())

	models, err := bootstrap.NewModelSet(cfg)
	if err != nil {
		return nil, fmt.Errorf("create models: %w", err)
	}
	slog.Info("Model đã sẵn sàng", "module", "boot", "summary", models.Summary())

	usage := NewUsageTracker(models, store)
	// Ưu tiên đọc meta/usage.json; các trường hợp sau đều hồi từ sessions/*.jsonl một lần:
	//   - file không tồn tại (trước lần lưu bền đầu tiên)
	//   - schema không khớp (nâng cấp sau thì bỏ định dạng cũ)
	//   - file tồn tại nhưng hỏng / lỗi IO (dữ liệu hỏng không được làm số liệu tích luỹ vĩnh viễn về 0)
	// Hồi xong thì SaveNow ngay để cố kết quả, lần khởi động sau Load là trúng ngay.
	loaded, loadErr := usage.LoadFromStore()
	if loadErr != nil {
		slog.Warn("Không tải được usage, sẽ thử hồi từ sessions", "module", "usage", "err", loadErr)
	}
	if !loaded {
		if n, err := usage.ReplaySessions(cfg.OutputDir); err != nil {
			slog.Warn("usage replay thất bại", "module", "usage", "err", err)
		} else if n > 0 {
			slog.Info("Đã hồi xong usage từ session", "module", "usage", "messages", n)
			if err := usage.SaveNow(); err != nil {
				slog.Warn("Không lưu được usage sau khi hồi", "module", "usage", "err", err)
			}
		}
	}
	usageCtx, usageCancel := context.WithCancel(context.Background())
	usage.StartAutoSave(usageCtx)

	// Khai báo trước onGuardBlock: chỉ gắn được closure phát sự kiện sau khi h đã dựng xong.
	var onGuardBlock func(agent, reason string, consecutive int32)
	styleStats := tools.NewStyleStatsIndex(store)
	workers, restore, applyThinking := agents.BuildWorkers(cfg, store, styleStats, models, bundle, usage.Record,
		func(agent, reason string, consecutive int32) {
			if onGuardBlock != nil {
				onGuardBlock(agent, reason, consecutive)
			}
		})
	store.Signals.ClearStaleSignals()

	h := &Host{
		cfg:             cfg,
		bundle:          bundle,
		store:           store,
		bookLease:       bookLease,
		styleStats:      styleStats,
		models:          models,
		thinkingApplier: applyThinking,
		writerRestore:   restore,
		userRules:       userrules.NewService(store, models.Default, rules.DefaultOptions()),
		usage:           usage,
		usageCancel:     usageCancel,
		configPath:      bootstrap.EffectiveConfigPath(),
		logCleanup:      logCleanup,
		fileLogErr:      fileLogErr,
		events:          make(chan Event, 100),
		streamCh:        make(chan string, 256),
		done:            make(chan struct{}, 4),
		lifecycle:       lifecycleIdle,
	}
	h.runCtx, h.runCancel = context.WithCancel(context.Background())
	h.observer = newObserver(store, h.emitEvent, h.emitDelta, h.emitClear)
	workers.SetEventObserver(func(meta subagent.RunMeta, ev agentcore.Event) {
		h.observer.handleWorkerEvent(meta.Agent, ev)
	})
	// Arbiter phía Host và Worker dùng chung một đường ToolProgress → observer → bảng làm việc.
	h.runCtx = agentcore.WithToolProgress(h.runCtx, h.observer.workerProgress)
	if cfg.Notify.IsEnabled() {
		h.notifier = notify.New(cfg.Notify.Command, cfg.Notify.Events)
	}
	// Cảnh báo ngân sách: Engine gọi thẳng HandleBoundary ở ranh giới mỗi vòng (không qua đăng ký sự kiện).
	if sentinel := NewBudgetSentinel(cfg.Budget,
		func() float64 { c, _, _, _, _ := usage.Totals(); return c },
		func(reason string) { h.abortWithEvent(reason, "error") },
		func(level, summary string) {
			h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Summary: summary, Level: level})
			h.notifier.Send(notify.Notification{Kind: notify.KindBudget, Level: level, Title: buildversion.AppName + ": Ngân sách", Body: summary})
		},
	); sentinel != nil {
		h.budget = sentinel
		usage.SetOnCost(sentinel.OnCost)
		// Cảnh báo vùng mù tính phí: khi model không báo usage thì chi phí luôn bằng 0, ngân sách không bao giờ kích hoạt —
		// cầu chạy không nối thì phải báo người.
		usage.SetOnMissingUsage(func() {
			const blind = "Vùng mù ngân sách: model không trả dữ liệu usage, chi phí tính ra 0 nên trần ngân sách không kích hoạt (với model tuỳ chỉnh, hãy xác nhận giá trong registry hoặc include_usage ở phía trên)"
			h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Summary: blind, Level: "warn"})
			h.notifier.Send(notify.Notification{Kind: notify.KindBudget, Level: "warn", Title: buildversion.AppName + ": Ngân sách", Body: blind})
		})
	}
	// Cổng tiến thế thống nhất: thực hiện hold một lần và chặn chương mới không có giấy phép ở chế độ review.
	h.gate = NewChapterAdvanceGate(store,
		func(reason string) {
			h.abortWithEvent(reason, "info")
			h.notifier.Send(notify.Notification{Kind: notify.KindAdvanceGate, Level: "info", Title: buildversion.AppName + ": Chờ duyệt", Body: reason})
		},
		func(level, summary string) {
			h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Summary: summary, Level: level})
			h.notifier.Send(notify.Notification{Kind: notify.KindAdvanceGate, Level: level, Title: buildversion.AppName + ": Đẩy chương", Body: summary})
		},
	)
	// Sự kiện chặn của StopGuard: blocked là hành động tự lành tần suất cao, chỉ vào luồng sự kiện trong
	// màn hình (đẩy thông báo sẽ tràn màn hình);
	// escalated / hard_stop nghĩa là nhiệm vụ con của vòng này hỏng, gửi cặp sự kiện + notify (kiến trúc §2.3).
	onGuardBlock = func(agent, reason string, n int32) {
		switch reason {
		case "escalated":
			body := fmt.Sprintf("%s liên tục %d lần quay không tạo ra sản phẩm bắt buộc, dừng nhiệm vụ vòng này và trả lại Engine xử lý", agent, n)
			h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Agent: agent, Summary: "StopGuard nâng cấp: " + body, Level: "warn"})
			h.notifier.Send(notify.Notification{Kind: notify.KindStopGuard, Level: "warn", Title: buildversion.AppName + ": StopGuard", Body: body})
		case "hard_stop":
			body := fmt.Sprintf("%s bị provider từ chối (safety/content_filter), dừng ngay nhiệm vụ vòng này", agent)
			h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Agent: agent, Summary: "StopGuard nâng cấp: " + body, Level: "warn"})
			h.notifier.Send(notify.Notification{Kind: notify.KindStopGuard, Level: "warn", Title: buildversion.AppName + ": StopGuard", Body: body})
		default: // blocked
			h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Agent: agent,
				Summary: fmt.Sprintf("StopGuard: %s định kết thúc khi chưa tạo đủ sản phẩm bắt buộc, đã chặn và nhắc lại (lần thứ %d liên tiếp)", agent, n), Level: "info"})
		}
	}
	// Engine: engine thực thi tất định (docs/engine-rfc.md). arbiter dùng model Default (giới hạn tạm thời,
	// xem engine-arbiter.md §4.2).
	h.engine = &engine{
		store:           store,
		workers:         workers,
		arbiterModel:    newUsageTrackedModel(models.Default, "arbiter", usage.Record),
		failurePrompt:   bundle.Prompts.ArbiterFailure,
		planStartPrompt: bundle.Prompts.ArbiterPlanStart,
		style:           cfg.Style,
		// Hỏi lại đồng bộ: chặn vòng lặp Engine để định đoạn một lần (vài giây), đổi lấy "can thiệp có hiệu lực trước khi viết tiếp".
		reconsult: h.handleIntervention,
		observer:  h.observer,
		budget:    h.budget,
		gate:      h.gate,
		refresh:   h.refreshWriterRestore,
		emitEvent: h.emitEvent,
		notify: func(kind, level, title, body string) {
			h.notifier.Send(notify.Notification{Kind: kind, Level: level, Title: title, Body: body})
		},
		onPause: func(summary string) { h.abortWithEvent(summary, "warn") },
		onDone:  h.runEnded,
	}

	keepBookLease = true
	return h, nil
}

// ── Vòng đời ──

// PrepareUserRules sinh snapshot quy tắc người dùng cho cuốn sách này ở chế độ tạo mới (tất định phía khởi động, không đi vào Run sáng tác chính).
//
// Đầu vào là yêu cầu sáng tác **gốc** của người dùng (chưa bọc qua BuildStartPrompt) — thứ cần chuẩn hóa là chính quy tắc
// của người dùng, không phải khung khởi động. Cửa vào phải được gọi một lần trước StartPrepared (cả hai đường tạo mới quick/cocreate đều đi qua đây).
//
// Chuẩn hóa hỏng chỉ hạ cấp, không báo lỗi (đường tăng cường); chỉ khi không ghi được snapshot mới trả error
// để dừng việc tạo truyện —
// nếu không thì các lần chạy sau sẽ không có nguồn dữ kiện ổn định (xem thiết kế §thất bại và hạ cấp).
func (h *Host) PrepareUserRules(rawPrompt string) error {
	if err := h.refuseNewBookOverExisting(); err != nil {
		return err
	}
	// Chuẩn hóa là lần gọi LLM đầu tiên lúc khởi động: bắt buộc phải huỷ được (runCtx) và có trần thời gian —
	// llmretry thử lại liên tục với lỗi retryable (429/5xx/timeout) cho tới khi ctx kết thúc,
	// dùng context.Background() trần thì khi provider liên tục giới hạn tần suất sẽ kẹt vĩnh viễn ở đây (issue #125).
	// Quá thời gian thì rơi vào đường degraded sẵn có: thà hạ cấp về raw preferences còn hơn chặn việc tạo truyện.
	ctx, cancel := context.WithTimeout(h.runCtx, userRulesBuildTimeout)
	defer cancel()
	svc := userrules.NewService(h.store, h.models.Default, rules.DefaultOptions())
	snap, err := runObservedStep(h.observer, "SYSTEM", "rules", "Chuẩn hóa quy tắc",
		func() (*rules.Snapshot, error) { return svc.Build(ctx, rawPrompt) })
	if err != nil {
		return fmt.Errorf("Không ghi được snapshot quy tắc người dùng, không thể tiếp tục: %w", err)
	}
	logUserRulesSnapshot(snap)
	// Chuẩn hóa rớt là giảm cấp âm thầm (normalizeOrDegrade) — không báo lỗi
	// nhưng quy tắc không được cấu trúc hóa, người dùng có quyền biết yêu cầu của
	// mình chỉ đang có hiệu lực ở dạng nguyên văn.
	if snap != nil && snap.Status == rules.StatusDegraded {
		h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Level: "warn",
			Summary: "Một phần quy tắc không chuẩn hóa được, đang áp dụng nguyên văn làm sở thích văn phong",
			Detail:  strings.Join(snap.Uncertain, "; ")})
	}
	return nil
}

// ensureUserRules bảo đảm snapshot tồn tại trên đường khôi phục; khi thiếu thì sinh từ
// system_defaults + file quy tắc.
func (h *Host) ensureUserRules() {
	// Cùng nguồn với PrepareUserRules: khi snapshot thiếu, GetOrBuild sẽ gọi Build
	// và phát sinh LLM call, nên cũng bắt buộc phải hủy được + có timeout, nếu không
	// đường khôi phục sẽ tái diễn issue #125 (treo im lặng).
	ctx, cancel := context.WithTimeout(h.runCtx, userRulesBuildTimeout)
	defer cancel()
	svc := userrules.NewService(h.store, h.models.Default, rules.DefaultOptions())
	snap, err := svc.GetOrBuild(ctx)
	if err != nil {
		slog.Warn("Đọc/sinh snapshot quy tắc người dùng thất bại, runtime sẽ rơi về mặc định tích hợp", "module", "rules", "err", err)
		return
	}
	logUserRulesSnapshot(snap)
}

// logUserRulesSnapshot in ra lúc khởi động: cho người dùng thấy hệ thống đã hiểu
// quy tắc thành gì (tái dùng log, không thêm cơ chế mới).
func logUserRulesSnapshot(snap *rules.Snapshot) {
	if snap == nil {
		return
	}
	slog.Info("Snapshot quy tắc người dùng",
		"module", "rules",
		"status", string(snap.Status),
		"nguồn", snap.Sources,
		"cụm cấm", len(snap.Structured.ForbiddenPhrases),
		"từ gây mệt", len(snap.Structured.FatigueWords),
	)
	if snap.Status == rules.StatusDegraded {
		slog.Warn("Một phần quy tắc không phân tích được, đang chạy theo raw preferences (có thể sinh lại snapshot)",
			"module", "rules", "uncertain", snap.Uncertain)
	}
}

// StartPrepared bắt đầu viết từ yêu cầu sáng tác **gốc** của người dùng: định đoạn plan_start chọn kiến trúc sư và mở rộng
// yêu cầu, kết quả định đoạn được cố thành dữ kiện trước
// (PlanStartRecord) rồi mới khởi động Engine — mọi lần khôi phục luôn dựa trên dữ kiện đã ghi đĩa, không làm lại định đoạn đã có.
// Dữ kiện đầu vào (StartPrompt) được ghi đĩa trước khi định đoạn: nếu định đoạn hỏng thì đây là căn cứ để Engine bù định đoạn,
// nên khởi động hỏng vẫn tự lành từ mọi cửa khôi phục (Resume/tiếp tục), không phải ngõ cụt.
func (h *Host) StartPrepared(rawRequirement string) error {
	h.mu.Lock()
	if h.lifecycle == lifecycleRunning {
		h.mu.Unlock()
		return fmt.Errorf("already running")
	}
	if h.cocreating {
		h.mu.Unlock()
		return fmt.Errorf("Đang trong đồng sáng tác, hãy kết thúc đồng sáng tác trước")
	}
	h.mu.Unlock()

	rawRequirement = strings.TrimSpace(rawRequirement)
	if rawRequirement == "" {
		return fmt.Errorf("prompt is required")
	}
	if err := h.refuseNewBookOverExisting(); err != nil {
		return err
	}
	if err := h.budget.Refuse(); err != nil {
		return err
	}
	if err := h.store.Checkpoints.Reset(); err != nil {
		return fmt.Errorf("reset checkpoints: %w", err)
	}
	if err := h.store.Progress.Init(0); err != nil {
		return fmt.Errorf("init progress: %w", err)
	}
	// Ghi dữ kiện đầu vào trước khi định đoạn: khi định đoạn hỏng (lỗi model…) thì
	// StartPrompt vẫn còn, Engine sẽ định đoạn bù theo đó khi khôi phục/tiếp tục
	// (planStartFallback), nên khởi động hỏng không còn là ngõ cụt.
	if err := h.store.RunMeta.SetStartPrompt(rawRequirement); err != nil {
		return fmt.Errorf("Ghi yêu cầu truyện: %w", err)
	}

	// Định đoạn lúc khởi động: hỏng thì báo lỗi tường minh và dừng (lúc này người
	// dùng còn ở đó, báo lỗi tốt hơn đoán bừa).
	start := time.Now()
	decision, derr := runObservedDecision(h.observer, "Định đoạn khởi động", func() (arbiter.PlanStartDecision, error) {
		return arbiter.DecidePlanStart(h.runCtx, h.arbiterModel(),
			h.bundle.Prompts.ArbiterPlanStart, rawRequirement, h.cfg.Style)
	})
	rec := storepkg.DecisionRecord{Kind: "plan_start", Decider: "arbiter", Input: rawRequirement,
		Reason: decision.Reason, DurationMs: time.Since(start).Milliseconds()}
	if derr == nil {
		if data, err := json.Marshal(decision); err == nil {
			rec.Decision = data
		}
	} else {
		rec.Error = derr.Error()
	}
	var recErr error
	if rec, recErr = h.store.Decisions.Append(rec); recErr != nil {
		slog.Warn("Không ghi được bản ghi kiểm toán định đoạn khởi động", "module", "host", "err", recErr)
	}
	if derr != nil {
		return fmt.Errorf("Định đoạn khởi động thất bại: %w", derr)
	}
	if err := h.store.RunMeta.SetPlanStart(domain.PlanStartRecord{
		RawPrompt: rawRequirement, Planner: decision.Planner, PlannerTask: decision.Task, DecisionID: rec.ID,
	}); err != nil {
		return fmt.Errorf("Ghi định đoạn khởi động: %w", err)
	}

	h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM",
		Summary: fmt.Sprintf("Bắt đầu viết (kiến trúc sư: %s — %s)", decision.Planner, decision.Reason), Level: "info"})
	if !h.startEngine(&flow.Instruction{Agent: decision.Planner, Task: decision.Task, Reason: decision.Reason}) {
		return fmt.Errorf("Engine đang chạy hoặc đang dừng, không thể tạo truyện mới")
	}
	return nil
}

// refuseNewBookOverExisting từ chối mở truyện mới trong thư mục đã có chương:
// StartPrepared sẽ xóa checkpoints và progress, lỡ tay là xóa im lặng cả chuỗi
// tiến độ của cuốn sách (sau khi import xong dừng ở màn hình chào rồi bấm nhầm
// Enter là ví dụ điển hình). Chỉ tính số chương đã hoàn thành — dư lại ở giai đoạn
// quy hoạch/khởi động hỏng chưa có chương nào, vẫn cho qua để giữ đường tự lành:
// thử lại trong cùng phiên bằng Ctrl+S ở đồng sáng tác, và bù định đoạn khi khôi phục.
func (h *Host) refuseNewBookOverExisting() error {
	progress, err := h.store.Progress.Load()
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if progress == nil || len(progress.CompletedChapters) == 0 {
		return nil
	}
	book, err := h.store.Book.Load()
	if err != nil {
		return err
	}
	if book == nil {
		return fmt.Errorf("Thư mục đầu ra đã có chương, nhưng không tìm thấy thông tin tác phẩm")
	}
	name := book.Title
	return fmt.Errorf("Thư mục đầu ra đã có %d chương của «%s», tạo mới sẽ xóa tiến độ và checkpoint: muốn viết tiếp thì dùng đường khôi phục (khởi động lại app là tự khôi phục), muốn truyện mới thì đổi thư mục đầu ra",
		len(progress.CompletedChapters), name)
}

// startEngine là điểm vào thống nhất để khởi động Engine (dùng chung cho
// Start/Resume/Continue/khởi động lại sau can thiệp).
// lifecycle phải được đặt running trước khi goroutine chạy: Engine có thể kết
// thúc ngay (xong sách/không còn tuyến), runEnded sẽ đưa lifecycle về trạng thái
// cuối; nếu đảo thứ tự, runEnded chạy trước, còn đây lại ghi running sau, TUI sẽ
// hiện "đang chạy" mà Engine thực tế đã dừng.
func (h *Host) startEngine(initial *flow.Instruction) bool {
	// Chốt chặn qua restart: khi còn workspace nhập dở, Engine thường không được
	// tiêu thụ trạng thái nửa vời (RFC §12.5).
	active, done, importErr := imp.ResumeStatus(h.store)
	if importErr != nil {
		h.emitEvent(Event{Time: time.Now(), Category: "ERROR", Level: "error",
			Summary: "Đọc trạng thái nhập thất bại, đã chặn việc viết thường ghi đè lên hiện trạng: " + importErr.Error()})
		return false
	}
	if active && !done {
		h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Level: "warn",
			Summary: "Còn tiến trình nhập truyện ngoài chưa xong, hãy chạy /import để hoàn tất rồi mới viết tiếp"})
		return false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closing {
		return false
	}
	// Khi có việc nền độc quyền (import/mô phỏng) đang chạy, Engine không được
	// chạy trước để tránh ghi đè chung. Đây là chốt chặn cuối cho mọi đường khởi
	// động Engine (Resume/Continue khởi động lại/tự chuyển tiếp/next) — canh cửa ở
	// đầu vào là lớp thứ nhất, đây là lớp cuối.
	if h.exclusive != "" {
		return false
	}
	// lifecycle có thể đã là paused trong khi goroutine Engine cũ vẫn đang chạy
	// phần defer thoát. Bắt buộc kiểm tra cả trạng thái thật của Engine; nếu không
	// sẽ gán lifecycle về running trong khi start thực tế no-op, rồi runEnded cũ
	// lại ghi nó xuống idle.
	if h.engine.isRunning() {
		return false
	}
	previous := h.lifecycle
	h.lifecycle = lifecycleRunning
	if !h.engine.start(initial) {
		h.lifecycle = previous
		return false
	}
	return true
}

// Reopen mở lại cuốn đã hoàn thành thành trạng thái đang viết. Xong sách và mở
// lại đều là quyết định nặng: xong sách có thể do kiến trúc sư định đoạn, còn mở lại
// chỉ người dùng chủ động yêu cầu (/reopen), không qua định đoạn của model.
// direction khác rỗng được ghi là can thiệp chờ xử lý; khi khôi phục, Arbiter sẽ
// định đoạn rồi chèn (cùng kênh với can thiệp lúc dừng), sau đó Engine chạy tiếp
// (định tuyến cuối tập sẽ phát ra tập tiếp theo).
func (h *Host) Reopen(direction string) error {
	h.mu.Lock()
	switch {
	case h.lifecycle == lifecycleRunning:
		h.mu.Unlock()
		return fmt.Errorf("Engine đang viết, không cần mở lại")
	case h.cocreating:
		h.mu.Unlock()
		return fmt.Errorf("Đang trong đồng sáng tác, hãy kết thúc đồng sáng tác trước")
	case h.exclusive != "":
		ex := h.exclusive
		h.mu.Unlock()
		return fmt.Errorf("Đang %s, xong rồi hãy mở lại", ex)
	}
	h.mu.Unlock()
	if err := h.requireCleanChapters(); err != nil {
		return err
	}

	if err := h.store.Progress.ReopenContinue(); err != nil {
		return err
	}
	reopenEvent := Event{Time: time.Now(), Category: "SYSTEM", Summary: "Đã mở lại truyện thành trạng thái đang viết (người dùng hủy định đoạn xong sách)", Level: "info"}
	if d := strings.TrimSpace(direction); d != "" {
		reopenEvent.Detail = reopenEvent.Summary + "\nHướng viết tiếp: " + d
	}
	h.emitEvent(reopenEvent)
	if d := strings.TrimSpace(direction); d != "" {
		if err := h.store.RunMeta.SetPendingSteer(d); err != nil {
			return fmt.Errorf("Đã mở lại, nhưng không ghi được hướng viết tiếp: %v — hãy nhập lại hướng ở ô nhập", err)
		}
	}
	return nil
}

// Resume chế độ khôi phục: sinh resume prompt từ checkpoint + progress rồi khởi động.
// Resume khôi phục phiên viết từ store. Trả về Msg (chưa dịch) để TUI hiển thị
// đúng ngôn ngữ; dùng resumeLabel.String() nếu cần chuỗi thô cho log.
func (h *Host) Resume() (Msg, error) {
	h.mu.Lock()
	if h.lifecycle == lifecycleRunning {
		h.mu.Unlock()
		return Msg{}, fmt.Errorf("already running")
	}
	if h.cocreating {
		h.mu.Unlock()
		return Msg{}, fmt.Errorf("Đang trong đồng sáng tác, hãy kết thúc đồng sáng tác trước")
	}
	if h.exclusive != "" {
		ex := h.exclusive
		h.mu.Unlock()
		return Msg{}, fmt.Errorf("Đang %s, xong rồi hãy khôi phục để viết tiếp", ex)
	}
	h.mu.Unlock()
	label, err := resumeLabel(h.store)
	if err != nil {
		return Msg{}, err
	}
	if label.Empty() {
		return Msg{}, nil // Chế độ tạo mới, không có gì để khôi phục
	}
	if err := h.requireCleanChapters(); err != nil {
		return label, err
	}
	if err := h.budget.Refuse(); err != nil {
		return Msg{}, err
	}

	h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM",
		SummaryMsg: &Msg{Key: "Khôi phục việc viết: %s", Args: []any{label.String()}},
		Summary:    "Khôi phục việc viết: " + label.String(), Level: "info"})
	for _, w := range h.store.CheckConsistency() {
		h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Summary: "Cảnh báo nhất quán: " + w, Level: "warn"})
	}
	// Bảo đảm snapshot quy tắc người dùng tồn tại; đã có thì đọc rất rẻ.
	h.ensureUserRules()
	h.refreshWriterRestore()
	// Can thiệp chờ xử lý (để lại lúc dừng / sót lại vì sập lúc định đoạn) bắt buộc
	// phải được định đoạn trước khi Engine chạy tiếp — nếu không, Engine có thể viết
	// tiếp ra chương trái với can thiệp. Chạy đồng bộ (chặn vài giây là chấp nhận
	// được, TUI đã hiện "Khôi phục việc viết"); doIntervention thành công sẽ tự xóa
	// PendingSteer và kéo Engine lên với restart=true. Không có can thiệp chờ →
	// chạy tiếp thẳng.
	meta, err := h.store.RunMeta.Load()
	if err != nil {
		return label, fmt.Errorf("Đọc can thiệp chờ xử lý: %w", err)
	}
	if meta != nil && meta.PendingSteer != "" {
		if err := h.doIntervention(meta.PendingSteer, true); err != nil {
			return label, err
		}
	} else {
		// Chỉ khôi phục dữ kiện, không khôi phục phiên (RFC §6): Engine tính lại
		// tuyến từ store rồi chạy tiếp.
		if !h.startEngine(nil) {
			return label, fmt.Errorf("Engine đang hoàn tất lần dừng trước, thử khôi phục lại sau ít phút")
		}
	}
	// lifecycle do startEngine / runEnded quản lý, ở đây không ghi đè —
	// Engine kết thúc ngay (xong sách…) mà ghi đè sẽ biến trạng thái cuối về running.
	return label, nil
}

// handleIntervention nối callback hỏi lại không trả về của Engine; lỗi đã do
// doIntervention phát sự kiện.
func (h *Host) handleIntervention(text string) {
	_ = h.doIntervention(text, false)
}

// doIntervention là đường định đoạn thống nhất cho can thiệp của người dùng:
// Collect → Decide → thực thi.
// FIFO tuần tự (mỗi thời điểm tối đa một lần tư vấn đang chờ); answer/rules thì
// thực thi ngay, còn hành động điều khiển (hold/reopen/dispatch) thì xếp hàng ở
// ranh giới vòng lặp khi Engine đang chạy, và chạy ngay khi đã dừng.
// restart=true (ngữ nghĩa Continue) bảo đảm Engine chạy sau khi xử lý can thiệp.
func (h *Host) doIntervention(text string, restart bool) error {
	h.interMu.Lock()
	defer h.interMu.Unlock()

	// Chống sập: ghi bền (PendingSteer) trước khi định đoạn, xóa theo kiểu nguyên tử
	// sau khi áp dụng thành công hoặc khi đã báo lỗi ngay trước mặt người dùng
	// (ClearHandledSteer đồng thời reset FlowSteering). Sập lúc đang định đoạn →
	// lần Resume sau phát lại.
	if err := h.store.RunMeta.SetPendingSteer(text); err != nil {
		wrapped := fmt.Errorf("Không lưu được can thiệp, đã dừng định đoạn: %w", err)
		h.emitEvent(Event{Time: time.Now(), Category: "ERROR", Agent: "arbiter",
			Summary: wrapped.Error(), Detail: wrapped.Error(), Level: "error"})
		return wrapped
	}
	clearPending := func() error {
		if err := h.store.ClearHandledSteer(); err != nil {
			return fmt.Errorf("Xóa can thiệp đã xử lý thất bại: %w", err)
		}
		return nil
	}

	facts, err := arbiter.CollectInterventionFacts(h.store)
	if err != nil {
		wrapped := fmt.Errorf("Thu thập dữ kiện can thiệp thất bại, không gọi Arbiter: %w", err)
		h.emitEvent(Event{Time: time.Now(), Category: "ERROR", Agent: "arbiter",
			Summary: wrapped.Error(), Detail: wrapped.Error(), Level: "error"})
		return wrapped
	}
	facts.Running = h.engine.isRunning()

	start := time.Now()
	decision, derr := runObservedDecision(h.observer, "Định đoạn can thiệp người dùng", func() (arbiter.InterventionDecision, error) {
		return arbiter.DecideIntervention(h.runCtx, h.arbiterModel(),
			h.bundle.Prompts.ArbiterIntervention, facts, text)
	})

	rec := storepkg.DecisionRecord{Kind: "intervention", Decider: "arbiter", Input: text,
		Reason: decision.Reason, DurationMs: time.Since(start).Milliseconds()}
	if cp := h.store.Checkpoints.LatestGlobal(); cp != nil {
		rec.CheckpointSeq = cp.Seq
	}
	if data, err := json.Marshal(facts); err == nil {
		rec.Facts = data
	}
	if derr == nil {
		if data, err := json.Marshal(decision); err == nil {
			rec.Decision = data
		}
	} else {
		rec.Error = derr.Error()
	}
	if _, err := h.store.Decisions.Append(rec); err != nil {
		wrapped := fmt.Errorf("Không ghi được bản ghi kiểm toán định đoạn can thiệp, từ chối thực thi hành động: %w", err)
		h.emitEvent(Event{Time: time.Now(), Category: "ERROR", Agent: "arbiter",
			Summary: wrapped.Error(), Detail: wrapped.Error(), Level: "error"})
		return wrapped
	}

	if derr != nil {
		// Thà để yên còn hơn động nhầm: không ghi bất cứ thứ gì. Lỗi gọi model và
		// lỗi kiểm tra output dùng chung một kênh error, phải hiển thị nguyên văn,
		// không được bịa chung thành "không hiểu yêu cầu".
		// Đã báo ngay trước mặt người dùng → xóa pending (nếu không, lần Resume sau
		// sẽ tự phát lại đúng can thiệp hỏng đó).
		h.emitEvent(newInterventionFailureEvent(derr))
		if err := clearPending(); err != nil {
			return fmt.Errorf("%v; %w", derr, err)
		}
		return derr
	}

	h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Summary: "Định đoạn: " + decision.Reason, Level: "info"})
	if decision.Answer != "" {
		h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Summary: decision.Answer, Level: "info"})
	}
	// Hành động nào lưu bền thất bại → giữ PendingSteer (khi khôi phục sẽ phát lại
	// toàn bộ để định đoạn mới; hold/reopen là idempotent, dispatch hỏi lại theo dữ
	// kiện mới nên phát lại vẫn an toàn).
	var actionErr error
	if decision.Rules != "" {
		if snap, _, err := h.userRules.AddRuntimeRule(h.runCtx, decision.Rules); err != nil {
			h.emitEvent(Event{Time: time.Now(), Category: "ERROR", Summary: "Không lưu được quy tắc viết: " + err.Error(), Level: "error"})
			actionErr = err
		} else if snap != nil {
			h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Summary: "Đã cập nhật và lưu quy tắc viết", Level: "info"})
		}
	}

	if decision.Hold != nil || decision.Reopen != nil || decision.Dispatch != nil {
		op := controlOp{hold: decision.Hold, reopen: decision.Reopen, dispatch: decision.Dispatch, text: text, facts: facts}
		if !h.engine.enqueue(op) {
			// Engine không chạy: thực thi ngay; lưu bền thất bại → giữ PendingSteer,
			// khi khôi phục sẽ phát lại toàn bộ can thiệp.
			if err := h.engine.applyControlOp(context.Background(), op); err != nil {
				h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Level: "warn",
					Summary: "Thực thi hành động can thiệp thất bại, đã giữ lại; sẽ tự thử lại khi khôi phục/tiếp tục"})
				return err
			}
			// reopen/dispatch thể hiện ý định viết tiếp, kéo Engine lên.
			if decision.Reopen != nil || decision.Dispatch != nil {
				restart = true
			}
		}
	}
	if actionErr != nil {
		// Giữ PendingSteer: khi khôi phục/tiếp tục sẽ phát lại toàn bộ để định đoạn lại.
		h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Level: "warn",
			Summary: "Một phần hành động can thiệp không thành công, can thiệp đã được giữ; sẽ tự thử lại khi khôi phục/tiếp tục"})
		return actionErr
	}
	// Hành động đã áp dụng/xếp hàng thành công → xóa chốt chặn chống sập (sau khi
	// xếp hàng mà Engine bên trong lỗi hoặc race khi thoát thì engine tự ghi lại
	// PendingSteer để phòng).
	if err := clearPending(); err != nil {
		h.emitEvent(Event{Time: time.Now(), Category: "ERROR", Level: "error", Summary: err.Error()})
		return err
	}

	if restart && !h.engine.isRunning() {
		if err := h.budget.Refuse(); err != nil {
			h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Summary: err.Error(), Level: "warn"})
			return err
		}
		h.refreshWriterRestore()
		if !h.startEngine(nil) {
			// Lúc này hành động can thiệp đã có hiệu lực và PendingSteer đã xóa, chỉ là
			// Engine không kéo lên được ngay — không được nói dối là "đã lưu".
			h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Level: "warn",
				Summary: "Can thiệp đã có hiệu lực, nhưng Engine không chạy tiếp được ngay; hãy nhập tiếp ở ô nhập hoặc khởi động lại app để khôi phục"})
			return fmt.Errorf("Can thiệp đã có hiệu lực, nhưng Engine không chạy tiếp được ngay")
		}
	}
	return nil
}

func newInterventionFailureEvent(err error) Event {
	detail := err.Error()
	return Event{
		Time:     time.Now(),
		Category: "ERROR",
		Agent:    "arbiter",
		Summary:  "Định đoạn can thiệp thất bại: " + detail + " (không sửa gì cả)",
		Detail:   detail,
		Kind:     errorKind(err, detail),
		Level:    "error",
	}
}

// arbiterModel trả về model định đoạn có ghi vết mức dùng (token/chi phí chảy vào
// ngân sách và hệ thống usage).
func (h *Host) arbiterModel() agentcore.ChatModel {
	return newUsageTrackedModel(h.models.Default, "arbiter", h.usage.Record)
}

// Continue is called when the user types in the input box after pausing: it locks
// in the intervention and makes sure the Engine runs again.
func (h *Host) Continue(text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return fmt.Errorf("text is required")
	}
	h.mu.Lock()
	if h.cocreating {
		h.mu.Unlock()
		return fmt.Errorf("Đang trong đồng sáng tác, hãy kết thúc đồng sáng tác trước")
	}
	if h.exclusive != "" {
		ex := h.exclusive
		h.mu.Unlock()
		// Phải chặn trước khi định đoạn khi đang có việc nền độc quyền: nếu không,
		// Arbiter đã kịp đổi PendingSteer/quy tắc/trạng thái điều khiển, rồi mới
		// bị chốt chặn của Engine chặn lại.
		return fmt.Errorf("Đang %s, xong rồi hãy viết tiếp", ex)
	}
	h.mu.Unlock()
	if err := h.requireCleanChapters(); err != nil {
		return err
	}
	if err := h.budget.Refuse(); err != nil {
		return err
	}

	err, launched := h.runAsync(func() error {
		h.emitEvent(Event{Time: time.Now(), Category: "USER", Summary: "[Tiếp tục] " + text, Level: "info"})
		return h.doIntervention(text, true)
	})
	if !launched {
		return fmt.Errorf("Host đang đóng, không thể viết tiếp")
	}
	return err
}

// SetAdvanceMode chuyển chế độ đẩy chương theo cách tất định. Nó chỉ ghi ý định
// chạy của người dùng, không gọi Arbiter và không tự khởi động Engine đang tạm dừng.
func (h *Host) SetAdvanceMode(mode domain.ChapterAdvanceMode) error {
	h.interMu.Lock()
	defer h.interMu.Unlock()
	if err := h.store.RunMeta.SetAdvanceMode(mode); err != nil {
		return err
	}
	label := "tự động đẩy"
	if mode == domain.ChapterAdvanceReview {
		label = "duyệt từng chương"
	}
	summary := "Đã đổi chế độ đẩy chương sang " + label
	h.mu.Lock()
	state := h.lifecycle
	h.mu.Unlock()
	if mode == domain.ChapterAdvanceAuto && state != lifecycleRunning && state != lifecycleCompleted {
		summary += "; hiện vẫn đang tạm dừng, nhập lệnh tiếp tục để chạy lại"
	}
	h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Summary: summary, Level: "info"})
	return nil
}

// AdvanceOneChapter cấp quyền cho đúng một chương ở chế độ duyệt từng chương rồi
// khởi động Engine.
func (h *Host) AdvanceOneChapter() error {
	h.interMu.Lock()
	defer h.interMu.Unlock()

	h.mu.Lock()
	running, cocreating, ex := h.lifecycle == lifecycleRunning, h.cocreating, h.exclusive
	h.mu.Unlock()
	if running || h.engine.isRunning() {
		return fmt.Errorf("Việc viết vẫn đang chạy hoặc đang hoàn tất lần dừng, thử /next lại sau")
	}
	if cocreating {
		return fmt.Errorf("Đang trong đồng sáng tác, hãy kết thúc đồng sáng tác trước")
	}
	if ex != "" {
		return fmt.Errorf("Đang %s, xong rồi hãy chạy /next", ex)
	}
	if err := h.requireCleanChapters(); err != nil {
		return err
	}
	meta, err := h.store.RunMeta.Load()
	if err != nil {
		return err
	}
	if meta == nil {
		return fmt.Errorf("RunMeta chưa khởi tạo")
	}
	if meta.AdvanceMode != domain.ChapterAdvanceReview {
		return fmt.Errorf("/next chỉ dùng ở chế độ duyệt từng chương, hãy chạy /review on trước")
	}
	if meta.AdvanceHold != nil {
		return fmt.Errorf("Còn ý định tạm dừng một lần chưa xử lý (%s), hãy khôi phục hoặc hoàn tất can thiệp hiện tại", meta.AdvanceHold.Reason)
	}
	if err := h.budget.Refuse(); err != nil {
		return err
	}
	progress, err := h.store.Progress.Load()
	if err != nil {
		return err
	}
	if progress == nil || progress.Phase != domain.PhaseWriting {
		phase := "<nil>"
		if progress != nil {
			phase = string(progress.Phase)
		}
		return fmt.Errorf("Giai đoạn hiện tại không cho cấp quyền chương mới (phase=%s)", phase)
	}
	target := progress.NextChapter()
	if target <= 0 {
		return fmt.Errorf("Không suy ra được chương tiếp theo từ tiến độ hiện tại")
	}
	if err := h.store.RunMeta.GrantAdvancePermit(target); err != nil {
		return err
	}
	h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM",
		Summary: fmt.Sprintf("Đã cho viết chương %d; sau khi chương đó commit, hệ thống sẽ hoàn tất phần đánh giá và bảo trì cấu trúc cung/tập cần thiết rồi chờ duyệt tiếp", target), Level: "info"})
	h.refreshWriterRestore()
	if !h.startEngine(nil) {
		// Giấy phép được lưu theo số chương và idempotent với cùng mục tiêu, nên gọi
		// lại sau sẽ không cấp quyền trùng.
		return fmt.Errorf("Đã lưu giấy phép chương, nhưng Engine vẫn đang hoàn tất lần dừng trước; thử /next lại sau")
	}
	return nil
}

// Steer submits a user intervention (allowed at any point while running; once
// paused, whether the Engine is pulled back up afterwards depends on the action).
// The TUI waits on the result through a tea.Cmd, so it sees the real
// lock-in/persist error without blocking the interface.
func (h *Host) Steer(text string) error {
	err, launched := h.runAsync(func() error {
		h.emitEvent(Event{Time: time.Now(), Category: "USER", Summary: "[Can thiệp người dùng] " + text, Level: "info"})
		return h.doIntervention(text, false)
	})
	if !launched {
		return fmt.Errorf("Host đang đóng, không thể gửi can thiệp")
	}
	return err
}

// Abort tạm dừng vòng lặp Engine hiện tại.
func (h *Host) Abort() bool {
	return h.abortWithEvent("Người dùng đã tạm dừng việc viết", "warn")
}

// abortWithEvent thực hiện việc tạm dừng kèm sự kiện theo lý do đã nêu. Dừng vì
// ngân sách và dừng thủ công dùng chung một cơ chế, chỉ khác ở văn bản sự kiện
// (dừng vì ngân sách = lệnh Abort người dùng đã ký trước, ngữ nghĩa tương đương
// dừng thủ công).
func (h *Host) abortWithEvent(summary, level string) bool {
	h.mu.Lock()
	running := h.lifecycle == lifecycleRunning
	if running {
		h.lifecycle = lifecyclePaused
	}
	cancelExclusive := h.exclusiveCancel
	h.mu.Unlock()
	if running {
		h.engine.abort()
		h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Summary: summary, Level: level})
		return true
	}
	// Engine không chạy nhưng có việc nền độc quyền (import…) đang chạy: nó cũng
	// đang đốt tiền, nên dừng cứng theo ngân sách hay dừng thủ công đều phải dừng
	// được nó — nếu không thì chính sách ngân sách vô hiệu với import
	// (docs/import-pipeline.md §13.1).
	if cancelExclusive != nil {
		cancelExclusive()
		h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Summary: summary, Level: level})
		return true
	}
	return false
}

// Close dừng Engine và đóng kênh sự kiện.
//
// Ngữ nghĩa lưu bền của Usage: trước hết hủy autoSaveLoop (nó tự flush lần
// trạng thái dirty cuối), rồi gọi thêm một SaveNow đồng bộ để chốt. Sau khi dừng,
// vài trăm token cuối của lệnh gọi LLM đang dở có thể mất, nhưng sẽ được bù tự
// động bằng replay session jsonl ở lần khởi động sau.
func (h *Host) Close() {
	h.closeOnce.Do(func() {
		h.mu.Lock()
		h.closing = true
		cancelExclusive := h.exclusiveCancel
		h.mu.Unlock()

		if h.runCancel != nil {
			h.runCancel() // Cắt các lệnh gọi định đoạn phía Host đang dở và chuyển tiếp của supervisor
		}
		if cancelExclusive != nil {
			cancelExclusive()
		}
		h.engine.abort()
		h.engine.wait()
		h.asyncWG.Wait()

		if h.usageCancel != nil {
			h.usageCancel()
			h.usageCancel = nil
		}
		h.usage.WaitAutoSave()
		if err := h.usage.SaveNow(); err != nil {
			slog.Warn("Không ghi được usage trước khi thoát", "module", "usage", "err", err)
		}
		h.closeOutputChannels()
		if err := h.bookLease.Close(); err != nil {
			slog.Error("Không giải phóng được khoá thư mục truyện", "module", "host", "dir", h.cfg.OutputDir, "err", err)
		}
		if h.logCleanup != nil {
			h.logCleanup()
			h.logCleanup = nil
		}
	})
}

// FileLogError trả về lỗi khởi tạo log file lúc dựng; không đổi trong suốt vòng
// đời của Host.
func (h *Host) FileLogError() error {
	return h.fileLogErr
}

// runEnded được engine.onDone gọi khi vòng lặp Engine kết thúc (bất kể lý do):
// xác định trạng thái cuối theo dữ kiện trong store.
//   - Phase=Complete  → đánh dấu completed, phát sự kiện "hoàn thành truyện"
//   - còn lại         → đánh dấu idle/paused, phát sự kiện "dừng việc viết"
func (h *Host) runEnded() {
	h.observer.finalize()

	h.mu.Lock()
	progress, err := h.store.Progress.Load()
	if err != nil {
		if h.lifecycle == lifecycleRunning {
			h.lifecycle = lifecycleIdle
		}
		h.mu.Unlock()
		h.emitEvent(Event{Time: time.Now(), Category: "ERROR", Level: "error",
			Summary: "Đọc tiến độ lúc Engine kết thúc thất bại: " + err.Error()})
		select {
		case h.done <- struct{}{}:
		default:
		}
		return
	}
	book, err := h.store.Book.Load()
	if err != nil {
		h.lifecycle = lifecycleIdle
		h.mu.Unlock()
		h.emitEvent(Event{Time: time.Now(), Category: "ERROR", Level: "error",
			Summary: "Đọc thông tin tác phẩm lúc Engine kết thúc thất bại: " + err.Error()})
		select {
		case h.done <- struct{}{}:
		default:
		}
		return
	}
	if progress != nil && progress.Phase == domain.PhaseComplete {
		if book == nil {
			h.lifecycle = lifecycleIdle
			h.mu.Unlock()
			h.emitEvent(Event{Time: time.Now(), Category: "ERROR", Level: "error",
				Summary: "Lúc Engine kết thúc không có thông tin tác phẩm"})
			select {
			case h.done <- struct{}{}:
			default:
			}
			return
		}
		h.lifecycle = lifecycleCompleted
		// Chốt khi xong sách: sinh tất định (store đã có đủ dữ kiện, không tốn lệnh gọi LLM; mục cuối RFC).
		summary := completionSummary(*progress, *book)
		h.mu.Unlock()
		h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Summary: summary, Level: "success"})
		h.notifier.Send(notify.Notification{
			Kind: notify.KindRunEnd, Level: "info", Title: buildversion.AppName + ": Đã hoàn thành truyện",
			Body: h.runEndBody("", summary),
		})
	} else {
		wasRunning := h.lifecycle == lifecycleRunning
		if wasRunning {
			h.lifecycle = lifecycleIdle
		}
		completed := 0
		title := ""
		if progress != nil {
			completed = len(progress.CompletedChapters)
		}
		if book != nil {
			title = book.Title
		}
		h.mu.Unlock()
		if wasRunning {
			summary := fmt.Sprintf("Engine đã dừng (đã xong %d chương)", completed)
			h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Summary: summary, Level: "warn"})
			h.notifier.Send(notify.Notification{
				Kind: notify.KindRunEnd, Level: "warn", Title: buildversion.AppName + ": Đã dừng viết",
				Body: h.runEndBody(title, summary),
			})
		}
	}

	select {
	case h.done <- struct{}{}:
	default:
	}
}

// runEndBody ghép nội dung thông báo run_end: tên sách + tóm tắt tiến độ + tổng chi phí.
func (h *Host) runEndBody(title, summary string) string {
	if name := strings.TrimSpace(title); name != "" {
		summary = "《" + name + "》" + summary
	}
	cost, _, _, _, _ := h.usage.Totals()
	if cost > 0 {
		summary += fmt.Sprintf(" · chi phí $%.2f", cost)
	}
	return summary
}

// ── Kênh ──

// StreamClearSentinel gửi một mục qua streamCh để báo hiệu "xoá round đang stream".
// Không dùng clearCh riêng nữa — hai kênh không có thứ tự khiến header ✻ hay rơi xuống cuối round trước.
const StreamClearSentinel = "\x00\x00CLEAR\x00\x00"

func (h *Host) Events() <-chan Event  { return h.events }
func (h *Host) Stream() <-chan string { return h.streamCh }
func (h *Host) Done() <-chan struct{} { return h.done }
func (h *Host) Dir() string           { return h.store.Dir() }

// ── Phát sự kiện ──

func (h *Host) emitEvent(ev Event) {
	h.outputMu.RLock()
	defer h.outputMu.RUnlock()
	if h.outputClosed {
		return
	}
	// Khoá đọc bảo đảm sự kiện trước lúc đóng được ghi trọn vẹn; sự kiện sau khi đóng bị từ chối thẳng.
	LogEvent(ev)
	select {
	case h.events <- ev:
	default:
		select {
		case <-h.events:
		default:
		}
		select {
		case h.events <- ev:
		default:
		}
	}
}

func (h *Host) emitDelta(delta string) {
	h.outputMu.RLock()
	defer h.outputMu.RUnlock()
	if h.outputClosed {
		return
	}
	select {
	case h.streamCh <- delta:
	default:
		select {
		case <-h.streamCh:
		default:
		}
		select {
		case h.streamCh <- delta:
		default:
		}
	}
}

func (h *Host) closeOutputChannels() {
	h.outputMu.Lock()
	defer h.outputMu.Unlock()
	if h.outputClosed {
		return
	}
	h.outputClosed = true
	close(h.done)
	close(h.events)
	close(h.streamCh)
}

func (h *Host) emitClear() {
	// Gửi "sentinel" qua streamCh để bảo đảm tới TUI có thứ tự, cùng hàng với emitDelta.
	h.emitDelta(StreamClearSentinel)
}

// ── Snapshot (tổng hợp trạng thái cho TUI) ──

func (h *Host) Snapshot() UISnapshot {
	h.mu.Lock()
	state := h.lifecycle
	provider, model, _ := h.models.CurrentSelection("default")
	modelWindow, _ := h.cfg.ResolveContextWindow(provider, model)
	thinkingLevel := h.cfg.ResolveReasoningEffort("default")
	style := h.cfg.Style
	h.mu.Unlock()

	// Phân giải động cửa sổ ngữ cảnh của model hiện tại, lần Snapshot sau sẽ tự phản ánh sau khi /model hoặc /config đổi.
	cost, tokIn, tokOut, cacheRead, cacheWrite := h.usage.Totals()
	saved := h.usage.SavedUSD()
	overallCapable := h.usage.OverallCacheCapable()
	recentRead, recentInput, recentSamples := h.usage.OverallRecent()
	perAgent := h.usage.PerAgent()
	cacheStats := make([]AgentCacheStat, 0, len(perAgent))
	for _, a := range perAgent {
		cacheStats = append(cacheStats, AgentCacheStat{
			Role:            a.Role,
			Input:           a.Input,
			Output:          a.Output,
			CacheRead:       a.CacheRead,
			CacheWrite:      a.CacheWrite,
			Cost:            a.Cost,
			Saved:           a.Saved,
			CacheCapable:    a.CacheCapable,
			RecentCacheRead: a.RecentCacheRead,
			RecentInput:     a.RecentInput,
			RecentSamples:   a.RecentSamples,
		})
	}
	perModel := h.usage.PerModel()
	modelStats := make([]AgentCacheStat, 0, len(perModel))
	for _, a := range perModel {
		modelStats = append(modelStats, AgentCacheStat{
			Model:        a.Model,
			Input:        a.Input,
			Output:       a.Output,
			CacheRead:    a.CacheRead,
			CacheWrite:   a.CacheWrite,
			Cost:         a.Cost,
			Saved:        a.Saved,
			CacheCapable: a.CacheCapable,
		})
	}

	snap := UISnapshot{
		Provider:               provider,
		ModelName:              model,
		ModelContextWindow:     modelWindow,
		ThinkingLevel:          thinkingLevel,
		Style:                  style,
		RuntimeState:           string(state),
		IsRunning:              state == lifecycleRunning,
		TotalInputTokens:       tokIn,
		TotalOutputTokens:      tokOut,
		TotalCacheReadTokens:   cacheRead,
		TotalCacheWriteTokens:  cacheWrite,
		TotalCostUSD:           cost,
		TotalSavedUSD:          saved,
		BudgetLimitUSD:         h.budget.Limit(),
		OverallCacheCapable:    overallCapable,
		OverallRecentCacheRead: recentRead,
		OverallRecentInput:     recentInput,
		OverallRecentSamples:   recentSamples,
		TotalCacheBreaks:       h.usage.OverallCacheBreaks(),
		CachePerAgent:          cacheStats,
		CachePerModel:          modelStats,
		MissingAssistantUsage:  h.usage.MissingAssistantUsage(),
	}

	if book, _ := h.store.Book.Load(); book != nil {
		snap.BookTitle = book.Title
		snap.Synopsis = utils.TruncateRunes(book.Synopsis, 200)
	}
	progress, _ := h.store.Progress.Load()
	if progress != nil {
		snap.Phase = string(progress.Phase)
		snap.Flow = string(progress.Flow)
		snap.CurrentChapter = progress.CurrentChapter
		snap.TotalChapters = progress.TotalChapters
		snap.CompletedCount = len(progress.CompletedChapters)
		snap.TotalWordCount = progress.TotalWordCount
		snap.InProgressChapter = progress.InProgressChapter
		snap.PendingRewrites = progress.PendingRewrites
		snap.RewriteReason = progress.RewriteReason
		snap.Layered = progress.Layered
		if progress.CurrentVolume > 0 {
			snap.CurrentVolumeArc = fmt.Sprintf("Tập %d · Cung %d", progress.CurrentVolume, progress.CurrentArc)
		}
	}
	if meta, _ := h.store.RunMeta.Load(); meta != nil {
		snap.PendingSteer = meta.PendingSteer
		snap.AdvanceMode = string(meta.AdvanceMode)
		snap.AdvancePermitChapter = meta.AdvancePermitChapter
		if meta.AdvanceHold != nil {
			snap.HasAdvanceHold = true
			snap.AdvanceHoldReason = meta.AdvanceHold.Reason
		}
	}

	snap.Agents = h.observer.agentSnapshots()
	snap.StatusLabel = deriveStatusLabel(snap)

	// Nhãn khôi phục: giữ dạng Msg (chưa dịch) để TUI hiển thị đúng ngôn ngữ.
	if label, err := resumeLabel(h.store); err == nil && !label.Empty() {
		snap.RecoveryLabel = label
	}

	h.fillDetails(&snap, progress)

	return snap
}

// fillDetails đổ đầy khu vực chi tiết: thiết lập, nhân vật, commit/review/tóm tắt gần nhất.
func (h *Host) fillDetails(snap *UISnapshot, progress *domain.Progress) {
	if premise, _ := h.store.Outline.LoadPremise(); premise != "" {
		snap.Premise = utils.TruncateRunes(premise, 80)
	}
	if outline, _ := h.store.Outline.LoadOutline(); len(outline) > 0 {
		completed := make(map[int]struct{})
		if progress != nil {
			completed = make(map[int]struct{}, len(progress.CompletedChapters))
			for _, chapter := range progress.CompletedChapters {
				completed[chapter] = struct{}{}
			}
		}
		for _, e := range outline {
			title := e.Title
			if _, ok := completed[e.Chapter]; ok {
				committedTitle, err := h.store.Summaries.LoadSummaryTitle(e.Chapter)
				if err != nil {
					slog.Warn("Không chiếu được tiêu đề chương", "module", "host.snapshot", "chapter", e.Chapter, "err", err)
				} else if strings.TrimSpace(committedTitle) != "" {
					title = committedTitle
				}
			}
			snap.Outline = append(snap.Outline, OutlineSnapshot{
				Chapter: e.Chapter, Title: title, CoreEvent: e.CoreEvent,
			})
		}
	}
	if progress != nil && progress.Layered {
		if compass, _ := h.store.Outline.LoadCompass(); compass != nil {
			snap.CompassDirection = compass.EndingDirection
			snap.CompassScale = compass.EstimatedScale
		}
		if volumes, _ := h.store.Outline.LoadLayeredOutline(); len(volumes) > 0 {
			for _, v := range volumes {
				if v.Index > progress.CurrentVolume {
					snap.NextVolumeTitle = v.Title
					break
				}
			}
		}
	}
	if chars, _ := h.store.Characters.Load(); len(chars) > 0 {
		for _, c := range chars {
			label := c.Name
			if c.Role != "" {
				label += "（" + c.Role + "）"
			}
			snap.Characters = append(snap.Characters, label)
		}
	}
	if progress != nil && len(progress.CompletedChapters) > 0 {
		cast, err := h.store.BuildCast(progress.CompletedChapters)
		if err != nil {
			slog.Warn("Không chiếu được khung nhân vật phụ", "module", "host.snapshot", "err", err)
		}
		snap.SupportingCount = len(cast)
		recent := domain.RecentCast(cast, 5)
		for _, e := range recent {
			label := e.Name
			if e.BriefRole != "" {
				label += "（" + e.BriefRole + "）"
			}
			snap.RecentSupporting = append(snap.RecentSupporting, label)
		}
	}
	if progress != nil && len(progress.CompletedChapters) > 0 {
		lastCh := progress.CompletedChapters[len(progress.CompletedChapters)-1]
		wc := progress.ChapterWordCounts[lastCh]
		snap.LastCommitSummary = fmt.Sprintf("Chương %d · %d chữ", lastCh, wc)
	}
	currentCh := 1
	if progress != nil && len(progress.CompletedChapters) > 0 {
		currentCh = progress.CompletedChapters[len(progress.CompletedChapters)-1]
	}
	if review, err := h.store.World.LoadLastReview(currentCh); err == nil && review != nil {
		snap.LastReviewSummary = fmt.Sprintf("kết luận=%s · %d vấn đề", review.Verdict, len(review.Issues))
		if len(review.AffectedChapters) > 0 {
			snap.LastReviewSummary += fmt.Sprintf(" · ảnh hưởng %v", review.AffectedChapters)
		}
	}
	if cp := h.store.Checkpoints.LatestGlobal(); cp != nil {
		snap.LastCheckpointName = fmt.Sprintf("%s.%s", cp.Scope, cp.Step)
	}
	if progress != nil {
		for i := len(progress.CompletedChapters) - 1; i >= 0 && len(snap.RecentSummaries) < 2; i-- {
			ch := progress.CompletedChapters[i]
			if summary, err := h.store.Summaries.LoadSummary(ch); err == nil && summary != nil {
				snap.RecentSummaries = append(snap.RecentSummaries,
					fmt.Sprintf("Chương %d: %s", ch, utils.TruncateRunes(summary.Summary, 50)))
			}
		}
	}
}

func deriveStatusLabel(s UISnapshot) string {
	switch {
	case s.Phase == string(domain.PhaseComplete):
		return "COMPLETE"
	case s.Flow == string(domain.FlowReviewing):
		return "REVIEW"
	case s.Flow == string(domain.FlowRewriting) || s.Flow == string(domain.FlowPolishing):
		return "REWRITE"
	case s.RuntimeState == "running":
		return "RUNNING"
	default:
		return "READY"
	}
}

// ── Quản lý model ──

func (h *Host) ConfiguredProviders() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	providers := make([]string, 0, len(h.cfg.Providers))
	for name := range h.cfg.Providers {
		providers = append(providers, name)
	}
	sort.Strings(providers)
	return providers
}

func (h *Host) ConfiguredModels(provider string) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.cfg.CandidateModels(provider)
}

func (h *Host) CurrentModelSelection(role string) (string, string, bool) {
	return h.models.CurrentSelection(role)
}

func (h *Host) SwitchModel(role, provider, model string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if provider == "" || model == "" {
		return fmt.Errorf("provider and model are required")
	}
	if err := h.models.Swap(role, provider, model); err != nil {
		return err
	}
	if role == "" || role == "default" {
		h.cfg.Provider = provider
		h.cfg.ModelName = model
	} else {
		if h.cfg.Roles == nil {
			h.cfg.Roles = make(map[string]bootstrap.RoleConfig)
		}
		rc := h.cfg.Roles[role]
		rc.Provider = provider
		rc.Model = model
		h.cfg.Roles[role] = rc
	}
	// Đổi model không sửa ý định mức suy luận đã lưu: chỉ kẹp theo năng lực model mới lúc gửi xuống.
	if h.configPath != "" {
		if err := bootstrap.SaveConfig(h.configPath, h.cfg); err != nil {
			slog.Warn("保存配置失败", "module", "host", "err", err)
		}
	}
	h.applyThinkingLocked(role)
	// Khi chuyển sang model chưa đăng ký thì in một dòng warn, nhắc người dùng đang dùng mức dự phòng 128k — truyện dài dễ bị nén sớm.
	logRole := role
	if logRole == "" {
		logRole = "default"
	}
	window, source := h.cfg.ResolveContextWindow(provider, model)
	bootstrap.LogContextWindowChoice(logRole, model, window, source)

	// Không có ngữ cảnh thường trú nào cần liên kết: ContextManager của writer/architect/editor đi qua
	// ContextManagerFactory, lần spawn sau sẽ tự dựng lại theo cửa sổ của model mới.

	h.emitEvent(Event{
		Time:     time.Now(),
		Category: "SYSTEM",
		Summary:  fmt.Sprintf("Đã đổi model: %s → %s/%s", role, provider, model),
		Level:    "info",
	})
	return nil
}

// concreteThinkingRoles là các vai trò cụ thể có thể áp mức suy luận (khớp với tuyến agents.ApplyThinking).
// Khi gọi default thì áp lại từng vai trò theo ResolveReasoningEffort của vai trò đó.
var concreteThinkingRoles = []string{"architect", "writer", "editor"}

// CurrentThinking trả về chuỗi mức suy luận đang có hiệu lực của một vai trò (để /model đồng bộ giá trị hiện tại).
func (h *Host) CurrentThinking(role string) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.cfg.ResolveReasoningEffort(strings.ToLower(strings.TrimSpace(role)))
}

func (h *Host) AvailableThinking(role string) []agentcore.ThinkingLevel {
	h.mu.Lock()
	model := h.models.ForRole(strings.ToLower(strings.TrimSpace(role)))
	h.mu.Unlock()
	return agents.AvailableThinkingForModel(model)
}

// resolveThinkingForRoleLocked tính mức suy luận thực sự có hiệu lực của một vai trò: lấy ý định gốc
// (ResolveReasoningEffort: mức vai trò → mặc định top-level), rồi kẹp theo năng lực model hiện tại của vai trò.
// Việc kẹp chỉ xảy ra trên “đường có hiệu lực” này, không ghi ngược cấu hình — nơi lưu luôn giữ ý định gốc của người dùng.
func (h *Host) resolveThinkingForRoleLocked(role string) agentcore.ThinkingLevel {
	parsed, _ := agents.ParseThinkingLevel(h.cfg.ResolveReasoningEffort(role))
	resolved, _ := agents.ResolveThinkingForModel(h.models.ForRole(role), parsed)
	return resolved
}

// applyThinkingLocked gửi mức có hiệu lực xuống agent đang chạy; mỗi vai trò kẹp theo model của riêng nó.
func (h *Host) applyThinkingLocked(role string) {
	if h.thinkingApplier == nil {
		return
	}
	role = strings.ToLower(strings.TrimSpace(role))
	if role == "" || role == "default" {
		for _, r := range concreteThinkingRoles {
			h.thinkingApplier(r, h.resolveThinkingForRoleLocked(r))
		}
		return
	}
	h.thinkingApplier(role, h.resolveThinkingForRoleLocked(role))
}

// SetRoleThinking đặt mức suy luận cho một vai trò (hoặc default): kiểm tra → lưu bền → liên kết agent đang chạy → sự kiện.
// Cấu trúc đối xứng với SwitchModel; độc lập với việc chọn model nên chỉnh riêng được. level rỗng = không ghi đè (kế thừa).
func (h *Host) SetRoleThinking(role, level string) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	parsed, err := agents.ParseThinkingLevel(level)
	if err != nil {
		return err
	}
	role = strings.ToLower(strings.TrimSpace(role))
	// Nơi lưu giữ ý định gốc: lưu thẳng mức người dùng chọn, việc kẹp chỉ xảy ra lúc gửi xuống (applyThinkingLocked) theo năng lực model.
	if role == "" || role == "default" {
		h.cfg.ReasoningEffort = string(parsed)
	} else {
		if h.cfg.Roles == nil {
			h.cfg.Roles = make(map[string]bootstrap.RoleConfig)
		}
		rc := h.cfg.Roles[role]
		rc.ReasoningEffort = string(parsed)
		h.cfg.Roles[role] = rc
	}
	if h.configPath != "" {
		if err := bootstrap.SaveConfig(h.configPath, h.cfg); err != nil {
			slog.Warn("保存配置失败", "module", "host", "err", err)
		}
	}

	// Liên kết agent đang chạy: vai trò cụ thể thì áp thẳng; default thì duyệt từng vai trò cụ thể theo ResolveReasoningEffort
	// (vai trò đã bị ghi đè ở mức vai trò thì giữ nguyên, chưa ghi đè thì nhận mặc định mới).
	h.applyThinkingLocked(role)

	logRole := role
	if logRole == "" {
		logRole = "default"
	}
	shown := string(parsed)
	if shown == "" {
		shown = "Mặc định (kế thừa)"
	}
	h.emitEvent(Event{
		Time:     time.Now(),
		Category: "SYSTEM",
		Summary:  fmt.Sprintf("Đã đổi mức suy luận: %s → %s", logRole, shown),
		Level:    "info",
	})
	return nil
}

// ── Phát lại sự kiện ──

func (h *Host) ReplayQueue(afterSeq int64) ([]domain.RuntimeQueueItem, error) {
	if h.store == nil || h.store.Runtime == nil {
		return nil, nil
	}
	return h.store.Runtime.LoadQueueAfter(afterSeq)
}

// ── Đồng sáng tác ──

// CoCreateStream đồng sáng tác khởi đầu từ số 0: làm rõ yêu cầu và sinh chỉ đạo sáng tác cho cả cuốn.
func (h *Host) CoCreateStream(ctx context.Context, history []CoCreateMessage, onProgress func(kind, text string)) (CoCreateReply, error) {
	return coCreateStream(ctx, h.models, h.store.Sessions, coCreateSystemPrompt, history, onProgress)
}

// StageCoCreateStream đồng sáng tác theo giai đoạn: quy hoạch hướng đi tiếp dựa trên phần đã viết.
// Prompt hệ thống = prompt giai đoạn + tóm tắt trạng thái câu chuyện hiện tại, để trợ lý biết "đã viết đến đâu".
func (h *Host) StageCoCreateStream(ctx context.Context, history []CoCreateMessage, onProgress func(kind, text string)) (CoCreateReply, error) {
	return coCreateStream(ctx, h.models, h.store.Sessions, stageSystemPrompt(h.store), history, onProgress)
}

// stagePlanPrefix bọc "bản tóm tắt hướng đi tiếp" do đồng sáng tác sinh ra thành một can thiệp quy hoạch giai đoạn, đưa Arbiter định đoạn.
// Chỉ dán nhãn dữ kiện [Giai đoạn] + phát biểu trung tính, không viết cứng "làm thế nào" — tuyến cụ thể (compass / architect /
// user_rules) giao cho tiêu chí 「Giai đoạn」 trong arbiter-intervention.md, tránh tạo nguồn sự thật thứ hai với prompt,
// và không bịt các yêu cầu về văn phong đi qua user_rules (giữ nguyên "phân loại thì LLM quyết"). Continue còn chồng tiền tố [Can thiệp người dùng].
const stagePlanPrefix = "[Giai đoạn] Tôi tạm dừng việc viết và cùng trợ lý đồng sáng tác đã sơ khai hướng đi tiếp dưới đây, xin hãy định đoạn theo phân loại can thiệp của bạn rồi tiếp tục viết. Hướng đi tiếp như sau:\n\n"

// PauseForCoCreate vào đồng sáng tác giai đoạn: đặt cờ chiếm đồng sáng tác, nếu đang chạy thì tạm dừng Engine luôn.
// Trả false nghĩa là không vào được (đã xong sách hoặc đang trong đồng sáng tác), bên gọi có thể bỏ qua.
// Cờ chiếm trong cửa sổ đồng sáng tác chặn can thiệp chồng nhau từ import/simulate/start/resume/continue —
// khi tạm dừng lúc đang chạy thì lifecycle=paused, ràng buộc loại trừ ==running mất hiệu lực, nên phải có cờ này bù;
// đã dừng (idle/paused) vẫn cho vào, xong phần quy hoạch sẽ chạy tiếp qua Continue.
func (h *Host) PauseForCoCreate() bool {
	h.mu.Lock()
	if h.cocreating || h.lifecycle == lifecycleCompleted {
		h.mu.Unlock()
		return false
	}
	h.cocreating = true
	running := h.lifecycle == lifecycleRunning
	h.mu.Unlock()

	// Khi đang chạy thì tái dùng abortWithEvent để dừng (running→paused + Abort +
	// sự kiện), cùng thứ tự với dừng thủ công, không chép lại logic; nếu đã dừng
	// (idle/paused) thì chỉ đặt cờ, xong phần quy hoạch sẽ chạy tiếp qua Continue.
	if running {
		h.abortWithEvent("Vào đồng sáng tác, việc viết đã tạm dừng", "info")
	} else {
		h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Summary: "Đã vào đồng sáng tác", Level: "info"})
	}
	return true
}

// ResumeFromCoCreate kết thúc đồng sáng tác: chèn hướng đi tiếp do đồng sáng tác
// sinh ra thành can thiệp rồi khôi phục việc viết.
// Sau khi xóa cờ chiếm, tái dùng đường chèn của Continue khi đã dừng (vẫn chịu ràng
// buộc ngân sách trước).
// Lưu ý: draft rỗng thì trả về sớm mà không xóa cờ là cố ý (đồng sáng tác chưa
// kết thúc); phía TUI dùng cùng một tiêu chí "không rỗng" ở canStart(), nên đường
// này không tới được và cocreating không bị rò.
func (h *Host) ResumeFromCoCreate(draft string) error {
	draft = strings.TrimSpace(draft)
	if draft == "" {
		return fmt.Errorf("draft is required")
	}
	h.mu.Lock()
	if !h.cocreating {
		h.mu.Unlock()
		return fmt.Errorf("not in co-create")
	}
	h.cocreating = false
	h.mu.Unlock()

	// Lệnh abort của PauseForCoCreate chạy bất đồng bộ: phải chờ vòng lặp Engine
	// hội tụ thật rồi mới tiếp tục, để trở về đúng tiền đề "thật sự đã dừng" như khi
	// dừng thủ công rồi gọi Continue. Cửa sổ đồng sáng tác ở thang thời gian tương tác
	// người, poll ngắn không thấy được.
	for h.engine.isRunning() {
		time.Sleep(20 * time.Millisecond)
	}

	h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Summary: "Đồng sáng tác xong, đã chèn hướng đi tiếp và khôi phục việc viết", Level: "info"})
	return h.Continue(stagePlanPrefix + draft)
}

// CancelCoCreate bỏ dở đồng sáng tác: xóa cờ chiếm, giữ trạng thái tạm dừng (người
// dùng có thể nhập tiếp ở ô nhập hoặc khởi động lại để Resume).
func (h *Host) CancelCoCreate() {
	h.mu.Lock()
	if !h.cocreating {
		h.mu.Unlock()
		return
	}
	h.cocreating = false
	h.mu.Unlock()
	h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Summary: "Đã thoát đồng sáng tác, việc viết vẫn tạm dừng (có thể nhập tiếp ở ô nhập)", Level: "info"})
}

// ── Công cụ ──

func (h *Host) refreshWriterRestore() {
	if h.writerRestore != nil {
		h.writerRestore.Refresh(h.store)
	}
}

func (h *Host) CheckChapterRevisions() ([]int, error) {
	pending, err := h.store.Revisions.LoadPending()
	if err != nil {
		return nil, fmt.Errorf("Không đọc được bản ghi khôi phục sửa chương: %w", err)
	}
	if pending != nil {
		chapters := make([]int, 0, len(pending.Items))
		for _, item := range pending.Items {
			chapters = append(chapters, item.Chapter)
		}
		return chapters, nil
	}
	changes, err := revision.Scan(h.store)
	if err != nil {
		return nil, err
	}
	return revision.ChangedChapters(changes), nil
}

func (h *Host) SyncChapterRevisions(ctx context.Context) (*revision.Result, error) {
	if err := h.acquireExclusive("đồng bộ sửa chương"); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	h.mu.Lock()
	h.exclusiveCancel = cancel
	h.mu.Unlock()
	defer h.releaseExclusive()

	pending, err := h.store.Revisions.LoadPending()
	if err != nil {
		return nil, err
	}
	if pending == nil {
		changes, err := revision.Scan(h.store)
		if err != nil {
			return nil, err
		}
		if len(changes) == 0 {
			return &revision.Result{}, nil
		}
		if err := h.budget.Refuse(); err != nil {
			return nil, err
		}
	}
	model := h.models.ForRoleWithFailover("editor", func(ev bootstrap.FailoverEvent) {
		slog.Warn("Chuyển provider cho sửa chương", "module", "revision", "role", ev.Role,
			"reason", ev.Reason, "from", fmt.Sprintf("%s/%s", ev.FromProvider, ev.FromModel),
			"to", fmt.Sprintf("%s/%s", ev.ToProvider, ev.ToModel), "err", ev.Err)
	})
	model = newUsageTrackedModel(model, "editor", h.usage.Record)
	service := revision.NewService(h.store, model, h.bundle.Prompts.RevisionAnalyze, h.styleStats)
	return service.Sync(ctx)
}

func (h *Host) requireCleanChapters() error {
	chapters, err := h.CheckChapterRevisions()
	if err != nil {
		return fmt.Errorf("Kiểm tra sửa chương từ bên ngoài thất bại: %w", err)
	}
	if len(chapters) > 0 {
		return fmt.Errorf("Phát hiện nội dung chương đã bị sửa từ bên ngoài: %v; hãy chạy /sync trước", chapters)
	}
	return nil
}

// ImportFrom chạy một lần nhập biên dịch ngữ nghĩa truyện ngoài: ingest → segment → analyze → synthesize → publish.
// Model chỉ định đoạn phần ngữ nghĩa mở (biên/độ kiện/tổng hợp), Go phụ trách toạ độ/ghi đè/idempotent; loại trừ với Engine đang chạy,
// sau khi nhập xong thì AdvanceHold quyết định có viết tiếp hay không.
// Kênh sự kiện trả về do imp.Run đóng; bên gọi chịu trách nhiệm tiêu thụ (đầy thì bỏ để không chặn goroutine pipeline).
func (h *Host) ImportFrom(ctx context.Context, opts imp.Options) (<-chan imp.Event, error) {
	// Kiểm tra ngân sách trước khi chạy theo đúng kỷ luật của Start/Resume/Continue: nhập là toàn bộ quy trình gọi model,
	// đã quá hạn ngân sách thì không được chạy (§13.1 "nằm trong cảnh báo ngân sách sẵn có").
	if err := h.budget.Refuse(); err != nil {
		return nil, err
	}
	if err := h.acquireExclusive("nhập truyện"); err != nil {
		return nil, err
	}
	// Ghi nhận hàm huỷ: dừng cứng theo ngân sách hay dừng thủ công sẽ qua abortWithEvent huỷ context riêng của nhập
	// (nếu không thì cảnh báo chỉ dừng Engine chưa chạy, còn nhập vẫn đốt tiền).
	ctx, cancel := context.WithCancel(ctx)
	h.mu.Lock()
	h.exclusiveCancel = cancel
	h.mu.Unlock()

	deps := imp.Deps{
		Store:         h.store,
		CommitChapter: tools.NewCommitChapterTool(h.store, h.styleStats),
		Segment:       h.importCaller("segment"),
		Analyze:       h.importCaller("analyze"),
		Synthesize:    h.importCaller("synthesize"),
		Prompts: imp.Prompts{
			Segment:    h.bundle.Prompts.ImportSegment,
			Analyze:    h.bundle.Prompts.ImportAnalyze,
			Synthesize: h.bundle.Prompts.ImportSynthesize,
			Range:      h.bundle.Prompts.ImportRange,
		},
	}
	ch, err := imp.Run(ctx, deps, opts)
	if err != nil {
		h.releaseExclusive()
		return nil, err
	}
	return h.superviseImport(ch, opts), nil
}

// ImportResumeHint trả về một dòng nhắc về lần nhập chưa xong (rỗng nếu không có), để TUI chủ động báo lúc khởi động (RFC §18.2).
// Chỉ gọi một lần lúc khởi động: bên trong tính lại InputDigest của từng artifact trong workspace, không hợp đặt vào vòng poll snapshot.
func (h *Host) ImportResumeHint() string {
	return imp.ResumeSummary(h.store)
}

// importCaller phân giải nấc model của một hàm ngữ nghĩa nhập (RFC §13.1): nếu roles có import_<fn>
// thì dùng nấc đó (mức dùng cũng tính vào vai trò đó), không thì rơi về architect. Đây chỉ là cấu hình lời gọi, không đổi bất kỳ hợp đồng ngữ nghĩa nào.
func (h *Host) importCaller(fn string) imp.Caller {
	role := "import_" + fn
	if _, _, explicit := h.models.CurrentSelection(role); !explicit {
		role = "architect"
	}
	model := h.models.ForRoleWithFailover(role, func(ev bootstrap.FailoverEvent) {
		slog.Warn("Chuyển provider cho nhập truyện", "module", "import", "role", ev.Role,
			"reason", ev.Reason,
			"from", fmt.Sprintf("%s/%s", ev.FromProvider, ev.FromModel),
			"to", fmt.Sprintf("%s/%s", ev.ToProvider, ev.ToModel),
			"err", ev.Err)
	})
	model = newUsageTrackedModel(model, role, h.usage.Record)
	return imp.Caller{Model: model, Runtime: h.importModelRuntime(role, model)}
}

// importModelRuntime dò năng lực gọi của model ở nấc vai trò đã chọn, cho imp dùng ngân sách kép / thích ứng thinking (RFC §13/§21).
// Trường dò lỗi để nguyên giá trị 0, phía imp rơi về mặc định thận trọng, đảm bảo không có thông tin năng lực vẫn chạy đúng.
// Output có cấu trúc do llmcontract của imp đọc trực tiếp sự thật của model trước mỗi request, không cache lặp trong Runtime.
func (h *Host) importModelRuntime(role string, model agentcore.ChatModel) imp.ModelRuntime {
	var rt imp.ModelRuntime
	provider, name, _ := h.models.CurrentSelection(role)
	if name == "" {
		name = bootstrap.ModelName(model)
		provider = bootstrap.ModelProvider(model)
	}
	// Trần context / completion: registry là nguồn tin cậy duy nhất (Info() của model bọc không chứa cửa sổ).
	rt.ContextTokens, _ = h.cfg.ResolveContextWindow(provider, name)
	if entry, ok := modelreg.DefaultRegistry().Resolve(name); ok {
		rt.MaxOutputTokens = entry.MaxTokens
	}
	// thinking: phân giải theo reasoning effort của vai trò và năng lực model; không hỗ trợ thì không gửi (cùng chiến lược với arbiter).
	if level, err := agents.ParseThinkingLevel(h.cfg.ResolveReasoningEffort(role)); err == nil {
		if resolved, ok := agents.ResolveThinkingForModel(model, level); ok {
			rt.Thinking = resolved
		}
	}
	return rt
}

// Simulate đọc thư mục simulate rồi sinh hoặc cập nhật dần hồ sơ văn phong.
func (h *Host) Simulate(ctx context.Context) (<-chan sim.Event, error) {
	if err := h.acquireExclusive("sinh hồ sơ văn phong"); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	h.mu.Lock()
	h.exclusiveCancel = cancel
	h.mu.Unlock()

	wd, err := os.Getwd()
	if err != nil {
		h.releaseExclusive()
		return nil, fmt.Errorf("get working dir: %w", err)
	}
	deps := sim.Deps{
		Store: h.store,
		LLM:   h.models.ForRole("architect"),
		Prompts: sim.Prompts{
			Source: h.bundle.Prompts.SimulationSource,
			Merge:  h.bundle.Prompts.SimulationMerge,
		},
	}
	ch, err := sim.Run(ctx, deps, sim.Options{SourceDir: filepath.Join(wd, "simulate")})
	if err != nil {
		h.releaseExclusive()
		return nil, err
	}
	return superviseExclusive(h, ch), nil
}

// ImportSimulationProfile nhập hồ sơ văn phong đã sinh từ trước.
func (h *Host) ImportSimulationProfile(ctx context.Context, path string) (<-chan sim.Event, error) {
	if err := h.acquireExclusive("nhập hồ sơ văn phong"); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	h.mu.Lock()
	h.exclusiveCancel = cancel
	h.mu.Unlock()
	ch, err := sim.RunImport(ctx, h.store, path)
	if err != nil {
		h.releaseExclusive()
		return nil, err
	}
	return superviseExclusive(h, ch), nil
}

// acquireExclusive chiếm nguyên tử ô việc nền độc quyền (import/simulate/revision): khi Engine đang chạy, trong cửa sổ đồng sáng tác,
// hoặc đã có việc độc quyền khác chạy thì từ chối. Thành công là ghi cờ chiếm, khi xong phải gọi releaseExclusive — nếu không thì hai lần nhập
// hay nhập + mô phỏng sẽ tranh nhau sửa cùng một trạng thái. Vá lỗ hổng trước đây: chỉ kiểm ==running/cocreating mà không ghi cờ chính việc đang chạy.
func (h *Host) acquireExclusive(action string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	switch {
	case h.closing:
		return fmt.Errorf("Host đang đóng, không thể %s", action)
	// engine.isRunning() phải kiểm: Abort đặt lifecycle=paused trước rồi mới chờ goroutine thoát bất đồng bộ,
	// trong khoảng đó lifecycle không còn running nhưng Engine vẫn có thể đang ghi store (cùng kỷ luật với cổng chặn lúc khởi động).
	case h.lifecycle == lifecycleRunning || h.engine.isRunning():
		return fmt.Errorf("Engine đang viết hoặc đang dừng, chờ một lát rồi hãy %s", action)
	case h.cocreating:
		return fmt.Errorf("Đang trong đồng sáng tác, kết thúc đồng sáng tác rồi hãy %s", action)
	case h.exclusive != "":
		return fmt.Errorf("Đang %s, xong rồi hãy %s", h.exclusive, action)
	}
	h.exclusive = action
	return nil
}

// releaseExclusive giải phóng ô việc nền độc quyền (kèm hàm huỷ đã đăng ký).
func (h *Host) releaseExclusive() {
	h.mu.Lock()
	cancel := h.exclusiveCancel
	h.exclusive = ""
	h.exclusiveCancel = nil
	h.mu.Unlock()
	if cancel != nil {
		cancel() // Việc đã xong: huỷ context dẫn xuất; runner đã thoát thì không có tác dụng phụ
	}
}

// superviseExclusive chuyển tiếp sự kiện của việc độc quyền, và giải phóng ô chiếm khi kênh đóng (việc đã xong).
func superviseExclusive[T any](h *Host, src <-chan T) <-chan T {
	out := make(chan T, 32)
	if !h.launchAsync(func() {
		defer close(out)
		defer h.releaseExclusive()
		for ev := range src {
			select {
			case out <- ev:
			case <-h.runCtx.Done():
				// Trong lúc đóng vẫn tiếp tục rút hết kênh nguồn, tránh producer bị chặn ở sự kiện trạng thái cuối mà không thoát được.
				for range src {
				}
				return
			}
		}
	}) {
		close(out)
		h.releaseExclusive()
	}
	return out
}

// superviseImport là chủ sở hữu duy nhất của câu hỏi "có tự chuyển tiếp sau khi nhập": chuyển tiếp sự kiện nhập, khi hoàn tất thành công thì
// giải phóng ô độc quyền trước, rồi quyết định và thực hiện việc chuyển tiếp, cuối cùng ghi kết quả chuyển tiếp thật vào trường Continued của sự kiện StageDone. TUI chỉ dựa vào đó để vẽ,
// không còn đoán trạng thái chạy từ cờ --continue cục bộ (loại bỏ race thứ tự do ba bên Runner/Host/TUI hiểu khác nhau).
func (h *Host) superviseImport(src <-chan imp.Event, opts imp.Options) <-chan imp.Event {
	out := make(chan imp.Event, 32)
	if !h.launchAsync(func() {
		defer close(out)
		released := false
		release := func() {
			if !released {
				released = true
				h.releaseExclusive()
			}
		}
		defer release()
		for ev := range src {
			if ev.Stage == imp.StageDone {
				release() // Giải phóng ô độc quyền trước, để startEngine của lần chuyển tiếp qua được cổng chặn độc quyền
				ev.Continued = h.continueAfterImport(opts)
			}
			select {
			case out <- ev:
			case <-h.runCtx.Done():
				for range src {
				}
				return
			}
		}
	}) {
		close(out)
		h.releaseExclusive()
	}
	return out
}

// launchAsync đăng ký một tác vụ nền trong vòng đời của Host. closing và WaitGroup.Add dùng chung
// một khoá, bảo đảm sau khi Close bắt đầu Wait sẽ không còn lệnh Add nào nữa.
func (h *Host) launchAsync(fn func()) bool {
	h.mu.Lock()
	if h.closing {
		h.mu.Unlock()
		return false
	}
	h.asyncWG.Add(1)
	h.mu.Unlock()
	go func() {
		defer h.asyncWG.Done()
		fn()
	}()
	return true
}

// runAsync tái dùng đăng ký tác vụ nền sẵn có của Host, đồng thời trả lỗi nghiệp vụ về cho bên gọi.
func (h *Host) runAsync(fn func() error) (error, bool) {
	result := make(chan error, 1)
	if !h.launchAsync(func() { result <- fn() }) {
		return nil, false
	}
	return <-result, true
}

// continueAfterImport quyết định và thực hiện việc tự chuyển tiếp thật của
// --continue, trả về Engine đã khởi động hay chưa.
// Ý định tự chuyển tiếp hợp lệ = opts lần này hoặc intent đã lưu trong workspace
// (phủ cả tình huống sập rồi khôi phục bằng /import không tham số);
// chỉ chuyển tiếp ở chế độ tự động đẩy, do quy hoạch mở rộng cung thích ứng tiếp nhận
// câu chuyện đang mở, hoặc để truyện đã xong viết nốt; ở chế độ duyệt thì chờ
// người dùng bấm /next.
func (h *Host) continueAfterImport(opts imp.Options) bool {
	want := opts.ContinueAfter
	if !want {
		in, err := imp.OpenWorkspace(h.store.Dir()).LoadIntent()
		if err != nil {
			h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Level: "warn",
				Summary: "Đã nhập xong, nhưng không đọc được ý định tự chuyển tiếp: " + err.Error()})
		} else if in != nil {
			want = in.ContinueAfterImport
		}
	}
	if !want {
		return false
	}
	meta, err := h.store.RunMeta.Load()
	if err != nil || meta == nil {
		slog.Warn("Không đọc được RunMeta cho tự chuyển tiếp sau khi nhập", "module", "host", "err", err)
		return false
	}
	if meta.AdvanceMode != domain.ChapterAdvanceAuto {
		h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Level: "info",
			Summary: "Đã nhập xong; hiện ở chế độ duyệt từng chương, nhập tiếp hoặc dùng /next để viết nốt"})
		return false
	}
	h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Level: "info", Summary: "Đã nhập xong, tự chuyển tiếp viết tiếp"})
	if !h.startEngine(nil) {
		h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Level: "warn",
			Summary: "Không khởi động được tự chuyển tiếp, hãy nhập lệnh tiếp tục để khôi phục thủ công"})
		return false
	}
	return true
}

// Export xuất các chương đã hoàn thành ra file ngoài (hiện chỉ hỗ trợ TXT).
//
// Khác với ImportFrom: xuất là thao tác chỉ đọc (không đụng Progress / Checkpoint),
// nên **không yêu cầu Engine dừng** — kể cả khi đang viết cũng xuất được "thành phẩm tại thời điểm này".
// Chỉ đọc một snapshot nhất quán gồm Progress.CompletedChapters + bản chính thức chương + dàn ý + premise.
func (h *Host) Export(ctx context.Context, opts exp.Options) (*exp.Result, error) {
	return exp.Run(ctx, exp.Deps{Store: h.store}, opts)
}
