package model

import (
	"encoding/json"
	"fmt"
	"strings"
)

// 语义影响报告（§5.5）：独立分析给出冲突与处置选项，用户按选项裁决。

type SemanticImpactStatus string

const (
	SemanticImpactConsistent SemanticImpactStatus = "consistent"
	SemanticImpactConflict   SemanticImpactStatus = "conflict"
	SemanticImpactUncertain  SemanticImpactStatus = "uncertain"
)

type ResolutionStrategy string

const (
	ResolutionRewriteAffected   ResolutionStrategy = "rewrite_affected"
	ResolutionReinterpretFuture ResolutionStrategy = "reinterpret_future"
	ResolutionAbandon           ResolutionStrategy = "abandon"
)

type SemanticImpactFinding struct {
	Document    *DocumentRef `json:"document,omitempty"`
	Explanation string       `json:"explanation"`
}

type ResolutionOption struct {
	Strategy    ResolutionStrategy `json:"strategy"`
	ChapterIDs  []string           `json:"chapter_ids,omitempty"`
	Explanation string             `json:"explanation"`
}

type SemanticImpactReport struct {
	Status   SemanticImpactStatus    `json:"status"`
	Findings []SemanticImpactFinding `json:"findings"`
	Options  []ResolutionOption      `json:"options"`
}

func (r SemanticImpactReport) Validate() error {
	switch r.Status {
	case SemanticImpactConsistent:
		if len(r.Findings) != 0 || len(r.Options) != 0 {
			return fmt.Errorf("consistent semantic impact cannot contain findings or resolution options: %w", ErrInvalid)
		}
		return nil
	case SemanticImpactConflict, SemanticImpactUncertain:
		if len(r.Findings) == 0 {
			return fmt.Errorf("semantic conflict or uncertainty requires findings: %w", ErrInvalid)
		}
	default:
		return fmt.Errorf("unknown semantic impact status %q: %w", r.Status, ErrInvalid)
	}
	for index, finding := range r.Findings {
		if strings.TrimSpace(finding.Explanation) == "" {
			return fmt.Errorf("semantic finding %d requires an explanation: %w", index, ErrInvalid)
		}
		if finding.Document != nil {
			if err := finding.Document.Validate(); err != nil {
				return err
			}
		}
	}
	required := map[ResolutionStrategy]bool{
		ResolutionRewriteAffected: false, ResolutionReinterpretFuture: false, ResolutionAbandon: false,
	}
	for index, option := range r.Options {
		if _, ok := required[option.Strategy]; !ok || required[option.Strategy] {
			return fmt.Errorf("resolution option %d has an unknown or duplicate strategy %q: %w", index, option.Strategy, ErrInvalid)
		}
		if strings.TrimSpace(option.Explanation) == "" {
			return fmt.Errorf("resolution option %q requires an explanation: %w", option.Strategy, ErrInvalid)
		}
		if option.Strategy == ResolutionRewriteAffected && len(option.ChapterIDs) == 0 {
			return fmt.Errorf("rewrite_affected requires chapter_ids: %w", ErrInvalid)
		}
		seen := make(map[string]struct{}, len(option.ChapterIDs))
		for _, id := range option.ChapterIDs {
			if strings.TrimSpace(id) == "" {
				return fmt.Errorf("resolution chapter id is required: %w", ErrInvalid)
			}
			if _, exists := seen[id]; exists {
				return fmt.Errorf("duplicate resolution chapter %q: %w", id, ErrInvalid)
			}
			seen[id] = struct{}{}
		}
		required[option.Strategy] = true
	}
	for strategy, present := range required {
		if !present {
			return fmt.Errorf("semantic impact is missing resolution strategy %q: %w", strategy, ErrInvalid)
		}
	}
	return nil
}

// ValidateSemanticImpact 校验报告与作品基线一致：处置选项引用的章节必须存在。
func ValidateSemanticImpact(report SemanticImpactReport, base DocumentSet) error {
	if err := report.Validate(); err != nil {
		return err
	}
	for _, option := range report.Options {
		for _, chapterID := range option.ChapterIDs {
			if _, exists := base[(DocumentRef{Kind: DocumentManuscript, ID: chapterID}).Key()]; !exists {
				return fmt.Errorf("semantic resolution references missing chapter %q: %w", chapterID, ErrStructuralConflict)
			}
		}
	}
	return nil
}

// DecodeSemanticImpact 解码并校验语义影响报告。
func DecodeSemanticImpact(payload json.RawMessage, base DocumentSet) (SemanticImpactReport, error) {
	var report SemanticImpactReport
	if err := json.Unmarshal(payload, &report); err != nil {
		return SemanticImpactReport{}, fmt.Errorf("decode semantic impact: %w", err)
	}
	return report, ValidateSemanticImpact(report, base)
}
