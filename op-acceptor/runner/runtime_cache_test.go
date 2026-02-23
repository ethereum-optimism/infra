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

func TestSaveRuntimeCache_NilCache(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "runtime-cache.json")
	assert.NoError(t, SaveRuntimeCache(path, nil))
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

func TestSaveRuntimeCache_PartialParseRoundTrip(t *testing.T) {
	// A cache file with one valid and one invalid duration entry should load
	// only the valid entry (the invalid one is silently skipped).
	dir := t.TempDir()
	path := filepath.Join(dir, "runtime-cache.json")
	content := `{"version":1,"updated_at":"2026-02-23T12:00:00Z","runtimes":{"TestGood":"4m20s","TestBad":"not-a-duration"}}`
	require.NoError(t, os.WriteFile(path, []byte(content), 0644))

	cache, err := LoadRuntimeCache(path)
	require.NoError(t, err)
	assert.Equal(t, 4*time.Minute+20*time.Second, cache.Runtimes["TestGood"])
	assert.NotContains(t, cache.Runtimes, "TestBad", "unparseable entry should be silently skipped")
}

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

func TestSortWorkByRuntime_NilCache(t *testing.T) {
	items := []TestWork{
		{ResultKey: "TestA"},
		{ResultKey: "TestB"},
	}
	// Should not panic
	assert.NotPanics(t, func() {
		SortWorkByRuntime(items, nil)
	})
}
