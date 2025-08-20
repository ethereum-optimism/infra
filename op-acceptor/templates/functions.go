package templates

import (
	"fmt"
	"html/template"
	"time"

	"github.com/ethereum-optimism/infra/op-acceptor/types"
)

// GetTemplateFunc returns the centralized template functions used across the application
func GetTemplateFunc() template.FuncMap {
	return template.FuncMap{
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
	}
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
