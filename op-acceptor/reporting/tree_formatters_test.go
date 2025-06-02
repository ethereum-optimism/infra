package reporting

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/ethereum-optimism/infra/op-acceptor/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetStatusString(t *testing.T) {
	tests := []struct {
		name     string
		status   types.TestStatus
		expected string
	}{
		{
			name:     "pass",
			status:   types.TestStatusPass,
			expected: "pass",
		},
		{
			name:     "fail",
			status:   types.TestStatusFail,
			expected: "fail",
		},
		{
			name:     "skip",
			status:   types.TestStatusSkip,
			expected: "skip",
		},
		{
			name:     "error",
			status:   types.TestStatusError,
			expected: "error",
		},
		{
			name:     "unknown",
			status:   types.TestStatus("invalid"),
			expected: "unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getStatusString(tt.status)
			if result != tt.expected {
				t.Errorf("getStatusString(%v) = %q, expected %q", tt.status, result, tt.expected)
			}
		})
	}
}

func TestTreeTableFormatterGetStatusString(t *testing.T) {
	formatter := NewTreeTableFormatter("test", true, false)

	tests := []struct {
		name     string
		status   types.TestStatus
		expected string
	}{
		{
			name:     "pass",
			status:   types.TestStatusPass,
			expected: "pass",
		},
		{
			name:     "fail",
			status:   types.TestStatusFail,
			expected: "fail",
		},
		{
			name:     "skip",
			status:   types.TestStatusSkip,
			expected: "skip",
		},
		{
			name:     "error",
			status:   types.TestStatusError,
			expected: "error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatter.GetStatusString(tt.status)
			if result != tt.expected {
				t.Errorf("TreeTableFormatter.GetStatusString(%v) = %q, expected %q", tt.status, result, tt.expected)
			}
		})
	}
}

func TestTreeJSONFormatterWithStructs(t *testing.T) {
	// Create a simple test tree
	tree := &types.TestTree{
		RunID:       "test-run-123",
		NetworkName: "test-network",
		Timestamp:   time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC),
		Duration:    500 * time.Millisecond,
		Stats: types.TestTreeStats{
			Total:    2,
			Passed:   1,
			Failed:   1,
			Skipped:  0,
			Errored:  0,
			PassRate: 50.0,
		},
		Root: &types.TestTreeNode{
			ID:   "root",
			Name: "Test Results",
			Type: types.NodeTypeRoot,
		},
		FailedNodes: []*types.TestTreeNode{},
	}

	// Add some test nodes
	passedTest := &types.TestTreeNode{
		ID:             "test-1",
		Name:           "TestPassing",
		Type:           types.NodeTypeTest,
		Status:         types.TestStatusPass,
		Duration:       200 * time.Millisecond,
		ExecutionOrder: 1,
		Package:        "example",
		Gate:           "gate1",
		Suite:          "suite1",
		Depth:          1,
	}

	failedTest := &types.TestTreeNode{
		ID:             "test-2",
		Name:           "TestFailing",
		Type:           types.NodeTypeTest,
		Status:         types.TestStatusFail,
		Duration:       300 * time.Millisecond,
		ExecutionOrder: 2,
		Package:        "example",
		Gate:           "gate1",
		Suite:          "suite1",
		Depth:          1,
	}

	tree.TestNodes = []*types.TestTreeNode{passedTest, failedTest}
	tree.FailedNodes = []*types.TestTreeNode{failedTest}

	// Test the formatter
	formatter := NewTreeJSONFormatter(false, true)
	result, err := formatter.Format(tree)
	require.NoError(t, err)

	// Parse the JSON to verify structure
	var parsed TreeJSONResponse
	err = json.Unmarshal([]byte(result), &parsed)
	require.NoError(t, err)

	// Verify the main fields
	assert.Equal(t, "test-run-123", parsed.RunID)
	assert.Equal(t, "test-network", parsed.NetworkName)
	assert.Equal(t, time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC), parsed.Timestamp)
	assert.Equal(t, 500*time.Millisecond, parsed.Duration)
	assert.Equal(t, 2, parsed.Stats.Total)
	assert.Equal(t, 1, parsed.Stats.Passed)
	assert.Equal(t, 1, parsed.Stats.Failed)

	// Verify test nodes
	require.Len(t, parsed.Tests, 2)

	// Check first test (passing)
	test1 := parsed.Tests[0]
	assert.Equal(t, "test-1", test1.ID)
	assert.Equal(t, "TestPassing", test1.Name)
	assert.Equal(t, types.NodeTypeTest, test1.Type)
	assert.Equal(t, types.TestStatusPass, test1.Status)
	assert.Equal(t, 200*time.Millisecond, test1.Duration)
	assert.Equal(t, "example", test1.Package)
	assert.Equal(t, "gate1", test1.Gate)
	assert.Equal(t, "suite1", test1.Suite)
	assert.Equal(t, "", test1.Error) // Should be empty for passing test

	// Check second test (failing)
	test2 := parsed.Tests[1]
	assert.Equal(t, "test-2", test2.ID)
	assert.Equal(t, "TestFailing", test2.Name)
	assert.Equal(t, types.NodeTypeTest, test2.Type)
	assert.Equal(t, types.TestStatusFail, test2.Status)
	assert.Equal(t, 300*time.Millisecond, test2.Duration)

	// Verify failed tests list
	require.Len(t, parsed.FailedTests, 1)

	// Verify hierarchy is included
	assert.NotNil(t, parsed.Hierarchy)
	assert.Equal(t, "root", parsed.Hierarchy.ID)
	assert.Equal(t, types.NodeTypeRoot, parsed.Hierarchy.Type)
}

func TestTreeJSONFormatterOmitsEmptyFields(t *testing.T) {
	// Create a minimal test tree to verify omitempty works
	tree := &types.TestTree{
		RunID:     "test-run-minimal",
		Timestamp: time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC),
		Duration:  100 * time.Millisecond,
		Stats: types.TestTreeStats{
			Total:    1,
			Passed:   1,
			Failed:   0,
			Skipped:  0,
			Errored:  0,
			PassRate: 100.0,
		},
		Root: &types.TestTreeNode{
			ID:   "root",
			Name: "Test Results",
			Type: types.NodeTypeRoot,
		},
		TestNodes:   []*types.TestTreeNode{},
		FailedNodes: []*types.TestTreeNode{},
	}

	formatter := NewTreeJSONFormatter(false, false) // No hierarchy
	result, err := formatter.Format(tree)
	require.NoError(t, err)

	// Parse JSON to verify omitempty behavior
	var jsonMap map[string]interface{}
	err = json.Unmarshal([]byte(result), &jsonMap)
	require.NoError(t, err)

	// NetworkName should be omitted (empty string with omitempty)
	_, hasNetworkName := jsonMap["networkName"]
	assert.False(t, hasNetworkName, "Empty networkName should be omitted")

	// Hierarchy should be omitted (nil pointer with omitempty)
	_, hasHierarchy := jsonMap["hierarchy"]
	assert.False(t, hasHierarchy, "Nil hierarchy should be omitted")

	// Tests should be empty array but present
	tests, hasTests := jsonMap["tests"]
	assert.True(t, hasTests, "Tests array should be present")
	assert.IsType(t, []interface{}{}, tests)
	assert.Len(t, tests, 0)

	// FailedTests should be empty array but present
	failedTests, hasFailedTests := jsonMap["failedTests"]
	assert.True(t, hasFailedTests, "FailedTests array should be present")
	assert.IsType(t, []interface{}{}, failedTests)
	assert.Len(t, failedTests, 0)
}
