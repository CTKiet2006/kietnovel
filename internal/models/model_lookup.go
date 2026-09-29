package models

import "strings"

func lookupModelEntry(models []ModelEntry, providerName, modelID string) (ModelEntry, bool) {
	providerName = strings.ToLower(strings.TrimSpace(providerName))
	targetID := normalizeModelLookupID(modelID)
	for _, m := range models {
		if providerName != "" && !strings.EqualFold(m.Provider, providerName) {
			continue
		}
		if modelLookupMatches(normalizeModelLookupID(m.ID), targetID) {
			return m, true
		}
	}
	return ModelEntry{}, false
}

func normalizeModelLookupID(modelID string) string {
	modelID = strings.ToLower(strings.TrimSpace(modelID))
	return strings.ReplaceAll(modelID, ".", "-")
}

// modelLookupMatches does exact matching, or matching with a date suffix.
// e.g. "claude-sonnet-4" matches "claude-sonnet-4-20250514".
func modelLookupMatches(knownID, targetID string) bool {
	if knownID == targetID {
		return true
	}
	if strings.HasPrefix(targetID, knownID) && isDatedModelSuffix(targetID[len(knownID):]) {
		return true
	}
	if strings.HasPrefix(knownID, targetID) && isDatedModelSuffix(knownID[len(targetID):]) {
		return true
	}
	return false
}

// isDatedModelSuffix reports whether a string looks like "-20250514" (a hyphen plus 8 digits).
func isDatedModelSuffix(s string) bool {
	if len(s) != 9 || s[0] != '-' {
		return false
	}
	for _, c := range s[1:] {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func hasDatedSuffix(id string) bool {
	if len(id) < 9 {
		return false
	}
	return isDatedModelSuffix(id[len(id)-9:])
}
