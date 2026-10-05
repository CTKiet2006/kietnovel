package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/CTKiet2006/kietnovel/internal/bridge"
	"github.com/CTKiet2006/kietnovel/internal/errs"
	"github.com/CTKiet2006/kietnovel/internal/llmcontract"
	"github.com/voocel/agentcore"
	"github.com/voocel/agentcore/llm"
	"github.com/voocel/litellm"
)

// FailoverEvent biểu diễn một lần chuyển provider tường minh.
// Reason là nhãn ngắn (rate_limit / timeout / stream_idle / network), để log có cấu trúc.
type FailoverEvent struct {
	Role         string
	Reason       string
	FromProvider string
	FromModel    string
	ToProvider   string
	ToModel      string
	Err          error
}

// FailoverReporter được gọi khi xảy ra chuyển đổi tường minh.
type FailoverReporter func(FailoverEvent)

type modelTarget struct {
	provider   string
	name       string
	model      agentcore.ChatModel
	jsonSchema *bool
}

// SwappableModel là wrapper ChatModel có thể hot-swap.
// Request đã bắt đầu vẫn dùng instance cũ; request sau tự cắt sang instance mới.
type SwappableModel struct {
	*agentcore.SwappableModel
	mu       sync.RWMutex
	provider string
	name     string
	// jsonSchema là khai báo ba trạng thái config json_schema của model đang chọn, chuyển nguyên tử
	// cùng khóa với provider/name; llmcontract.Resolve đọc hiện hành qua interface khớp cấu trúc mỗi lần.
	jsonSchema *bool
}

func NewSwappableModel(provider, name string, model agentcore.ChatModel, jsonSchema *bool) *SwappableModel {
	return &SwappableModel{
		SwappableModel: agentcore.NewSwappableModel(model),
		provider:       provider,
		name:           name,
		jsonSchema:     jsonSchema,
	}
}

func (m *SwappableModel) ProviderName() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.provider
}

func (m *SwappableModel) Info() llm.ModelInfo {
	return m.StructuredOutputFacts().Info
}

// StructuredOutputFacts đọc dưới cùng một khóa instance model, danh tính và override cấu hình, đảm bảo một lần
// chọn giao thức cấu trúc chỉ quan sát một phiên bản đầy đủ duy nhất.
func (m *SwappableModel) StructuredOutputFacts() llmcontract.ModelFacts {
	m.mu.RLock()
	defer m.mu.RUnlock()
	current := m.SwappableModel.Current()
	facts := llmcontract.ModelFacts{
		Info:               llm.ModelInfo{Name: m.name, Provider: m.provider},
		JSONSchemaOverride: cloneBoolPtr(m.jsonSchema),
	}
	if cp, ok := current.(llm.CapabilityProvider); ok {
		facts.Capabilities = cp.Capabilities()
	}
	if info, ok := current.(interface{ Info() llm.ModelInfo }); ok {
		modelInfo := info.Info()
		if modelInfo.Name == "" {
			modelInfo.Name = m.name
		}
		if modelInfo.Provider == "" {
			modelInfo.Provider = m.provider
		}
		facts.Info = modelInfo
	}
	return facts
}

func (m *SwappableModel) Capabilities() llm.Capabilities {
	return m.StructuredOutputFacts().Capabilities
}

func (m *SwappableModel) Swap(provider, name string, model agentcore.ChatModel, jsonSchema *bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.SwappableModel.Swap(model)
	m.provider = provider
	m.name = name
	m.jsonSchema = jsonSchema
}

// JSONSchemaOverride trả về khai báo config json_schema ba trạng thái của model đang chọn.
func (m *SwappableModel) JSONSchemaOverride() *bool {
	return m.StructuredOutputFacts().JSONSchemaOverride
}

func cloneBoolPtr(value *bool) *bool {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func (m *SwappableModel) Current() (provider, name string) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.provider, m.name
}

// ModelSet giữ instance model phân theo vai trò, vai trò chưa cấu hình thì rơi về model mặc định.
type ModelSet struct {
	mu        sync.RWMutex
	Default   *SwappableModel
	models    map[string]*SwappableModel
	fallbacks map[string][]modelTarget
	config    Config
}

// NormRole chuẩn hoá tên vai trò về dạng canonical (lowercase + trim).
// Lý do tồn tại: ValidateBase chấp nhận "Writer"/"WRITER" (đúng), nhưng nếu key
// map giữ nguyên dạng gốc thì lookup "writer" sẽ trượt silent về default model.
// Mọi điểm đọc/ghi ms.models/ms.fallbacks/ms.config.Roles đều phải qua đây.
func NormRole(role string) string {
	return strings.ToLower(strings.TrimSpace(role))
}

// ForRole trả về model của vai trò chỉ định, chưa cấu hình thì trả model mặc định.
func (ms *ModelSet) ForRole(role string) agentcore.ChatModel {
	role = NormRole(role)
	ms.mu.RLock()
	defer ms.mu.RUnlock()
	if m, ok := ms.models[role]; ok {
		return m
	}
	return ms.Default
}

// RoleTarget là snapshot nguyên tử của model + identity một vai trò, đọc dưới
// một RLock duy nhất. Dùng khi caller cần cả object lẫn metadata (provider/name)
// mà không chịu được khe giữa hai lần đọc riêng (ví dụ /model advisor đổi đúng
// lúc resolve: object là model mới nhưng metadata vẫn là model cũ).
type RoleTarget struct {
	Model    agentcore.ChatModel
	Provider string
	Name     string
	Explicit bool
}

// ResolveRoleTarget resolve vai trò + identity dưới một RLock duy nhất.
func (ms *ModelSet) ResolveRoleTarget(role string, report FailoverReporter) RoleTarget {
	role = NormRole(role)
	ms.mu.RLock()
	defer ms.mu.RUnlock()
	if primary, ok := ms.models[role]; ok {
		p, n := primary.Current()
		if len(ms.fallbacks[role]) == 0 {
			return RoleTarget{Model: primary, Provider: p, Name: n, Explicit: true}
		}
		return RoleTarget{
			Model:    &failoverModel{role: role, primary: primary, set: ms, report: report},
			Provider: p, Name: n, Explicit: true,
		}
	}
	p, n := ms.Default.Current()
	return RoleTarget{Model: ms.Default, Provider: p, Name: n, Explicit: false}
}

// ForRoleWithFailover trả về model vai trò kèm fallback ở cấp từng request.
// Chỉ hiệu lực khi vai trò đó cấu hình fallbacks tường minh; chưa cấu hình thì suy biến thành model thường.
func (ms *ModelSet) ForRoleWithFailover(role string, report FailoverReporter) agentcore.ChatModel {
	return ms.ResolveRoleTarget(role, report).Model
}

// Summary trả về tóm tắt phân bổ model (để log).
func (ms *ModelSet) Summary() string {
	ms.mu.RLock()
	defer ms.mu.RUnlock()
	var parts []string
	for role, m := range ms.models {
		provider, name := m.Current()
		parts = append(parts, fmt.Sprintf("%s=%s/%s", role, provider, name))
	}
	if len(parts) == 0 {
		provider, name := ms.Default.Current()
		return fmt.Sprintf("default=%s/%s", provider, name)
	}
	provider, name := ms.Default.Current()
	return fmt.Sprintf("default=%s/%s %s", provider, name, strings.Join(parts, " "))
}

// CurrentSelection trả về provider/model đang hiệu lực của vai trò.
// role trống hoặc "default" thì trả model mặc định.
func (ms *ModelSet) CurrentSelection(role string) (provider, model string, explicit bool) {
	role = NormRole(role)
	ms.mu.RLock()
	defer ms.mu.RUnlock()
	if role == "" || role == "default" {
		provider, model = ms.Default.Current()
		return provider, model, true
	}
	if sw, ok := ms.models[role]; ok {
		provider, model = sw.Current()
		return provider, model, true
	}
	provider, model = ms.Default.Current()
	return provider, model, false
}

// Swap chuyển model mặc định hoặc model của vai trò chỉ định.
// role trống hoặc "default" thì chuyển model mặc định; vai trò khác thì chuyển thành override tường minh.
func (ms *ModelSet) Swap(role, provider, model string) error {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	pc, ok := ms.config.Providers[provider]
	if !ok {
		return fmt.Errorf("provider %q chưa được cấu hình: %w", provider, errs.ErrConfig)
	}
	next, err := createModelFromConfig(provider, model, pc, make(map[string]agentcore.ChatModel))
	if err != nil {
		return fmt.Errorf("chuyển model thất bại: %w", err)
	}

	jsonSchema := ms.config.ModelJSONSchema(provider, model)
	if role == "" || role == "default" {
		ms.Default.Swap(provider, model, next, jsonSchema)
		ms.config.Provider = provider
		ms.config.ModelName = model
		return nil
	}

	role = NormRole(role)
	if !knownRoles[role] {
		return fmt.Errorf("role %q không xác định: %w", role, errs.ErrConfig)
	}

	if existing, ok := ms.models[role]; ok {
		existing.Swap(provider, model, next, jsonSchema)
	} else {
		ms.models[role] = NewSwappableModel(provider, model, next, jsonSchema)
	}
	if ms.config.Roles == nil {
		ms.config.Roles = make(map[string]RoleConfig)
	}
	rc := ms.config.Roles[role]
	rc.Provider = provider
	rc.Model = model
	ms.config.Roles[role] = rc
	return nil
}

// ResolveContextWindow giải cửa sổ bằng cấu hình mới nhất của ModelSet, để
// ContextManagerFactory dùng sau hot-swap lúc runtime, tránh chụp bản copy Config lúc khởi động.
func (ms *ModelSet) ResolveContextWindow(provider, model string) (int, ContextWindowSource) {
	ms.mu.RLock()
	defer ms.mu.RUnlock()
	return ms.config.ResolveContextWindow(provider, model)
}

// ApplyPrepared chốt một ModelSet ứng viên đã build thành công. Địa chỉ SwappableModel hiện có
// giữ nguyên, nên Worker/Arbiter đã lắp ráp sẽ tự dùng client mới ở request tiếp theo.
func (ms *ModelSet) ApplyPrepared(candidate *ModelSet) {
	if candidate == nil {
		return
	}
	ms.mu.Lock()
	defer ms.mu.Unlock()

	defaultProvider, defaultName := candidate.Default.Current()
	ms.Default.Swap(defaultProvider, defaultName, candidate.Default.SwappableModel.Current(), candidate.Default.JSONSchemaOverride())

	nextModels := make(map[string]*SwappableModel, len(candidate.models))
	for role, next := range candidate.models {
		role = NormRole(role)
		provider, name := next.Current()
		if existing, ok := ms.models[role]; ok {
			existing.Swap(provider, name, next.SwappableModel.Current(), next.JSONSchemaOverride())
			nextModels[role] = existing
		} else {
			nextModels[role] = next
		}
	}
	nextFallbacks := make(map[string][]modelTarget, len(candidate.fallbacks))
	for role, targets := range candidate.fallbacks {
		nextFallbacks[NormRole(role)] = targets
	}
	ms.models = nextModels
	ms.fallbacks = nextFallbacks
	ms.config = CloneConfig(candidate.config)
}

func (ms *ModelSet) fallbackTargets(role string) []modelTarget {
	role = NormRole(role)
	ms.mu.RLock()
	defer ms.mu.RUnlock()
	return append([]modelTarget(nil), ms.fallbacks[role]...)
}

// ModelName trích tên model hiện tại từ ChatModel, thất bại trả chuỗi trống.
// Hỗ trợ hot-swap của SwappableModel: mỗi lần gọi luôn trả giá trị mới nhất.
// ModelProvider trích tên provider hiện tại từ ChatModel, thất bại trả chuỗi trống.
func ModelName(m agentcore.ChatModel) string {
	if info, ok := m.(interface{ Info() llm.ModelInfo }); ok {
		return info.Info().Name
	}
	return ""
}

// ModelProvider trích tên provider hiện tại từ ChatModel, thất bại trả chuỗi trống.
func ModelProvider(m agentcore.ChatModel) string {
	if info, ok := m.(interface{ Info() llm.ModelInfo }); ok {
		return info.Info().Provider
	}
	if provider, ok := m.(interface{ ProviderName() string }); ok {
		return provider.ProviderName()
	}
	return ""
}

// NewModelSet tạo tập đa model theo cấu hình.
// Tổ hợp provider+model giống nhau dùng chung một instance.
func NewModelSet(cfg Config) (*ModelSet, error) {
	cache := make(map[string]agentcore.ChatModel)

	// Tạo model mặc định
	defaultPC := cfg.DefaultProviderConfig()
	defaultModel, err := createModelFromConfig(cfg.Provider, cfg.ModelName, defaultPC, cache)
	if err != nil {
		return nil, fmt.Errorf("model mặc định: %w", err)
	}

	ms := &ModelSet{
		Default:   NewSwappableModel(cfg.Provider, cfg.ModelName, defaultModel, cfg.ModelJSONSchema(cfg.Provider, cfg.ModelName)),
		models:    make(map[string]*SwappableModel),
		fallbacks: make(map[string][]modelTarget),
		config:    cfg,
	}

	// Tạo model override cho vai trò. Key chuẩn hoá một lần ở đây để
	// "Writer"/"WRITER" trong file cấu hình không tạo entry ma trượt lookup.
	for role, rc := range cfg.Roles {
		role = NormRole(role)
		pc, ok := cfg.Providers[rc.Provider]
		if !ok {
			return nil, fmt.Errorf("role %s trỏ tới provider %q không xác định: %w", role, rc.Provider, errs.ErrConfig)
		}
		m, err := createModelFromConfig(rc.Provider, rc.Model, pc, cache)
		if err != nil {
			return nil, fmt.Errorf("model của role %s: %w", role, err)
		}
		ms.models[role] = NewSwappableModel(rc.Provider, rc.Model, m, cfg.ModelJSONSchema(rc.Provider, rc.Model))
		slog.Info("Phân bổ model vai trò", "module", "config", "role", role, "provider", rc.Provider, "model", rc.Model)
		if len(rc.Fallbacks) == 0 {
			continue
		}

		targets := make([]modelTarget, 0, len(rc.Fallbacks))
		for _, fallback := range rc.Fallbacks {
			fpc, ok := cfg.Providers[fallback.Provider]
			if !ok {
				return nil, fmt.Errorf("fallback của role %s trỏ tới provider %q không xác định: %w", role, fallback.Provider, errs.ErrConfig)
			}
			fm, err := createModelFromConfig(fallback.Provider, fallback.Model, fpc, cache)
			if err != nil {
				return nil, fmt.Errorf("fallback %s/%s của role %s: %w", fallback.Provider, fallback.Model, role, err)
			}
			targets = append(targets, modelTarget{
				provider:   fallback.Provider,
				name:       fallback.Model,
				model:      fm,
				jsonSchema: cfg.ModelJSONSchema(fallback.Provider, fallback.Model),
			})
		}
		ms.fallbacks[role] = targets
	}

	return ms, nil
}

// createModelFromConfig tạo mới hoặc dùng lại instance ChatModel.
func createModelFromConfig(providerKey, model string, pc ProviderConfig, cache map[string]agentcore.ChatModel) (agentcore.ChatModel, error) {
	cacheKey := providerKey + "|" + model
	if m, ok := cache[cacheKey]; ok {
		return m, nil
	}

	// chatgpt-web dùng provider riêng nói thẳng với bridge (body native có
	// client_metadata + turn_id) vì litellm không forward được field này.
	if strings.EqualFold(strings.TrimSpace(providerKey), "chatgpt-web") {
		streamIdle, err := pc.StreamIdleTimeoutValue()
		if err != nil {
			return nil, fmt.Errorf("provider %s stream_idle_timeout: %w: %w", providerKey, errs.ErrConfig, err)
		}
		if streamIdle <= 0 {
			streamIdle = 60 * time.Minute
		}
		baseURL := strings.TrimSpace(pc.BaseURL)
		if baseURL == "" {
			baseURL = "http://127.0.0.1:17841/v1"
		}
		modelName := strings.TrimSpace(model)
		if i := strings.LastIndex(modelName, "/"); i >= 0 {
			modelName = modelName[i+1:]
		}
		if !strings.HasPrefix(strings.ToLower(modelName), "chatgpt-web/") {
			modelName = "chatgpt-web/" + modelName
		}
		m := bridge.New(baseURL, modelName, streamIdle)
		cache[cacheKey] = m
		return m, nil
	}
	providerType, err := pc.ProviderType(providerKey)
	if err != nil {
		return nil, fmt.Errorf("giải loại provider thất bại: %w", err)
	}
	// chatgpt-web là bridge local, không cần key thật — litellm vẫn bắt key
	// non-empty ở tầng client nên điền dummy. Bridge bỏ qua Bearer này.
	apiKey := pc.APIKey
	if strings.TrimSpace(apiKey) == "" && strings.EqualFold(strings.TrimSpace(providerKey), "chatgpt-web") {
		apiKey = "local"
	}
	providerExtra := cloneMap(pc.Extra)
	if pc.API != "" {
		if providerExtra == nil {
			providerExtra = make(map[string]any, 1)
		}
		providerExtra["api"] = pc.API
	}

	streamIdle, err := pc.StreamIdleTimeoutValue()
	if err != nil {
		return nil, fmt.Errorf("provider %s stream_idle_timeout: %w: %w", providerKey, errs.ErrConfig, err)
	}

	m, err := llm.NewModel(providerType, model,
		llm.WithAPIKey(apiKey),
		llm.WithBaseURL(pc.BaseURL),
		llm.WithStreamIdleTimeout(streamIdle),
		llm.WithProviderExtra(providerExtra),
		llm.WithExtra(pc.ExtraBody),
		// Some upstreams allow tool_use IDs containing '#', ':', ... while the
		// pre-flight check only accepts [A-Za-z0-9_-]; normalising means the
		// matching tool_result is rewritten through the same mapping.
		llm.WithClientOptions(litellm.WithMessageRepair(litellm.RepairNormalizeToolUseIDs)),
	)
	if err != nil {
		return nil, fmt.Errorf("provider %s (%s): %w: %w", providerKey, providerType, errs.ErrProvider, err)
	}
	cache[cacheKey] = m
	return m, nil
}

type failoverModel struct {
	role    string
	primary *SwappableModel
	set     *ModelSet
	report  FailoverReporter
	// lastMu/lastTarget ghi lại target của attempt THÀNH CÔNG gần nhất (primary
	// hay fallback). Để caller (audit /sp) biết request thực tế chạy model nào
	// khi Usage không có identity — Info() chỉ trả primary nên không dùng được.
	lastMu     sync.Mutex
	lastTarget modelTarget
}

// LastTarget trả provider/model của attempt thành công gần nhất.
// Rỗng khi chưa có attempt nào thành công.

func (m *failoverModel) Generate(ctx context.Context, messages []agentcore.Message, tools []agentcore.ToolSpec, opts ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	current := m.currentTarget()
	resp, err := current.model.Generate(ctx, messages, tools, opts...)
	if err == nil {
		m.setLastTarget(current)
		return resp, nil
	}

	// Cancel đi trước fallback: lỗi provider có thể bọc context.Canceled dưới
	// dạng string ("provider client: context canceled") mà errors.Is không bắt
	// được. Thử fallback lúc này là đốt thêm một request cho lượt đã chết.
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	next, reason, ok := m.pickFallback(current, err, requestsJSONSchema(opts))
	if !ok {
		return nil, err
	}
	m.reportFailover(current, next, reason, err)
	resp, err = next.model.Generate(ctx, messages, tools, opts...)
	if err == nil {
		m.setLastTarget(next)
	}
	return resp, err
}

// setLastTarget ghi target thành công. Mutex riêng vì Generate có thể chạy
// concurrent qua cùng failoverModel (single-flight ở tầng Host là policy, không
// phải đảm bảo của tầng này).
func (m *failoverModel) setLastTarget(t modelTarget) {
	m.lastMu.Lock()
	defer m.lastMu.Unlock()
	m.lastTarget = t
}

// LastTarget trả provider/model của attempt thành công gần nhất.
// Rỗng khi chưa có attempt nào thành công — caller giữ identity cũ, không đoán.
func (m *failoverModel) LastTarget() (provider, name string) {
	m.lastMu.Lock()
	defer m.lastMu.Unlock()
	return m.lastTarget.provider, m.lastTarget.name
}

func (m *failoverModel) GenerateStream(ctx context.Context, messages []agentcore.Message, tools []agentcore.ToolSpec, opts ...agentcore.CallOption) (<-chan agentcore.StreamEvent, error) {
	out := make(chan agentcore.StreamEvent, 100)

	go func() {
		defer close(out)

		current := m.currentTarget()
		fallbackUsed := false

	retry:
		source, resp, err := m.startAttempt(ctx, current, messages, tools, opts...)
		if err != nil {
			if !fallbackUsed {
				// Như Generate: cancel đi trước fallback. Lỗi bọc string không
				// qua được errors.Is nên phải check ctx trực tiếp, nếu không
				// stream đã chết vẫn thử fallback oan.
				if ctx.Err() != nil {
					out <- agentcore.StreamEvent{Type: agentcore.StreamEventError, Err: ctx.Err()}
					return
				}
				if next, reason, ok := m.pickFallback(current, err, requestsJSONSchema(opts)); ok {
					fallbackUsed = true
					m.reportFailover(current, next, reason, err)
					current = next
					goto retry
				}
			}
			out <- agentcore.StreamEvent{Type: agentcore.StreamEventError, Err: err}
			return
		}
		if resp != nil {
			out <- agentcore.StreamEvent{
				Type:       agentcore.StreamEventDone,
				Message:    resp.Message,
				StopReason: resp.Message.StopReason,
			}
			return
		}

		forwarded := false
		for ev := range source {
			switch ev.Type {
			case agentcore.StreamEventError:
				if ev.Err != nil && !forwarded && !fallbackUsed {
					if next, reason, ok := m.pickFallback(current, ev.Err, requestsJSONSchema(opts)); ok {
						fallbackUsed = true
						m.reportFailover(current, next, reason, ev.Err)
						current = next
						goto retry
					}
				}
				out <- ev
				return
			case agentcore.StreamEventDone:
				out <- ev
				return
			default:
				forwarded = true
				out <- ev
			}
		}
	}()

	return out, nil
}

func (m *failoverModel) SupportsTools() bool {
	return m.primary != nil && m.primary.SupportsTools()
}

func (m *failoverModel) ProviderName() string {
	if m.primary == nil {
		return ""
	}
	return m.primary.ProviderName()
}

func (m *failoverModel) Info() llm.ModelInfo {
	if m.primary == nil {
		return llm.ModelInfo{}
	}
	return m.primary.Info()
}

func (m *failoverModel) Capabilities() llm.Capabilities {
	return m.StructuredOutputFacts().Capabilities
}

func (m *failoverModel) JSONSchemaOverride() *bool {
	return m.StructuredOutputFacts().JSONSchemaOverride
}

func (m *failoverModel) StructuredOutputFacts() llmcontract.ModelFacts {
	if m.primary == nil {
		return llmcontract.ModelFacts{}
	}
	return m.primary.StructuredOutputFacts()
}

func (m *failoverModel) currentTarget() modelTarget {
	if m.primary == nil {
		return modelTarget{}
	}
	provider, name := m.primary.Current()
	return modelTarget{
		provider:   provider,
		name:       name,
		model:      m.primary,
		jsonSchema: m.primary.JSONSchemaOverride(),
	}
}

func (m *failoverModel) pickFallback(current modelTarget, err error, requireJSONSchema bool) (modelTarget, string, bool) {
	if err == nil || current.model == nil {
		return modelTarget{}, "", false
	}
	if errors.Is(err, context.Canceled) {
		return modelTarget{}, "", false
	}

	if !agentcore.IsFailoverEligible(err) {
		return modelTarget{}, agentcore.FailoverReason(err), false
	}
	reason := agentcore.FailoverReason(err)
	var targets []modelTarget
	if m.set != nil {
		targets = m.set.fallbackTargets(m.role)
	}
	for _, target := range targets {
		if target.provider == current.provider && target.name == current.name {
			continue
		}
		if target.model == nil {
			continue
		}
		if requireJSONSchema && !supportsJSONSchema(target) {
			continue
		}
		return target, reason, true
	}
	return modelTarget{}, reason, false
}

func requestsJSONSchema(opts []agentcore.CallOption) bool {
	format := agentcore.ResolveCallConfig(opts).ResponseFormat
	return format != nil && format.Type == agentcore.ResponseFormatJSONSchema
}

func supportsJSONSchema(target modelTarget) bool {
	if target.jsonSchema != nil {
		return *target.jsonSchema
	}
	cp, ok := target.model.(llm.CapabilityProvider)
	return ok && cp.Capabilities().Structured.JSONSchema == llm.SupportYes
}

func (m *failoverModel) reportFailover(from, to modelTarget, reason string, err error) {
	if m.report != nil {
		m.report(FailoverEvent{
			Role:         m.role,
			Reason:       reason,
			FromProvider: from.provider,
			FromModel:    from.name,
			ToProvider:   to.provider,
			ToModel:      to.name,
			Err:          err,
		})
	}
}

func (m *failoverModel) startAttempt(ctx context.Context, target modelTarget, messages []agentcore.Message, tools []agentcore.ToolSpec, opts ...agentcore.CallOption) (<-chan agentcore.StreamEvent, *agentcore.LLMResponse, error) {
	if target.model == nil {
		return nil, nil, fmt.Errorf("chưa cấu hình model")
	}

	streamCh, err := target.model.GenerateStream(ctx, messages, tools, opts...)
	if err == nil {
		return streamCh, nil, nil
	}

	resp, genErr := target.model.Generate(ctx, messages, tools, opts...)
	if genErr != nil {
		return nil, nil, genErr
	}
	return nil, resp, nil
}
