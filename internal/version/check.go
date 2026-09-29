package version

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/mod/semver"
)

// DefaultRepo is the repository that version checks and self-update target by default.
const DefaultRepo = "CTKiet2006/kietnovel"

// DefaultCheckInterval is the minimum spacing between two online checks. The anonymous GitHub API rate-limits
// to 60 req/h and releases ship on a daily cadence, so checking more often brings only annoyance, no benefit.
const DefaultCheckInterval = 24 * time.Hour

// CheckOptions is the input of a single version check. CachePath is supplied by the caller (usually
// update-check.json under the config directory); this package deliberately does not depend on bootstrap, keeping
// version a leaf package in the dependency direction.
type CheckOptions struct {
	Repo           string
	CurrentVersion string
	Client         *http.Client
	CachePath      string        // empty = no disk cache, every call goes online
	MaxAge         time.Duration // cache lifetime; <=0 falls back to DefaultCheckInterval
}

// CheckResult is the outcome of a version check. Notes carries the raw release text (markdown),
// which the caller must sanitize for its own output medium before displaying; whether to upgrade is the user's decision.
type CheckResult struct {
	Latest          string
	Current         string
	Notes           string
	UpdateAvailable bool
	FromCache       bool
}

// checkCache is the on-disk structure behind CachePath.
type checkCache struct {
	LastCheck time.Time `json:"last_check"`
	Latest    string    `json:"latest"`
	Notes     string    `json:"notes"`
}

// CheckUpdate queries the latest upstream release and decides whether to nudge an upgrade. It is read-only and never writes any
// binary; whether and when to upgrade is entirely up to the user via `ainovel-cli update`.
// Cache or network failures surface through error; when the cache write fails the already-obtained result is still returned.
func CheckUpdate(ctx context.Context, opts CheckOptions) (*CheckResult, error) {
	current := Normalize(opts.CurrentVersion)
	if current == "dev" {
		// A local build has no comparable version semantics, so the check is skipped rather than misreporting any build as upgradable.
		return &CheckResult{Current: current}, nil
	}
	repo := strings.TrimSpace(opts.Repo)
	if repo == "" {
		repo = DefaultRepo
	}
	maxAge := opts.MaxAge
	if maxAge <= 0 {
		maxAge = DefaultCheckInterval
	}

	var cacheErr error
	if opts.CachePath != "" {
		c, err := loadCache(opts.CachePath)
		switch {
		case err == nil:
			age := time.Since(c.LastCheck)
			if age < 0 {
				cacheErr = fmt.Errorf("update check cache timestamp is in the future: %s", c.LastCheck.Format(time.RFC3339))
			} else if age <= maxAge {
				result, resultErr := c.result(current)
				if resultErr == nil {
					return result, nil
				}
				cacheErr = fmt.Errorf("validate update check cache: %w", resultErr)
			}
		case errors.Is(err, os.ErrNotExist):
			// Having no cache on the first check is a normal state.
		default:
			cacheErr = fmt.Errorf("load update check cache: %w", err)
		}
	}

	client := opts.Client
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	rel, err := fetchRelease(ctx, client, repo, "latest")
	if err != nil {
		return nil, errors.Join(cacheErr, err)
	}
	if rel.TagName == "" {
		return nil, errors.Join(cacheErr, fmt.Errorf("release 缺少 tag_name"))
	}

	result, err := newCheckResult(rel.TagName, current, rel.Body, false)
	if err != nil {
		return nil, errors.Join(cacheErr, err)
	}
	if err := writeCache(opts.CachePath, rel); err != nil {
		cacheErr = errors.Join(cacheErr, fmt.Errorf("write update check cache: %w", err))
	}
	// The cache error is returned alongside and the caller should log it, but the check result is still usable.
	return result, cacheErr
}

func newCheckResult(latest, current, notes string, fromCache bool) (*CheckResult, error) {
	updateAvailable, err := isNewer(latest, current)
	if err != nil {
		return nil, err
	}
	return &CheckResult{
		Latest:          latest,
		Current:         current,
		Notes:           notes,
		UpdateAvailable: updateAvailable,
		FromCache:       fromCache,
	}, nil
}

// loadCache reads and validates the cache; a missing file, corruption and missing fields are handled separately by the caller.
func loadCache(path string) (*checkCache, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c checkCache
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("decode cache: %w", err)
	}
	if c.Latest == "" || c.LastCheck.IsZero() {
		return nil, fmt.Errorf("cache missing latest or last_check")
	}
	return &c, nil
}

// result converts cached content into a check outcome (FromCache=true).
func (c *checkCache) result(current string) (*CheckResult, error) {
	return newCheckResult(c.Latest, current, c.Notes, true)
}

// writeCache atomically persists this check result, creating the directory if needed.
func writeCache(path string, rel *release) error {
	if path == "" {
		return nil
	}
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	data, err := json.Marshal(checkCache{LastCheck: time.Now(), Latest: rel.TagName, Notes: rel.Body})
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".update-check-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	committed := false
	defer func() {
		_ = tmp.Close()
		if !committed {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return err
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	committed = true
	return nil
}

// isNewer compares versions with the full SemVer rules; an invalid version returns an explicit error so nothing is silently missed.
func isNewer(latest, current string) (bool, error) {
	latest = Normalize(latest)
	current = Normalize(current)
	if !semver.IsValid(latest) {
		return false, fmt.Errorf("invalid latest version %q", latest)
	}
	if !semver.IsValid(current) {
		return false, fmt.Errorf("invalid current version %q", current)
	}
	return semver.Compare(latest, current) > 0, nil
}
