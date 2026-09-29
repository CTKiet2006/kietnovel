// Package models provides a registry of LLM model metadata (context window, output cap, price),
// sourced from the OpenRouter API, with a compile-time baseline plus runtime refresh.
package models

//go:generate go run gen_models.go

import (
	"strings"
	"sync"
)

// ModelEntry describes a known LLM model.
type ModelEntry struct {
	Provider            string  `json:"provider"`               // vendor name after OpenRouter normalization (anthropic/openai/gemini/...)
	ID                  string  `json:"id"`                     // model ID (without the vendor prefix)
	Name                string  `json:"name"`                   // display name
	ContextWindow       int     `json:"context_window"`         // input window
	MaxTokens           int     `json:"max_tokens"`             // per-call output cap
	InputCostPer1M      float64 `json:"input_cost_per_1m"`      // input price (USD/1M tokens)
	OutputCostPer1M     float64 `json:"output_cost_per_1m"`     // output price
	CacheReadCostPer1M  float64 `json:"cache_read_cost_per_1m"` // cache-read price
	CacheWriteCostPer1M float64 `json:"cache_write_cost_per_1m"`
}

// ModelRegistry holds the known models and supports fuzzy resolution plus runtime merging.
type ModelRegistry struct {
	mu     sync.RWMutex
	models []ModelEntry
}

// NewModelRegistry returns a registry preloaded with the compile-time baseline.
func NewModelRegistry() *ModelRegistry {
	r := &ModelRegistry{}
	r.models = append(r.models, generatedModels...)
	return r
}

var (
	defaultRegistry     *ModelRegistry
	defaultRegistryOnce sync.Once
)

// DefaultRegistry returns the global registry (lazily loaded, thread-safe).
// Calling StartPricingRefresh during startup lets the background refresh price/window information.
func DefaultRegistry() *ModelRegistry {
	defaultRegistryOnce.Do(func() {
		defaultRegistry = NewModelRegistry()
	})
	return defaultRegistry
}

// Resolve looks up an entry by a model identifier (which may be "provider/model", a full ID, or a partial name).
//
// Match order:
//  1. if it contains "/", look up "provider/model" exactly
//  2. exact / date-suffix match
//  3. substring match (ID or Name contains pattern)
//
// When several entries match, prefer the alias without a date suffix (e.g. claude-sonnet-4 over claude-sonnet-4-20250514).
func (r *ModelRegistry) Resolve(pattern string) (*ModelEntry, bool) {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return nil, false
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	if idx := strings.Index(pattern, "/"); idx > 0 {
		prov := pattern[:idx]
		modelID := pattern[idx+1:]
		if entry, ok := lookupModelEntry(r.models, prov, modelID); ok {
			return &entry, true
		}
		// An OpenRouter vendor prefix (google/, x-ai/) does not necessarily equal the local Provider name,
		// so fall back to a modelID-only lookup, which still lets "google/gemini-2.5-pro" hit the gemini entry.
		if entry, ok := lookupModelEntry(r.models, "", modelID); ok {
			return &entry, true
		}
	}

	if entry, ok := lookupModelEntry(r.models, "", pattern); ok {
		return &entry, true
	}

	lower := strings.ToLower(pattern)
	normalized := normalizeModelLookupID(pattern)
	var candidates []int
	for i := range r.models {
		if strings.Contains(normalizeModelLookupID(r.models[i].ID), normalized) ||
			strings.Contains(strings.ToLower(r.models[i].ID), lower) ||
			strings.Contains(strings.ToLower(r.models[i].Name), lower) {
			candidates = append(candidates, i)
		}
	}
	if len(candidates) == 0 {
		return nil, false
	}

	best := candidates[0]
	for _, i := range candidates[1:] {
		if !hasDatedSuffix(r.models[i].ID) && hasDatedSuffix(r.models[best].ID) {
			best = i
		}
	}
	entry := r.models[best]
	return &entry, true
}

// ResolveContextWindow returns a model's context window, or 0 when there is no match.
func (r *ModelRegistry) ResolveContextWindow(pattern string) int {
	if e, ok := r.Resolve(pattern); ok {
		return e.ContextWindow
	}
	return 0
}

// MergeModels merges by provider+id, case-insensitively.
// A non-zero price/window/MaxTokens/Name overrides the existing entry; a new entry is simply appended.
func (r *ModelRegistry) MergeModels(fetched []ModelEntry) {
	r.mu.Lock()
	defer r.mu.Unlock()

	idx := make(map[string]int, len(r.models))
	for i, m := range r.models {
		idx[strings.ToLower(m.Provider+"/"+m.ID)] = i
	}
	for _, f := range fetched {
		key := strings.ToLower(f.Provider + "/" + f.ID)
		if i, ok := idx[key]; ok {
			if f.InputCostPer1M > 0 || f.OutputCostPer1M > 0 {
				r.models[i].InputCostPer1M = f.InputCostPer1M
				r.models[i].OutputCostPer1M = f.OutputCostPer1M
				r.models[i].CacheReadCostPer1M = f.CacheReadCostPer1M
				r.models[i].CacheWriteCostPer1M = f.CacheWriteCostPer1M
			}
			if f.ContextWindow > 0 {
				r.models[i].ContextWindow = f.ContextWindow
			}
			if f.MaxTokens > 0 {
				r.models[i].MaxTokens = f.MaxTokens
			}
			if f.Name != "" {
				r.models[i].Name = f.Name
			}
		} else {
			r.models = append(r.models, f)
			idx[key] = len(r.models) - 1
		}
	}
}
