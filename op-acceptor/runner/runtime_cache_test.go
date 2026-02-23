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
