// Package store provides filesystem-based persistent storage.
//
// Architecture: 1 IO base + several sub-stores + 1 composition root.
// Each sub-store holds its own IO instance and its own sync.RWMutex.
// Reads and writes of the main domains (Progress, Outline, Drafts, Summaries, ...) never block one another;
// WorldStore merges several low-traffic small domains so that they share a single lock.
//
// The composition root Store holds references to all sub-stores and serializes cross-domain operations
// (ExpandArc, AppendVolume, ClearHandledSteer); the multiple files do not form a transactional atomic commit,
// so callers recover through a safe write order, explicit errors and idempotent replay with the same arguments.
//
// Sub-store breakdown:
//   - ProgressStore: main progress state (meta/progress.json)
//   - OutlineStore: premise, outline (flat/layered), compass
//   - DraftStore: chapter plans, drafts, final drafts
//   - SummaryStore: chapter/arc/volume summaries
//   - RunMetaStore: run metadata (model, intervention history)
//   - SignalStore: one-shot signal files (PendingCommit recovery)
//   - CheckpointStore: step-level checkpoints (meta/checkpoints.jsonl)
//   - RuntimeStore: runtime event queue (meta/runtime/*.jsonl)
//   - CharacterStore: character profiles, state snapshots
//   - WorldStore: timeline, setups, relations, state changes, world rules, style rules, review
package store
