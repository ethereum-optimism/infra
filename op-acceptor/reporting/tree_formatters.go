package reporting

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"strings"
	"time"

	"github.com/ethereum-optimism/infra/op-acceptor/types"
	"github.com/ethereum-optimism/infra/op-acceptor/ui"
	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
)

// formatDuration formats a duration for display
func formatDuration(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	return d.Truncate(time.Millisecond).String()
}

// TreeHTMLFormatter formats test trees as HTML using the tree structure
type TreeHTMLFormatter struct {
	template *template.Template
}

// NewTreeHTMLFormatter creates a new tree-based HTML formatter
func NewTreeHTMLFormatter(templateContent string) (*TreeHTMLFormatter, error) {
	tmpl, err := template.New("tree-report").Funcs(template.FuncMap{
		"formatDuration": func(d time.Duration) string {
			if d < time.Second {
				return fmt.Sprintf("%dms", d.Milliseconds())
			}
			return d.Truncate(time.Millisecond).String()
		},
		"getStatusClass": func(status types.TestStatus) string {
			switch status {
			case types.TestStatusPass:
				return "pass"
			case types.TestStatusFail:
				return "fail"
			case types.TestStatusSkip:
				return "skip"
			case types.TestStatusError:
				return "error"
			default:
				return "unknown"
			}
		},
		"getStatusText": func(status types.TestStatus) string {
			switch status {
			case types.TestStatusPass:
				return "PASS"
			case types.TestStatusFail:
				return "FAIL"
			case types.TestStatusSkip:
				return "SKIP"
			case types.TestStatusError:
				return "ERROR"
			default:
				return "UNKNOWN"
			}
		},
		"isTestNode": func(nodeType types.TestTreeNodeType) bool {
			return nodeType == types.NodeTypeTest || nodeType == types.NodeTypeSubtest
		},
		"getIndentClass": func(depth int) string {
			switch depth {
			case 0:
				return "indent-0"
			case 1:
				return "indent-1"
			case 2:
				return "indent-2"
			case 3:
				return "indent-3"
			default:
				return fmt.Sprintf("indent-%d", depth)
			}
		},
		"multiply": func(a, b int) int {
			return a * b
		},
	}).Parse(templateContent)
	if err != nil {
		return nil, fmt.Errorf("failed to parse HTML template: %w", err)
	}

	return &TreeHTMLFormatter{
		template: tmpl,
	}, nil
}

// Format formats a test tree as HTML
func (f *TreeHTMLFormatter) Format(tree *types.TestTree) (string, error) {
	var buf bytes.Buffer
	if err := f.template.Execute(&buf, tree); err != nil {
		return "", fmt.Errorf("failed to execute HTML template: %w", err)
	}
	return buf.String(), nil
}

// TreeTableFormatter formats test trees as ASCII tables using the tree structure
type TreeTableFormatter struct {
	title              string
	showContainers     bool
	showExecutionOrder bool
}

// NewTreeTableFormatter creates a new tree-based table formatter
func NewTreeTableFormatter(title string, showContainers, showExecutionOrder bool) *TreeTableFormatter {
	return &TreeTableFormatter{
		title:              title,
		showContainers:     showContainers,
		showExecutionOrder: showExecutionOrder,
	}
}

// Format formats a test tree as an ASCII table
func (f *TreeTableFormatter) Format(tree *types.TestTree) (string, error) {
	var buf bytes.Buffer

	t := table.NewWriter()
	t.SetOutputMirror(&buf)
	t.SetTitle(f.title)

	// Configure columns based on options
	headers := []interface{}{"TYPE", "ID", "DURATION", "TESTS", "PASSED", "FAILED", "SKIPPED", "STATUS"}
	if f.showExecutionOrder {
		headers = append([]interface{}{"ORDER"}, headers...)
	}
	t.AppendHeader(table.Row(headers))

	// Set column configurations
	configs := []table.ColumnConfig{
		{Name: "TYPE", AutoMerge: true},
		{Name: "ID", WidthMax: 200, WidthMaxEnforcer: text.WrapSoft},
		{Name: "DURATION", Align: text.AlignRight},
		{Name: "TESTS", Align: text.AlignRight},
		{Name: "PASSED", Align: text.AlignRight},
		{Name: "FAILED", Align: text.AlignRight},
		{Name: "SKIPPED", Align: text.AlignRight},
	}
	if f.showExecutionOrder {
		configs = append([]table.ColumnConfig{{Name: "ORDER", Align: text.AlignRight}}, configs...)
	}
	t.SetColumnConfigs(configs)

	// Walk the tree and add rows
	tree.Walk(func(node *types.TestTreeNode) bool {
		// Skip root node
		if node.Type == types.NodeTypeRoot {
			return true
		}

		// Skip containers if not showing them
		if !f.showContainers && node.Type != types.NodeTypeTest && node.Type != types.NodeTypeSubtest {
			return true
		}

		f.addNodeRow(t, node)
		return true
	})

	// Set table style based on overall result
	if tree.Stats.Failed > 0 {
		t.SetStyle(table.StyleColoredBlackOnRedWhite)
	} else if tree.Stats.Skipped > 0 {
		t.SetStyle(table.StyleColoredBlackOnYellowWhite)
	} else {
		t.SetStyle(table.StyleColoredBlackOnGreenWhite)
	}

	// Add summary footer
	overallStatus := "PASS"
	if tree.Stats.Failed > 0 {
		overallStatus = "FAIL"
	} else if tree.Stats.Skipped > 0 && tree.Stats.Passed == 0 {
		overallStatus = "SKIP"
	}

	footerRow := []interface{}{
		"TOTAL",
		"",
		formatDuration(tree.Duration),
		tree.Stats.Total,
		tree.Stats.Passed,
		tree.Stats.Failed,
		tree.Stats.Skipped,
		overallStatus,
	}
	if f.showExecutionOrder {
		footerRow = append([]interface{}{""}, footerRow...)
	}
	t.AppendFooter(table.Row(footerRow))

	t.Render()
	return buf.String(), nil
}

// addNodeRow adds a single node as a row in the table
func (f *TreeTableFormatter) addNodeRow(t table.Writer, node *types.TestTreeNode) {
	// Generate tree prefix for hierarchical display
	prefix := f.generateTreePrefix(node)
	displayName := prefix + node.Name

	// Determine node type for display
	nodeTypeStr := f.getNodeTypeString(node)

	// Get node statistics
	stats := node.GetTestStats()

	// Build row data
	rowData := []interface{}{
		nodeTypeStr,
		displayName,
		formatDuration(node.Duration),
		stats.Total,
		stats.Passed,
		stats.Failed,
		stats.Skipped,
		f.getStatusString(node.Status),
	}

	// Add execution order if showing it
	if f.showExecutionOrder {
		orderStr := ""
		if node.Type == types.NodeTypeTest || node.Type == types.NodeTypeSubtest {
			orderStr = fmt.Sprintf("%d", node.ExecutionOrder)
		}
		rowData = append([]interface{}{orderStr}, rowData...)
	}

	t.AppendRow(table.Row(rowData))
}

// generateTreePrefix generates the tree-style prefix for a node
func (f *TreeTableFormatter) generateTreePrefix(node *types.TestTreeNode) string {
	if node.Parent == nil || node.Parent.Type == types.NodeTypeRoot {
		return ""
	}

	// Calculate whether this node is the last among its siblings
	isLast := f.isLastSibling(node)

	// Build parent "isLast" chain
	var parentIsLast []bool
	current := node.Parent
	for current != nil && current.Parent != nil && current.Parent.Type != types.NodeTypeRoot {
		parentIsLast = append([]bool{f.isLastSibling(current)}, parentIsLast...)
		current = current.Parent
	}

	return ui.BuildTreePrefix(node.Depth-1, isLast, parentIsLast)
}

// isLastSibling checks if a node is the last among its siblings
func (f *TreeTableFormatter) isLastSibling(node *types.TestTreeNode) bool {
	if node.Parent == nil {
		return true
	}

	siblings := node.Parent.Children
	for i, sibling := range siblings {
		if sibling == node {
			return i == len(siblings)-1
		}
	}
	return true
}

// getNodeTypeString returns a display string for the node type
func (f *TreeTableFormatter) getNodeTypeString(node *types.TestTreeNode) string {
	switch node.Type {
	case types.NodeTypeGate:
		return "Gate"
	case types.NodeTypeSuite:
		return "Suite"
	case types.NodeTypePackage:
		return "Package"
	case types.NodeTypeTest:
		return "Test"
	case types.NodeTypeSubtest:
		return "Subtest"
	default:
		return "Unknown"
	}
}

// getStatusString returns a display string for the status
func (f *TreeTableFormatter) getStatusString(status types.TestStatus) string {
	switch status {
	case types.TestStatusPass:
		return "PASS"
	case types.TestStatusFail:
		return "FAIL"
	case types.TestStatusSkip:
		return "SKIP"
	case types.TestStatusError:
		return "ERROR"
	default:
		return "UNKNOWN"
	}
}

// TreeTextFormatter formats test trees as plain text using the tree structure
type TreeTextFormatter struct {
	includeContainers  bool
	includeStats       bool
	includeDetails     bool
	showExecutionOrder bool
}

// NewTreeTextFormatter creates a new tree-based text formatter
func NewTreeTextFormatter(includeContainers, includeStats, includeDetails, showExecutionOrder bool) *TreeTextFormatter {
	return &TreeTextFormatter{
		includeContainers:  includeContainers,
		includeStats:       includeStats,
		includeDetails:     includeDetails,
		showExecutionOrder: showExecutionOrder,
	}
}

// Format formats a test tree as plain text
func (f *TreeTextFormatter) Format(tree *types.TestTree) (string, error) {
	var buf bytes.Buffer

	// Header
	buf.WriteString("Test Results Summary\n")
	buf.WriteString(strings.Repeat("=", 50) + "\n\n")

	// Overall statistics
	if f.includeStats {
		buf.WriteString(fmt.Sprintf("Run ID: %s\n", tree.RunID))
		if tree.NetworkName != "" {
			buf.WriteString(fmt.Sprintf("Network: %s\n", tree.NetworkName))
		}
		buf.WriteString(fmt.Sprintf("Duration: %s\n", formatDuration(tree.Duration)))
		buf.WriteString(fmt.Sprintf("Total Tests: %d\n", tree.Stats.Total))
		buf.WriteString(fmt.Sprintf("Passed: %d\n", tree.Stats.Passed))
		buf.WriteString(fmt.Sprintf("Failed: %d\n", tree.Stats.Failed))
		buf.WriteString(fmt.Sprintf("Skipped: %d\n", tree.Stats.Skipped))
		buf.WriteString(fmt.Sprintf("Errored: %d\n", tree.Stats.Errored))
		buf.WriteString(fmt.Sprintf("Pass Rate: %.1f%%\n", tree.Stats.PassRate))
		buf.WriteString("\n")
	}

	// Test tree
	buf.WriteString("Test Hierarchy:\n")
	buf.WriteString(strings.Repeat("-", 30) + "\n")

	tree.Walk(func(node *types.TestTreeNode) bool {
		// Skip root
		if node.Type == types.NodeTypeRoot {
			return true
		}

		// Skip containers if not including them
		if !f.includeContainers && node.Type != types.NodeTypeTest && node.Type != types.NodeTypeSubtest {
			return true
		}

		f.writeNodeText(&buf, node)
		return true
	})

	// Failed tests summary
	if len(tree.FailedNodes) > 0 {
		buf.WriteString("\nFailed Tests:\n")
		buf.WriteString(strings.Repeat("-", 20) + "\n")
		for _, node := range tree.FailedNodes {
			buf.WriteString(fmt.Sprintf("- %s", node.GetPath()))
			if f.includeDetails && node.Error != nil {
				buf.WriteString(fmt.Sprintf(" (Error: %s)", node.Error.Error()))
			}
			buf.WriteString("\n")
		}
	}

	return buf.String(), nil
}

// writeNodeText writes a single node as text
func (f *TreeTextFormatter) writeNodeText(buf *bytes.Buffer, node *types.TestTreeNode) {
	// Generate tree prefix
	prefix := f.generateTextTreePrefix(node)

	// Status indicator
	statusChar := f.getStatusChar(node.Status)

	// Build the line
	line := fmt.Sprintf("%s%s %s", prefix, statusChar, node.Name)

	// Add execution order if requested
	if f.showExecutionOrder && (node.Type == types.NodeTypeTest || node.Type == types.NodeTypeSubtest) {
		line += fmt.Sprintf(" [#%d]", node.ExecutionOrder)
	}

	// Add duration for tests
	if node.Type == types.NodeTypeTest || node.Type == types.NodeTypeSubtest {
		line += fmt.Sprintf(" (%s)", formatDuration(node.Duration))
	}

	// Add stats for containers if requested
	if f.includeStats && (node.Type == types.NodeTypeGate || node.Type == types.NodeTypeSuite || node.Type == types.NodeTypePackage) {
		stats := node.GetTestStats()
		line += fmt.Sprintf(" [%d tests, %d passed, %d failed]", stats.Total, stats.Passed, stats.Failed)
	}

	buf.WriteString(line + "\n")

	// Add error details if requested and available
	if f.includeDetails && node.Error != nil {
		errorPrefix := strings.Repeat(" ", len(prefix)+2) // Align with node content
		buf.WriteString(fmt.Sprintf("%sError: %s\n", errorPrefix, node.Error.Error()))
	}
}

// generateTextTreePrefix generates the tree-style prefix for text output
func (f *TreeTextFormatter) generateTextTreePrefix(node *types.TestTreeNode) string {
	if node.Parent == nil || node.Parent.Type == types.NodeTypeRoot {
		return ""
	}

	// Calculate whether this node is the last among its siblings
	isLast := f.isLastTextSibling(node)

	// Build parent "isLast" chain
	var parentIsLast []bool
	current := node.Parent
	for current != nil && current.Parent != nil && current.Parent.Type != types.NodeTypeRoot {
		parentIsLast = append([]bool{f.isLastTextSibling(current)}, parentIsLast...)
		current = current.Parent
	}

	return ui.BuildTreePrefix(node.Depth-1, isLast, parentIsLast)
}

// isLastTextSibling checks if a node is the last among its visible siblings
func (f *TreeTextFormatter) isLastTextSibling(node *types.TestTreeNode) bool {
	if node.Parent == nil {
		return true
	}

	// Find visible siblings
	var visibleSiblings []*types.TestTreeNode
	for _, sibling := range node.Parent.Children {
		// Include based on formatter settings
		if f.includeContainers || sibling.Type == types.NodeTypeTest || sibling.Type == types.NodeTypeSubtest {
			visibleSiblings = append(visibleSiblings, sibling)
		}
	}

	// Check if this node is the last visible sibling
	for i, sibling := range visibleSiblings {
		if sibling == node {
			return i == len(visibleSiblings)-1
		}
	}
	return true
}

// getStatusChar returns a character representing the test status
func (f *TreeTextFormatter) getStatusChar(status types.TestStatus) string {
	switch status {
	case types.TestStatusPass:
		return "✓"
	case types.TestStatusFail:
		return "✗"
	case types.TestStatusSkip:
		return "⊝"
	case types.TestStatusError:
		return "⚠"
	default:
		return "?"
	}
}

// TreeJSONFormatter formats test trees as JSON
type TreeJSONFormatter struct {
	includeTestResults bool
	includeHierarchy   bool
}

// NewTreeJSONFormatter creates a new tree-based JSON formatter
func NewTreeJSONFormatter(includeTestResults, includeHierarchy bool) *TreeJSONFormatter {
	return &TreeJSONFormatter{
		includeTestResults: includeTestResults,
		includeHierarchy:   includeHierarchy,
	}
}

// Format formats a test tree as JSON
func (f *TreeJSONFormatter) Format(tree *types.TestTree) (string, error) {
	data := make(map[string]interface{})

	// Basic tree information
	data["runId"] = tree.RunID
	data["networkName"] = tree.NetworkName
	data["timestamp"] = tree.Timestamp
	data["duration"] = tree.Duration
	data["stats"] = tree.Stats

	// Include hierarchy if requested
	if f.includeHierarchy {
		data["hierarchy"] = f.nodeToJSON(tree.Root)
	}

	// Include flat test list
	var tests []map[string]interface{}
	for _, node := range tree.TestNodes {
		testData := map[string]interface{}{
			"id":             node.ID,
			"name":           node.Name,
			"type":           node.Type,
			"status":         node.Status,
			"duration":       node.Duration,
			"executionOrder": node.ExecutionOrder,
			"package":        node.Package,
			"gate":           node.Gate,
			"suite":          node.Suite,
			"depth":          node.Depth,
			"path":           node.GetPath(),
		}

		if node.Error != nil {
			testData["error"] = node.Error.Error()
		}

		if node.LogPath != "" {
			testData["logPath"] = node.LogPath
		}

		// Include original test result if requested
		if f.includeTestResults && node.TestResult != nil {
			testData["testResult"] = node.TestResult
		}

		tests = append(tests, testData)
	}
	data["tests"] = tests

	// Include failed tests summary
	var failed []string
	for _, node := range tree.FailedNodes {
		failed = append(failed, node.GetPath())
	}
	data["failedTests"] = failed

	// Convert to JSON
	jsonBytes, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal JSON: %w", err)
	}

	return string(jsonBytes), nil
}

// nodeToJSON converts a tree node to JSON representation
func (f *TreeJSONFormatter) nodeToJSON(node *types.TestTreeNode) map[string]interface{} {
	nodeData := map[string]interface{}{
		"id":       node.ID,
		"name":     node.Name,
		"type":     node.Type,
		"status":   node.Status,
		"duration": node.Duration,
		"depth":    node.Depth,
	}

	if node.Package != "" {
		nodeData["package"] = node.Package
	}
	if node.Gate != "" {
		nodeData["gate"] = node.Gate
	}
	if node.Suite != "" {
		nodeData["suite"] = node.Suite
	}
	if node.Error != nil {
		nodeData["error"] = node.Error.Error()
	}
	if node.LogPath != "" {
		nodeData["logPath"] = node.LogPath
	}
	if node.Type == types.NodeTypeTest || node.Type == types.NodeTypeSubtest {
		nodeData["executionOrder"] = node.ExecutionOrder
	}

	// Add children if any
	if len(node.Children) > 0 {
		var children []map[string]interface{}
		for _, child := range node.Children {
			children = append(children, f.nodeToJSON(child))
		}
		nodeData["children"] = children
	}

	// Add statistics for containers
	if node.Type != types.NodeTypeTest && node.Type != types.NodeTypeSubtest {
		nodeData["stats"] = node.GetTestStats()
	}

	return nodeData
}
