package runner

import (
	"strings"
	"testing"

	"github.com/ethereum-optimism/infra/op-acceptor/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseMultiPackageEvents(t *testing.T) {
	// Simulate events from two packages as they'd appear in a raw_go_events.log
	events := `{"Time":"2024-01-01T00:00:00Z","Action":"start","Package":"./tests/pkgA"}
{"Time":"2024-01-01T00:00:00Z","Action":"run","Package":"./tests/pkgA","Test":"TestAlpha"}
{"Time":"2024-01-01T00:00:00Z","Action":"start","Package":"./tests/pkgB"}
{"Time":"2024-01-01T00:00:00Z","Action":"run","Package":"./tests/pkgB","Test":"TestBeta"}
{"Time":"2024-01-01T00:00:01Z","Action":"output","Package":"./tests/pkgA","Test":"TestAlpha","Output":"alpha running\n"}
{"Time":"2024-01-01T00:00:01Z","Action":"pass","Package":"./tests/pkgA","Test":"TestAlpha","Elapsed":1.0}
{"Time":"2024-01-01T00:00:01Z","Action":"pass","Package":"./tests/pkgA","Elapsed":1.0}
{"Time":"2024-01-01T00:00:02Z","Action":"output","Package":"./tests/pkgB","Test":"TestBeta","Output":"--- FAIL: TestBeta\n"}
{"Time":"2024-01-01T00:00:02Z","Action":"fail","Package":"./tests/pkgB","Test":"TestBeta","Elapsed":2.0}
{"Time":"2024-01-01T00:00:02Z","Action":"fail","Package":"./tests/pkgB","Elapsed":2.0}`

	results, err := ParseMultiPackageEvents(strings.NewReader(events))
	require.NoError(t, err)
	require.Len(t, results, 2, "should produce one result per package")

	// Results should be sorted by package name
	assert.Equal(t, "./tests/pkgA", results[0].Metadata.Package)
	assert.Equal(t, "./tests/pkgB", results[1].Metadata.Package)

	// pkgA should pass
	assert.Equal(t, types.TestStatusPass, results[0].Status)

	// pkgB should fail
	assert.Equal(t, types.TestStatusFail, results[1].Status)
}

func TestParseMultiPackageEventsEmpty(t *testing.T) {
	results, err := ParseMultiPackageEvents(strings.NewReader(""))
	require.NoError(t, err)
	assert.Nil(t, results)
}

func TestParseMultiPackageEventsNilReader(t *testing.T) {
	_, err := ParseMultiPackageEvents(nil)
	assert.Error(t, err)
}

func TestParseMultiPackageEventsNonJSON(t *testing.T) {
	// Lines that aren't valid JSON should be skipped
	events := `not json
{"Time":"2024-01-01T00:00:00Z","Action":"start","Package":"./tests/pkg"}
also not json
{"Time":"2024-01-01T00:00:01Z","Action":"pass","Package":"./tests/pkg","Elapsed":1.0}`

	results, err := ParseMultiPackageEvents(strings.NewReader(events))
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "./tests/pkg", results[0].Metadata.Package)
}
