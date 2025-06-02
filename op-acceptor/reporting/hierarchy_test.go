package reporting

import (
	"strings"
	"testing"
	"time"

	"github.com/ethereum-optimism/infra/op-acceptor/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDepthBasedHierarchy tests the new depth-based hierarchy system
func TestDepthBasedHierarchy(t *testing.T) {
	testResults := []*types.TestResult{
		// Top-level test
		{
			Metadata: types.ValidatorMetadata{
				ID:       "test-1",
				FuncName: "TestTopLevel",
				Package:  "github.com/example/test",
				Gate:     "test-gate",
			},
			Status:   types.TestStatusPass,
			Duration: 100 * time.Millisecond,
		},
		// First level subtest
		{
			Metadata: types.ValidatorMetadata{
				ID:       "test-2",
				FuncName: "TestParent/SubTest1",
				Package:  "github.com/example/test",
				Gate:     "test-gate",
			},
			Status:   types.TestStatusPass,
			Duration: 50 * time.Millisecond,
		},
		// Second level subtest
		{
			Metadata: types.ValidatorMetadata{
				ID:       "test-3",
				FuncName: "TestParent/SubTest1/SubSubTest1",
				Package:  "github.com/example/test",
				Gate:     "test-gate",
			},
			Status:   types.TestStatusPass,
			Duration: 25 * time.Millisecond,
		},
		// Third level subtest
		{
			Metadata: types.ValidatorMetadata{
				ID:       "test-4",
				FuncName: "TestParent/SubTest1/SubSubTest1/DeepSubTest",
				Package:  "github.com/example/test",
				Gate:     "test-gate",
			},
			Status:   types.TestStatusFail,
			Duration: 10 * time.Millisecond,
		},
		// Another first level subtest
		{
			Metadata: types.ValidatorMetadata{
				ID:       "test-5",
				FuncName: "TestParent/SubTest2",
				Package:  "github.com/example/test",
				Gate:     "test-gate",
			},
			Status:   types.TestStatusSkip,
			Duration: 5 * time.Millisecond,
		},
	}

	builder := NewReportBuilder()
	reportData := builder.BuildFromTestResults(testResults, "test-run", "test-network", "test-gate")

	// Verify all tests are included (parent test excluded since it has children)
	require.Len(t, reportData.AllTests, 5, "Should have 5 tests")

	// Find tests by name and verify their hierarchy
	testsByName := make(map[string]*ReportTestItem)
	for i := range reportData.AllTests {
		item := &reportData.AllTests[i]
		testsByName[item.Name] = item
	}

	// Verify TestTopLevel
	topLevel := testsByName["TestTopLevel"]
	require.NotNil(t, topLevel, "TestTopLevel should exist")
	assert.Equal(t, 0, topLevel.Depth, "TestTopLevel should have depth 0")
	assert.False(t, topLevel.IsSubTest, "TestTopLevel should not be a subtest")
	assert.Equal(t, []string{"TestTopLevel"}, topLevel.HierarchyPath, "TestTopLevel should have correct path")

	// Verify SubTest1
	subTest1 := testsByName["SubTest1"]
	require.NotNil(t, subTest1, "SubTest1 should exist")
	assert.Equal(t, 1, subTest1.Depth, "SubTest1 should have depth 1")
	assert.True(t, subTest1.IsSubTest, "SubTest1 should be a subtest")
	assert.Equal(t, []string{"TestParent", "SubTest1"}, subTest1.HierarchyPath, "SubTest1 should have correct path")
	assert.Equal(t, "TestParent", subTest1.ParentTest, "SubTest1 should have correct parent")

	// Verify SubSubTest1
	subSubTest1 := testsByName["SubSubTest1"]
	require.NotNil(t, subSubTest1, "SubSubTest1 should exist")
	assert.Equal(t, 2, subSubTest1.Depth, "SubSubTest1 should have depth 2")
	assert.True(t, subSubTest1.IsSubTest, "SubSubTest1 should be a subtest")
	assert.Equal(t, []string{"TestParent", "SubTest1", "SubSubTest1"}, subSubTest1.HierarchyPath, "SubSubTest1 should have correct path")
	assert.Equal(t, "SubTest1", subSubTest1.ParentTest, "SubSubTest1 should have correct parent")

	// Verify DeepSubTest
	deepSubTest := testsByName["DeepSubTest"]
	require.NotNil(t, deepSubTest, "DeepSubTest should exist")
	assert.Equal(t, 3, deepSubTest.Depth, "DeepSubTest should have depth 3")
	assert.True(t, deepSubTest.IsSubTest, "DeepSubTest should be a subtest")
	assert.Equal(t, []string{"TestParent", "SubTest1", "SubSubTest1", "DeepSubTest"}, deepSubTest.HierarchyPath, "DeepSubTest should have correct path")
	assert.Equal(t, "SubSubTest1", deepSubTest.ParentTest, "DeepSubTest should have correct parent")

	// Verify SubTest2
	subTest2 := testsByName["SubTest2"]
	require.NotNil(t, subTest2, "SubTest2 should exist")
	assert.Equal(t, 1, subTest2.Depth, "SubTest2 should have depth 1")
	assert.True(t, subTest2.IsSubTest, "SubTest2 should be a subtest")
	assert.Equal(t, []string{"TestParent", "SubTest2"}, subTest2.HierarchyPath, "SubTest2 should have correct path")
	assert.Equal(t, "TestParent", subTest2.ParentTest, "SubTest2 should have correct parent")
}

// TestTreePrefixGeneration tests the new tree prefix generation
func TestTreePrefixGeneration(t *testing.T) {
	tests := []struct {
		name           string
		depth          int
		isLast         bool
		parentIsLast   []bool
		expectedPrefix string
	}{
		{
			name:           "top level",
			depth:          0,
			isLast:         false,
			parentIsLast:   []bool{},
			expectedPrefix: "",
		},
		{
			name:           "first subtest, not last",
			depth:          1,
			isLast:         false,
			parentIsLast:   []bool{},
			expectedPrefix: "├── ",
		},
		{
			name:           "first subtest, last",
			depth:          1,
			isLast:         true,
			parentIsLast:   []bool{},
			expectedPrefix: "└── ",
		},
		{
			name:           "second level, parent not last",
			depth:          2,
			isLast:         false,
			parentIsLast:   []bool{false},
			expectedPrefix: "│   ├── ",
		},
		{
			name:           "second level, parent last",
			depth:          2,
			isLast:         false,
			parentIsLast:   []bool{true},
			expectedPrefix: "    ├── ",
		},
		{
			name:           "third level, complex",
			depth:          3,
			isLast:         true,
			parentIsLast:   []bool{false, true},
			expectedPrefix: "│       └── ",
		},
		{
			name:           "deep nesting",
			depth:          5,
			isLast:         false,
			parentIsLast:   []bool{false, true, false, false},
			expectedPrefix: "│       │   │   ├── ",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := generateTreePrefixByDepth(tt.depth, tt.isLast, tt.parentIsLast)
			assert.Equal(t, tt.expectedPrefix, result, "Tree prefix should match expected")
		})
	}
}

// TestTableFormatterWithDeepNesting tests table formatting with deep nesting
func TestTableFormatterWithDeepNesting(t *testing.T) {
	testResults := []*types.TestResult{
		{
			Metadata: types.ValidatorMetadata{
				ID:       "test-1",
				FuncName: "TestA/Sub1/SubSub1",
				Package:  "pkg",
				Gate:     "gate",
			},
			Status:   types.TestStatusPass,
			Duration: 10 * time.Millisecond,
		},
		{
			Metadata: types.ValidatorMetadata{
				ID:       "test-2",
				FuncName: "TestA/Sub1/SubSub2",
				Package:  "pkg",
				Gate:     "gate",
			},
			Status:   types.TestStatusFail,
			Duration: 20 * time.Millisecond,
		},
		{
			Metadata: types.ValidatorMetadata{
				ID:       "test-3",
				FuncName: "TestA/Sub2",
				Package:  "pkg",
				Gate:     "gate",
			},
			Status:   types.TestStatusPass,
			Duration: 15 * time.Millisecond,
		},
		{
			Metadata: types.ValidatorMetadata{
				ID:       "test-4",
				FuncName: "TestB",
				Package:  "pkg",
				Gate:     "gate",
			},
			Status:   types.TestStatusPass,
			Duration: 30 * time.Millisecond,
		},
	}

	builder := NewReportBuilder()
	reportData := builder.BuildFromTestResults(testResults, "test-run", "test-network", "test-gate")

	formatter := NewTableFormatter("Test Results", true)
	result, err := formatter.Format(reportData)
	require.NoError(t, err)

	// Verify the output contains properly nested structure
	assert.Contains(t, result, "TestB", "Should contain TestB")
	assert.Contains(t, result, "SubSub1", "Should contain SubSub1")
	assert.Contains(t, result, "SubSub2", "Should contain SubSub2")
	assert.Contains(t, result, "Sub2", "Should contain Sub2")

	// The exact tree structure will depend on implementation,
	// but we should at least verify all tests are present
	lines := strings.Split(result, "\n")
	var testLines []string
	for _, line := range lines {
		if strings.Contains(line, "SubSub1") || strings.Contains(line, "SubSub2") ||
			strings.Contains(line, "Sub2") || strings.Contains(line, "TestB") {
			testLines = append(testLines, line)
		}
	}

	assert.Len(t, testLines, 4, "Should have 4 test lines in output")
}

// TestTypesParseTestNameHierarchy tests the hierarchy parsing function
func TestTypesParseTestNameHierarchy(t *testing.T) {
	tests := []struct {
		testName      string
		expectedDepth int
		expectedPath  []string
	}{
		{
			testName:      "",
			expectedDepth: 0,
			expectedPath:  []string{},
		},
		{
			testName:      "TestSimple",
			expectedDepth: 0,
			expectedPath:  []string{"TestSimple"},
		},
		{
			testName:      "TestParent/SubTest",
			expectedDepth: 1,
			expectedPath:  []string{"TestParent", "SubTest"},
		},
		{
			testName:      "TestParent/SubTest/SubSubTest",
			expectedDepth: 2,
			expectedPath:  []string{"TestParent", "SubTest", "SubSubTest"},
		},
		{
			testName:      "TestA/B/C/D/E",
			expectedDepth: 4,
			expectedPath:  []string{"TestA", "B", "C", "D", "E"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.testName, func(t *testing.T) {
			depth, path := types.ParseTestNameHierarchy(tt.testName)
			assert.Equal(t, tt.expectedDepth, depth, "Depth should match")
			assert.Equal(t, tt.expectedPath, path, "Path should match")
		})
	}
}
