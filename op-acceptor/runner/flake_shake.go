package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ethereum-optimism/infra/op-acceptor/types"
	"github.com/ethereum/go-ethereum/log"
)

// FlakeShakeResult represents aggregated results for a test across multiple runs
type FlakeShakeResult struct {
	TestName       string        `json:"test_name"`
	Package        string        `json:"package"`
	TotalRuns      int           `json:"total_runs"`
	Passes         int           `json:"passes"`
	Failures       int           `json:"failures"`
	Skipped        int           `json:"skipped"`
	PassRate       float64       `json:"pass_rate"`
	AvgDuration    time.Duration `json:"avg_duration"`
	MinDuration    time.Duration `json:"min_duration"`
	MaxDuration    time.Duration `json:"max_duration"`
	FailureLogs    []string      `json:"failure_logs,omitempty"`
	LastFailure    *time.Time    `json:"last_failure,omitempty"`
	Recommendation string        `json:"recommendation"`
}

// FlakeShakeReport contains the complete flake-shake analysis
type FlakeShakeReport struct {
	Date        string             `json:"date"`
	Gate        string             `json:"gate"`
	TotalRuns   int                `json:"total_runs"`
	Iterations  int                `json:"iterations"`
	Tests       []FlakeShakeResult `json:"tests"`
	GeneratedAt time.Time          `json:"generated_at"`
	RunID       string             `json:"run_id"`
}

// FlakeShakeRunner wraps a TestRunner to provide flake-shake functionality
type FlakeShakeRunner struct {
	baseRunner TestRunner
	iterations int
	log        log.Logger
}

// NewFlakeShakeRunner creates a new flake-shake runner
func NewFlakeShakeRunner(baseRunner TestRunner, iterations int, log log.Logger) *FlakeShakeRunner {
	return &FlakeShakeRunner{
		baseRunner: baseRunner,
		iterations: iterations,
		log:        log,
	}
}

// RunFlakeShake runs tests multiple times and generates stability report
func (f *FlakeShakeRunner) RunFlakeShake(ctx context.Context, gate string) (*FlakeShakeReport, error) {
	f.log.Info("Starting flake-shake analysis", "gate", gate, "iterations", f.iterations)

	results := make(map[string][]types.TestResult)

	// Run tests multiple times
	for i := 1; i <= f.iterations; i++ {
		f.log.Info("Running iteration", "iteration", i, "total", f.iterations)

		// Run all tests once
		runResult, err := f.baseRunner.RunAllTests(ctx)
		if err != nil {
			f.log.Error("Failed to run tests", "iteration", i, "error", err)
			// Continue with other iterations even if one fails
			continue
		}

		// Collect results from all gates/suites/tests
		for _, gateResult := range runResult.Gates {
			for testName, testResult := range gateResult.Tests {
				// For package-level tests, use package name as key
				key := testResult.Metadata.Package
				if testName != "" && testName != testResult.Metadata.Package {
					key = fmt.Sprintf("%s::%s", testResult.Metadata.Package, testName)
				}
				results[key] = append(results[key], *testResult)
			}
			for _, suiteResult := range gateResult.Suites {
				for testName, testResult := range suiteResult.Tests {
					// For package-level tests, use package name as key
					key := testResult.Metadata.Package
					if testName != "" && testName != testResult.Metadata.Package {
						key = fmt.Sprintf("%s::%s", testResult.Metadata.Package, testName)
					}
					results[key] = append(results[key], *testResult)
				}
			}
		}
	}

	// Generate report
	report := f.generateReport(results, gate)

	return report, nil
}

// generateReport creates a FlakeShakeReport from aggregated test results
func (f *FlakeShakeRunner) generateReport(results map[string][]types.TestResult, gate string) *FlakeShakeReport {
	report := &FlakeShakeReport{
		Date:        time.Now().Format("2006-01-02"),
		Gate:        gate,
		Iterations:  f.iterations,
		GeneratedAt: time.Now(),
		RunID:       "", // RunID will be set from environment if available
	}

	for testKey, testResults := range results {
		var pkg, name string

		// Parse the key - it's either "package" or "package::testname"
		if strings.Contains(testKey, "::") {
			parts := strings.SplitN(testKey, "::", 2)
			pkg = parts[0]
			name = parts[1]
		} else {
			// Package-level test
			pkg = testKey
			// Extract the last component of the package path as the test name
			pkgParts := strings.Split(pkg, "/")
			if len(pkgParts) > 0 {
				name = pkgParts[len(pkgParts)-1]
			}
		}

		result := FlakeShakeResult{
			TestName:    name,
			Package:     pkg,
			TotalRuns:   len(testResults),
			MinDuration: time.Hour, // Start with large value
		}

		var totalDuration time.Duration

		for _, tr := range testResults {
			switch tr.Status {
			case types.TestStatusPass:
				result.Passes++
			case types.TestStatusFail:
				result.Failures++
				if len(result.FailureLogs) < 5 { // Keep first 5 failure logs
					result.FailureLogs = append(result.FailureLogs, tr.Stdout)
				}
				now := time.Now()
				result.LastFailure = &now
			case types.TestStatusSkip:
				result.Skipped++
			}

			duration := tr.Duration
			totalDuration += duration
			if duration < result.MinDuration {
				result.MinDuration = duration
			}
			if duration > result.MaxDuration {
				result.MaxDuration = duration
			}
		}

		if result.TotalRuns > 0 {
			result.AvgDuration = totalDuration / time.Duration(result.TotalRuns)
			result.PassRate = float64(result.Passes) / float64(result.TotalRuns) * 100
		}

		// Generate recommendation - simple binary classification
		if result.PassRate == 100 {
			result.Recommendation = "STABLE"
		} else {
			result.Recommendation = "UNSTABLE"
		}

		report.Tests = append(report.Tests, result)
		report.TotalRuns += result.TotalRuns
	}

	return report
}

// SaveFlakeShakeReport saves the report in both JSON and HTML formats
func SaveFlakeShakeReport(report *FlakeShakeReport, outputDir string) ([]string, error) {
	var savedFiles []string
	var errorsList []error

	// Save JSON report
	jsonFilename := filepath.Join(outputDir, "flake-shake-report.json")
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		errorsList = append(errorsList, fmt.Errorf("failed to marshal JSON: %w", err))
	} else {
		if err := os.WriteFile(jsonFilename, data, 0644); err != nil {
			errorsList = append(errorsList, fmt.Errorf("failed to write JSON file: %w", err))
		} else {
			savedFiles = append(savedFiles, jsonFilename)
		}
	}

	// Save HTML report
	htmlFilename := filepath.Join(outputDir, "flake-shake-report.html")
	if err := saveHTMLReport(report, htmlFilename); err != nil {
		errorsList = append(errorsList, fmt.Errorf("failed to save HTML report: %w", err))
	} else {
		savedFiles = append(savedFiles, htmlFilename)
	}

	// Return error if any saves failed
	if len(errorsList) > 0 {
		errMsg := "failed to save some report formats:"
		for _, e := range errorsList {
			errMsg += "\n  - " + e.Error()
		}
		return savedFiles, errors.New(errMsg)
	}

	return savedFiles, nil
}

// saveHTMLReport saves the report as HTML
func saveHTMLReport(report *FlakeShakeReport, filename string) error {
	htmlTemplate := `<!DOCTYPE html>
<html>
<head>
    <title>Flake-Shake Report - {{.Date}}</title>
    <style>
        body { font-family: Arial, sans-serif; margin: 20px; }
        h1 { color: #333; }
        .summary { background: #f5f5f5; padding: 15px; border-radius: 5px; margin: 20px 0; }
        table { border-collapse: collapse; width: 100%; }
        th, td { border: 1px solid #ddd; padding: 12px; text-align: left; }
        th { background: #4CAF50; color: white; }
        .pass-rate-100 { color: #4CAF50; font-weight: bold; }
        .pass-rate-low { color: #f44336; }
        .recommendation-STABLE { color: #4CAF50; font-weight: bold; }
        .recommendation-UNSTABLE { color: #f44336; font-weight: bold; }
        details { margin: 10px 0; }
        summary { cursor: pointer; padding: 5px; background: #f0f0f0; }
        .failure-log { background: #ffebee; padding: 10px; margin: 5px 0; font-family: monospace; font-size: 12px; white-space: pre-wrap; }
    </style>
</head>
<body>
    <h1>Flake-Shake Report</h1>
    <div class="summary">
        <p><strong>Date:</strong> {{.Date}}</p>
        <p><strong>Gate:</strong> {{.Gate}}</p>
        <p><strong>Iterations per Test:</strong> {{.Iterations}}</p>
        <p><strong>Run ID:</strong> {{.RunID}}</p>
    </div>

    <h2>Test Results</h2>
    <table>
        <tr>
            <th>Test Name</th>
            <th>Package</th>
            <th>Runs</th>
            <th>Pass Rate</th>
            <th>Avg Duration</th>
            <th>Recommendation</th>
            <th>Details</th>
        </tr>
        {{range .Tests}}
        <tr>
            <td>{{.TestName}}</td>
            <td style="font-size: 12px;">{{.Package}}</td>
            <td>{{.TotalRuns}}</td>
            <td class="pass-rate-{{if eq .PassRate 100.0}}100{{else}}low{{end}}">
                {{printf "%.1f" .PassRate}}%
            </td>
            <td>{{.AvgDuration}}</td>
            <td class="recommendation-{{.Recommendation}}">{{.Recommendation}}</td>
            <td>
                {{if gt .Failures 0}}
                <details>
                    <summary>{{.Failures}} failure(s)</summary>
                    <!-- Last failure timestamp removed per requirements -->
                    {{range .FailureLogs}}
                    <div class="failure-log">{{.}}</div>
                    {{end}}
                </details>
                {{else}}
                <span style="color: #4CAF50;">✓ All passed</span>
                {{end}}
            </td>
        </tr>
        {{end}}
    </table>
</body>
</html>`

	tmpl, err := template.New("report").Parse(htmlTemplate)
	if err != nil {
		return fmt.Errorf("failed to parse template: %w", err)
	}

	file, err := os.Create(filename)
	if err != nil {
		return fmt.Errorf("failed to create file: %w", err)
	}
	defer func() { _ = file.Close() }()

	return tmpl.Execute(file, report)
}
