package reporting

import (
	"testing"
	"time"

	"github.com/ethereum-optimism/infra/op-acceptor/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSubtestDataStructure verifies that subtests have proper parent relationships and ordering
func TestSubtestDataStructure(t *testing.T) {
	// Create test results with a parent test and subtests
	testResults := []*types.TestResult{
		{
			Metadata: types.ValidatorMetadata{
				ID:       "test-1",
				FuncName: "TestParent",
				Package:  "github.com/example/test",
				Gate:     "test-gate",
				Suite:    "test-suite",
			},
			Status:   types.TestStatusPass,
			Duration: 100 * time.Millisecond,
			SubTests: map[string]*types.TestResult{
				"SubTest1": {
					Metadata: types.ValidatorMetadata{
						ID:       "subtest-1",
						FuncName: "SubTest1",
						Package:  "github.com/example/test",
						Gate:     "test-gate",
						Suite:    "test-suite",
					},
					Status:   types.TestStatusPass,
					Duration: 50 * time.Millisecond,
				},
				"SubTest2": {
					Metadata: types.ValidatorMetadata{
						ID:       "subtest-2",
						FuncName: "SubTest2",
						Package:  "github.com/example/test",
						Gate:     "test-gate",
						Suite:    "test-suite",
					},
					Status:   types.TestStatusFail,
					Duration: 75 * time.Millisecond,
				},
			},
		},
		{
			Metadata: types.ValidatorMetadata{
				ID:       "test-2",
				FuncName: "TestOther",
				Package:  "github.com/example/test",
				Gate:     "test-gate",
				Suite:    "test-suite",
			},
			Status:   types.TestStatusPass,
			Duration: 200 * time.Millisecond,
		},
	}

	// Build report data
	builder := NewReportBuilder()
	reportData := builder.BuildFromTestResults(testResults, "test-run", "test-network", "test-gate")

	// Verify we have exactly 3 tests in AllTests: SubTest1, SubTest2, and TestOther
	// (TestParent is excluded because it has subtests)
	require.Len(t, reportData.AllTests, 3, "Should have 3 tests in AllTests (2 subtests + 1 standalone)")

	// Find the subtests and verify their properties
	var subtest1, subtest2, standaloneTest *ReportTestItem
	for i := range reportData.AllTests {
		item := &reportData.AllTests[i]
		switch item.Name {
		case "SubTest1":
			subtest1 = item
		case "SubTest2":
			subtest2 = item
		case "TestOther":
			standaloneTest = item
		}
	}

	// Verify subtests are properly identified
	require.NotNil(t, subtest1, "SubTest1 should be found")
	require.NotNil(t, subtest2, "SubTest2 should be found")
	require.NotNil(t, standaloneTest, "TestOther should be found")

	// Verify subtest properties
	assert.True(t, subtest1.IsSubTest, "SubTest1 should be marked as subtest")
	assert.Equal(t, "TestParent", subtest1.ParentTest, "SubTest1 should have correct parent")
	assert.Equal(t, types.TestStatusPass, subtest1.Status, "SubTest1 should have correct status")

	assert.True(t, subtest2.IsSubTest, "SubTest2 should be marked as subtest")
	assert.Equal(t, "TestParent", subtest2.ParentTest, "SubTest2 should have correct parent")
	assert.Equal(t, types.TestStatusFail, subtest2.Status, "SubTest2 should have correct status")

	// Verify standalone test properties
	assert.False(t, standaloneTest.IsSubTest, "TestOther should not be marked as subtest")
	assert.Empty(t, standaloneTest.ParentTest, "TestOther should have no parent")
	assert.Equal(t, types.TestStatusPass, standaloneTest.Status, "TestOther should have correct status")

	// Verify execution ordering is sequential
	assert.Greater(t, subtest1.ExecutionOrder, 0, "SubTest1 should have execution order")
	assert.Greater(t, subtest2.ExecutionOrder, 0, "SubTest2 should have execution order")
	assert.Greater(t, standaloneTest.ExecutionOrder, 0, "TestOther should have execution order")

	// Verify subtests come after their parent in execution order
	// (even though parent is not in AllTests, it gets execution order 1)
	assert.Greater(t, subtest1.ExecutionOrder, 1, "SubTest1 should come after parent")
	assert.Greater(t, subtest2.ExecutionOrder, 1, "SubTest2 should come after parent")
}

// TestHTMLTemplateDataGeneration verifies that the HTML template receives the correct data
func TestHTMLTemplateDataGeneration(t *testing.T) {
	templateContent := `Tests={{len .Tests}} Total={{.Total}} Passed={{.Passed}} Failed={{.Failed}}`

	formatter, err := NewHTMLFormatter(templateContent)
	require.NoError(t, err)

	testResults := []*types.TestResult{
		{
			Metadata: types.ValidatorMetadata{
				ID:       "parent-test",
				FuncName: "TestMainFunction",
				Package:  "github.com/example/pkg",
				Gate:     "main-gate",
				Suite:    "main-suite",
			},
			Status:   types.TestStatusPass,
			Duration: 100 * time.Millisecond,
			SubTests: map[string]*types.TestResult{
				"Subtest_A": {
					Metadata: types.ValidatorMetadata{
						ID:       "subtest-a",
						FuncName: "Subtest_A",
						Package:  "github.com/example/pkg",
						Gate:     "main-gate",
						Suite:    "main-suite",
					},
					Status:   types.TestStatusPass,
					Duration: 50 * time.Millisecond,
				},
			},
		},
	}

	builder := NewReportBuilder()
	reportData := builder.BuildFromTestResults(testResults, "test-run", "test-network", "main-gate")

	html, err := formatter.Format(reportData)
	require.NoError(t, err)

	// Verify the template receives correct counts
	// Only subtest appears in Tests list (parent excluded), but total stats include both
	assert.Contains(t, html, "Tests=1", "Should have 1 test in template data (subtest only)")
	assert.Contains(t, html, "Total=2", "Should have 2 total tests in stats (parent + subtest)")
	assert.Contains(t, html, "Passed=2", "Should have 2 passed tests (parent + subtest)")
	assert.Contains(t, html, "Failed=0", "Should have 0 failed tests")
}

// TestSubtestOrderingBehavior tests the logical ordering behavior without HTML coupling
func TestSubtestOrderingBehavior(t *testing.T) {
	testResults := []*types.TestResult{
		// Parent test that should be excluded from AllTests
		{
			Metadata: types.ValidatorMetadata{
				ID:       "parent",
				FuncName: "TestParent",
				Package:  "pkg",
				Gate:     "gate",
			},
			Status:   types.TestStatusPass,
			Duration: 100 * time.Millisecond,
			SubTests: map[string]*types.TestResult{
				"SubA": {
					Metadata: types.ValidatorMetadata{FuncName: "SubA", Package: "pkg", Gate: "gate"},
					Status:   types.TestStatusPass,
					Duration: 30 * time.Millisecond,
				},
				"SubB": {
					Metadata: types.ValidatorMetadata{FuncName: "SubB", Package: "pkg", Gate: "gate"},
					Status:   types.TestStatusFail,
					Duration: 40 * time.Millisecond,
				},
			},
		},
		// Standalone test
		{
			Metadata: types.ValidatorMetadata{
				ID:       "standalone",
				FuncName: "TestStandalone",
				Package:  "pkg",
				Gate:     "gate",
			},
			Status:   types.TestStatusPass,
			Duration: 200 * time.Millisecond,
		},
	}

	builder := NewReportBuilder()
	reportData := builder.BuildFromTestResults(testResults, "run", "net", "gate")

	// Verify structure
	assert.Len(t, reportData.AllTests, 3, "Should have 3 tests: 2 subtests + 1 standalone")

	// Verify ordering by execution order
	executionOrders := make([]int, len(reportData.AllTests))
	for i, test := range reportData.AllTests {
		executionOrders[i] = test.ExecutionOrder
	}

	// Should be in ascending order
	for i := 1; i < len(executionOrders); i++ {
		assert.Greater(t, executionOrders[i], executionOrders[i-1],
			"Tests should be ordered by execution order")
	}

	// Verify subtests have correct parent references
	subtestCount := 0
	for _, test := range reportData.AllTests {
		if test.IsSubTest {
			subtestCount++
			assert.Equal(t, "TestParent", test.ParentTest,
				"Subtests should reference correct parent")
		}
	}
	assert.Equal(t, 2, subtestCount, "Should have exactly 2 subtests")
}
