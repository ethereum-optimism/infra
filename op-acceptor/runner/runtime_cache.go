package runner

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

const runtimeCacheVersion = 1

// runtimeCacheJSON is the on-disk representation. Durations are stored as Go
// duration strings (e.g. "4m20s") for human readability.
type runtimeCacheJSON struct {
	Version   int               `json:"version"`
	UpdatedAt time.Time         `json:"updated_at"`
	Runtimes  map[string]string `json:"runtimes"`
}

// RuntimeCache holds the most-recent wall-clock runtime per validator key.
type RuntimeCache struct {
	Runtimes map[string]time.Duration
}

// LoadRuntimeCache reads the cache from path. Returns an empty cache (no
// error) if the file does not exist. Returns an empty cache and an error for
// all other failures so callers can log and proceed.
func LoadRuntimeCache(path string) (*RuntimeCache, error) {
	empty := &RuntimeCache{Runtimes: map[string]time.Duration{}}
	if path == "" {
		return empty, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return empty, nil
		}
		return empty, fmt.Errorf("reading runtime cache: %w", err)
	}

	var raw runtimeCacheJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		return empty, fmt.Errorf("parsing runtime cache: %w", err)
	}

	cache := &RuntimeCache{Runtimes: make(map[string]time.Duration, len(raw.Runtimes))}
	for k, v := range raw.Runtimes {
		d, parseErr := time.ParseDuration(v)
		if parseErr != nil {
			continue // skip unparseable entries; don't fail the whole load
		}
		cache.Runtimes[k] = d
	}
	return cache, nil
}

// SaveRuntimeCache writes cache to path atomically (temp file + rename).
// A no-op when path is empty.
func SaveRuntimeCache(path string, cache *RuntimeCache) error {
	if path == "" {
		return nil
	}

	raw := runtimeCacheJSON{
		Version:   runtimeCacheVersion,
		UpdatedAt: time.Now().UTC(),
		Runtimes:  make(map[string]string, len(cache.Runtimes)),
	}
	for k, v := range cache.Runtimes {
		raw.Runtimes[k] = v.String()
	}

	data, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling runtime cache: %w", err)
	}

	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "runtime-cache-*.json.tmp")
	if err != nil {
		return fmt.Errorf("creating temp file for runtime cache: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("writing runtime cache temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing runtime cache temp file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("renaming runtime cache temp file: %w", err)
	}
	return nil
}

// SortWorkByRuntime sorts items in-place: unknown validators first, then
// known validators longest-first. Uses a stable sort to preserve relative
// order among items with equal duration.
func SortWorkByRuntime(items []TestWork, cache *RuntimeCache) {
	sort.SliceStable(items, func(i, j int) bool {
		di, iKnown := cache.Runtimes[items[i].ResultKey]
		dj, jKnown := cache.Runtimes[items[j].ResultKey]
		if !iKnown && !jKnown {
			return false
		}
		if !iKnown {
			return true // unknown i before known j
		}
		if !jKnown {
			return false // known i after unknown j
		}
		return di > dj // longest first
	})
}
