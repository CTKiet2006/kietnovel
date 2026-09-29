package store

import (
	"fmt"
	"os"
	"strings"

	"github.com/CTKiet2006/kietnovel/internal/domain"
)

// CharacterStore manages character profiles and state snapshots.
type CharacterStore struct {
	io      *IO
	outline *OutlineStore // read-only dependency, used to walk the snapshots
}

func NewCharacterStore(io *IO, outline *OutlineStore) *CharacterStore {
	return &CharacterStore{io: io, outline: outline}
}

// Save writes both characters.json and characters.md (atomically).
func (s *CharacterStore) Save(chars []domain.Character) error {
	return s.io.WithWriteLock(func() error {
		if err := s.io.WriteJSONUnlocked("characters.json", chars); err != nil {
			return err
		}
		return s.io.WriteMarkdownUnlocked("characters.md", renderCharacters(chars))
	})
}

// Load reads the character profiles from characters.json.
func (s *CharacterStore) Load() ([]domain.Character, error) {
	var chars []domain.Character
	if err := s.io.ReadJSON("characters.json", &chars); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return chars, nil
}

// SaveSnapshots writes the character state snapshot to meta/snapshots/v{vol}a{arc}.json.
func (s *CharacterStore) SaveSnapshots(volume, arc int, snapshots []domain.CharacterSnapshot) error {
	return s.io.WriteJSON(fmt.Sprintf("meta/snapshots/v%02da%02d.json", volume, arc), snapshots)
}

// LoadSnapshots reads the character snapshot of a given volume/arc.
func (s *CharacterStore) LoadSnapshots(volume, arc int) ([]domain.CharacterSnapshot, error) {
	var snapshots []domain.CharacterSnapshot
	if err := s.io.ReadJSON(fmt.Sprintf("meta/snapshots/v%02da%02d.json", volume, arc), &snapshots); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return snapshots, nil
}

// LoadLatestSnapshots loads the most recent character snapshot (searching volume/arc in reverse order).
func (s *CharacterStore) LoadLatestSnapshots() ([]domain.CharacterSnapshot, error) {
	// A missing outline -> (nil, nil) is already absorbed inside LoadLayeredOutline; corruption must bubble up, otherwise the character snapshot
	// silently disappears together with the corrupted layered outline and the writing loses character continuity without anyone noticing.
	volumes, err := s.outline.LoadLayeredOutline()
	if err != nil {
		return nil, err
	}
	if len(volumes) == 0 {
		return nil, nil
	}
	for vi := len(volumes) - 1; vi >= 0; vi-- {
		v := volumes[vi]
		for ai := len(v.Arcs) - 1; ai >= 0; ai-- {
			snaps, err := s.LoadSnapshots(v.Index, v.Arcs[ai].Index)
			if err != nil {
				return nil, err
			}
			if len(snaps) > 0 {
				return snaps, nil
			}
		}
	}
	return nil, nil
}

func renderCharacters(chars []domain.Character) string {
	var b strings.Builder
	b.WriteString("# 角色档案\n\n")
	for _, c := range chars {
		fmt.Fprintf(&b, "## %s（%s）\n\n", c.Name, c.Role)
		fmt.Fprintf(&b, "%s\n\n", c.Description)
		if c.Arc != "" {
			fmt.Fprintf(&b, "**角色弧线**：%s\n\n", c.Arc)
		}
		if len(c.Traits) > 0 {
			fmt.Fprintf(&b, "**特征**：%s\n\n", strings.Join(c.Traits, "、"))
		}
	}
	return b.String()
}
