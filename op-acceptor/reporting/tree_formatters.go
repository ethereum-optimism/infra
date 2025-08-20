package reporting

import (
	"bytes"
	"fmt"
	"html/template"
	"strings"
	"time"

	"github.com/ethereum-optimism/infra/op-acceptor/types"
	"github.com/ethereum-optimism/infra/op-acceptor/ui"
	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
)

// JSON response structures for tree formatting

// TreeJSONResponse represents the complete JSON response for a test tree
type TreeJSONResponse struct {
	RunID       string              `json:"runId"`
	NetworkName string              `json:"networkName,omitempty"`
	Timestamp   time.Time           `json:"timestamp"`
	Duration    time.Duration       `json:"duration"`
	Stats       types.TestTreeStats `json:"stats"`
	Hierarchy   *TreeNodeJSON       `json:"hierarchy,omitempty"`
	Tests       []TestNodeJSON      `json:"tests"`
	FailedTests []string            `json:"failedTests"`
}

// TreeNodeJSON represents a tree node in JSON format
type TreeNodeJSON struct {
	ID             string                 `json:"id"`
	Name           string                 `json:"name"`
	Type           types.TestTreeNodeType `json:"type"`
	Status         types.TestStatus       `json:"status"`
	Duration       time.Duration          `json:"duration"`
	Depth          int                    `json:"depth"`
	Package        string                 `json:"package,omitempty"`
	Gate           string                 `json:"gate,omitempty"`
	Suite          string                 `json:"suite,omitempty"`
	Error          string                 `json:"error,omitempty"`
	LogPath        string                 `json:"logPath,omitempty"`
	ExecutionOrder int                    `json:"executionOrder,omitempty"`
	Children       []TreeNodeJSON         `json:"children,omitempty"`
	Stats          *types.TestTreeStats   `json:"stats,omitempty"`
}

// TestNodeJSON represents a flat test node in JSON format
type TestNodeJSON struct {
	ID             string                 `json:"id"`
	Name           string                 `json:"name"`
	Type           types.TestTreeNodeType `json:"type"`
	Status         types.TestStatus       `json:"status"`
	Duration       time.Duration          `json:"duration"`
	ExecutionOrder int                    `json:"executionOrder"`
	Package        string                 `json:"package,omitempty"`
	Gate           string                 `json:"gate,omitempty"`
	Suite          string                 `json:"suite,omitempty"`
	Depth          int                    `json:"depth"`
	Path           string                 `json:"path"`
	Error          string                 `json:"error,omitempty"`
	LogPath        string                 `json:"logPath,omitempty"`
	TestResult     interface{}            `json:"testResult,omitempty"`
}

// formatDuration formats a duration for display
func formatDuration(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	return d.Truncate(time.Millisecond).String()
}

// getStatusString returns a consistent lowercase status string
func getStatusString(status types.TestStatus) string {
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
			return getStatusString(status)
		},
		"getStatusText": func(status types.TestStatus) string {
			return getStatusString(status)
		},
		"getIndentClass": func(depth int) string {
			return fmt.Sprintf("indent-%d", depth)
		},
		"multiply": func(a, b int) int {
			return a * b
		},
		"getOverallStatus": func(stats types.TestTreeStats) types.TestStatus {
			if stats.Failed > 0 {
				return types.TestStatusFail
			}
			if stats.Passed > 0 {
				return types.TestStatusPass
			}
			if stats.Skipped > 0 {
				return types.TestStatusSkip
			}
			return types.TestStatusError
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
	switch tree.Stats.Status {
	case types.TestStatusFail:
		t.SetStyle(table.StyleColoredBlackOnRedWhite)
	case types.TestStatusSkip:
		t.SetStyle(table.StyleColoredBlackOnYellowWhite)
	case types.TestStatusPass:
		t.SetStyle(table.StyleColoredBlackOnGreenWhite)
	default:
		t.SetStyle(table.StyleDefault)
	}

	// Add summary footer
	overallStatus := strings.ToUpper(getStatusString(tree.Stats.Status))

	// Use enhanced duration formatting that shows speedup for parallel runs
	durationDisplay := formatDuration(tree.Duration)

	footerRow := []interface{}{
		"TOTAL",
		"",
		durationDisplay,
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
		strings.ToUpper(f.GetStatusString(node.Status)), // uppercase for table display
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

// GetStatusString returns a consistent lowercase status string
func (f *TreeTableFormatter) GetStatusString(status types.TestStatus) string {
	return getStatusString(status)
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
		buf.WriteString(fmt.Sprintf("Status: %s\n", strings.ToUpper(getStatusString(tree.Stats.Status))))
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
		fmt.Fprintf(buf, "%sError: %s\n", errorPrefix, node.Error.Error())
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
