package store

import (
	"os"

	"github.com/CTKiet2006/kietnovel/internal/domain"
)

// UsageStore persists the accumulated token / cost usage in meta/usage.json.
// The write goes through the IO atomic write (tmp + rename); the Save path overwrites the whole state every time.
type UsageStore struct{ io *IO }

func NewUsageStore(io *IO) *UsageStore { return &UsageStore{io: io} }

// Load reads usage.json. It returns (nil, nil) when the file is missing or the schema version does not match,
// and the caller decides whether to backfill in one go via session replay.
func (s *UsageStore) Load() (*domain.UsageState, error) {
	var state domain.UsageState
	if err := s.io.ReadJSON("meta/usage.json", &state); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if state.Schema != domain.UsageSchemaVersion {
		return nil, nil
	}
	return &state, nil
}

// Save overwrites the whole state on disk. The caller is responsible for debouncing / throttling.
func (s *UsageStore) Save(state domain.UsageState) error {
	state.Schema = domain.UsageSchemaVersion
	return s.io.WriteJSON("meta/usage.json", state)
}
