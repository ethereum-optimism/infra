package nat

import (
	"fmt"
	"os"
	"time"

	"github.com/ethereum/go-ethereum/log"
	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"

	"github.com/ethereum-optimism/infra/op-acceptor/runner"
	"github.com/ethereum-optimism/infra/op-acceptor/types"
)

// ResultFormatter is responsible for formatting and displaying test results.
type ResultFormatter interface {
	FormatResults(result *runner.RunnerResult) error
}

// ConsoleResultFormatter implements the ResultFormatter interface.
type ConsoleResultFormatter struct {
	logger log.Logger
}

// NewConsoleResultFormatter creates a new ConsoleResultFormatter.
func NewConsoleResultFormatter(logger log.Logger) *ConsoleResultFormatter {
	return &ConsoleResultFormatter{
		logger: logger,
	}
}

// FormatResults formats and displays the test results.
func (f *ConsoleResultFormatter) FormatResults(result *runner.RunnerResult) error {
	f.logger.Info("Printing results...")
	t := table.NewWriter()
	t.SetOutputMirror(os.Stdout)
	t.SetTitle(fmt.Sprintf("Acceptance Testing Results (%s)", formatDuration(result.Duration)))

	// Configure columns
	t.AppendHeader(table.Row{
		"Type", "ID", "Duration", "Tests", "Passed", "Failed", "Skipped", "Status", "Error",
	})

	// Set column configurations for better readability
	t.SetColumnConfigs([]table.ColumnConfig{
		{Name: "Type", AutoMerge: true},
		{Name: "ID", WidthMax: 50, WidthMaxEnforcer: text.WrapSoft},
		{Name: "Duration", Align: text.AlignRight},
		{Name: "Tests", Align: text.AlignRight},
		{Name: "Passed", Align: text.AlignRight},
		{Name: "Failed", Align: text.AlignRight},
		{Name: "Skipped", Align: text.AlignRight},
	})

	// Add flag to show individual tests for packages
	showIndividualTests := true

	// Print gates and their results
	for _, gate := range result.Gates {
		// Gate row - show test counts but no "1" in Tests column
		t.AppendRow(table.Row{
			"Gate",
			gate.ID,
			formatDuration(gate.Duration),
			"-", // Don't count gate as a test
			gate.Stats.Passed,
			gate.Stats.Failed,
			gate.Stats.Skipped,
			getResultString(gate.Status),
			"",
		})

		// Print standalone tests for this gate
		i := 0
		for testName, test := range gate.Tests {
			prefix := "├─"
			if i == len(gate.Tests)-1 && len(gate.Suites) == 0 {
				prefix = "└─"
			}

			t.AppendRow(table.Row{
				"",
				fmt.Sprintf("%s %s", prefix, testName),
				formatDuration(test.Duration),
				"1", // Count actual test
				boolToInt(test.Status == types.TestStatusPass),
				boolToInt(test.Status == types.TestStatusFail),
				boolToInt(test.Status == types.TestStatusSkip),
				getResultString(test.Status),
				test.Error,
			})
			i++
		}

		// Print suites and their tests
		i = 0
		for suiteName, suite := range gate.Suites {
			prefix := "├─"
			if i == len(gate.Suites)-1 {
				prefix = "└─"
			}

			// Suite row - show test counts but no "1" in Tests column
			t.AppendRow(table.Row{
				"Suite",
				fmt.Sprintf("%s %s", prefix, suiteName),
				formatDuration(suite.Duration),
				"-", // Don't count suite as a test
				suite.Stats.Passed,
				suite.Stats.Failed,
				suite.Stats.Skipped,
				getResultString(suite.Status),
				"",
			})

			// Print tests for this suite
			if showIndividualTests {
				j := 0
				for testName, subTest := range suite.Tests {
					subPrefix := "   ├─"
					if j == len(suite.Tests)-1 {
						subPrefix = "   └─"
					}

					t.AppendRow(table.Row{
						"",
						fmt.Sprintf("%s %s", subPrefix, testName),
						formatDuration(subTest.Duration),
						"1", // Count actual test
						boolToInt(subTest.Status == types.TestStatusPass),
						boolToInt(subTest.Status == types.TestStatusFail),
						boolToInt(subTest.Status == types.TestStatusSkip),
						getResultString(subTest.Status),
						subTest.Error,
					})
					j++
				}
			}

			i++
		}

		t.AppendSeparator()
	}

	// Update the table style setting based on result status
	if result.Status == types.TestStatusPass {
		t.SetStyle(table.StyleColoredBlackOnGreenWhite)
	} else if result.Status == types.TestStatusSkip {
		t.SetStyle(table.StyleColoredBlackOnYellowWhite)
	} else {
		t.SetStyle(table.StyleColoredBlackOnRedWhite)
	}

	// Add summary footer
	t.AppendFooter(table.Row{
		"TOTAL",
		"",
		formatDuration(result.Duration),
		result.Stats.Total, // Show total number of actual tests
		result.Stats.Passed,
		result.Stats.Failed,
		result.Stats.Skipped,
		getResultString(result.Status),
		"",
	})

	t.Render()

	fmt.Println(result.String())
	if result.Status == types.TestStatusFail {
		printGandalf()
	}

	return nil
}

// Helper function to format duration to seconds with 1 decimal place
func formatDuration(d time.Duration) string {
	return fmt.Sprintf("%.1fs", d.Seconds())
}
