# Test Runtime Ordering Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Load per-validator wall-clock runtimes from the previous run and sort `TestWork` items longest-first before execution, so the critical path in parallel execution is front-loaded.

**Architecture:** A new `RuntimeCache` type in `runner/runtime_cache.go` handles load/save/sort. The runner struct gains `runtimeCache *RuntimeCache` and `runtimeCachePath string`. On startup the cache loads from the configured path; before dispatch the work list is sorted longest-first (unknowns first); after run completion the cache is overwritten with fresh durations.

**Tech Stack:** Go standard library only — `encoding/json`, `sort`, `os`, `time`, `path/filepath`.

---

### Task 1: `RuntimeCache` type and `LoadRuntimeCache`

**Files:**
- Create: `runner/runtime_cache.go`
- Create: `runner/runtime_cache_test.go`

**Step 1: Write the failing tests**

Create `runner/runtime_cache_test.go`:

```go
package runner

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadRuntimeCache_MissingFile(t *testing.T) {
	cache, err := LoadRuntimeCache(filepath.Join(t.TempDir(), "runtime-cache.json"))
	require.NoError(t, err)
	assert.Empty(t, cache.Runtimes)
}

func TestLoadRuntimeCache_EmptyPath(t *testing.T) {
	cache, err := LoadRuntimeCache("")
	require.NoError(t, err)
	assert.Empty(t, cache.Runtimes)
}

func TestLoadRuntimeCache_ValidFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "runtime-cache.json")
	content := `{"version":1,"updated_at":"2026-02-23T12:00:00Z","runtimes":{"TestFoo":"4m20s","TestBar":"8s"}}`
	require.NoError(t, os.WriteFile(path, []byte(content), 0644))

	cache, err := LoadRuntimeCache(path)
	require.NoError(t, err)
	assert.Equal(t, 4*time.Minute+20*time.Second, cache.Runtimes["TestFoo"])
	assert.Equal(t, 8*time.Second, cache.Runtimes["TestBar"])
}

func TestLoadRuntimeCache_CorruptFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "runtime-cache.json")
	require.NoError(t, os.WriteFile(path, []byte("not json {{{"), 0644))

	cache, err := LoadRuntimeCache(path)
	require.Error(t, err)
	assert.Empty(t, cache.Runtimes)
}
```

**Step 2: Run tests to verify they fail**

```
cd /workspace/infra/op-acceptor
go test ./runner/... -run TestLoadRuntimeCache -v
```

Expected: compile error — `LoadRuntimeCache` undefined.

**Step 3: Write minimal implementation**

Create `runner/runtime_cache.go`:

```go
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
```

**Step 4: Run tests to verify they pass**

```
cd /workspace/infra/op-acceptor
go test ./runner/... -run TestLoadRuntimeCache -v
```

Expected: all 4 tests PASS.

**Step 5: Commit**

```bash
git add op-acceptor/runner/runtime_cache.go op-acceptor/runner/runtime_cache_test.go
git commit -m "feat(op-acceptor): add RuntimeCache type with load function"
```

---

### Task 2: `SaveRuntimeCache` tests

**Files:**
- Modify: `runner/runtime_cache_test.go`

The implementation was already written in Task 1. Add tests now.

**Step 1: Add tests to `runtime_cache_test.go`**

Append to the existing test file:

```go
func TestSaveRuntimeCache_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "runtime-cache.json")

	original := &RuntimeCache{
		Runtimes: map[string]time.Duration{
			"TestFoo": 4*time.Minute + 20*time.Second,
			"TestBar": 8 * time.Second,
		},
	}

	require.NoError(t, SaveRuntimeCache(path, original))

	loaded, err := LoadRuntimeCache(path)
	require.NoError(t, err)
	assert.Equal(t, original.Runtimes["TestFoo"], loaded.Runtimes["TestFoo"])
	assert.Equal(t, original.Runtimes["TestBar"], loaded.Runtimes["TestBar"])
}

func TestSaveRuntimeCache_EmptyPath(t *testing.T) {
	cache := &RuntimeCache{Runtimes: map[string]time.Duration{"TestFoo": time.Second}}
	assert.NoError(t, SaveRuntimeCache("", cache))
}

func TestSaveRuntimeCache_Atomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "runtime-cache.json")

	// Write once, verify file exists after
	require.NoError(t, SaveRuntimeCache(path, &RuntimeCache{Runtimes: map[string]time.Duration{"TestFoo": time.Second}}))
	_, err := os.Stat(path)
	require.NoError(t, err)

	// Write again (overwrite), verify no temp files left behind
	require.NoError(t, SaveRuntimeCache(path, &RuntimeCache{Runtimes: map[string]time.Duration{"TestBar": 2 * time.Second}}))
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Len(t, entries, 1, "only runtime-cache.json should remain, no temp files")
}
```

**Step 2: Run tests**

```
cd /workspace/infra/op-acceptor
go test ./runner/... -run TestSaveRuntimeCache -v
```

Expected: all 3 tests PASS.

**Step 3: Commit**

```bash
git add op-acceptor/runner/runtime_cache_test.go
git commit -m "test(op-acceptor): add SaveRuntimeCache tests"
```

---

### Task 3: `SortWorkByRuntime` tests

**Files:**
- Modify: `runner/runtime_cache_test.go`

**Step 1: Add tests**

Append to `runtime_cache_test.go`:

```go
func TestSortWorkByRuntime_LongestFirst(t *testing.T) {
	cache := &RuntimeCache{
		Runtimes: map[string]time.Duration{
			"TestFast":   5 * time.Second,
			"TestSlow":   5 * time.Minute,
			"TestMedium": 30 * time.Second,
		},
	}
	items := []TestWork{
		{ResultKey: "TestFast"},
		{ResultKey: "TestMedium"},
		{ResultKey: "TestSlow"},
	}

	SortWorkByRuntime(items, cache)

	assert.Equal(t, "TestSlow", items[0].ResultKey)
	assert.Equal(t, "TestMedium", items[1].ResultKey)
	assert.Equal(t, "TestFast", items[2].ResultKey)
}

func TestSortWorkByRuntime_UnknownFirst(t *testing.T) {
	cache := &RuntimeCache{
		Runtimes: map[string]time.Duration{
			"TestKnown": 5 * time.Minute,
		},
	}
	items := []TestWork{
		{ResultKey: "TestKnown"},
		{ResultKey: "TestNewUnknown"},
	}

	SortWorkByRuntime(items, cache)

	assert.Equal(t, "TestNewUnknown", items[0].ResultKey, "unknown should sort before known")
	assert.Equal(t, "TestKnown", items[1].ResultKey)
}

func TestSortWorkByRuntime_EmptyCache(t *testing.T) {
	cache := &RuntimeCache{Runtimes: map[string]time.Duration{}}
	items := []TestWork{
		{ResultKey: "TestA"},
		{ResultKey: "TestB"},
		{ResultKey: "TestC"},
	}
	original := []string{"TestA", "TestB", "TestC"}

	SortWorkByRuntime(items, cache)

	// All unknown — stable sort preserves original order
	for i, item := range items {
		assert.Equal(t, original[i], item.ResultKey)
	}
}

func TestSortWorkByRuntime_MixedUnknownsAndKnowns(t *testing.T) {
	cache := &RuntimeCache{
		Runtimes: map[string]time.Duration{
			"TestSlow": 5 * time.Minute,
			"TestFast": 5 * time.Second,
		},
	}
	items := []TestWork{
		{ResultKey: "TestSlow"},
		{ResultKey: "TestUnknownA"},
		{ResultKey: "TestFast"},
		{ResultKey: "TestUnknownB"},
	}

	SortWorkByRuntime(items, cache)

	// Unknowns first (stable: A before B), then slow, then fast
	assert.Equal(t, "TestUnknownA", items[0].ResultKey)
	assert.Equal(t, "TestUnknownB", items[1].ResultKey)
	assert.Equal(t, "TestSlow", items[2].ResultKey)
	assert.Equal(t, "TestFast", items[3].ResultKey)
}
```

**Step 2: Run tests**

```
cd /workspace/infra/op-acceptor
go test ./runner/... -run TestSortWorkByRuntime -v
```

Expected: all 4 tests PASS.

**Step 3: Commit**

```bash
git add op-acceptor/runner/runtime_cache_test.go
git commit -m "test(op-acceptor): add SortWorkByRuntime tests"
```

---

### Task 4: Wire cache into `runner` struct and `NewTestRunner`

**Files:**
- Modify: `runner/runner.go`
- Modify: `nat.go`

**Step 1: Add `RuntimeCachePath` to runner `Config` struct**

In `runner/runner.go`, after the existing `ProgressInterval` field (line 159), add:

```go
RuntimeCachePath string // Path to runtime cache file (empty = no caching)
```

**Step 2: Add cache fields to `runner` struct**

In `runner/runner.go`, after the `targetGates` field (line 133), add:

```go
runtimeCache     *RuntimeCache
runtimeCachePath string
```

**Step 3: Load cache and sort validators in `NewTestRunner`**

In `runner/runner.go`, after the `r := &runner{...}` block (after line 211), add:

```go
// Load runtime cache for test ordering
r.runtimeCachePath = cfg.RuntimeCachePath
if r.runtimeCachePath != "" {
    cache, err := LoadRuntimeCache(r.runtimeCachePath)
    if err != nil {
        cfg.Log.Warn("Failed to load runtime cache, proceeding without it", "err", err, "path", r.runtimeCachePath)
    }
    r.runtimeCache = cache
} else {
    r.runtimeCache = &RuntimeCache{Runtimes: map[string]time.Duration{}}
}

// Sort validators by runtime for serial execution (longest first, unknowns first)
sort.SliceStable(r.validators, func(i, j int) bool {
    di, iKnown := r.runtimeCache.Runtimes[r.getTestKey(r.validators[i])]
    dj, jKnown := r.runtimeCache.Runtimes[r.getTestKey(r.validators[j])]
    if !iKnown && !jKnown {
        return false
    }
    if !iKnown {
        return true
    }
    if !jKnown {
        return false
    }
    return di > dj
})
```

Also add `"sort"` to the imports in `runner.go` if not already present (check first — it is a large file with many imports; search for `"sort"` before adding).

**Step 4: Pass `RuntimeCachePath` from `nat.go`**

In `nat.go`, the `runner.NewTestRunner(runner.Config{...})` call starts at line 170. Add the field inside that struct literal:

```go
RuntimeCachePath: filepath.Join(config.LogDir, "runtime-cache.json"),
```

`filepath` is already imported in `nat.go` (it's used on line 466). If not, add `"path/filepath"` to imports.

**Step 5: Verify existing tests still pass**

```
cd /workspace/infra/op-acceptor
go test ./runner/... -v -count=1 2>&1 | tail -20
```

Expected: all existing tests pass (no regressions). The cache fields default safely when `RuntimeCachePath` is empty.

**Step 6: Commit**

```bash
git add op-acceptor/runner/runner.go op-acceptor/nat.go
git commit -m "feat(op-acceptor): wire RuntimeCache into runner and sort validators on startup"
```

---

### Task 5: Sort work items before parallel execution

**Files:**
- Modify: `runner/runner.go`

**Step 1: Add sort call in `runAllTestsParallel`**

In `runner/runner.go`, `runAllTestsParallel` (line 311). After `workItems := r.collectTestWork()` (line 315), add:

```go
// Sort work items longest-first so the critical path is front-loaded.
// Unknown validators sort to the front.
SortWorkByRuntime(workItems, r.runtimeCache)
```

**Step 2: Verify**

```
cd /workspace/infra/op-acceptor
go test ./runner/... -run TestParallel -v -count=1 2>&1 | tail -30
```

Expected: all parallel tests pass.

**Step 3: Commit**

```bash
git add op-acceptor/runner/runner.go
git commit -m "feat(op-acceptor): sort parallel work items by cached runtime before dispatch"
```

---

### Task 6: Write cache after run completion

**Files:**
- Modify: `runner/runner.go`

**Step 1: Add `updateRuntimeCache` helper method**

Add this method anywhere in `runner/runner.go` (e.g. after `runAllTestsSerial`):

```go
// updateRuntimeCache writes a new runtime cache snapshot from the completed run.
// Logs a warning on failure but does not affect the run result.
func (r *runner) updateRuntimeCache(result *RunnerResult) {
    if r.runtimeCachePath == "" {
        return
    }
    runtimes := make(map[string]time.Duration)
    for _, gate := range result.Gates {
        for _, test := range gate.Tests {
            key := r.getTestKey(test.Metadata)
            runtimes[key] = test.Duration
        }
        for _, suite := range gate.Suites {
            for _, test := range suite.Tests {
                key := r.getTestKey(test.Metadata)
                runtimes[key] = test.Duration
            }
        }
    }
    newCache := &RuntimeCache{Runtimes: runtimes}
    if err := SaveRuntimeCache(r.runtimeCachePath, newCache); err != nil {
        r.log.Warn("Failed to save runtime cache", "err", err, "path", r.runtimeCachePath)
    }
}
```

**Step 2: Call it at the end of `runAllTestsParallel`**

In `runAllTestsParallel`, before `return result, nil` (just before the function returns), add:

```go
r.updateRuntimeCache(result)
```

**Step 3: Call it at the end of `runAllTestsSerial`**

In `runAllTestsSerial`, before `return result, nil` (line 307), add:

```go
r.updateRuntimeCache(result)
```

**Step 4: Verify all tests still pass**

```
cd /workspace/infra/op-acceptor
go test ./runner/... -count=1 2>&1 | tail -20
```

Expected: all tests pass.

**Step 5: Commit**

```bash
git add op-acceptor/runner/runner.go
git commit -m "feat(op-acceptor): write runtime cache after each run completes"
```

---

### Task 7: Run full test suite and verify

**Step 1: Run all op-acceptor tests**

```
cd /workspace/infra/op-acceptor
go test ./... -count=1 2>&1 | tail -40
```

Expected: all packages pass.

**Step 2: Verify `go vet` is clean**

```
cd /workspace/infra/op-acceptor
go vet ./...
```

Expected: no output.

**Step 3: Verify cache file format manually**

```
cd /workspace/infra/op-acceptor
go test ./runner/... -run TestSaveRuntimeCache_RoundTrip -v
```

Then inspect the written file to confirm it looks like:

```json
{
  "version": 1,
  "updated_at": "2026-02-23T12:00:00Z",
  "runtimes": {
    "TestFoo": "4m20s",
    "TestBar": "8s"
  }
}
```

**Step 4: Final commit if any fixups were needed**

```bash
git add -p
git commit -m "fix(op-acceptor): address any issues from full test suite run"
```
