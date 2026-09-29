package userrules

import (
	"context"
	"log/slog"
	"strings"

	"github.com/CTKiet2006/kietnovel/internal/rules"
	"github.com/CTKiet2006/kietnovel/internal/store"
	"github.com/voocel/agentcore"
)

// Service orchestrates generating and updating the user-rule snapshot: normalize each source → deterministic merge → persist to disk.
//
// Two callers share the same logic:
//   - Book creation/refresh: Build / GetOrBuild, called deterministically by the Host.
//   - Mid-run update: after the Arbiter extracts rules, the Host calls AddRuntimeRule.
type Service struct {
	store     *store.Store
	norm      *Normalizer
	rulesOpts rules.LoadOptions
}

// NewService builds the service. model is used for normalization (it should be a model with stronger capabilities); when model is nil
// every source degrades to raw preferences (a snapshot can still be produced, with system_defaults as the fallback for mechanical checks).
func NewService(st *store.Store, model agentcore.ChatModel, opts rules.LoadOptions) *Service {
	return &Service{store: st, norm: NewNormalizer(model), rulesOpts: opts}
}

// normalizeOrDegrade normalizes one source; on failure it records the real error and degrades to raw preferences
// (snapshot Status=degraded, raw text preserved) — the degradation is a visible fact, the error reason goes to the log.
func (s *Service) normalizeOrDegrade(ctx context.Context, source, text string) rules.Candidate {
	cand, err := s.norm.Normalize(ctx, source, text)
	if err != nil {
		slog.Warn("规则归一化失败，降级为原文偏好", "module", "rules", "source", source, "err", err)
		return degraded(source, text)
	}
	return cand
}

// Build normalizes the static sources (system_defaults + rules files + startup prompt), builds a snapshot and persists it.
// Called on book creation/refresh. startupPrompt may be empty.
func (s *Service) Build(ctx context.Context, startupPrompt string) (*rules.Snapshot, error) {
	cands := []rules.Candidate{rules.SystemDefaults()}
	for _, rs := range rules.RawFileSources(s.rulesOpts) {
		cands = append(cands, s.normalizeOrDegrade(ctx, rs.Label, rs.Text))
	}
	if strings.TrimSpace(startupPrompt) != "" {
		cands = append(cands, s.normalizeOrDegrade(ctx, "startup_prompt", startupPrompt))
	}
	snap := rules.BuildSnapshot(cands)
	if err := s.store.UserRules.Save(&snap); err != nil {
		return nil, err
	}
	return &snap, nil
}

// GetOrBuild returns the current snapshot; when it is missing it initializes from system_defaults + the rules files.
// The runtime read path always goes through here.
func (s *Service) GetOrBuild(ctx context.Context) (*rules.Snapshot, error) {
	cur, err := s.store.UserRules.Load()
	if err != nil {
		return nil, err
	}
	if cur != nil {
		return cur, nil
	}
	return s.Build(ctx, "")
}

// AddRuntimeRule normalizes one long-term runtime rule, overlays it onto the current snapshot with the highest priority and persists it.
// It never returns an error because normalization failed — on failure that entry degrades to raw preferences.
// It returns the overlaid snapshot and the normalization candidate from this run.
func (s *Service) AddRuntimeRule(ctx context.Context, text string) (*rules.Snapshot, rules.Candidate, error) {
	cur, err := s.GetOrBuild(ctx)
	if err != nil {
		return nil, rules.Candidate{}, err
	}
	cand := s.normalizeOrDegrade(ctx, "runtime_update", text)
	merged := rules.OverlaySnapshot(*cur, cand)
	if err := s.store.UserRules.Save(&merged); err != nil {
		return nil, cand, err
	}
	return &merged, cand, nil
}
