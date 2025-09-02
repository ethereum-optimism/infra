package runner

import (
	"context"
	"os/exec"
	"testing"
	"time"

	"github.com/ethereum-optimism/infra/op-acceptor/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewTestExecutor tests the NewTestExecutor constructor function
func TestNewTestExecutor(t *testing.T) {
	// Valid inputs for successful cases
	validTestDir := "/tmp/test"
	validTimeout := time.Minute
	validGoBinary := "go"
	validEnvProvider := func() Env { return nil }
	validCmdBuilder := func(ctx context.Context, name string, arg ...string) (*exec.Cmd, func()) {
		return &exec.Cmd{}, func() {}
	}
	validOutputParser := &mockOutputParser{}
	validJSONStore := &mockJSONStore{}

	tests := []struct {
		name        string
		testDir     string
		timeout     time.Duration
		goBinary    string
		envProvider func() Env
		cmdBuilder  func(ctx context.Context, name string, arg ...string) (*exec.Cmd, func())
		parser      OutputParser
		jsonStore   JSONStore
		expectError bool
		errorMsg    string
	}{
		{
			name:        "valid inputs should succeed",
			testDir:     validTestDir,
			timeout:     validTimeout,
			goBinary:    validGoBinary,
			envProvider: validEnvProvider,
			cmdBuilder:  validCmdBuilder,
			parser:      validOutputParser,
			jsonStore:   validJSONStore,
			expectError: false,
		},
		{
			name:        "empty goBinary should use default and succeed",
			testDir:     validTestDir,
			timeout:     validTimeout,
			goBinary:    "", // Should use DefaultGoBinary
			envProvider: validEnvProvider,
			cmdBuilder:  validCmdBuilder,
			parser:      validOutputParser,
			jsonStore:   validJSONStore,
			expectError: false,
		},
		{
			name:        "nil jsonStore should succeed (optional parameter)",
			testDir:     validTestDir,
			timeout:     validTimeout,
			goBinary:    validGoBinary,
			envProvider: validEnvProvider,
			cmdBuilder:  validCmdBuilder,
			parser:      validOutputParser,
			jsonStore:   nil, // JSONStore is optional
			expectError: false,
		},
		{
			name:        "empty testDir should return error",
			testDir:     "",
			timeout:     validTimeout,
			goBinary:    validGoBinary,
			envProvider: validEnvProvider,
			cmdBuilder:  validCmdBuilder,
			parser:      validOutputParser,
			jsonStore:   validJSONStore,
			expectError: true,
			errorMsg:    "testDir cannot be empty",
		},
		{
			name:        "nil envProvider should return error",
			testDir:     validTestDir,
			timeout:     validTimeout,
			goBinary:    validGoBinary,
			envProvider: nil,
			cmdBuilder:  validCmdBuilder,
			parser:      validOutputParser,
			jsonStore:   validJSONStore,
			expectError: true,
			errorMsg:    "envProvider cannot be nil",
		},
		{
			name:        "nil cmdBuilder should return error",
			testDir:     validTestDir,
			timeout:     validTimeout,
			goBinary:    validGoBinary,
			envProvider: validEnvProvider,
			cmdBuilder:  nil,
			parser:      validOutputParser,
			jsonStore:   validJSONStore,
			expectError: true,
			errorMsg:    "cmdBuilder cannot be nil",
		},
		{
			name:        "nil outputParser should return error",
			testDir:     validTestDir,
			timeout:     validTimeout,
			goBinary:    validGoBinary,
			envProvider: validEnvProvider,
			cmdBuilder:  validCmdBuilder,
			parser:      nil,
			jsonStore:   validJSONStore,
			expectError: true,
			errorMsg:    "outputParser cannot be nil",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			executor, err := NewTestExecutor(
				tt.testDir,
				tt.timeout,
				tt.goBinary,
				tt.envProvider,
				tt.cmdBuilder,
				tt.parser,
				tt.jsonStore,
			)

			if tt.expectError {
				assert.Error(t, err, "Expected an error but got none")
				assert.Nil(t, executor, "Expected nil executor when error occurs")
				assert.Equal(t, tt.errorMsg, err.Error(), "Error message should match expected")
			} else {
				assert.NoError(t, err, "Unexpected error: %v", err)
				require.NotNil(t, executor, "Expected valid executor, got nil")

				// Verify the executor is properly initialized
				testExec, ok := executor.(*testExecutor)
				require.True(t, ok, "Expected *testExecutor type")

				assert.Equal(t, tt.testDir, testExec.testDir, "testDir should be set correctly")
				assert.Equal(t, tt.timeout, testExec.timeout, "timeout should be set correctly")

				// Check goBinary handling
				expectedGoBinary := tt.goBinary
				if expectedGoBinary == "" {
					expectedGoBinary = DefaultGoBinary
				}
				assert.Equal(t, expectedGoBinary, testExec.goBinary, "goBinary should be set correctly")

				assert.NotNil(t, testExec.envProvider, "envProvider should be set")
				assert.NotNil(t, testExec.cmdBuilder, "cmdBuilder should be set")
				assert.NotNil(t, testExec.outputParser, "outputParser should be set")

				// jsonStore can be nil, so we just check it's set to what we passed
				assert.Equal(t, tt.jsonStore, testExec.jsonStore, "jsonStore should be set correctly")
			}
		})
	}
}

// TestNewTestExecutor_EdgeCases tests edge cases and boundary conditions
func TestNewTestExecutor_EdgeCases(t *testing.T) {
	t.Run("zero timeout should be allowed", func(t *testing.T) {
		executor, err := NewTestExecutor(
			"/tmp/test",
			0, // Zero timeout
			"go",
			func() Env { return nil },
			func(ctx context.Context, name string, arg ...string) (*exec.Cmd, func()) {
				return &exec.Cmd{}, func() {}
			},
			&mockOutputParser{},
			&mockJSONStore{},
		)

		assert.NoError(t, err)
		assert.NotNil(t, executor)
	})

	t.Run("negative timeout should be allowed", func(t *testing.T) {
		executor, err := NewTestExecutor(
			"/tmp/test",
			-time.Second, // Negative timeout
			"go",
			func() Env { return nil },
			func(ctx context.Context, name string, arg ...string) (*exec.Cmd, func()) {
				return &exec.Cmd{}, func() {}
			},
			&mockOutputParser{},
			&mockJSONStore{},
		)

		assert.NoError(t, err)
		assert.NotNil(t, executor)
	})

	t.Run("whitespace-only testDir should return error", func(t *testing.T) {
		executor, err := NewTestExecutor(
			"   ", // Whitespace only
			time.Minute,
			"go",
			func() Env { return nil },
			func(ctx context.Context, name string, arg ...string) (*exec.Cmd, func()) {
				return &exec.Cmd{}, func() {}
			},
			&mockOutputParser{},
			&mockJSONStore{},
		)

		// Note: Current implementation only checks for empty string, not whitespace
		// This test documents current behavior - whitespace is currently allowed
		assert.NoError(t, err)
		assert.NotNil(t, executor)
	})
}

// TestNewTestExecutor_DefaultGoBinaryBehavior specifically tests the goBinary default handling
func TestNewTestExecutor_DefaultGoBinaryBehavior(t *testing.T) {
	testCases := []struct {
		name           string
		inputGoBinary  string
		expectedBinary string
	}{
		{
			name:           "empty string uses default",
			inputGoBinary:  "",
			expectedBinary: DefaultGoBinary,
		},
		{
			name:           "custom binary is preserved",
			inputGoBinary:  "/usr/local/bin/go",
			expectedBinary: "/usr/local/bin/go",
		},
		{
			name:           "go binary name is preserved",
			inputGoBinary:  "go1.21",
			expectedBinary: "go1.21",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			executor, err := NewTestExecutor(
				"/tmp/test",
				time.Minute,
				tc.inputGoBinary,
				func() Env { return nil },
				func(ctx context.Context, name string, arg ...string) (*exec.Cmd, func()) {
					return &exec.Cmd{}, func() {}
				},
				&mockOutputParser{},
				&mockJSONStore{},
			)

			require.NoError(t, err)
			require.NotNil(t, executor)

			testExec := executor.(*testExecutor)
			assert.Equal(t, tc.expectedBinary, testExec.goBinary)
		})
	}
}

// Mock implementations for testing
type mockOutputParser struct{}

func (m *mockOutputParser) Parse(output []byte, metadata types.ValidatorMetadata) *types.TestResult {
	return &types.TestResult{Metadata: metadata, Status: types.TestStatusPass}
}

func (m *mockOutputParser) ParseWithTimeout(output []byte, metadata types.ValidatorMetadata, timeout time.Duration) *types.TestResult {
	return &types.TestResult{Metadata: metadata, Status: types.TestStatusPass}
}

type mockJSONStore struct{}

func (m *mockJSONStore) Store(testID string, rawJSON []byte) {}

func (m *mockJSONStore) Get(testID string) ([]byte, bool) {
	return nil, false
}
