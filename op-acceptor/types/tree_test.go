package types

import (
	"errors"
	"testing"
	"time"
)

func TestTestTreeBuilder_BuildFromTestResults(t *testing.T) {
	tests := []struct {
		name        string
		testResults []*TestResult
		runID       string
		networkName string
		want        *TestTree
	}{
		{
			name: "simple package test with subtests",
			testResults: []*TestResult{
				{
					Metadata: ValidatorMetadata{
						Package:  "github.com/test/package",
						FuncName: "",
						Gate:     "base",
						RunAll:   true,
					},
					Status:   TestStatusPass,
					Duration: 5 * time.Second,
					SubTests: map[string]*TestResult{
						"TestFunction": {
							Metadata: ValidatorMetadata{
								FuncName: "TestFunction",
							},
							Status:   TestStatusPass,
							Duration: 2 * time.Second,
						},
						"TestAnotherFunction": {
							Metadata: ValidatorMetadata{
								FuncName: "TestAnotherFunction",
							},
							Status:   TestStatusFail,
							Duration: 1 * time.Second,
							Error:    errors.New("test failed"),
						},
					},
				},
			},
			runID:       "test-run-1",
			networkName: "test-network",
			want: &TestTree{
				RunID:       "test-run-1",
				NetworkName: "test-network",
				Stats: TestTreeStats{
					Total:  2, // 2 subtests (package test is not counted separately when it has subtests)
					Passed: 1, // TestFunction
					Failed: 1, // TestAnotherFunction
				},
			},
		},
		{
			name: "individual test without subtests",
			testResults: []*TestResult{
				{
					Metadata: ValidatorMetadata{
						Package:  "github.com/test/package",
						FuncName: "TestStandalone",
						Gate:     "base",
						RunAll:   false,
					},
					Status:   TestStatusPass,
					Duration: 1 * time.Second,
				},
			},
			runID:       "test-run-2",
			networkName: "test-network",
			want: &TestTree{
				RunID:       "test-run-2",
				NetworkName: "test-network",
				Stats: TestTreeStats{
					Total:  1,
					Passed: 1,
					Failed: 0,
				},
			},
		},
		{
			name: "mixed package and individual tests with duplicates",
			testResults: []*TestResult{
				// Package test with subtests
				{
					Metadata: ValidatorMetadata{
						Package:  "github.com/test/package",
						FuncName: "",
						Gate:     "base",
						RunAll:   true,
					},
					Status:   TestStatusPass,
					Duration: 5 * time.Second,
					SubTests: map[string]*TestResult{
						"TestDuplicate": {
							Metadata: ValidatorMetadata{
								FuncName: "TestDuplicate",
							},
							Status:   TestStatusPass,
							Duration: 2 * time.Second,
						},
					},
				},
				// Individual test that should be deduplicated
				{
					Metadata: ValidatorMetadata{
						Package:  "github.com/test/package",
						FuncName: "TestDuplicate",
						Gate:     "base",
						RunAll:   false,
					},
					Status:   TestStatusPass,
					Duration: 1 * time.Second,
				},
				// Individual test that's not covered by package
				{
					Metadata: ValidatorMetadata{
						Package:  "github.com/test/package",
						FuncName: "TestStandalone",
						Gate:     "base",
						RunAll:   false,
					},
					Status:   TestStatusFail,
					Duration: 1 * time.Second,
					Error:    errors.New("standalone test failed"),
				},
			},
			runID:       "test-run-3",
			networkName: "test-network",
			want: &TestTree{
				RunID:       "test-run-3",
				NetworkName: "test-network",
				Stats: TestTreeStats{
					Total:  2, // TestDuplicate subtest + TestStandalone (individual TestDuplicate is deduplicated)
					Passed: 1, // TestDuplicate subtest
					Failed: 1, // TestStandalone
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			builder := NewTestTreeBuilder()
			got := builder.BuildFromTestResults(tt.testResults, tt.runID, tt.networkName)

			// Check basic properties
			if got.RunID != tt.want.RunID {
				t.Errorf("TestTreeBuilder.BuildFromTestResults() RunID = %v, want %v", got.RunID, tt.want.RunID)
			}
			if got.NetworkName != tt.want.NetworkName {
				t.Errorf("TestTreeBuilder.BuildFromTestResults() NetworkName = %v, want %v", got.NetworkName, tt.want.NetworkName)
			}

			// Check statistics
			if got.Stats.Total != tt.want.Stats.Total {
				t.Errorf("TestTreeBuilder.BuildFromTestResults() Stats.Total = %v, want %v", got.Stats.Total, tt.want.Stats.Total)
				t.Logf("TestNodes count: %d", len(got.TestNodes))
				for i, node := range got.TestNodes {
					t.Logf("TestNode[%d]: Type=%v, Name=%s, Status=%v", i, node.Type, node.Name, node.Status)
				}
			}
			if got.Stats.Passed != tt.want.Stats.Passed {
				t.Errorf("TestTreeBuilder.BuildFromTestResults() Stats.Passed = %v, want %v", got.Stats.Passed, tt.want.Stats.Passed)
			}
			if got.Stats.Failed != tt.want.Stats.Failed {
				t.Errorf("TestTreeBuilder.BuildFromTestResults() Stats.Failed = %v, want %v", got.Stats.Failed, tt.want.Stats.Failed)
			}

			// Check tree structure
			if got.Root == nil {
				t.Fatal("TestTreeBuilder.BuildFromTestResults() Root is nil")
			}
			if got.Root.Type != NodeTypeRoot {
				t.Errorf("TestTreeBuilder.BuildFromTestResults() Root.Type = %v, want %v", got.Root.Type, NodeTypeRoot)
			}

			// Verify no duplicates in test nodes
			testNames := make(map[string]int)
			for _, node := range got.TestNodes {
				key := node.Package + ":" + node.Name
				testNames[key]++
			}
			for name, count := range testNames {
				if count > 1 {
					t.Errorf("Duplicate test found: %s appears %d times", name, count)
				}
			}
		})
	}
}

func TestTestTreeNode_GetTestStats(t *testing.T) {
	// Create a test tree structure
	root := &TestTreeNode{
		Type: NodeTypeRoot,
		Children: []*TestTreeNode{
			{
				Type:   NodeTypeGate,
				Name:   "gate1",
				Status: TestStatusFail,
				Children: []*TestTreeNode{
					{
						Type:     NodeTypeTest,
						Name:     "test1",
						Status:   TestStatusPass,
						Duration: time.Second,
					},
					{
						Type:     NodeTypeTest,
						Name:     "test2",
						Status:   TestStatusFail,
						Duration: 2 * time.Second,
					},
					{
						Type:     NodeTypeTest,
						Name:     "test3",
						Status:   TestStatusSkip,
						Duration: time.Second,
					},
				},
			},
		},
	}

	stats := root.GetTestStats()

	if stats.Total != 3 {
		t.Errorf("GetTestStats() Total = %v, want %v", stats.Total, 3)
	}
	if stats.Passed != 1 {
		t.Errorf("GetTestStats() Passed = %v, want %v", stats.Passed, 1)
	}
	if stats.Failed != 1 {
		t.Errorf("GetTestStats() Failed = %v, want %v", stats.Failed, 1)
	}
	if stats.Skipped != 1 {
		t.Errorf("GetTestStats() Skipped = %v, want %v", stats.Skipped, 1)
	}

	expectedPassRate := float64(1) / float64(3) * 100
	if stats.PassRate != expectedPassRate {
		t.Errorf("GetTestStats() PassRate = %v, want %v", stats.PassRate, expectedPassRate)
	}
}

func TestTestTree_Walk(t *testing.T) {
	// Create a simple tree structure
	root := &TestTreeNode{
		Type: NodeTypeRoot,
		Children: []*TestTreeNode{
			{
				Type: NodeTypeGate,
				Name: "gate1",
				Children: []*TestTreeNode{
					{
						Type: NodeTypeTest,
						Name: "test1",
					},
				},
			},
		},
	}

	tree := &TestTree{Root: root}

	// Walk the tree and collect node names
	var visited []string
	tree.Walk(func(node *TestTreeNode) bool {
		visited = append(visited, node.Name)
		return true
	})

	expected := []string{"", "gate1", "test1"} // root has empty name
	if len(visited) != len(expected) {
		t.Errorf("Walk() visited %d nodes, want %d", len(visited), len(expected))
	}

	for i, name := range expected {
		if i < len(visited) && visited[i] != name {
			t.Errorf("Walk() visited[%d] = %v, want %v", i, visited[i], name)
		}
	}
}

func TestTestTree_Filter(t *testing.T) {
	// Create a test tree
	builder := NewTestTreeBuilder()
	testResults := []*TestResult{
		{
			Metadata: ValidatorMetadata{
				Package:  "github.com/test/package",
				FuncName: "TestPass",
				Gate:     "base",
				RunAll:   false,
			},
			Status:   TestStatusPass,
			Duration: time.Second,
		},
		{
			Metadata: ValidatorMetadata{
				Package:  "github.com/test/package",
				FuncName: "TestFail",
				Gate:     "base",
				RunAll:   false,
			},
			Status:   TestStatusFail,
			Duration: time.Second,
			Error:    errors.New("test failed"),
		},
	}

	tree := builder.BuildFromTestResults(testResults, "test-run", "test-network")

	// Test ShowOnlyFailed filter
	tree.ShowOnlyFailed()

	// Count visible nodes
	visibleNodes := tree.GetVisibleNodes()

	// Should have root, gate, and failed test visible (plus container nodes leading to failed test)
	failedTestVisible := false
	passedTestVisible := false

	for _, node := range visibleNodes {
		if node.Name == "TestFail" {
			failedTestVisible = true
		}
		if node.Name == "TestPass" {
			passedTestVisible = true
		}
	}

	if !failedTestVisible {
		t.Error("ShowOnlyFailed() should make failed test visible")
	}
	if passedTestVisible {
		t.Error("ShowOnlyFailed() should hide passed test")
	}

	// Test ShowAll filter
	tree.ShowAll()
	visibleNodes = tree.GetVisibleNodes()

	passedTestVisible = false
	failedTestVisible = false

	for _, node := range visibleNodes {
		if node.Name == "TestFail" {
			failedTestVisible = true
		}
		if node.Name == "TestPass" {
			passedTestVisible = true
		}
	}

	if !failedTestVisible || !passedTestVisible {
		t.Error("ShowAll() should make both tests visible")
	}
}

func TestTestTreeBuilder_DuplicateDetection(t *testing.T) {
	// Test the duplicate detection logic specifically
	builder := NewTestTreeBuilder()

	testResults := []*TestResult{
		// Package test that runs TestChainFork as a subtest
		{
			Metadata: ValidatorMetadata{
				Package:  "github.com/ethereum-optimism/optimism/op-acceptance-tests/tests/base",
				FuncName: "",
				Gate:     "base",
				RunAll:   true,
			},
			Status:   TestStatusPass,
			Duration: 5 * time.Second,
			SubTests: map[string]*TestResult{
				"TestChainFork": {
					Metadata: ValidatorMetadata{
						FuncName: "TestChainFork",
					},
					Status:   TestStatusPass,
					Duration: 2 * time.Second,
				},
			},
		},
		// Individual TestChainFork test that should be deduplicated
		{
			Metadata: ValidatorMetadata{
				Package:  "github.com/ethereum-optimism/optimism/op-acceptance-tests/tests/base",
				FuncName: "TestChainFork",
				Gate:     "base",
				RunAll:   false,
			},
			Status:   TestStatusPass,
			Duration: 2 * time.Second,
		},
	}

	tree := builder.BuildFromTestResults(testResults, "test-run", "test-network")

	// Should only have 1 test node: TestChainFork subtest
	// The individual TestChainFork should be deduplicated, and package test only contains subtests
	if len(tree.TestNodes) != 1 {
		t.Errorf("Expected 1 test node (subtest only), got %d", len(tree.TestNodes))
		for i, node := range tree.TestNodes {
			t.Logf("TestNode[%d]: Type=%v, Name=%s", i, node.Type, node.Name)
		}
	}

	// Verify the correct nodes are present
	foundSubtest := false

	for _, node := range tree.TestNodes {
		if node.Type == NodeTypeSubtest && node.Name == "TestChainFork" {
			foundSubtest = true
		}
	}

	if !foundSubtest {
		t.Error("TestChainFork subtest node not found")
	}

	// Verify we don't have a standalone TestChainFork test node
	for _, node := range tree.TestNodes {
		if node.Type == NodeTypeTest && node.Name == "TestChainFork" {
			t.Error("Found duplicate individual TestChainFork test that should have been deduplicated")
		}
	}
}

func TestTestTreeNode_GetPath(t *testing.T) {
	// Create a nested tree structure
	root := &TestTreeNode{
		Type: NodeTypeRoot,
		Name: "Test Results",
	}

	gate := &TestTreeNode{
		Type:   NodeTypeGate,
		Name:   "base",
		Parent: root,
	}

	packageNode := &TestTreeNode{
		Type:   NodeTypePackage,
		Name:   "github.com/test/package (package)",
		Parent: gate,
	}

	testNode := &TestTreeNode{
		Type:   NodeTypeSubtest,
		Name:   "TestFunction",
		Parent: packageNode,
	}

	expectedPath := "base/github.com/test/package (package)/TestFunction"
	actualPath := testNode.GetPath()

	if actualPath != expectedPath {
		t.Errorf("GetPath() = %v, want %v", actualPath, expectedPath)
	}
}

func TestTestTreeBuilder_WithOptions(t *testing.T) {
	builder := NewTestTreeBuilder()

	// Test method chaining
	builder = builder.WithSubtests(false).WithCollapsedPackages(true)

	if builder.showSubtests {
		t.Error("WithSubtests(false) did not set showSubtests to false")
	}
	if !builder.collapsePackages {
		t.Error("WithCollapsedPackages(true) did not set collapsePackages to true")
	}

	// Test that log path generator can be set
	called := false
	builder = builder.WithLogPathGenerator(func(*TestResult, bool, string) string {
		called = true
		return "test-log-path"
	})

	// Create a simple test to trigger the log path generator
	testResults := []*TestResult{
		{
			Metadata: ValidatorMetadata{
				Package:  "test",
				FuncName: "TestExample",
				RunAll:   false,
			},
			Status:   TestStatusPass,
			Duration: time.Second,
		},
	}

	tree := builder.BuildFromTestResults(testResults, "test-run", "test-network")

	if !called {
		t.Error("Log path generator was not called during tree building")
	}

	// Verify the log path was set
	if len(tree.TestNodes) > 0 && tree.TestNodes[0].LogPath != "test-log-path" {
		t.Errorf("Log path = %v, want %v", tree.TestNodes[0].LogPath, "test-log-path")
	}
}
