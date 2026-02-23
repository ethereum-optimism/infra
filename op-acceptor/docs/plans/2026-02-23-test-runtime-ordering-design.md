# Test Runtime Ordering Design

**Date:** 2026-02-23
**Status:** Approved

## Problem

op-acceptor currently dispatches test work items in an arbitrary order (map iteration + execution order). Long-running tests that happen to be scheduled last create an unnecessary long tail in CI. Sorting tests longest-first means the critical path is front-loaded and total wall-clock time is minimized.

## Goal

Load per-validator wall-clock runtimes from the previous run and sort `TestWork` items so the longest-running validators execute first. Tests with no prior runtime data run before all known tests to avoid new slow tests hiding at the end.

## Decisions

| Question | Decision |
|---|---|
| Storage location | `<log-dir>/runtime-cache.json` |
| CI integration | Caller configures path; CI saves/restores that file |
| Granularity | Validator (package) level — wall-clock time per `go test` invocation |
| Fallback for unknowns | Sort to the front (treated as infinite duration) |
| Cache update strategy | Full replacement — no merging with previous entries |

## Data Model

**File:** `<log-dir>/runtime-cache.json`

```json
{
  "version": 1,
  "updated_at": "2026-02-23T12:00:00Z",
  "runtimes": {
    "TestCheckWithdrawals": "4m20s",
    "github.com/ethereum-optimism/infra/op-tests/some/pkg": "8m0s"
  }
}
```

- Keys are `TestWork.ResultKey` values: `validator.FuncName` for named tests, `validator.Package` for RunAll tests.
- Durations are stored as Go duration strings (e.g. `"4m20s"`) using `time.Duration.String()` / `time.ParseDuration()`.
- The file is a full snapshot of the most recently completed run — not a merge of all historical runs.

## Architecture

### New file: `runner/runtime_cache.go`

Contains the cache type and three functions:

```go
type RuntimeCache struct {
    Version   int
    UpdatedAt time.Time
    Runtimes  map[string]time.Duration
}

func LoadRuntimeCache(path string) (*RuntimeCache, error)
func SaveRuntimeCache(path string, cache *RuntimeCache) error
func SortWorkByRuntime(items []TestWork, cache *RuntimeCache)
```

### Loading (startup)

In `NewTestRunner`, after the runner struct is initialized:

```go
cachePath := filepath.Join(cfg.LogDir, "runtime-cache.json")
r.runtimeCache, _ = LoadRuntimeCache(cachePath)  // error already logged inside
r.runtimeCachePath = cachePath
```

If `cfg.LogDir` is empty, skip cache load entirely.

### Sorting (pre-execution)

In `runAllTestsParallel`, immediately after `collectTestWork()`:

```go
workItems := r.collectTestWork()
SortWorkByRuntime(workItems, r.runtimeCache)
```

Same sort applied in serial mode before iterating gates.

Sort order: unknowns first, then known validators longest-first.

### Writing (post-run)

After each run completes (both serial and parallel paths), before returning `RunnerResult`:

```go
updatedRuntimes := map[string]time.Duration{}
for _, gateResult := range result.Gates {
    for _, testResult := range gateResult.Tests {
        key := r.getTestKey(testResult.Metadata)
        updatedRuntimes[key] = testResult.Duration
    }
    for _, suiteResult := range gateResult.Suites {
        for _, testResult := range suiteResult.Tests {
            key := r.getTestKey(testResult.Metadata)
            updatedRuntimes[key] = testResult.Duration
        }
    }
}
SaveRuntimeCache(r.runtimeCachePath, newCache)
```

`SaveRuntimeCache` writes atomically: marshal to a temp file in the same directory, then `os.Rename`.

## Error Handling

- **Cache load failure** (corrupt JSON, permission error): log a warning, proceed with empty cache. The run is never blocked.
- **Cache save failure**: log a warning, don't fail the run. Worst case: next run has no runtime data.
- **Empty `LogDir`**: skip cache load and save entirely.

## Testing

- `LoadRuntimeCache`: missing file returns empty cache; valid file round-trips; corrupt file returns empty cache without panic.
- `SaveRuntimeCache`: atomic write (temp + rename); output parses back correctly.
- `SortWorkByRuntime`: unknowns sort before knowns; knowns sort longest-first; stable for equal durations.
- Existing runner integration tests pass unchanged — cache is purely additive.

## Files Changed

| File | Change |
|---|---|
| `runner/runtime_cache.go` | New — cache type, load, save, sort |
| `runner/runner.go` | Add `runtimeCache` and `runtimeCachePath` fields to `runner` struct; load cache in `NewTestRunner`; sort work items in parallel and serial paths; write cache after run |
| `runner/runtime_cache_test.go` | New — unit tests for all three functions |
