package change

import (
	"encoding/json"
	"fmt"

	"github.com/voocel/ainovel-cli/internal/domain/model"
)

// authorize 执行裁决者的权限边界。basis 是提案受 locked/guided 约束时合规证据必须绑定
// 的摘要（无约束为空）：guided 文档的系统放行与 Admit 共用 complianceGap。
func authorize(change model.Proposal, base model.DocumentSet, basis string) error {
	decider := change.DecidedBy
	if decider == nil {
		return fmt.Errorf("approval decider is missing: %w", ErrUnauthorized)
	}
	if change.Author.Kind == model.AuthorUser && decider.Kind != model.AuthorUser {
		return fmt.Errorf("user-authored change requires user decision: %w", ErrUnauthorized)
	}
	if change.Target.Kind == model.AuthorityProfile || change.Target.Kind == model.AuthorityPack {
		if change.Author.Machine() && decider.Kind != model.AuthorUser {
			return fmt.Errorf("AI changes to creator assets require user approval: %w", ErrUnauthorized)
		}
	}
	// 创作领域规定的必须由用户裁决的 AI 变更（Intent、罗盘护栏）。
	if change.Author.Machine() && decider.Kind != model.AuthorUser {
		reason, err := model.RequiresUser(change, base)
		if err != nil {
			return err
		}
		if reason != "" {
			return fmt.Errorf("%s: %w", reason, ErrUnauthorized)
		}
	}
	for _, patch := range change.Patches {
		// D25/D31/D33：用户专属文档是创作边界，AI 可以提出 Proposal，但任何作者的
		// 变更都必须由用户裁决。
		spec, err := model.DocumentType(patch.Document.Kind)
		if err != nil {
			return err
		}
		if spec.UserOnly && decider.Kind != model.AuthorUser {
			return fmt.Errorf("creative boundary changes require user approval: %w", ErrUnauthorized)
		}
		control, err := controlLevel(base, patch.Document)
		if err != nil {
			return err
		}
		if !change.Author.Machine() {
			continue
		}
		switch control {
		case model.ControlLocked:
			if decider.Kind != model.AuthorUser {
				return fmt.Errorf("locked document %q requires user approval: %w", patch.Document.Key(), ErrUnauthorized)
			}
		case model.ControlGuided:
			if decider.Kind != model.AuthorUser {
				reason, _, err := complianceGap(change, basis)
				if err != nil {
					return err
				}
				if reason != "" {
					return fmt.Errorf("guided document %q: %s: %w", patch.Document.Key(), reason, ErrUnauthorized)
				}
			}
		}
	}
	return nil
}

// controlLevel 取文档的控制级别：有 Ownership 规则以规则为准，没有时取创作领域的
// 默认级别（用户亲笔的章节 locked，D34）。
func controlLevel(state model.DocumentSet, ref model.DocumentRef) (model.ControlLevel, error) {
	ownershipRef := model.DocumentRef{Kind: model.DocumentOwnership, ID: ref.Key()}
	document, ok := state[ownershipRef.Key()]
	if !ok {
		return model.DefaultControl(ref, state)
	}
	var rule model.OwnershipRule
	if err := json.Unmarshal(document.Content, &rule); err != nil {
		return "", fmt.Errorf("decode ownership for %q: %w", ref.Key(), err)
	}
	if err := rule.Validate(); err != nil {
		return "", err
	}
	return rule.Control, nil
}
