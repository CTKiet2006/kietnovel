package store

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"time"
)

// DecisionStore audits the runtime LLM semantic rulings (meta/decisions.jsonl, append-only).
//
// Purpose (docs/engine-arbiter.md §4.3): the data source for auditing and offline replay - it records "what was seen at the time,
// what facts were known and what ruling was made", for eval regression and for future A/B comparison against the Arbiter. It is **not** event sourcing,
// and it is **not** a recovery data source either (recovery depends only on the fact layers such as Progress/Checkpoint/RunMeta).
type DecisionStore struct{ io *IO }

func NewDecisionStore(io *IO) *DecisionStore { return &DecisionStore{io: io} }

const (
	decisionSchemaVersion = 1
	decisionsFile         = "meta/decisions.jsonl"
	// maxDecisionInputBytes is the cap for a single input; anything longer is truncated and flagged, so a huge paste cannot blow up the audit file.
	maxDecisionInputBytes = 8 << 10
)

// DecisionRecord is the audit record of one semantic ruling. facts only holds structured facts and references, never a copy of the body text.
// input stays inside the record (required for offline replay); redaction happens at the diag export boundary, not at write time.
type DecisionRecord struct {
	SchemaVersion  int             `json:"schema_version"`
	ID             string          `json:"id"`
	At             string          `json:"at"`
	Kind           string          `json:"kind"`    // intervention | plan_start | volume_end | ...
	Decider        string          `json:"decider"` // arbiter | architect (end-of-volume review)
	CheckpointSeq  int64           `json:"checkpoint_seq,omitempty"`
	Input          string          `json:"input,omitempty"`
	InputTruncated bool            `json:"input_truncated,omitempty"`
	Facts          json.RawMessage `json:"facts,omitempty"`
	Decision       json.RawMessage `json:"decision,omitempty"`
	Reason         string          `json:"reason,omitempty"`
	Error          string          `json:"error,omitempty"` // error text when the ruling failed - a failure is an audit fact too, and without it troubleshooting is pure guesswork
	Model          string          `json:"model,omitempty"`
	DurationMs     int64           `json:"duration_ms,omitempty"`
}

// Append writes one ruling record; SchemaVersion/At/ID are filled in by this method and an over-long input is truncated.
// It returns the completed record (the ID lets callers correlate it, e.g. PlanStartRecord.DecisionID).
func (s *DecisionStore) Append(rec DecisionRecord) (DecisionRecord, error) {
	rec.SchemaVersion = decisionSchemaVersion
	if rec.At == "" {
		rec.At = time.Now().Format(time.RFC3339)
	}
	if rec.ID == "" {
		rec.ID = newDecisionID()
	}
	if len(rec.Input) > maxDecisionInputBytes {
		rec.Input = rec.Input[:maxDecisionInputBytes]
		rec.InputTruncated = true
	}
	data, err := json.Marshal(rec)
	if err != nil {
		return rec, fmt.Errorf("marshal decision: %w", err)
	}
	s.io.mu.Lock()
	defer s.io.mu.Unlock()
	// The previous append may have crashed before the newline was written. First drop the tail that the protocol proves was never committed, so a new JSON
	// fragment is not appended straight onto the leftover; complete newline-terminated records are never modified automatically.
	if _, err := s.committedDataUnlocked(); err != nil {
		return rec, fmt.Errorf("repair decision tail: %w", err)
	}
	if err := s.io.AppendLineUnlocked(decisionsFile, append(data, '\n')); err != nil {
		return rec, err
	}
	return rec, nil
}

// Recent returns the most recent n records (old -> new); a missing file yields nothing.
//
// A corrupted committed line must surface an explicit error - the Arbiter cannot keep ruling on a fact pack that is missing part of its history.
// The trailing fragment left behind by a crash (last byte is not '\n') is truncated by committedDataUnlocked with an explicit warning; this is not
// a guess-based repair, because this file's protocol states that only newline-terminated records count as committed.
func (s *DecisionStore) Recent(n int) ([]DecisionRecord, error) {
	s.io.mu.Lock()
	defer s.io.mu.Unlock()
	data, err := s.committedDataUnlocked()
	if err != nil {
		return nil, err
	}
	all, err := parseDecisionRecords(data)
	if err != nil {
		return nil, err
	}
	if n > 0 && len(all) > n {
		all = all[len(all)-n:]
	}
	return all, nil
}

// committedDataUnlocked returns the complete newline-terminated records and truncates the leftover bytes after the last newline from disk. The caller
// must hold the io.mu write lock. Truncation is idempotent, the original file survives a failure and the error is raised explicitly.
func (s *DecisionStore) committedDataUnlocked() ([]byte, error) {
	data, err := s.io.ReadFileUnlocked(decisionsFile)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if len(data) == 0 || data[len(data)-1] == '\n' {
		return data, nil
	}
	keep := bytes.LastIndexByte(data, '\n') + 1
	if err := os.Truncate(s.io.path(decisionsFile), int64(keep)); err != nil {
		return nil, err
	}
	slog.Warn("已修复裁定审计的未提交尾部",
		"module", "store", "file", decisionsFile, "discarded_bytes", len(data)-keep)
	return data[:keep], nil
}

func parseDecisionRecords(data []byte) ([]DecisionRecord, error) {
	var all []DecisionRecord
	lines := bytes.Split(data, []byte{'\n'})
	for i, raw := range lines {
		if i == len(lines)-1 && len(raw) == 0 {
			break
		}
		var rec DecisionRecord
		if err := json.Unmarshal(raw, &rec); err != nil {
			return nil, fmt.Errorf("parse %s line %d: %w", decisionsFile, i+1, err)
		}
		all = append(all, rec)
	}
	return all, nil
}

func newDecisionID() string {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("dec-%d", time.Now().UnixNano())
	}
	return "dec-" + hex.EncodeToString(b[:])
}
