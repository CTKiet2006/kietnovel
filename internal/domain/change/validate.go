package change

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/voocel/ainovel-cli/internal/domain/model"
)

// 通用结构校验（D64）：目标允许的文档种类、只追加、工件引用、依赖存在性与环、结构
// 影响。创作领域的不变量与任务提交契约由 model.ValidateChange 负责。

func validateTargetDocuments(target model.AuthorityTarget, patches []model.Patch) error {
	allowed := make(map[model.DocumentKind]struct{})
	for _, kind := range model.DocumentKindsFor(target.Kind) {
		allowed[kind] = struct{}{}
	}
	for _, patch := range patches {
		if _, ok := allowed[patch.Document.Kind]; !ok {
			return fmt.Errorf("document %s cannot be written to %s authority: %w", patch.Document.Kind, target.Kind, model.ErrInvalid)
		}
	}
	return nil
}

// validateAppendOnly 落实只追加文档（D43 裁决）：不得覆盖已存在的记录，也不得删除。
func validateAppendOnly(base model.DocumentSet, patches []model.Patch) error {
	for _, patch := range patches {
		spec, err := model.DocumentType(patch.Document.Kind)
		if err != nil {
			return err
		}
		if !spec.AppendOnly {
			continue
		}
		if _, exists := base[patch.Document.Key()]; exists || patch.Operation == model.PatchDelete {
			return fmt.Errorf("%s is append-only: %w", patch.Document.Key(), model.ErrStructuralConflict)
		}
	}
	return nil
}

// validateArtifacts 要求文档引用的工件已发布、属于本作品且摘要一致（D47）：权威只能
// 指向不可变内容。哪些文档引用工件由类型登记声明。
func (e *Engine) validateArtifacts(ctx context.Context, change model.Proposal) error {
	for _, patch := range change.Patches {
		if patch.Operation != model.PatchPut {
			continue
		}
		refs, err := model.DocumentArtifacts(patch.Document, patch.Content)
		if err != nil {
			return err
		}
		for _, ref := range refs {
			artifact, err := e.store.GetArtifact(ctx, ref.ID)
			if errors.Is(err, model.ErrNotFound) {
				return fmt.Errorf("%s references unpublished artifact %q: %w", patch.Document.Key(), ref.ID, model.ErrStructuralConflict)
			}
			if err != nil {
				return err
			}
			if artifact.ProjectID != change.Target.ID || artifact.Digest != ref.Digest {
				return fmt.Errorf("%s artifact %q does not match the published object: %w", patch.Document.Key(), ref.ID, model.ErrStructuralConflict)
			}
		}
	}
	return nil
}

// dependencyGraph 是投影状态的依赖边，每份文档只提取一次，供存在性、环检测与影响
// 分析共用。
type dependencyGraph map[string][]model.DocumentRef

func dependenciesOf(state model.DocumentSet) (dependencyGraph, error) {
	graph := make(dependencyGraph, len(state))
	for key, document := range state {
		dependencies, err := model.DocumentDependencies(document.Document, document.Content)
		if err != nil {
			return nil, err
		}
		for _, dependency := range dependencies {
			if _, ok := state[dependency.Key()]; !ok {
				return nil, fmt.Errorf("document %q depends on missing %q: %w", key, dependency.Key(), model.ErrStructuralConflict)
			}
		}
		graph[key] = dependencies
	}
	return graph, graph.detectCycles()
}

func (g dependencyGraph) detectCycles() error {
	visiting := make(map[string]bool)
	visited := make(map[string]bool)
	var visit func(string) error
	visit = func(key string) error {
		if visiting[key] {
			return fmt.Errorf("document dependency cycle at %q: %w", key, model.ErrStructuralConflict)
		}
		if visited[key] {
			return nil
		}
		visiting[key] = true
		for _, dependency := range g[key] {
			if err := visit(dependency.Key()); err != nil {
				return err
			}
		}
		visiting[key] = false
		visited[key] = true
		return nil
	}
	for key := range g {
		if err := visit(key); err != nil {
			return err
		}
	}
	return nil
}

// impact 沿反向依赖边求受直接改动波及的文档。
func (g dependencyGraph) impact(state model.DocumentSet, direct []model.DocumentRef) StructuralImpact {
	slices.SortFunc(direct, compareDocumentRef)
	direct = slices.CompactFunc(direct, func(a, b model.DocumentRef) bool { return a.Key() == b.Key() })
	reverse := make(map[string][]model.DocumentRef)
	for key, dependencies := range g {
		for _, dependency := range dependencies {
			reverse[dependency.Key()] = append(reverse[dependency.Key()], state[key].Document)
		}
	}
	seen := make(map[string]struct{}, len(direct))
	for _, ref := range direct {
		seen[ref.Key()] = struct{}{}
	}
	queue := slices.Clone(direct)
	var affected []model.DocumentRef
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, dependent := range reverse[current.Key()] {
			if _, ok := seen[dependent.Key()]; ok {
				continue
			}
			seen[dependent.Key()] = struct{}{}
			queue = append(queue, dependent)
			affected = append(affected, dependent)
		}
	}
	slices.SortFunc(affected, compareDocumentRef)
	if affected == nil {
		affected = []model.DocumentRef{}
	}
	return StructuralImpact{Direct: direct, Affected: affected}
}

func compareDocumentRef(a, b model.DocumentRef) int {
	return strings.Compare(a.Key(), b.Key())
}
