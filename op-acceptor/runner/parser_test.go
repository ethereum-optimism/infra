package runner

import (
	"strings"
	"testing"
	"time"

	"github.com/ethereum-optimism/infra/op-acceptor/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewOutputParser(t *testing.T) {
	parser := NewOutputParser()
	assert.NotNil(t, parser, "NewOutputParser should return non-nil parser")
}

func TestOutputParser_Parse(t *testing.T) {
	parser := NewOutputParser()

	tests := []struct {
		name         string
		output       string
		metadata     types.ValidatorMetadata
		wantStatus   types.TestStatus
		wantError    bool
		wantSubTests int
	}{
		{
			name:   "empty output",
			output: "",
			metadata: types.ValidatorMetadata{
				FuncName: "TestExample",
				Package:  "example/pkg",
			},
			wantStatus:   types.TestStatusFail,
			wantError:    true,
			wantSubTests: 0,
		},
		{
			name: "passing test",
			output: `{"Time":"2023-05-01T12:00:00Z","Action":"start","Package":"example/pkg","Test":"TestExample"}
{"Time":"2023-05-01T12:00:01Z","Action":"pass","Package":"example/pkg","Test":"TestExample","Elapsed":1.0}`,
			metadata: types.ValidatorMetadata{
				FuncName: "TestExample",
				Package:  "example/pkg",
			},
			wantStatus:   types.TestStatusPass,
			wantError:    false,
			wantSubTests: 0,
		},
		{
			name: "failing test with output",
			output: `{"Time":"2023-05-01T12:00:00Z","Action":"start","Package":"example/pkg","Test":"TestExample"}
{"Time":"2023-05-01T12:00:00.1Z","Action":"output","Package":"example/pkg","Test":"TestExample","Output":"Test failed with error\n"}
{"Time":"2023-05-01T12:00:01Z","Action":"fail","Package":"example/pkg","Test":"TestExample","Elapsed":1.0}`,
			metadata: types.ValidatorMetadata{
				FuncName: "TestExample",
				Package:  "example/pkg",
			},
			wantStatus:   types.TestStatusFail,
			wantError:    true,
			wantSubTests: 0,
		},
		{
			name: "test with subtests",
			output: `{"Time":"2023-05-01T12:00:00Z","Action":"start","Package":"example/pkg","Test":"TestExample"}
{"Time":"2023-05-01T12:00:00.1Z","Action":"start","Package":"example/pkg","Test":"TestExample/SubTest1"}
{"Time":"2023-05-01T12:00:00.2Z","Action":"pass","Package":"example/pkg","Test":"TestExample/SubTest1","Elapsed":0.1}
{"Time":"2023-05-01T12:00:00.3Z","Action":"start","Package":"example/pkg","Test":"TestExample/SubTest2"}
{"Time":"2023-05-01T12:00:00.4Z","Action":"fail","Package":"example/pkg","Test":"TestExample/SubTest2","Elapsed":0.1}
{"Time":"2023-05-01T12:00:01Z","Action":"fail","Package":"example/pkg","Test":"TestExample","Elapsed":1.0}`,
			metadata: types.ValidatorMetadata{
				FuncName: "TestExample",
				Package:  "example/pkg",
			},
			wantStatus:   types.TestStatusFail,
			wantError:    false, // Main test doesn't have its own error output
			wantSubTests: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parser.Parse([]byte(tt.output), tt.metadata)

			require.NotNil(t, result, "Parse should return non-nil result")
			assert.Equal(t, tt.wantStatus, result.Status, "Status should match expected")
			assert.Equal(t, tt.wantError, result.Error != nil, "Error presence should match expected")
			assert.Len(t, result.SubTests, tt.wantSubTests, "Should have expected number of subtests")

			// Verify metadata is preserved
			assert.Equal(t, tt.metadata.FuncName, result.Metadata.FuncName, "FuncName should be preserved")
			assert.Equal(t, tt.metadata.Package, result.Metadata.Package, "Package should be preserved")
		})
	}
}

func TestOutputParser_ParseWithTimeout(t *testing.T) {
	parser := NewOutputParser()
	timeout := 500 * time.Millisecond

	output := `{"Time":"2023-05-01T12:00:00Z","Action":"start","Package":"example/pkg","Test":"TestExample"}
{"Time":"2023-05-01T12:00:01Z","Action":"pass","Package":"example/pkg","Test":"TestExample","Elapsed":1.0}`

	metadata := types.ValidatorMetadata{
		FuncName: "TestExample",
		Package:  "example/pkg",
	}

	result := parser.ParseWithTimeout([]byte(output), metadata, timeout)

	require.NotNil(t, result, "ParseWithTimeout should return non-nil result")

	// Since elapsed time (1.0s) exceeds timeout (500ms), it should be marked as failed
	assert.Equal(t, types.TestStatusFail, result.Status, "Should be marked as failed due to timeout")
	assert.True(t, result.TimedOut, "Should be marked as timed out")
	assert.NotNil(t, result.Error, "Should have timeout error")
	assert.Contains(t, strings.ToLower(result.Error.Error()), "timed out", "Error should mention timed out")
}

func TestOutputParser_ParseWithTimeout_PreservesCompletedSubtests(t *testing.T) {
	parser := NewOutputParser()
	timeout := 500 * time.Millisecond

	// SubTest1 passes, SubTest2 starts but never completes
	output := `{"Time":"2023-05-01T12:00:00Z","Action":"start","Package":"example/pkg","Test":"TestExample"}
{"Time":"2023-05-01T12:00:00.10Z","Action":"start","Package":"example/pkg","Test":"TestExample/SubTest1"}
{"Time":"2023-05-01T12:00:00.20Z","Action":"pass","Package":"example/pkg","Test":"TestExample/SubTest1","Elapsed":0.10}
{"Time":"2023-05-01T12:00:00.30Z","Action":"start","Package":"example/pkg","Test":"TestExample/SubTest2"}`

	metadata := types.ValidatorMetadata{
		FuncName: "TestExample",
		Package:  "example/pkg",
	}

	result := parser.ParseWithTimeout([]byte(output), metadata, timeout)
	require.NotNil(t, result)
	assert.True(t, result.TimedOut)

	// SubTest1 remains Pass
	sub1, ok := result.SubTests["TestExample/SubTest1"]
	require.True(t, ok)
	assert.Equal(t, types.TestStatusPass, sub1.Status)
	assert.False(t, sub1.TimedOut)

	// SubTest2 marked as timed out (started but did not complete)
	sub2, ok := result.SubTests["TestExample/SubTest2"]
	require.True(t, ok)
	assert.Equal(t, types.TestStatusFail, sub2.Status)
	assert.True(t, sub2.TimedOut)
	require.NotNil(t, sub2.Error)
	assert.Contains(t, sub2.Error.Error(), "SUBTEST TIMEOUT")
}

func TestCalculateTestDuration(t *testing.T) {
	baseTime := time.Date(2023, 5, 1, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name  string
		start time.Time
		end   time.Time
		want  time.Duration
	}{
		{
			name:  "valid duration",
			start: baseTime,
			end:   baseTime.Add(5 * time.Second),
			want:  5 * time.Second,
		},
		{
			name:  "zero start time",
			start: time.Time{},
			end:   baseTime,
			want:  0,
		},
		{
			name:  "zero end time",
			start: baseTime,
			end:   time.Time{},
			want:  0,
		},
		{
			name:  "negative duration",
			start: baseTime.Add(5 * time.Second),
			end:   baseTime,
			want:  0, // Should return 0 for negative duration
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := calculateTestDuration(tt.start, tt.end)
			assert.Equal(t, tt.want, got, "Duration should match expected")
		})
	}
}

func TestParseTestEvent(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
		want    TestEvent
	}{
		{
			name:    "valid event",
			input:   `{"Time":"2023-05-01T12:00:00Z","Action":"start","Package":"example/pkg","Test":"TestExample"}`,
			wantErr: false,
			want: TestEvent{
				Time:    time.Date(2023, 5, 1, 12, 0, 0, 0, time.UTC),
				Action:  "start",
				Package: "example/pkg",
				Test:    "TestExample",
			},
		},
		{
			name:    "invalid JSON",
			input:   `{invalid json}`,
			wantErr: true,
		},
		{
			name:    "empty input",
			input:   "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseTestEvent([]byte(tt.input))

			if tt.wantErr {
				assert.Error(t, err, "Should return error for invalid input")
			} else {
				require.NoError(t, err, "Should not return error for valid input")
				assert.Equal(t, tt.want.Time, got.Time, "Time should match")
				assert.Equal(t, tt.want.Action, got.Action, "Action should match")
				assert.Equal(t, tt.want.Package, got.Package, "Package should match")
				assert.Equal(t, tt.want.Test, got.Test, "Test should match")
			}
		})
	}
}

func TestOutputParser_ParsePackageMode(t *testing.T) {
	parser := NewOutputParser()

	// Real package mode JSON output with individual test timing
	output := `{"Time":"2025-08-21T12:00:00Z","Action":"start","Package":"example/pkg","Test":""}
{"Time":"2025-08-21T12:00:01Z","Action":"run","Package":"example/pkg","Test":"TestOne"}
{"Time":"2025-08-21T12:00:02Z","Action":"pass","Package":"example/pkg","Test":"TestOne","Elapsed":1.0}
{"Time":"2025-08-21T12:00:03Z","Action":"run","Package":"example/pkg","Test":"TestTwo"}
{"Time":"2025-08-21T12:00:05Z","Action":"pass","Package":"example/pkg","Test":"TestTwo","Elapsed":2.0}
{"Time":"2025-08-21T12:00:06Z","Action":"pass","Package":"example/pkg","Test":"","Elapsed":6.0}`

	// Package mode - empty FuncName
	metadata := types.ValidatorMetadata{
		FuncName: "", // Package mode
		Package:  "example/pkg",
	}

	result := parser.Parse([]byte(output), metadata)

	require.NotNil(t, result, "Parse should return non-nil result")
	assert.Equal(t, types.TestStatusPass, result.Status, "Package should pass")
	assert.Equal(t, 6*time.Second, result.Duration, "Package should have correct total duration")

	// Should have individual tests as subtests
	require.Len(t, result.SubTests, 2, "Should have 2 individual tests as subtests")

	// Check TestOne timing
	testOne, exists := result.SubTests["TestOne"]
	require.True(t, exists, "TestOne should exist as subtest")
	assert.Equal(t, types.TestStatusPass, testOne.Status, "TestOne should pass")
	assert.Equal(t, 1*time.Second, testOne.Duration, "TestOne should have correct duration from calculated time")
	assert.Equal(t, "TestOne", testOne.Metadata.FuncName, "TestOne should have correct FuncName")

	// Check TestTwo timing
	testTwo, exists := result.SubTests["TestTwo"]
	require.True(t, exists, "TestTwo should exist as subtest")
	assert.Equal(t, types.TestStatusPass, testTwo.Status, "TestTwo should pass")
	assert.Equal(t, 2*time.Second, testTwo.Duration, "TestTwo should have correct duration from calculated time")
	assert.Equal(t, "TestTwo", testTwo.Metadata.FuncName, "TestTwo should have correct FuncName")
}

func TestOutputParser_ParsePackageModeWithElapsedFallback(t *testing.T) {
	parser := NewOutputParser()

	// Package mode output where only Elapsed field is available (no run events)
	output := `{"Time":"2025-08-21T12:00:00Z","Action":"start","Package":"example/pkg","Test":""}
{"Time":"2025-08-21T12:00:03Z","Action":"pass","Package":"example/pkg","Test":"TestOne","Elapsed":1.5}
{"Time":"2025-08-21T12:00:06Z","Action":"pass","Package":"example/pkg","Test":"TestTwo","Elapsed":2.5}
{"Time":"2025-08-21T12:00:07Z","Action":"pass","Package":"example/pkg","Test":"","Elapsed":7.0}`

	metadata := types.ValidatorMetadata{
		FuncName: "",
		Package:  "example/pkg",
	}

	result := parser.Parse([]byte(output), metadata)

	require.NotNil(t, result, "Parse should return non-nil result")
	require.Len(t, result.SubTests, 2, "Should have 2 subtests")

	// Check that Elapsed field is used when no run events
	testOne := result.SubTests["TestOne"]
	assert.Equal(t, 1500*time.Millisecond, testOne.Duration, "TestOne should use Elapsed field")

	testTwo := result.SubTests["TestTwo"]
	assert.Equal(t, 2500*time.Millisecond, testTwo.Duration, "TestTwo should use Elapsed field")
}

// This tests a timing regression where single test mode
// tests were showing 0ms duration because package-level start/end events
// (with Test="") weren't being recognized as main test events.
func TestOutputParser_ParseSingleTestModeWithPackageLevelEvents(t *testing.T) {
	parser := NewOutputParser()

	// Real single test mode output where start/end events have Test=""
	// This is what happens when running a single test like "go test -run TestChainFork"
	output := `{"Time":"2025-08-21T12:00:00Z","Action":"start","Package":"example/pkg","Test":""}
{"Time":"2025-08-21T12:00:00.5Z","Action":"run","Package":"example/pkg","Test":"TestChainFork"}
{"Time":"2025-08-21T12:00:01Z","Action":"run","Package":"example/pkg","Test":"TestChainFork/Network_0"}
{"Time":"2025-08-21T12:00:03Z","Action":"pass","Package":"example/pkg","Test":"TestChainFork/Network_0","Elapsed":2.0}
{"Time":"2025-08-21T12:00:03.1Z","Action":"pass","Package":"example/pkg","Test":"TestChainFork","Elapsed":0}
{"Time":"2025-08-21T12:00:03.2Z","Action":"pass","Package":"example/pkg","Test":"","Elapsed":3.2}`

	// Single test mode - specific FuncName
	metadata := types.ValidatorMetadata{
		FuncName: "TestChainFork", // Single test mode
		Package:  "example/pkg",
	}

	result := parser.Parse([]byte(output), metadata)

	require.NotNil(t, result, "Parse should return non-nil result")
	assert.Equal(t, types.TestStatusPass, result.Status, "Test should pass")

	// main test should have duration calculated from start/end times
	// even when those events have Test=""
	assert.Equal(t, 3200*time.Millisecond, result.Duration,
		"Main test should have duration calculated from package-level start/end events")

	// Should have the subtest
	require.Len(t, result.SubTests, 1, "Should have 1 subtest")

	// Check subtest timing
	subtest, exists := result.SubTests["TestChainFork/Network_0"]
	require.True(t, exists, "TestChainFork/Network_0 should exist as subtest")
	assert.Equal(t, types.TestStatusPass, subtest.Status, "Subtest should pass")
	assert.Equal(t, 2*time.Second, subtest.Duration, "Subtest should have correct duration")
}

// Test that timing also works correctly when a test fails (fail events also have Test="")
func TestOutputParser_ParseSingleTestModeWithFailure(t *testing.T) {
	parser := NewOutputParser()

	output := `{"Time":"2025-08-21T12:00:00Z","Action":"start","Package":"example/pkg","Test":""}
{"Time":"2025-08-21T12:00:00.5Z","Action":"run","Package":"example/pkg","Test":"TestFailingTest"}
{"Time":"2025-08-21T12:00:00.5Z","Action":"output","Package":"example/pkg","Test":"TestFailingTest","Output":"=== RUN   TestFailingTest\n"}
{"Time":"2025-08-21T12:00:01.5Z","Action":"output","Package":"example/pkg","Test":"TestFailingTest","Output":"    test.go:10: Test failed\n"}
{"Time":"2025-08-21T12:00:01.5Z","Action":"fail","Package":"example/pkg","Test":"TestFailingTest","Elapsed":0}
{"Time":"2025-08-21T12:00:01.6Z","Action":"fail","Package":"example/pkg","Test":"","Elapsed":1.6}`

	metadata := types.ValidatorMetadata{
		FuncName: "TestFailingTest",
		Package:  "example/pkg",
	}

	result := parser.Parse([]byte(output), metadata)

	require.NotNil(t, result, "Parse should return non-nil result")
	assert.Equal(t, types.TestStatusFail, result.Status, "Test should fail")

	// Even for failing tests, duration should be calculated correctly
	assert.Equal(t, 1600*time.Millisecond, result.Duration,
		"Failed test should still have duration calculated from package-level start/fail events")
}

func TestIsMainTestEvent(t *testing.T) {
	tests := []struct {
		name         string
		event        TestEvent
		mainTestName string
		want         bool
	}{
		{
			name: "matches main test name",
			event: TestEvent{
				Test: "TestExample",
			},
			mainTestName: "TestExample",
			want:         true,
		},
		{
			name: "empty test name matches empty main test name",
			event: TestEvent{
				Test: "",
			},
			mainTestName: "",
			want:         true,
		},
		{
			name: "subtest should not match",
			event: TestEvent{
				Test: "TestExample/SubTest1",
			},
			mainTestName: "TestExample",
			want:         false,
		},
		{
			name: "different test name should not match",
			event: TestEvent{
				Test: "TestOther",
			},
			mainTestName: "TestExample",
			want:         false,
		},
		{
			name: "package-level start event in single test mode should match",
			event: TestEvent{
				Test:   "",
				Action: ActionStart,
			},
			mainTestName: "TestExample",
			want:         true,
		},
		{
			name: "package-level pass event in single test mode should match",
			event: TestEvent{
				Test:   "",
				Action: ActionPass,
			},
			mainTestName: "TestExample",
			want:         true,
		},
		{
			name: "package-level fail event in single test mode should match",
			event: TestEvent{
				Test:   "",
				Action: ActionFail,
			},
			mainTestName: "TestExample",
			want:         true,
		},
		{
			name: "package-level output event in single test mode should NOT match",
			event: TestEvent{
				Test:   "",
				Action: ActionOutput,
			},
			mainTestName: "TestExample",
			want:         false, // Output events are not main test timing events
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isMainTestEvent(tt.event, tt.mainTestName)
			assert.Equal(t, tt.want, got, "Should match expected result")
		})
	}
}

func TestIsSubTestEvent(t *testing.T) {
	tests := []struct {
		name         string
		event        TestEvent
		mainTestName string
		want         bool
	}{
		{
			name: "actual subtest with slash should match",
			event: TestEvent{
				Test: "TestExample/SubTest1",
			},
			mainTestName: "TestExample",
			want:         true,
		},
		{
			name: "nested subtest with multiple slashes should match",
			event: TestEvent{
				Test: "TestExample/SubTest1/NestedTest",
			},
			mainTestName: "TestExample",
			want:         true,
		},
		{
			name: "individual test in package mode should match",
			event: TestEvent{
				Test: "TestSomeFunction",
			},
			mainTestName: "", // Package mode
			want:         true,
		},
		{
			name: "individual test with specific main test should not match",
			event: TestEvent{
				Test: "TestSomeFunction",
			},
			mainTestName: "TestExample", // Single test mode
			want:         false,
		},
		{
			name: "empty test name should not match (no test name)",
			event: TestEvent{
				Test: "",
			},
			mainTestName: "TestExample",
			want:         false,
		},
		{
			name: "empty test name in package mode should not match",
			event: TestEvent{
				Test: "",
			},
			mainTestName: "", // Package mode
			want:         false,
		},
		{
			name: "main test name itself should not match as subtest",
			event: TestEvent{
				Test: "TestExample",
			},
			mainTestName: "TestExample",
			want:         false,
		},
		{
			name: "subtest in package mode should match (both conditions)",
			event: TestEvent{
				Test: "TestExample/SubTest1",
			},
			mainTestName: "", // Package mode
			want:         true,
		},
		{
			name: "test name with slash-like but not subtest should match in package mode",
			event: TestEvent{
				Test: "TestURL/HTTP",
			},
			mainTestName: "", // Package mode
			want:         true,
		},
		{
			name: "test name with slash-like but not subtest should not match in single test mode",
			event: TestEvent{
				Test: "TestURL/HTTP",
			},
			mainTestName: "TestExample",
			want:         true, // Still matches because it contains "/"
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isSubTestEvent(tt.event, tt.mainTestName)
			assert.Equal(t, tt.want, got, "Should match expected result for subtest classification")
		})
	}
}
