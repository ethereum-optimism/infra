package types

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// TestTreeNode represents a node in the hierarchical test tree
type TestTreeNode struct {
	// Node identity and metadata
	ID      string           // Unique identifier for this node
	Name    string           // Display name
	Type    TestTreeNodeType // Type of node (package, test, subtest, etc.)
	Package string           // Package name
	Gate    string           // Gate name
	Suite   string           // Suite name

	// Test execution data
	Status         TestStatus    // Overall status of this node
	Duration       time.Duration // Total duration including children
	Error          error         // Error if failed
	LogPath        string        // Path to log file
	ExecutionOrder int           // Order in which this was executed

	// Hierarchy
	Children []*TestTreeNode // Child nodes
	Parent   *TestTreeNode   // Parent node (nil for root)
	Depth    int             // Depth in tree (0 = root level)

	// Display control
	IsCollapsed bool // Whether this node should be collapsed in UI
	IsVisible   bool // Whether this node should be visible (for filtering)

	// Raw test data (for detailed views)
	TestResult *TestResult // Original test result, if any
}

// TestTreeNodeType defines the type of node in the test tree
type TestTreeNodeType string

const (
	NodeTypeRoot    TestTreeNodeType = "root"    // Root container
	NodeTypeGate    TestTreeNodeType = "gate"    // Gate container
	NodeTypeSuite   TestTreeNodeType = "suite"   // Suite container
	NodeTypePackage TestTreeNodeType = "package" // Package test runner
	NodeTypeTest    TestTreeNodeType = "test"    // Individual test function
	NodeTypeSubtest TestTreeNodeType = "subtest" // Subtest within a function
)

// TestTreeStats contains aggregated statistics for a tree node
type TestTreeStats struct {
	Total    int        // Total number of test nodes (excludes containers)
	Passed   int        // Number of passed tests
	Failed   int        // Number of failed tests
	Skipped  int        // Number of skipped tests
	Errored  int        // Number of errored tests
	PassRate float64    // Pass rate percentage
	Status   TestStatus // Overall status (PASS/FAIL/SKIP/ERROR)
}

// TestTree represents the complete hierarchical test structure
type TestTree struct {
	Root        *TestTreeNode // Root node containing all gates
	Stats       TestTreeStats // Overall statistics
	Duration    time.Duration // Total execution time
	RunID       string        // Test run identifier
	Timestamp   time.Time     // When the run started
	NetworkName string        // Network name

	// Flat indices for quick lookup
	AllNodes    []*TestTreeNode // All nodes in execution order
	TestNodes   []*TestTreeNode // Only test/subtest nodes (no containers)
	FailedNodes []*TestTreeNode // Only failed test nodes

	// Node lookup maps
	nodesByID   map[string]*TestTreeNode // Quick lookup by ID
	nodesByPath map[string]*TestTreeNode // Quick lookup by hierarchical path
}

// TestTreeBuilder builds a TestTree from TestResult data
type TestTreeBuilder struct {
	showSubtests     bool
	collapsePackages bool
	logPathGenerator func(*TestResult, bool, string) string
}

// NewTestTreeBuilder creates a new test tree builder
func NewTestTreeBuilder() *TestTreeBuilder {
	return &TestTreeBuilder{
		showSubtests:     true,
		collapsePackages: false,
		logPathGenerator: func(*TestResult, bool, string) string { return "" },
	}
}

// WithSubtests controls whether subtests are included in the tree
func (b *TestTreeBuilder) WithSubtests(show bool) *TestTreeBuilder {
	b.showSubtests = show
	return b
}

// WithCollapsedPackages controls whether package nodes start collapsed
func (b *TestTreeBuilder) WithCollapsedPackages(collapsed bool) *TestTreeBuilder {
	b.collapsePackages = collapsed
	return b
}

// WithLogPathGenerator sets the function for generating log paths
func (b *TestTreeBuilder) WithLogPathGenerator(fn func(*TestResult, bool, string) string) *TestTreeBuilder {
	b.logPathGenerator = fn
	return b
}

// BuildFromTestResults creates a TestTree from a collection of TestResults
func (b *TestTreeBuilder) BuildFromTestResults(results []*TestResult, runID, networkName string) *TestTree {
	tree := &TestTree{
		RunID:       runID,
		NetworkName: networkName,
		Timestamp:   time.Now(),
		nodesByID:   make(map[string]*TestTreeNode),
		nodesByPath: make(map[string]*TestTreeNode),
		AllNodes:    make([]*TestTreeNode, 0),
		TestNodes:   make([]*TestTreeNode, 0),
		FailedNodes: make([]*TestTreeNode, 0),
	}

	// Create root node
	tree.Root = &TestTreeNode{
		ID:        "root",
		Name:      "Test Results",
		Type:      NodeTypeRoot,
		Children:  make([]*TestTreeNode, 0),
		IsVisible: true,
		Depth:     0,
	}
	tree.nodesByID["root"] = tree.Root
	tree.nodesByPath[""] = tree.Root

	// Track duplicate detection
	individualTests := make(map[string]*TestResult)
	subtestCoverage := make(map[string]bool)

	// First pass: identify duplicates
	for _, result := range results {
		if !result.Metadata.RunAll && result.Metadata.FuncName != "" {
			key := fmt.Sprintf("%s:%s", result.Metadata.Package, result.Metadata.FuncName)
			individualTests[key] = result
		}
	}

	// Mark tests covered by package subtests
	for _, result := range results {
		if result.Metadata.RunAll && len(result.SubTests) > 0 {
			for subtestName := range result.SubTests {
				key := fmt.Sprintf("%s:%s", result.Metadata.Package, subtestName)
				if _, exists := individualTests[key]; exists {
					subtestCoverage[key] = true
				}
			}
		}
	}

	// Process test results
	executionOrder := 0
	for _, result := range results {
		executionOrder++

		// Skip individual tests that are covered by package subtests
		testKey := fmt.Sprintf("%s:%s", result.Metadata.Package, result.Metadata.FuncName)
		if !result.Metadata.RunAll && subtestCoverage[testKey] {
			continue
		}

		// Create/get container nodes (gate, suite, package as needed)
		parentNode := b.ensureContainerPath(tree, result.Metadata)

		// Create the test node
		testNode := b.createTestNode(result, parentNode, executionOrder)

		// Only add package tests to TestNodes if they don't have subtests
		// When they have subtests, we'll only count the subtests to avoid double-counting
		if !result.Metadata.RunAll || len(result.SubTests) == 0 {
			b.addNodeToTree(tree, testNode)
		} else {
			// Add to tree structure but not to TestNodes (for hierarchy display)
			if testNode.Parent != nil {
				testNode.Parent.Children = append(testNode.Parent.Children, testNode)
			}
			tree.AllNodes = append(tree.AllNodes, testNode)
			tree.nodesByID[testNode.ID] = testNode
			tree.nodesByPath[testNode.GetPath()] = testNode
		}

		// Process subtests if enabled
		if b.showSubtests && len(result.SubTests) > 0 {
			for subtestName, subResult := range result.SubTests {
				executionOrder++
				if subResult.Metadata.FuncName == "" {
					subResult.Metadata.FuncName = subtestName
				}
				// Inherit parent metadata
				if subResult.Metadata.Gate == "" {
					subResult.Metadata.Gate = result.Metadata.Gate
				}
				if subResult.Metadata.Suite == "" {
					subResult.Metadata.Suite = result.Metadata.Suite
				}
				if subResult.Metadata.Package == "" {
					subResult.Metadata.Package = result.Metadata.Package
				}

				subtestNode := b.createSubtestNode(subResult, testNode, executionOrder)
				b.addNodeToTree(tree, subtestNode)
			}
		}
	}

	// Calculate statistics and finalize tree
	b.calculateTreeStats(tree)
	b.sortNodes(tree)

	return tree
}

// ensureContainerPath creates or gets the container nodes (gate/suite/package) for a test
func (b *TestTreeBuilder) ensureContainerPath(tree *TestTree, metadata ValidatorMetadata) *TestTreeNode {
	currentNode := tree.Root
	path := ""

	// Create gate node if needed
	if metadata.Gate != "" {
		path = metadata.Gate
		gateNode := tree.nodesByPath[path]
		if gateNode == nil {
			gateNode = &TestTreeNode{
				ID:        fmt.Sprintf("gate-%s", metadata.Gate),
				Name:      metadata.Gate,
				Type:      NodeTypeGate,
				Gate:      metadata.Gate,
				Parent:    currentNode,
				Children:  make([]*TestTreeNode, 0),
				Depth:     currentNode.Depth + 1,
				IsVisible: true,
			}
			currentNode.Children = append(currentNode.Children, gateNode)
			tree.nodesByID[gateNode.ID] = gateNode
			tree.nodesByPath[path] = gateNode
		}
		currentNode = gateNode

		// Create suite node if needed
		if metadata.Suite != "" {
			path = fmt.Sprintf("%s/%s", metadata.Gate, metadata.Suite)
			suiteNode := tree.nodesByPath[path]
			if suiteNode == nil {
				suiteNode = &TestTreeNode{
					ID:        fmt.Sprintf("suite-%s-%s", metadata.Gate, metadata.Suite),
					Name:      metadata.Suite,
					Type:      NodeTypeSuite,
					Gate:      metadata.Gate,
					Suite:     metadata.Suite,
					Parent:    currentNode,
					Children:  make([]*TestTreeNode, 0),
					Depth:     currentNode.Depth + 1,
					IsVisible: true,
				}
				currentNode.Children = append(currentNode.Children, suiteNode)
				tree.nodesByID[suiteNode.ID] = suiteNode
				tree.nodesByPath[path] = suiteNode
			}
			currentNode = suiteNode
		}
	}

	// Create package node if needed and this is a package test
	if metadata.Package != "" && metadata.RunAll {
		packagePath := path
		if packagePath != "" {
			packagePath += "/" + metadata.Package
		} else {
			packagePath = metadata.Package
		}

		packageNode := tree.nodesByPath[packagePath]
		if packageNode == nil {
			packageNode = &TestTreeNode{
				ID:          fmt.Sprintf("package-%s", strings.ReplaceAll(packagePath, "/", "-")),
				Name:        fmt.Sprintf("%s (package)", metadata.Package),
				Type:        NodeTypePackage,
				Gate:        metadata.Gate,
				Suite:       metadata.Suite,
				Package:     metadata.Package,
				Parent:      currentNode,
				Children:    make([]*TestTreeNode, 0),
				Depth:       currentNode.Depth + 1,
				IsVisible:   true,
				IsCollapsed: b.collapsePackages,
			}
			currentNode.Children = append(currentNode.Children, packageNode)
			tree.nodesByID[packageNode.ID] = packageNode
			tree.nodesByPath[packagePath] = packageNode
		}
		currentNode = packageNode
	}

	return currentNode
}

// createTestNode creates a test node from a TestResult
func (b *TestTreeBuilder) createTestNode(result *TestResult, parent *TestTreeNode, order int) *TestTreeNode {
	nodeType := NodeTypeTest
	name := result.Metadata.FuncName

	// For package tests, use the package name if function name is empty
	if result.Metadata.RunAll {
		nodeType = NodeTypeTest // Keep as test node for statistics counting
		if name == "" && result.Metadata.Package != "" {
			name = fmt.Sprintf("%s (package)", result.Metadata.Package)
		}
	} else if name == "" && result.Metadata.Package != "" {
		name = result.Metadata.Package
	}

	return &TestTreeNode{
		ID:             fmt.Sprintf("test-%d", order),
		Name:           name,
		Type:           nodeType,
		Package:        result.Metadata.Package,
		Gate:           result.Metadata.Gate,
		Suite:          result.Metadata.Suite,
		Status:         result.Status,
		Duration:       result.Duration,
		Error:          result.Error,
		LogPath:        b.logPathGenerator(result, false, ""),
		ExecutionOrder: order,
		Parent:         parent,
		Children:       make([]*TestTreeNode, 0),
		Depth:          parent.Depth + 1,
		IsVisible:      true,
		TestResult:     result,
	}
}

// createSubtestNode creates a subtest node from a TestResult
func (b *TestTreeBuilder) createSubtestNode(result *TestResult, parent *TestTreeNode, order int) *TestTreeNode {
	return &TestTreeNode{
		ID:             fmt.Sprintf("subtest-%d", order),
		Name:           result.Metadata.FuncName,
		Type:           NodeTypeSubtest,
		Package:        result.Metadata.Package,
		Gate:           result.Metadata.Gate,
		Suite:          result.Metadata.Suite,
		Status:         result.Status,
		Duration:       result.Duration,
		Error:          result.Error,
		LogPath:        b.logPathGenerator(result, true, parent.Name),
		ExecutionOrder: order,
		Parent:         parent,
		Children:       make([]*TestTreeNode, 0),
		Depth:          parent.Depth + 1,
		IsVisible:      true,
		TestResult:     result,
	}
}

// addNodeToTree adds a node to the tree and updates indices
func (b *TestTreeBuilder) addNodeToTree(tree *TestTree, node *TestTreeNode) {
	// Add to parent's children
	if node.Parent != nil {
		node.Parent.Children = append(node.Parent.Children, node)
	}

	// Add to tree indices
	tree.AllNodes = append(tree.AllNodes, node)
	tree.nodesByID[node.ID] = node

	// Add to test nodes if it's actually a test
	if node.Type == NodeTypeTest || node.Type == NodeTypeSubtest {
		// Skip package test nodes that have subtests
		if node.Type == NodeTypeTest && node.Parent != nil && node.Parent.Type == NodeTypePackage && len(node.Children) > 0 {
			return
		}
		tree.TestNodes = append(tree.TestNodes, node)

		// Add to failed nodes if failed
		if node.Status == TestStatusFail || node.Status == TestStatusError {
			tree.FailedNodes = append(tree.FailedNodes, node)
		}
	}
}

// calculateTreeStats calculates statistics for all nodes in the tree
func (b *TestTreeBuilder) calculateTreeStats(tree *TestTree) {
	// Calculate stats bottom-up and assign to tree.Stats
	tree.Stats = b.calculateNodeStats(tree.Root)

	// Calculate total duration
	tree.Duration = tree.Root.Duration
}

// calculateNodeStats calculates statistics for a single node and its children
func (b *TestTreeBuilder) calculateNodeStats(node *TestTreeNode) TestTreeStats {
	stats := TestTreeStats{}

	// If this is a test/subtest node, count it
	// For package tests with subtests, we only count the subtests
	if (node.Type == NodeTypeTest || node.Type == NodeTypeSubtest) &&
		!(node.Type == NodeTypeTest && node.TestResult != nil && node.TestResult.Metadata.RunAll && len(node.Children) > 0) {
		stats.Total = 1
		switch node.Status {
		case TestStatusPass:
			stats.Passed = 1
		case TestStatusFail:
			stats.Failed = 1
		case TestStatusSkip:
			stats.Skipped = 1
		case TestStatusError:
			stats.Errored = 1
		}
	}

	// Add stats from all children
	for _, child := range node.Children {
		childStats := b.calculateNodeStats(child)
		stats.Total += childStats.Total
		stats.Passed += childStats.Passed
		stats.Failed += childStats.Failed
		stats.Skipped += childStats.Skipped
		stats.Errored += childStats.Errored
	}

	// Calculate pass rate
	if stats.Total > 0 {
		stats.PassRate = float64(stats.Passed) / float64(stats.Total) * 100
	}

	// Determine overall status
	if stats.Failed > 0 {
		stats.Status = TestStatusFail
	} else if stats.Passed > 0 {
		stats.Status = TestStatusPass
	} else if stats.Skipped > 0 {
		stats.Status = TestStatusSkip
	} else {
		stats.Status = TestStatusError
	}

	// Store stats in node
	if node.isContainer() {
		node.Status = stats.Status
	}

	return stats
}

// sortNodes sorts children of all nodes for consistent display
func (b *TestTreeBuilder) sortNodes(tree *TestTree) {
	b.sortNodeChildren(tree.Root)
}

// sortNodeChildren recursively sorts children of a node
func (b *TestTreeBuilder) sortNodeChildren(node *TestTreeNode) {
	// Sort children by execution order for tests, alphabetically for containers
	sort.Slice(node.Children, func(i, j int) bool {
		a, b := node.Children[i], node.Children[j]

		// Container nodes before test nodes
		if a.isContainer() && !b.isContainer() {
			return true
		}
		if !a.isContainer() && b.isContainer() {
			return false
		}

		// For test nodes, sort by execution order
		if !a.isContainer() && !b.isContainer() {
			return a.ExecutionOrder < b.ExecutionOrder
		}

		// For container nodes, sort alphabetically
		return a.Name < b.Name
	})

	// Recursively sort children
	for _, child := range node.Children {
		b.sortNodeChildren(child)
	}
}

// Helper methods for TestTreeNode

// isContainer returns true if this node is a container (not a test)
func (n *TestTreeNode) isContainer() bool {
	return n.Type == NodeTypeRoot || n.Type == NodeTypeGate ||
		n.Type == NodeTypeSuite || n.Type == NodeTypePackage
}

// getTestCount returns the number of test nodes under this node
func (n *TestTreeNode) getTestCount() int {
	if n.Type == NodeTypeTest || n.Type == NodeTypeSubtest {
		return 1
	}

	count := 0
	for _, child := range n.Children {
		count += child.getTestCount()
	}
	return count
}

// getStatusCount returns the number of test nodes with the given status
func (n *TestTreeNode) getStatusCount(status TestStatus) int {
	if (n.Type == NodeTypeTest || n.Type == NodeTypeSubtest) && n.Status == status {
		return 1
	}

	count := 0
	for _, child := range n.Children {
		count += child.getStatusCount(status)
	}
	return count
}

// GetPath returns the hierarchical path to this node
func (n *TestTreeNode) GetPath() string {
	if n.Parent == nil || n.Parent.Type == NodeTypeRoot {
		return n.Name
	}
	return n.Parent.GetPath() + "/" + n.Name
}

// GetTestStats returns statistics for this node
func (n *TestTreeNode) GetTestStats() TestTreeStats {
	return TestTreeStats{
		Total:   n.getTestCount(),
		Passed:  n.getStatusCount(TestStatusPass),
		Failed:  n.getStatusCount(TestStatusFail),
		Skipped: n.getStatusCount(TestStatusSkip),
		Errored: n.getStatusCount(TestStatusError),
		PassRate: func() float64 {
			total := n.getTestCount()
			if total == 0 {
				return 0
			}
			return float64(n.getStatusCount(TestStatusPass)) / float64(total) * 100
		}(),
	}
}

// Walk traverses the tree calling the visitor function for each node
func (tree *TestTree) Walk(visitor func(*TestTreeNode) bool) {
	tree.walkNode(tree.Root, visitor)
}

// walkNode recursively walks a node and its children
func (tree *TestTree) walkNode(node *TestTreeNode, visitor func(*TestTreeNode) bool) {
	if !visitor(node) {
		return // Stop traversal if visitor returns false
	}

	for _, child := range node.Children {
		tree.walkNode(child, visitor)
	}
}

// FindNode finds a node by ID
func (tree *TestTree) FindNode(id string) *TestTreeNode {
	return tree.nodesByID[id]
}

// GetVisibleNodes returns all currently visible nodes (for filtering)
func (tree *TestTree) GetVisibleNodes() []*TestTreeNode {
	var visible []*TestTreeNode
	tree.Walk(func(node *TestTreeNode) bool {
		if node.IsVisible {
			visible = append(visible, node)
		}
		return true
	})
	return visible
}

// Filter applies a filter function to determine node visibility
func (tree *TestTree) Filter(filterFn func(*TestTreeNode) bool) {
	tree.Walk(func(node *TestTreeNode) bool {
		node.IsVisible = filterFn(node)
		return true
	})
}

// ShowAll makes all nodes visible
func (tree *TestTree) ShowAll() {
	tree.Walk(func(node *TestTreeNode) bool {
		node.IsVisible = true
		return true
	})
}

// ShowOnlyFailed shows only failed tests and their parents
func (tree *TestTree) ShowOnlyFailed() {
	// First, hide everything
	tree.Walk(func(node *TestTreeNode) bool {
		node.IsVisible = false
		return true
	})

	// Then show failed tests and their ancestors
	tree.Walk(func(node *TestTreeNode) bool {
		if (node.Type == NodeTypeTest || node.Type == NodeTypeSubtest) &&
			(node.Status == TestStatusFail || node.Status == TestStatusError) {
			// Make this node and all ancestors visible
			current := node
			for current != nil {
				current.IsVisible = true
				current = current.Parent
			}
		}
		return true
	})
}
