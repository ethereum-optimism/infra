package runner

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ethereum-optimism/infra/op-acceptor/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestProgressLogging verifies that progress logs are output when ShowProgress is enabled
func TestProgressLogging(t *testing.T) {
	// Create test content with predictable delays
	testContent := []byte(`
package feature_test

import (
	"testing"
	"time"
)

func TestProgressOne(t *testing.T) {
	time.Sleep(1 * time.Second)
	t.Log("Progress test one completed")
}

func TestProgressTwo(t *testing.T) {
	time.Sleep(2 * time.Second)
	t.Log("Progress test two completed")
}

func TestProgressThree(t *testing.T) {
	time.Sleep(1 * time.Second)
	t.Log("Progress test one completed")
}

func TestProgressFour(t *testing.T) {
	time.Sleep(1 * time.Second)
	t.Log("Progress test one completed")
}
`)

	configContent := []byte(`
gates:
  - id: progress-gate
    description: "Gate for testing progress logging"
    suites:
      progress-suite:
        description: "Suite for testing progress logging"
        tests:
          - name: TestProgressOne
            package: "./feature"
          - name: TestProgressTwo
            package: "./feature"
          - name: TestProgressThree
            package: "./feature"
          - name: TestProgressFour
            package: "./feature"
`)

	// Capture progress logs
	var progressLogs []string
	var gateStartLogs []string
	var suiteStartLogs []string
	var mu sync.Mutex

	customLogger := &testLogger{
		logFn: func(msg string) {
			mu.Lock()
			defer mu.Unlock()

			// Capture different types of progress-related log messages
			if strings.Contains(msg, "progress update") && strings.Contains(msg, "gate=progress-gate") {
				progressLogs = append(progressLogs, msg)
			} else if strings.Contains(msg, "Starting gate") && strings.Contains(msg, "progress-gate") {
				gateStartLogs = append(gateStartLogs, msg)
			} else if strings.Contains(msg, "Starting suite") && strings.Contains(msg, "progress-suite") {
				suiteStartLogs = append(suiteStartLogs, msg)
			}
		},
	}

	r := setupTestRunner(
		t,
		testContent,
		configContent,
		WithLogger(customLogger),
		WithShowProgress(true),
		WithProgressInterval(200*time.Millisecond),
	)

	// Run the test
	result, err := r.RunAllTests(context.Background())
	require.NoError(t, err)
	assert.Equal(t, types.TestStatusPass, result.Status)

	// Verify logs were captured
	mu.Lock()
	defer mu.Unlock()

	// Log what we captured for debugging
	t.Logf("Captured %d progress update logs", len(progressLogs))
	t.Logf("Captured %d gate start logs", len(gateStartLogs))
	t.Logf("Captured %d suite start logs", len(suiteStartLogs))

	for i, log := range progressLogs {
		t.Logf("Progress log %d: %s", i+1, log)
	}

	// Assertions
	assert.Greater(t, len(gateStartLogs), 0, "Should have captured gate start log")
	assert.Greater(t, len(suiteStartLogs), 0, "Should have captured suite start log")
	assert.Greater(t, len(progressLogs), 0, "Should have captured progress update logs")

	// With a 200ms interval and tests taking 1s and 2s, we should get multiple progress updates
	// TestProgressOne (1s) should generate ~5 updates, TestProgressTwo (2s) should generate ~10 total
	// Expect at least 8-10 progress updates during test execution
	require.GreaterOrEqual(t, len(progressLogs), 8, "Should have captured multiple progress updates during 3s total execution")

	// Verify the progress logs contain expected content
	foundRunningTests := false
	for _, logMsg := range progressLogs {
		if strings.Contains(logMsg, "details=") && strings.Contains(logMsg, "TestProgress") {
			foundRunningTests = true
			break
		}
	}
	require.True(t, foundRunningTests, "Should have found progress logs with running tests information")

	// Verify that the last progress log only contains TestProgressTwo (TestProgressOne should be completed and removed)
	lastLog := progressLogs[len(progressLogs)-1]
	assert.Contains(t, lastLog, "TestProgressTwo", "Last progress log should contain TestProgressTwo")
	assert.NotContains(t, lastLog, "TestProgressOne", "Last progress log should NOT contain TestProgressOne (it should be completed)")
}

// TestProgressLoggingDisabled verifies that no progress logs are output when ShowProgress is disabled
func TestProgressLoggingDisabled(t *testing.T) {
	testContent := []byte(`
package feature_test

import (
	"testing"
	"time"
)

func TestProgressOne(t *testing.T) {
	time.Sleep(100 * time.Millisecond)
	t.Log("Progress test one completed")
}
`)

	configContent := []byte(`
gates:
  - id: progress-gate
    description: "Gate for testing progress logging disabled"
    suites:
      progress-suite:
        description: "Suite for testing progress logging disabled"
        tests:
          - name: TestProgressOne
            package: "./feature"
`)

	// Capture any progress logs (there shouldn't be any)
	var progressLogs []string
	var mu sync.Mutex

	customLogger := &testLogger{
		logFn: func(msg string) {
			mu.Lock()
			defer mu.Unlock()

			// Only capture progress update logs (not gate/suite start logs which might still happen)
			if strings.Contains(msg, "progress update") {
				progressLogs = append(progressLogs, msg)
			}
		},
	}

	r := setupTestRunner(
		t,
		testContent,
		configContent,
		WithLogger(customLogger),
		WithShowProgress(false),
	)

	// Run the test
	result, err := r.RunAllTests(context.Background())
	require.NoError(t, err)
	assert.Equal(t, types.TestStatusPass, result.Status)

	// Verify no progress update logs were captured
	mu.Lock()
	defer mu.Unlock()

	t.Logf("Captured %d progress logs (should be 0)", len(progressLogs))
	for i, log := range progressLogs {
		t.Logf("Unexpected progress log %d: %s", i+1, log)
	}

	assert.Empty(t, progressLogs, "Should not have captured any progress update logs when disabled")
}

func TestFormatRunningTests(t *testing.T) {
	baseTime := time.Now()

	tests := []struct {
		name         string
		runningTests map[string]time.Time
		maxShow      int
		expected     string
	}{
		{
			name:         "empty map",
			runningTests: map[string]time.Time{},
			maxShow:      3,
			expected:     "",
		},
		{
			name: "single test",
			runningTests: map[string]time.Time{
				"TestOne": baseTime.Add(-2 * time.Second),
			},
			maxShow:  3,
			expected: "TestOne (2s)",
		},
		{
			name: "multiple tests sorted by duration",
			runningTests: map[string]time.Time{
				"TestOne":   baseTime.Add(-1 * time.Second),
				"TestTwo":   baseTime.Add(-3 * time.Second),
				"TestThree": baseTime.Add(-2 * time.Second),
			},
			maxShow:  3,
			expected: "TestTwo (3s), TestThree (2s), TestOne (1s)",
		},
		{
			name: "respects maxShow limit",
			runningTests: map[string]time.Time{
				"TestOne":   baseTime.Add(-1 * time.Second),
				"TestTwo":   baseTime.Add(-4 * time.Second),
				"TestThree": baseTime.Add(-3 * time.Second),
				"TestFour":  baseTime.Add(-2 * time.Second),
			},
			maxShow:  2,
			expected: "TestTwo (4s), TestThree (3s)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatRunningTests(tt.runningTests, tt.maxShow)
			assert.Equal(t, tt.expected, result)
		})
	}
}
