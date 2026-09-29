package store

import (
	"os"

	"github.com/CTKiet2006/kietnovel/internal/rules"
)

// UserRulesStore manages the normalized snapshot of the user rules for this book (meta/user_rules.json).
//
// The single source of truth at runtime: both the novel_context injection and the commit_chapter check read only this one,
// instead of re-reading the rules files over and over (which avoids drift and dual-reader divergence). The snapshot is produced by normalization when a book is created/imported/refreshed.
type UserRulesStore struct{ io *IO }

func NewUserRulesStore(io *IO) *UserRulesStore { return &UserRulesStore{io: io} }

// Load reads meta/user_rules.json. It returns nil when the file does not exist (the caller then generates it lazily).
func (s *UserRulesStore) Load() (*rules.Snapshot, error) {
	s.io.mu.RLock()
	defer s.io.mu.RUnlock()
	var snap rules.Snapshot
	if err := s.io.ReadJSONUnlocked("meta/user_rules.json", &snap); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return &snap, nil
}

// Save stores the snapshot.
func (s *UserRulesStore) Save(snap *rules.Snapshot) error {
	s.io.mu.Lock()
	defer s.io.mu.Unlock()
	return s.io.WriteJSONUnlocked("meta/user_rules.json", snap)
}
