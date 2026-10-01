package host

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/CTKiet2006/kietnovel/internal/bootstrap"
	"github.com/CTKiet2006/kietnovel/internal/domain"
	"github.com/CTKiet2006/kietnovel/internal/models"
	storepkg "github.com/CTKiet2006/kietnovel/internal/store"
	"github.com/voocel/agentcore"
)

// recentSampleCap is the sliding window size: only the (cacheRead, input) pairs of the last N calls
// per role are kept, so the left column can compare the "cumulative" hit rate against the "last N calls" one and tell an early-drag problem from a steady low hit rate.
const recentSampleCap = 10

// Two thresholds decide a cache-chain break (aligned with Claude Code's empirical experience): the hit
// count must have dropped by more than 5% (relative) AND by at least 2000 tokens (absolute) - a single
// relative threshold gets drowned out by small-prefix noise, while a single absolute one misses significant degradation on a large prefix.
const (
	cacheBreakKeepRatio     = 0.95
	cacheBreakMinDropTokens = 2000
)

// UsageTracker accumulates the LLM input/output tokens and dollar cost of every agent for the whole session.
//
// How it works:
//   - Record(agentName, msg) is called on every agent OnMessage callback
//   - agentName maps to a role (architect_* is normalized to architect), then the model currently bound to that role is looked up in ModelSet
//   - models.DefaultRegistry supplies the model price, multiplied across four items: non-cached input / output / cache read / cache write
//   - when the model is not in the registry, fall back to msg.Usage.Cost.Total (whatever the provider reported, possibly 0)
//   - after a hot model switch (/model) later messages are priced with the new model while old messages keep their old cost
//
// It also maintains a per-role dimension (writer/editor/architect):
//   - cumulative hit data -> the overall optimization effect
//   - the sliding window of the last N calls -> tells early drag apart from a steady low hit rate
//   - the CacheCapable flag -> tells "not enabled" apart from "really a 0% hit rate"
//
// Thread-safe.
type UsageTracker struct {
	mu       sync.Mutex
	overall  agentTotals
	perAgent map[string]*agentTotals // key is the role name after agentRoleName normalisation
	perModel map[string]*agentTotals // key is provider/model; when the provider is unknown it degrades to model
	modelSet *bootstrap.ModelSet
	store    *storepkg.Store // may be nil (in tests); when nil every persistence method silently no-ops

	// cacheTrack is the per-role cache-chain baseline (the previous call's prefix length / hit count / time),
	// used for break detection. It is updated only on the live Record path - replaying history does not detect,
	// otherwise every startup would re-report ancient breaks as false positives. Not persisted.
	cacheTrack map[string]*cacheTrackState

	// missingAssistantUsage counts how often "an assistant message was received but Usage is nil".
	// In practice this mainly happens when a self-hosted OpenAI-compatible backend fails to send that final
	// usage chunk at the end of streaming per the OpenAI stream_options.include_usage protocol - partial.Usage
	// stays nil and every cumulative field stays at 0. The counter lets the UI tell the user directly "the
	// upstream is not returning usage, nothing here is broken" instead of digging through cache panel code.
	missingAssistantUsage int
	loggedMissingUsage    bool // warn once per session so tui.log is not flooded

	// saveCh is triggered non-blockingly by Record after accumulating; autoSaveLoop listens and writes to disk with a debounce.
	// buffered=1: several consecutive Record calls collapse into a single save signal; when the buffer is full the signal is dropped and written together on the next tick.
	saveCh       chan struct{}
	autoSaveMu   sync.Mutex
	autoSaveDone chan struct{}

	// onCost is called outside the lock after every accounting pass, carrying the latest cumulative cost (BudgetSentinel cap detection).
	// It must be set through SetOnCost before concurrent Record calls start; afterwards it is read-only.
	onCost func(total float64)

	// onMissingUsage is called once when "an assistant message carries no Usage" is first found (at the same
	// moment as the slog warn). With the budget enabled that means a billing blind spot - cost is always 0 and the budget never triggers, so someone has to be told.
	onMissingUsage func()
}

// usageSample is one hit sample from a single OnMessage, recording only the numerator and denominator of the hit rate.
type usageSample struct {
	CacheRead int
	Input     int
}

// cacheTrackState is a role's cache-chain baseline for the current session. The task (the spawn task text) is
// the session identity: changing task = a new spawn = a new cache lineage (prompt_cache_key carries #seq), and a
// low hit rate on the first request is normal, so switch the baseline and do not compare - otherwise
// "the previous session was very short while the new session's first request has a longer prefix" would
// report a false break. The Input semantics (including CacheRead, see the computeCost comment) is exactly
// "the prefix length the server processed", which distinguishes three trends: a shorter prefix = compression within the session (legitimate, reset the baseline); a growing prefix with growing hits = a healthy chain; a growing prefix with a collapsing hit count = a break.
type cacheTrackState struct {
	task          string
	lastPrefix    int
	lastCacheRead int
	lastAt        time.Time
}

// agentTotals is one agent's cumulative counters.
//   - Saved is back-computed from the current hit data as "what it would have cost at the non-cached price"
//   - CacheCapable is set to true only after that role has made at least one call through a model known to support cache
//   - samples is a fixed-length ring buffer: the first recentSampleCap entries are appended directly, after that it rotates by sampleIdx
type agentTotals struct {
	Input        int
	Output       int
	CacheRead    int
	CacheWrite   int
	Cost         float64
	Saved        float64
	CacheCapable bool
	CacheBreaks  int // number of cache-chain breaks detected live (replay is not counted)
	samples      []usageSample
	sampleIdx    int
}

func NewUsageTracker(set *bootstrap.ModelSet, store *storepkg.Store) *UsageTracker {
	return &UsageTracker{
		modelSet:   set,
		store:      store,
		perAgent:   make(map[string]*agentTotals, 4),
		perModel:   make(map[string]*agentTotals, 4),
		cacheTrack: make(map[string]*cacheTrackState, 4),
		saveCh:     make(chan struct{}, 1),
	}
}

// RecordSidecar ghi accounting cho tác vụ phụ (sidecar) như /sp hỏi.
//
// Khác Record ở đúng một điểm: KHÔNG cộng vào overall và KHÔNG gọi onCost.
// overall là số BudgetSentinel dùng để quyết định abort Engine; một câu hỏi phụ
// mà đẩy tổng vượt trần rồi dừng cả máy đang viết là sai nguyên tắc "sidecar
// không can thiệp Engine".
//
// Nhưng vẫn cộng vào perAgent["advisor"] + perModel và vẫn notifyDirty để persist:
// tiền thật đã đốt thì phải còn dấu vết, chỉ là không được dùng để giết Engine.
//
// task là lineage của cache detector: mỗi /sp request là một prompt lineage mới
// (snapshot/question khác nhau, model có thể đổi), nên mỗi request phải có task
// riêng. Dùng chung một task cho mọi request thì detector so B với baseline của
// A và báo "đứt cache" oan cho một lineage hoàn toàn mới.
func (t *UsageTracker) RecordSidecar(agentName, task string, u agentcore.Usage, provider, modelName string) {
	if t == nil {
		return
	}
	role := agentRoleName(agentName)
	t.noteCacheBreakSidecar(role, task, u)
	provider, modelName = t.effectiveModel(role, provider, modelName)
	cost, saved, capable := t.resolveCost(modelName, u)

	t.mu.Lock()
	per := t.perAgent[role]
	if per == nil {
		per = &agentTotals{}
		t.perAgent[role] = per
	}
	addUsage(per, u, cost, saved, capable)

	if key := modelUsageKey(provider, modelName); key != "" {
		perModel := t.perModel[key]
		if perModel == nil {
			perModel = &agentTotals{}
			t.perModel[key] = perModel
		}
		addUsage(perModel, u, cost, saved, capable)
	}
	t.mu.Unlock()

	t.notifyDirty()
}

// Record dispatches one agent message onto two paths: accumulation and diagnostics.
//
// Accumulation only looks at whether Usage exists - "which messages carry Usage" is an agentcore/litellm adapter
// assembly detail (the upstream protocol puts usage at the top level of the response), so if the assembly rules
// ever change, nothing here has to change. Diagnostics require Role=Assistant and non-empty Content so that
// AbortMsg / abnormal recovery / tool / user messages do not pollute the missingAssistantUsage counter.
func (t *UsageTracker) Record(agentName, task string, msg agentcore.AgentMessage) {
	if t == nil {
		return
	}
	m, ok := msg.(agentcore.Message)
	if !ok {
		return
	}
	if m.Usage == nil {
		if m.Role == agentcore.RoleAssistant && len(m.Content) > 0 {
			t.flagMissingUsage(agentName)
		}
		return
	}
	role := agentRoleName(agentName)
	t.noteCacheBreak(role, task, *m.Usage)
	provider, modelName := usageActualModel(m.Usage)
	t.accumulate(role, provider, modelName, *m.Usage)
}

// noteCacheBreak is the cache-chain break detection (pure observation, no repair, called only on the live Record path).
//
// Decision: within the same session (role+task) the prefix (Input, including CacheRead) did not shrink while the
// hit count dropped by >5% and by at least 2000 tokens versus the previous call. A task change = a new spawn =
// a new cache lineage, so switch the baseline without comparing; a shrinking prefix means context compression,
// a legitimate drop that only resets the baseline without warning. The attribution hint is given by priority:
// an interval over TTL -> suspected expiry; a very short interval while the client bytes should be stable -> suspected server-side eviction or routing drift (a relay round-robining over upstreams is a common cause).
func (t *UsageTracker) noteCacheBreak(role, task string, u agentcore.Usage) {
	t.noteCacheBreakInner(role, task, u, false)
}

// noteCacheBreakSidecar là đường phát hiện đứt cache cho sidecar (/sp hỏi).
// Chỉ cập nhật perAgent của advisor, TUYỆT ĐỐI không chạm overall — overall là
// số liệu của Engine, sidecar làm tăng OverallCacheBreaks sẽ khiến chẩn đoán
// cache của Engine bị nhiễm.
func (t *UsageTracker) noteCacheBreakSidecar(role, task string, u agentcore.Usage) {
	t.noteCacheBreakInner(role, task, u, true)
}

func (t *UsageTracker) noteCacheBreakInner(role, task string, u agentcore.Usage, sidecar bool) {
	now := time.Now()
	prefix := u.Input // every litellm provider guarantees that Input includes CacheRead

	t.mu.Lock()
	st := t.cacheTrack[role]
	if st == nil || st.task != task {
		t.cacheTrack[role] = &cacheTrackState{task: task, lastPrefix: prefix, lastCacheRead: u.CacheRead, lastAt: now}
		t.mu.Unlock()
		return
	}
	prevPrefix, prevRead, prevAt := st.lastPrefix, st.lastCacheRead, st.lastAt
	st.lastPrefix, st.lastCacheRead, st.lastAt = prefix, u.CacheRead, now

	broke := prevPrefix > 0 && prefix >= prevPrefix &&
		float64(u.CacheRead) < float64(prevRead)*cacheBreakKeepRatio &&
		prevRead-u.CacheRead >= cacheBreakMinDropTokens
	if broke {
		if !sidecar {
			t.overall.CacheBreaks++
		}
		per := t.perAgent[role]
		if per == nil {
			per = &agentTotals{}
			t.perAgent[role] = per
		}
		per.CacheBreaks++
	}
	t.mu.Unlock()

	if !broke {
		return
	}
	gap := now.Sub(prevAt).Round(time.Second)
	hint := "疑似服务端逐出/路由漂移（中转站轮询上游是常见原因）"
	if gap > time.Hour {
		hint = "疑似 1h TTL 过期"
	} else if gap > 5*time.Minute {
		hint = "疑似 5m TTL 过期"
	}
	slog.Warn("缓存链断裂：前缀未缩短而命中骤降",
		"module", "usage", "role", role,
		"cache_read", fmt.Sprintf("%d→%d", prevRead, u.CacheRead),
		"prefix", fmt.Sprintf("%d→%d", prevPrefix, prefix),
		"gap", gap.String(), "hint", hint)
	t.notifyDirty()
}

func usageActualModel(u *agentcore.Usage) (provider, modelName string) {
	if u == nil {
		return "", ""
	}
	return strings.TrimSpace(u.Provider), strings.TrimSpace(u.Model)
}

// flagMissingUsage counts one "looks like a real LLM response but no usage arrived" event, and logs a warning
// only once per session so tui.log does not get flooded.
func (t *UsageTracker) flagMissingUsage(agentName string) {
	t.mu.Lock()
	t.missingAssistantUsage++
	shouldLog := !t.loggedMissingUsage
	t.loggedMissingUsage = true
	t.mu.Unlock()
	if shouldLog {
		slog.Warn("LLM 响应未携带 usage 数据，缓存/成本面板将无累计——通常是上游 streaming 未按 OpenAI include_usage 协议发 final usage chunk",
			"module", "usage", "agent", agentName)
		if t.onMissingUsage != nil {
			t.onMissingUsage()
		}
	}
	t.notifyDirty()
}

// SetOnMissingUsage registers the one-shot callback for the first time usage is found missing.
// It must be called once during Host construction, before concurrent Record calls start.
func (t *UsageTracker) SetOnMissingUsage(cb func()) {
	if t == nil {
		return
	}
	t.onMissingUsage = cb
}

// notifyDirty triggers a save signal non-blockingly; autoSaveLoop does the actual write with a debounce.
// The signal channel is buffered=1: several consecutive Record calls just collapse into one save request.
func (t *UsageTracker) notifyDirty() {
	if t == nil || t.saveCh == nil {
		return
	}
	select {
	case t.saveCh <- struct{}{}:
	default:
	}
}

// accumulate adds a message carrying Usage into three sets of counters: overall / per-role / per-model.
// An empty provider/model means "take the model bound to the role from the current ModelSet" (the live path);
// non-empty means "force the price of the given model" (the replay path uses the _meta in the session jsonl).
// resolveCost runs outside the lock (it only reads modelSet/Registry); inside the lock only additions happen.
func (t *UsageTracker) accumulate(role, provider, modelName string, u agentcore.Usage) {
	provider, modelName = t.effectiveModel(role, provider, modelName)
	cost, saved, capable := t.resolveCost(modelName, u)

	t.mu.Lock()
	addUsage(&t.overall, u, cost, saved, capable)

	per := t.perAgent[role]
	if per == nil {
		per = &agentTotals{}
		t.perAgent[role] = per
	}
	addUsage(per, u, cost, saved, capable)

	if key := modelUsageKey(provider, modelName); key != "" {
		perModel := t.perModel[key]
		if perModel == nil {
			perModel = &agentTotals{}
			t.perModel[key] = perModel
		}
		addUsage(perModel, u, cost, saved, capable)
	}
	total := t.overall.Cost
	t.mu.Unlock()

	t.notifyDirty()
	if t.onCost != nil {
		t.onCost(total)
	}
}

// SetOnCost registers the accounting callback (carrying the latest cumulative cost, called outside the lock).
// It must be called once during Host construction, before concurrent Record calls start.
func (t *UsageTracker) SetOnCost(cb func(total float64)) {
	if t == nil {
		return
	}
	t.onCost = cb
}

func (t *UsageTracker) effectiveModel(role, provider, modelName string) (string, string) {
	provider = strings.TrimSpace(provider)
	modelName = strings.TrimSpace(modelName)
	if modelName == "" {
		if t != nil && t.modelSet != nil {
			p, m, _ := t.modelSet.CurrentSelection(role)
			return p, m
		}
		return "", ""
	}
	if provider == "" && t != nil && t.modelSet != nil {
		p, m, _ := t.modelSet.CurrentSelection(role)
		if m == modelName {
			provider = p
		}
	}
	return provider, modelName
}

func modelUsageKey(provider, modelName string) string {
	provider = strings.TrimSpace(provider)
	modelName = strings.TrimSpace(modelName)
	switch {
	case modelName == "":
		return ""
	case provider == "":
		return modelName
	default:
		return provider + "/" + modelName
	}
}

// addUsage adds one call's tokens and cost to a totals set.
// It must be called while holding UsageTracker.mu.
//
// CacheCapable is decided from "facts" first: as soon as CacheRead or CacheWrite > 0 has been seen, that proves
// the upstream really does prompt caching. The registry's CacheReadCostPer1M is only a fallback, because
// models of self-hosted backends (mimo-v2.5-pro / domestic proxies etc.) are usually absent from the
// BerriAI/litellm pricing index even though the real Usage does carry cache data, and the UI must not misjudge that as "not enabled".
func addUsage(t *agentTotals, u agentcore.Usage, cost, saved float64, capable bool) {
	t.Input += u.Input
	t.Output += u.Output
	t.CacheRead += u.CacheRead
	t.CacheWrite += u.CacheWrite
	t.Cost += cost
	t.Saved += saved
	if capable || u.CacheRead > 0 || u.CacheWrite > 0 {
		t.CacheCapable = true
	}
	pushSample(t, u.CacheRead, u.Input)
}

// pushSample pushes one sample into the ring buffer. The first recentSampleCap entries are pure appends, after that it rotates and overwrites.
func pushSample(t *agentTotals, cacheRead, input int) {
	s := usageSample{CacheRead: cacheRead, Input: input}
	if len(t.samples) < recentSampleCap {
		t.samples = append(t.samples, s)
		return
	}
	t.samples[t.sampleIdx] = s
	t.sampleIdx = (t.sampleIdx + 1) % recentSampleCap
}

// recentSums returns the totals of cacheRead and input inside the sliding window, i.e. the numerator and denominator of the "last N calls hit rate".
// It uses sum/sum rather than "the average of per-call ratios" to keep small samples (input of a few hundred tokens) from amplifying noise.
func recentSums(t *agentTotals) (cacheRead, input int) {
	for _, s := range t.samples {
		cacheRead += s.CacheRead
		input += s.Input
	}
	return cacheRead, input
}

// Totals returns a snapshot of the cumulative totals.
func (t *UsageTracker) Totals() (cost float64, input, output, cacheRead, cacheWrite int) {
	if t == nil {
		return 0, 0, 0, 0, 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.overall.Cost, t.overall.Input, t.overall.Output, t.overall.CacheRead, t.overall.CacheWrite
}

// SavedUSD returns the cumulative dollars saved by cache hits.
func (t *UsageTracker) SavedUSD() float64 {
	if t == nil {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.overall.Saved
}

// OverallRecent returns the cacheRead total, the input total and the sample count inside the sliding window (at most recentSampleCap calls).
func (t *UsageTracker) OverallRecent() (cacheRead, input, samples int) {
	if t == nil {
		return 0, 0, 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	r, in := recentSums(&t.overall)
	return r, in, len(t.overall.samples)
}

// OverallCacheBreaks returns the total number of cache-chain breaks detected live.
func (t *UsageTracker) OverallCacheBreaks() int {
	if t == nil {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.overall.CacheBreaks
}

// OverallCacheCapable reports whether the whole run has been through a model known to support cache at least once.
func (t *UsageTracker) OverallCacheCapable() bool {
	if t == nil {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.overall.CacheCapable
}

// MissingAssistantUsage returns the cumulative count of "an assistant message was received but Usage is nil".
// Greater than 0 usually means the upstream streaming did not send OpenAI's final usage chunk,
// and the UI shows a hint instead of letting the user think the cache module itself is broken.
func (t *UsageTracker) MissingAssistantUsage() int {
	if t == nil {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.missingAssistantUsage
}

// ── Persistence ──

// Snapshot copies the current cumulative state into a serializable domain.UsageState.
// The sliding-window samples do not go into the snapshot - it is a short-term diagnostic window and little of it carries across processes.
func (t *UsageTracker) Snapshot() domain.UsageState {
	if t == nil {
		return domain.UsageState{}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	state := domain.UsageState{
		Schema:       domain.UsageSchemaVersion,
		UpdatedAt:    time.Now(),
		Overall:      totalsSnapshot(&t.overall),
		PerAgent:     make(map[string]domain.AgentUsageTotals, len(t.perAgent)),
		PerModel:     make(map[string]domain.AgentUsageTotals, len(t.perModel)),
		MissingUsage: t.missingAssistantUsage,
	}
	for role, v := range t.perAgent {
		state.PerAgent[role] = totalsSnapshot(v)
	}
	for model, v := range t.perModel {
		state.PerModel[model] = totalsSnapshot(v)
	}
	return state
}

// LoadFromStore reads the persisted snapshot from store.Usage and back-fills it into memory. true means
// a non-empty (schema matching) state was loaded successfully; false means there is no file or it is
// unusable, and the caller should keep going with a one-off back-fill from the session replay.
func (t *UsageTracker) LoadFromStore() (bool, error) {
	if t == nil || t.store == nil {
		return false, nil
	}
	state, err := t.store.Usage.Load()
	if err != nil {
		return false, err
	}
	if state == nil {
		return false, nil
	}
	t.applyState(*state)
	return true, nil
}

// SaveNow writes the current snapshot to disk immediately. The autoSaveLoop / Close paths both write through it.
func (t *UsageTracker) SaveNow() error {
	if t == nil || t.store == nil {
		return nil
	}
	return t.store.Usage.Save(t.Snapshot())
}

// StartAutoSave starts a goroutine that listens on saveCh and writes to disk with a debounce. Before ctx
// is done it flushes the last unsaved state. Close triggers that flush and the exit by cancelling ctx.
func (t *UsageTracker) StartAutoSave(ctx context.Context) {
	if t == nil || t.store == nil {
		return
	}
	done := make(chan struct{})
	t.autoSaveMu.Lock()
	t.autoSaveDone = done
	t.autoSaveMu.Unlock()
	go func() {
		defer close(done)
		t.autoSaveLoop(ctx)
	}()
}

// WaitAutoSave waits for the final flush after cancellation. Host.Close calls cancel first and
// then waits here, so autoSaveLoop does not race the pre-exit SaveNow over the same snapshot.
func (t *UsageTracker) WaitAutoSave() {
	if t == nil {
		return
	}
	t.autoSaveMu.Lock()
	done := t.autoSaveDone
	t.autoSaveMu.Unlock()
	if done != nil {
		<-done
	}
}

// autoSaveLoop throttles high-frequency dirty signals into one write to disk every 500ms.
//
// Design note: 500ms is an empirical value - 1-2 LLM turns per chapter make 1-2 writes perfectly acceptable;
// even if the user quits with ctrl+C before the timer fires, the ctx cancellation path still flushes one last time.
// A real crash (OS kill -9) loses at most the last 0.5s of accumulation - the upstream session jsonl is
// still the complete truth, and the next startup patches the difference from a sessions/ replay.
func (t *UsageTracker) autoSaveLoop(ctx context.Context) {
	const debounce = 500 * time.Millisecond
	timer := time.NewTimer(time.Hour)
	timer.Stop()
	defer timer.Stop()

	var pending bool
	flush := func() {
		if err := t.SaveNow(); err != nil {
			slog.Warn("usage 落盘失败", "module", "usage", "err", err)
		}
		pending = false
	}
	for {
		select {
		case <-ctx.Done():
			if pending {
				flush()
			}
			return
		case <-t.saveCh:
			if pending {
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
			}
			timer.Reset(debounce)
			pending = true
		case <-timer.C:
			flush()
		}
	}
}

// applyState writes the persisted snapshot back into memory. It is called only at startup (after LoadFromStore /
// replay), when autoSaveLoop has not started and Record cannot fire concurrently, so it needs no lock; but
// mu is kept anyway to guard against concurrency introduced by tests or a future change in call order.
func (t *UsageTracker) applyState(state domain.UsageState) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.overall = totalsFromState(state.Overall)
	if state.PerAgent == nil {
		t.perAgent = make(map[string]*agentTotals, 4)
	} else {
		t.perAgent = make(map[string]*agentTotals, len(state.PerAgent))
		for role, v := range state.PerAgent {
			tot := totalsFromState(v)
			t.perAgent[role] = &tot
		}
	}
	if state.PerModel == nil {
		t.perModel = make(map[string]*agentTotals, 4)
	} else {
		t.perModel = make(map[string]*agentTotals, len(state.PerModel))
		for model, v := range state.PerModel {
			tot := totalsFromState(v)
			t.perModel[model] = &tot
		}
	}
	t.missingAssistantUsage = state.MissingUsage
}

// totalsSnapshot copies the in-memory agentTotals into a persistable domain.AgentUsageTotals.
// The samples ring buffer is deliberately left out - see the UsageState comment.
func totalsSnapshot(t *agentTotals) domain.AgentUsageTotals {
	if t == nil {
		return domain.AgentUsageTotals{}
	}
	return domain.AgentUsageTotals{
		Input:        t.Input,
		Output:       t.Output,
		CacheRead:    t.CacheRead,
		CacheWrite:   t.CacheWrite,
		Cost:         t.Cost,
		Saved:        t.Saved,
		CacheCapable: t.CacheCapable,
		CacheBreaks:  t.CacheBreaks,
	}
}

// totalsFromState turns the persisted form back into in-memory agentTotals. samples stays empty, so after a
// restart the accumulation begins again from 0 and a few Record calls are enough to restore the "last N calls hit rate" semantics.
func totalsFromState(s domain.AgentUsageTotals) agentTotals {
	return agentTotals{
		Input:        s.Input,
		Output:       s.Output,
		CacheRead:    s.CacheRead,
		CacheWrite:   s.CacheWrite,
		Cost:         s.Cost,
		Saved:        s.Saved,
		CacheCapable: s.CacheCapable,
		CacheBreaks:  s.CacheBreaks,
	}
}

// AgentUsage is a snapshot of one agent's cumulative usage (exposed to the UI).
type AgentUsage struct {
	Role            string
	Model           string
	Input           int
	Output          int
	CacheRead       int
	CacheWrite      int
	Cost            float64
	Saved           float64
	CacheCapable    bool
	RecentCacheRead int
	RecentInput     int
	RecentSamples   int
}

// PerAgent returns the cumulative usage per role, sorted by CacheRead descending, skipping roles that never consumed tokens.
func (t *UsageTracker) PerAgent() []AgentUsage {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]AgentUsage, 0, len(t.perAgent))
	for role, v := range t.perAgent {
		if v.Input == 0 && v.Output == 0 {
			continue
		}
		recentRead, recentInput := recentSums(v)
		out = append(out, AgentUsage{
			Role:            role,
			Input:           v.Input,
			Output:          v.Output,
			CacheRead:       v.CacheRead,
			CacheWrite:      v.CacheWrite,
			Cost:            v.Cost,
			Saved:           v.Saved,
			CacheCapable:    v.CacheCapable,
			RecentCacheRead: recentRead,
			RecentInput:     recentInput,
			RecentSamples:   len(v.samples),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CacheRead != out[j].CacheRead {
			return out[i].CacheRead > out[j].CacheRead
		}
		return out[i].Input > out[j].Input
	})
	return out
}

// PerModel returns the cumulative usage per model, sorted by cost descending and then by input volume descending.
func (t *UsageTracker) PerModel() []AgentUsage {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]AgentUsage, 0, len(t.perModel))
	for model, v := range t.perModel {
		if v.Input == 0 && v.Output == 0 {
			continue
		}
		out = append(out, AgentUsage{
			Model:        model,
			Input:        v.Input,
			Output:       v.Output,
			CacheRead:    v.CacheRead,
			CacheWrite:   v.CacheWrite,
			Cost:         v.Cost,
			Saved:        v.Saved,
			CacheCapable: v.CacheCapable,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Cost != out[j].Cost {
			return out[i].Cost > out[j].Cost
		}
		return out[i].Input > out[j].Input
	})
	return out
}

// resolveCost returns the cost / saved / capable of this message together.
//   - cost: multiplied across 4 items when the registry hits; otherwise it falls back to the provider-reported cost
//   - saved: greater than 0 only when the registry hits, CacheRead > 0 and InputCost > CacheReadCost
//   - capable: the registry hits and that model's CacheReadCostPer1M > 0 -> known to support prompt caching
//
// modelName prefers the one passed in by the caller (during replay it comes from _meta.model in the session jsonl).
func (t *UsageTracker) resolveCost(modelName string, u agentcore.Usage) (cost, saved float64, capable bool) {
	if entry, ok := models.DefaultRegistry().Resolve(modelName); ok {
		c := computeCost(u, *entry)
		s := computeSaved(u, *entry)
		canCache := entry.CacheReadCostPer1M > 0
		if c > 0 {
			return c, s, canCache
		}
	}
	if u.Cost != nil {
		return u.Cost.Total, 0, false
	}
	return 0, 0, false
}

// agentRoleName normalizes a subagent name to a role name.
// architect_short/mid/long all map to architect; everything else is returned as is.
func agentRoleName(agentName string) string {
	if strings.HasPrefix(agentName, "architect_") {
		return "architect"
	}
	return agentName
}

// computeCost computes the dollar cost of this call from $/1M tokens unit prices.
//
// Semantic premise (uniformly guaranteed by every litellm provider, see the Usage assembly points in
// anthropic.go / bedrock.go / openai.go / gemini.go / compat.go):
//
//	u.Input  = all input tokens, **including** CacheRead; excluding CacheWrite
//	u.Output = output tokens
//
// Therefore nonCachedInput = u.Input - u.CacheRead holds for every provider.
// The fallback branch is kept so a future provider wrongly returning dirty data cannot crash this.
func computeCost(u agentcore.Usage, e models.ModelEntry) float64 {
	nonCachedInput := u.Input - u.CacheRead
	if nonCachedInput < 0 {
		nonCachedInput = u.Input
	}
	c := 0.0
	c += float64(nonCachedInput) * e.InputCostPer1M / 1_000_000
	c += float64(u.Output) * e.OutputCostPer1M / 1_000_000
	c += float64(u.CacheRead) * e.CacheReadCostPer1M / 1_000_000
	c += float64(u.CacheWrite) * e.CacheWriteCostPer1M / 1_000_000
	return c
}

// computeSaved estimates the dollars saved by CacheRead hits relative to billing at the normal input price.
// Note that the CacheWrite premium is not deducted - it is a necessary investment that paves the way for
// later hits, and the real benefit is recovered through later accumulated CacheRead.
func computeSaved(u agentcore.Usage, e models.ModelEntry) float64 {
	if u.CacheRead <= 0 || e.InputCostPer1M <= 0 {
		return 0
	}
	delta := e.InputCostPer1M - e.CacheReadCostPer1M
	if delta <= 0 {
		return 0
	}
	return float64(u.CacheRead) * delta / 1_000_000
}
