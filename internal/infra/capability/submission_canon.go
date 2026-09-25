package capability

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/voocel/ainovel-cli/internal/domain/model"
)

// Materialization uses the frozen base, never the latest revision. Explicit
// old_value assertions remain subject to the change engine's strict comparison.
// Keyed facts (D61) are resolved by subject+predicate: a new ID for an existing
// key becomes an update of that fact, so the model never has to recall IDs.
func (r *Runtime) materializeCanon(ctx context.Context, operation model.Operation, patches []model.Patch, confirmations []string) ([]model.Patch, error) {
	base, err := r.baseCanon(ctx, operation)
	if err != nil {
		return nil, err
	}
	owners := make(map[model.CanonKey][]string)
	for id, fact := range base {
		if key, keyed := fact.ConceptKey(); keyed {
			owners[key] = append(owners[key], id)
		}
	}
	result := append([]model.Patch(nil), patches...)
	seen := make(map[string]string) // canon ID → the ID the model submitted
	for i, patch := range result {
		if patch.Document.Kind != model.DocumentCanon {
			continue
		}
		submitted := patch.Document.ID
		var fact model.CanonFact
		if patch.Operation == model.PatchPut {
			if err := decodeToolArgs(patch.Content, &fact); err != nil {
				return nil, fmt.Errorf("decode canon %q: %w", submitted, err)
			}
			if _, exists := base[submitted]; !exists {
				if key, keyed := fact.ConceptKey(); keyed {
					switch existing := owners[key]; len(existing) {
					case 0:
					case 1:
						fact.ID, result[i].Document.ID = existing[0], existing[0]
					default:
						return nil, fmt.Errorf("canon key %s has %d facts %v at revision %d; cannot tell which one to update: %w",
							key, len(existing), existing, operation.Snapshot.BaseRevision, model.ErrInvalid)
					}
				}
			}
		}
		id := result[i].Document.ID
		if first, dup := seen[id]; dup {
			return nil, fmt.Errorf("duplicate canon action on %q (submitted as %q and %q; same subject+predicate is one fact): %w",
				id, first, submitted, model.ErrInvalid)
		}
		seen[id] = submitted
		if patch.Operation != model.PatchPut {
			continue
		}
		if previous, exists := base[id]; exists && len(fact.PreviousValue) == 0 {
			fact.PreviousValue = previous.Value
		}
		if result[i].Content, err = json.Marshal(fact); err != nil {
			return nil, err
		}
	}
	for _, id := range confirmations {
		if _, dup := seen[id]; id == "" || dup {
			return nil, fmt.Errorf("confirm_canon requires distinct fact IDs without overlapping patches: %q: %w", id, model.ErrInvalid)
		}
		seen[id] = id
		fact, exists := base[id]
		if !exists {
			return nil, fmt.Errorf("confirm canon %q at revision %d: %w", id, operation.Snapshot.BaseRevision, model.ErrNotFound)
		}
		fact.PreviousValue = fact.Value
		content, err := json.Marshal(fact)
		if err != nil {
			return nil, err
		}
		result = append(result, model.Patch{Document: model.DocumentRef{Kind: model.DocumentCanon, ID: id}, Operation: model.PatchPut, Content: content})
	}
	return result, nil
}

// baseCanon 读冻结基线上的全部事实；初始 Revision 之前没有事实。
func (r *Runtime) baseCanon(ctx context.Context, operation model.Operation) (map[string]model.CanonFact, error) {
	facts := make(map[string]model.CanonFact)
	if operation.Snapshot.BaseRevision <= model.InitialRevision {
		return facts, nil
	}
	documents, err := r.store.ListDocuments(ctx, operation.Target, model.DocumentCanon, operation.Snapshot.BaseRevision)
	if err != nil {
		return nil, fmt.Errorf("read canon baseline: %w", err)
	}
	for _, document := range documents {
		var fact model.CanonFact
		if err := decodeToolArgs(document.Content, &fact); err != nil {
			return nil, err
		}
		facts[document.Document.ID] = fact
	}
	return facts, nil
}
