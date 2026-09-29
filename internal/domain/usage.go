package domain

import "time"

// UsageSchemaVersion is the compatibility version number of meta/usage.json.
// If the semantics of the AgentUsageTotals fields ever change, increment this value; UsageStore.Load should ignore a different version and trigger a replay rebuild.
const UsageSchemaVersion = 2

// UsageState is the persistable snapshot of the accumulated token / cost usage.
// It is maintained in memory by UsageTracker and periodically debounced to meta/usage.json.
//
// Note: the sliding-window samples inside UsageTracker ("hit rate over the last N calls") are **not** persisted -
// they only serve short-term UI diagnosis, and after a process restart the semantics come back after a few rounds of re-accumulation from empty.
// MissingAssistantUsage is persisted, because accumulating it across restarts is more valuable for diagnosis.
type UsageState struct {
	Schema       int                         `json:"schema"`
	UpdatedAt    time.Time                   `json:"updated_at"`
	Overall      AgentUsageTotals            `json:"overall"`
	PerAgent     map[string]AgentUsageTotals `json:"per_agent"`
	PerModel     map[string]AgentUsageTotals `json:"per_model,omitempty"`
	MissingUsage int                         `json:"missing_assistant_usage"`
}

// AgentUsageTotals is the persistable form of the accumulated counters of a single role (or overall).
type AgentUsageTotals struct {
	Input        int     `json:"input"`
	Output       int     `json:"output"`
	CacheRead    int     `json:"cache_read"`
	CacheWrite   int     `json:"cache_write"`
	Cost         float64 `json:"cost_usd"`
	Saved        float64 `json:"saved_usd"`
	CacheCapable bool    `json:"cache_capable"`
	// CacheBreaks is the number of cache-chain breaks detected live (the prefix did not shorten while the hit rate dropped sharply).
	// It is only accumulated on the live path; a session replay does not replay the detection.
	CacheBreaks int `json:"cache_breaks,omitempty"`
}
