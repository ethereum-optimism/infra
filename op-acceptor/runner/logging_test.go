package runner

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ethereum-optimism/infra/op-acceptor/types"
	"github.com/ethereum/go-ethereum/log"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFailingTestStdoutLogging verifies that stdout is captured when tests fail
func TestFailingTestStdoutLogging(t *testing.T) {
	// Setup test with a failing test that outputs to stdout
	testContent := []byte(`
package feature_test

import (
	"fmt"
	"testing"
)

func TestWithStdout(t *testing.T) {
	fmt.Println("This is some stdout output that should be captured")
	fmt.Println("This is a second line of output")
	t.Error("This test deliberately fails")
}
`)

	configContent := []byte(`
gates:
  - id: logging-gate
    description: "Gate with a test that outputs to stdout"
    suites:
      logging-suite:
        description: "Suite with a failing test that outputs to stdout"
        tests:
          - name: TestWithStdout
            package: "./feature"
`)

	// Setup the test runner
	r := setupTestRunner(t, testContent, configContent)

	// Run the test
	result, err := r.RunAllTests(context.Background())
	require.NoError(t, err)
	assert.Equal(t, types.TestStatusFail, result.Status)

	// Verify the structure
	require.Contains(t, result.Gates, "logging-gate")
	gate := result.Gates["logging-gate"]
	require.Contains(t, gate.Suites, "logging-suite")
	suite := gate.Suites["logging-suite"]

	// Get the failing test
	var failingTest *types.TestResult
	for _, test := range suite.Tests {
		failingTest = test
		break
	}
	require.NotNil(t, failingTest)

	// Verify the test failed and has stdout captured
	assert.Equal(t, types.TestStatusFail, failingTest.Status)
	assert.NotNil(t, failingTest.Error)
	assert.NotEmpty(t, failingTest.Stdout)
	assert.Contains(t, failingTest.Stdout, "This is some stdout output that should be captured")
	assert.Contains(t, failingTest.Stdout, "This is a second line of output")
}

// TestLogLevelEnvironment verifies that the TEST_LOG_LEVEL environment variable is correctly set and used
func TestLogLevelEnvironment(t *testing.T) {
	ctx := context.Background()

	// Create a simple test file in the work directory
	testContent := []byte(`
package main

import (
	"os"
	"testing"
)

func TestLogLevelEnvironment(t *testing.T) {
    // Get log level from environment
    logLevel := os.Getenv("TEST_LOG_LEVEL")
    if logLevel == "" {
		t.Log("TEST_LOG_LEVEL not set")
    } else {
		t.Log("TEST_LOG_LEVEL set to", logLevel)
	}
}
`)
	configContent := []byte(`
gates:
  - id: logging-gate
    description: "Gate with a test that outputs logs"
    suites:
      logging-suite:
        description: "Suite with a test that outputs logs"
        tests:
          - name: TestLogLevelEnvironment
            package: "./main"
`)

	r := setupTestRunner(t, testContent, configContent)
	r.testLogLevel = "debug"
	err := os.WriteFile(filepath.Join(r.workDir, "main_test.go"), testContent, 0644)
	require.NoError(t, err)

	result, err := r.RunTest(ctx, types.ValidatorMetadata{
		ID:       "test1",
		Gate:     "logging-gate",
		FuncName: "TestLogLevelEnvironment",
		Package:  ".",
	})

	require.NoError(t, err)
	assert.Equal(t, types.TestStatusPass, result.Status)
	assert.Equal(t, "test1", result.Metadata.ID)
	assert.Equal(t, "logging-gate", result.Metadata.Gate)
	assert.Equal(t, ".", result.Metadata.Package)
	assert.False(t, result.Metadata.RunAll)
	assert.Contains(t, result.Stdout, "TEST_LOG_LEVEL set to debug")
}

// testLogger implements the go-ethereum/log.Logger interface
type testLogger struct {
	logFn func(msg string)
}

func (l *testLogger) formatMessage(msg string, ctx ...interface{}) string {
	if len(ctx) == 0 {
		return msg
	}

	// Format key-value pairs
	var pairs []string
	for i := 0; i < len(ctx); i += 2 {
		if i+1 < len(ctx) {
			pairs = append(pairs, fmt.Sprintf("%v=%v", ctx[i], ctx[i+1]))
		}
	}

	if len(pairs) > 0 {
		return fmt.Sprintf("%s %s", msg, strings.Join(pairs, " "))
	}
	return msg
}

func (l *testLogger) Crit(msg string, ctx ...interface{}) {
	l.logFn(l.formatMessage(msg, ctx...))
}

func (l *testLogger) CritContext(_ context.Context, msg string, ctx ...interface{}) {
	l.logFn(l.formatMessage(msg, ctx...))
}

func (l *testLogger) Error(msg string, ctx ...interface{}) {
	l.logFn(l.formatMessage(msg, ctx...))
}

func (l *testLogger) ErrorContext(_ context.Context, msg string, ctx ...interface{}) {
	l.logFn(l.formatMessage(msg, ctx...))
}

func (l *testLogger) Warn(msg string, ctx ...interface{}) {
	l.logFn(l.formatMessage(msg, ctx...))
}

func (l *testLogger) WarnContext(_ context.Context, msg string, ctx ...interface{}) {
	l.logFn(l.formatMessage(msg, ctx...))
}

func (l *testLogger) Info(msg string, ctx ...interface{}) {
	l.logFn(l.formatMessage(msg, ctx...))
}

func (l *testLogger) InfoContext(_ context.Context, msg string, ctx ...interface{}) {
	l.logFn(l.formatMessage(msg, ctx...))
}

func (l *testLogger) Debug(msg string, ctx ...interface{}) {
	l.logFn(l.formatMessage(msg, ctx...))
}

func (l *testLogger) DebugContext(_ context.Context, msg string, ctx ...interface{}) {
	l.logFn(l.formatMessage(msg, ctx...))
}

func (l *testLogger) Trace(msg string, ctx ...interface{}) {
	l.logFn(l.formatMessage(msg, ctx...))
}

func (l *testLogger) TraceContext(_ context.Context, msg string, ctx ...interface{}) {
	l.logFn(l.formatMessage(msg, ctx...))
}

func (l *testLogger) New(ctx ...interface{}) log.Logger {
	return l
}

func (l *testLogger) Enabled(ctx context.Context, level slog.Level) bool {
	return true // Always enabled for testing
}

func (l *testLogger) With(ctx ...interface{}) log.Logger {
	return l
}

func (l *testLogger) Handler() slog.Handler {
	return nil // Not needed for testing
}

func (l *testLogger) Log(level slog.Level, msg string, ctx ...interface{}) {
	l.logFn(l.formatMessage(msg, ctx...))
}

func (l *testLogger) LogAttrs(_ context.Context, level slog.Level, msg string, attrs ...slog.Attr) {
	ctx := make([]interface{}, 0, len(attrs))
	for _, attr := range attrs {
		ctx = append(ctx, attr.Key, attr.Value.String())
	}
	l.logFn(l.formatMessage(msg, ctx...))
}

func (l *testLogger) Write(level slog.Level, msg string, attrs ...any) {
	l.logFn(l.formatMessage(msg, attrs...))
}

func (l *testLogger) WriteCtx(_ context.Context, level slog.Level, msg string, attrs ...any) {
	l.logFn(l.formatMessage(msg, attrs...))
}

func (l *testLogger) SetContext(_ context.Context) {
	// No-op
}

// TestOutputRealtimeLogs verifies that test logs are output in real-time when outputRealtimeLogs is enabled
func TestOutputRealtimeLogs(t *testing.T) {
	// Create a test file that outputs logs over time
	testContent := []byte(`
package feature_test

import (
	"fmt"
	"testing"
	"time"
)

func TestWithRealtimeLogs(t *testing.T) {
	fmt.Println("First log message")
	time.Sleep(100 * time.Millisecond)
	fmt.Println("Second log message")
	time.Sleep(100 * time.Millisecond)
	fmt.Println("Third log message")
	time.Sleep(200 * time.Millisecond)
}
`)

	configContent := []byte(`
gates:
  - id: logging-gate
    description: "Gate with a test that outputs logs in real-time"
    suites:
      logging-suite:
        description: "Suite with a test that outputs logs in real-time"
        tests:
          - name: TestWithRealtimeLogs
            package: "./feature"
`)

	logChan := make(chan string, 10)

	customLogger := &testLogger{
		logFn: func(msg string) {
			logChan <- msg
		},
	}

	r := setupTestRunner(t, testContent, configContent)
	r.outputRealtimeLogs = true
	r.log = customLogger

	// Run the test in a goroutine
	done := make(chan struct{})
	go func() {
		defer close(done)
		result, err := r.RunAllTests(context.Background())
		require.NoError(t, err)
		assert.Equal(t, types.TestStatusPass, result.Status)
	}()

	expectedLogs := []string{
		"First log message",
		"Second log message",
		"Third log message",
	}

	time.Sleep(10 * time.Millisecond)

	// Track which message we're expecting next
	nextExpectedIndex := 0
	timeout := time.After(1000 * time.Millisecond)

	for nextExpectedIndex < len(expectedLogs) {
		select {
		case msg := <-logChan:
			if strings.Contains(msg, expectedLogs[nextExpectedIndex]) {
				nextExpectedIndex++
				t.Logf("Received expected message: %s", msg)
			}
		case <-timeout:
			t.Fatalf("Did not receive all messages in order. Got %d/%d messages. Next expected: %s",
				nextExpectedIndex, len(expectedLogs), expectedLogs[nextExpectedIndex])
		}
	}

	// Wait for the test to complete
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Test did not complete in time")
	}
}

// TestOutputRealtimeLogsDisabled verifies that test logs are output in real-time when outputRealtimeLogs is disabled
func TestOutputRealtimeLogsDisabled(t *testing.T) {
	// Create a test file that outputs logs over time
	testContent := []byte(`
package feature_test

import (
	"fmt"
	"testing"
	"time"
)

func TestWithRealtimeLogs(t *testing.T) {
	fmt.Println("First log message")
	time.Sleep(100 * time.Millisecond)
	fmt.Println("Second log message")
	time.Sleep(100 * time.Millisecond)
	fmt.Println("Third log message")
	time.Sleep(200 * time.Millisecond)
}
`)

	configContent := []byte(`
gates:
  - id: logging-gate
    description: "Gate with a test that outputs logs in real-time"
    suites:
      logging-suite:
        description: "Suite with a test that outputs logs in real-time"
        tests:
          - name: TestWithRealtimeLogs
            package: "./feature"
`)

	logChan := make(chan string, 10)
	done := make(chan struct{})

	customLogger := &testLogger{
		logFn: func(msg string) {
			select {
			case logChan <- msg:
			default:
				// Channel is full, log is dropped
			}
		},
	}

	r := setupTestRunner(t, testContent, configContent)
	r.outputRealtimeLogs = false
	r.log = customLogger

	// Run the test in a goroutine
	go func() {
		defer close(done)
		result, err := r.RunAllTests(context.Background())
		require.NoError(t, err)
		assert.Equal(t, types.TestStatusPass, result.Status)
	}()

	// Wait for the running message to be logged
	timeout := time.After(5 * time.Second)
	found := false
	for !found {
		select {
		case msg := <-logChan:
			if strings.Contains(msg, "go test ./feature -run ^TestWithRealtimeLogs$") {
				found = true
				t.Logf("Found expected running message: %s", msg)
			} else {
				t.Logf("Received unexpected message: %s", msg)
			}
		case <-timeout:
			t.Fatal("Did not receive running message in time")
		}
	}

	// Wait for the test to complete
	select {
	case <-done:
		// Test completed successfully
	case <-timeout:
		t.Fatal("Test did not complete in time")
	}

	// Verify no test output messages were received
	select {
	case msg := <-logChan:
		if strings.Contains(msg, "First log message") ||
			strings.Contains(msg, "Second log message") ||
			strings.Contains(msg, "Third log message") {
			t.Fatalf("Received unexpected test output message: %s", msg)
		}
	default:
		// No messages in channel, which is what we want
	}
}
