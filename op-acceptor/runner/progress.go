package runner

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ethereum-optimism/infra/op-acceptor/types"
	"github.com/ethereum/go-ethereum/log"
)

// ProgressIndicator interface for UI updates
type ProgressIndicator interface {
	StartGate(gateName string, totalTests int)
	StartSuite(suiteName string, totalTests int)
	StartTest(testName string)
	UpdateTest(testName string, status types.TestStatus)
	CompleteSuite(suiteName string)
	CompleteGate(gateName string)
}

// noOpProgressIndicator provides a no-op implementation of ProgressIndicator
type noOpProgressIndicator struct{}

// NewNoOpProgressIndicator creates a progress indicator that does nothing
func NewNoOpProgressIndicator() ProgressIndicator {
	return &noOpProgressIndicator{}
}

func (n *noOpProgressIndicator) StartGate(gateName string, totalTests int)           {}
func (n *noOpProgressIndicator) StartSuite(suiteName string, totalTests int)         {}
func (n *noOpProgressIndicator) StartTest(testName string)                           {}
func (n *noOpProgressIndicator) UpdateTest(testName string, status types.TestStatus) {}
func (n *noOpProgressIndicator) CompleteSuite(suiteName string)                      {}
func (n *noOpProgressIndicator) CompleteGate(gateName string)                        {}

// consoleProgressIndicator provides a console-based progress indicator
type consoleProgressIndicator struct {
	logger log.Logger
	ticker *time.Ticker
	stopCh chan struct{}
	mu     sync.RWMutex

	currentGate    string
	currentSuite   string
	completedTests int
	totalTests     int
	gateStartTime  time.Time
	suiteStartTime time.Time

	// Track currently running tests
	runningTests map[string]time.Time // test name -> start time

	// Track test completion to provide better estimates
	lastUpdateTime time.Time
}

// NewConsoleProgressIndicator creates a progress indicator that shows updates in the console
func NewConsoleProgressIndicator(logger log.Logger, updateInterval time.Duration) ProgressIndicator {
	if updateInterval == 0 {
		updateInterval = 30 * time.Second // Default to 30 seconds
	}

	indicator := &consoleProgressIndicator{
		logger:       logger,
		ticker:       time.NewTicker(updateInterval),
		stopCh:       make(chan struct{}),
		runningTests: make(map[string]time.Time),
	}

	// Start the progress reporting goroutine
	go indicator.progressReporter()

	return indicator
}

func (c *consoleProgressIndicator) StartGate(gateName string, totalTests int) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.currentGate = gateName
	c.currentSuite = ""
	c.totalTests = totalTests
	c.completedTests = 0
	c.gateStartTime = time.Now()
	c.lastUpdateTime = time.Now()
	c.runningTests = make(map[string]time.Time) // Reset running tests

	c.logger.Info("Starting gate", "gate", gateName, "totalTests", totalTests)
}

func (c *consoleProgressIndicator) StartSuite(suiteName string, totalTests int) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.currentSuite = suiteName
	c.suiteStartTime = time.Now()

	c.logger.Info("Starting suite", "gate", c.currentGate, "suite", suiteName, "suiteTests", totalTests)
}

// StartTest tracks when a test starts running
func (c *consoleProgressIndicator) StartTest(testName string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.runningTests[testName] = time.Now()
	c.logger.Debug("Test started", "test", testName, "runningTests", len(c.runningTests))
}

func (c *consoleProgressIndicator) UpdateTest(testName string, status types.TestStatus) {
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.runningTests, testName)

	c.completedTests++
	c.lastUpdateTime = time.Now()

	// Log individual test completion at debug level to avoid spam
	c.logger.Debug("Test completed", "test", testName, "status", status, "completed", c.completedTests, "total", c.totalTests, "runningTests", len(c.runningTests))
}

func (c *consoleProgressIndicator) CompleteSuite(suiteName string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	duration := time.Since(c.suiteStartTime).Truncate(time.Second)
	c.logger.Info("Completed suite", "gate", c.currentGate, "suite", suiteName, "duration", duration)
	c.currentSuite = ""
}

func (c *consoleProgressIndicator) CompleteGate(gateName string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	duration := time.Since(c.gateStartTime).Truncate(time.Second)
	c.logger.Info("Completed gate", "gate", gateName, "totalTests", c.totalTests, "completed", c.completedTests, "duration", duration)
	c.currentGate = ""
	c.currentSuite = ""
	c.runningTests = make(map[string]time.Time) // Clear running tests
}

// progressReporter runs in a goroutine and periodically reports progress
func (c *consoleProgressIndicator) progressReporter() {
	for {
		select {
		case <-c.ticker.C:
			c.reportProgress()
		case <-c.stopCh:
			return
		}
	}
}

func (c *consoleProgressIndicator) reportProgress() {
	c.mu.RLock()
	defer c.mu.RUnlock()

	detailsStr := formatRunningTests(c.runningTests, 3)

	// Calculate completion percentage
	var percentComplete float64
	if c.totalTests > 0 {
		percentComplete = float64(c.completedTests) * 100.0 / float64(c.totalTests)
	}

	// Create structured log with JSON fields
	logFields := []interface{}{
		"gate", c.currentGate,
		"suite", c.currentSuite,
		"completed", c.completedTests,
		"total", c.totalTests,
		"percent", fmt.Sprintf("%.1f%%", percentComplete),
		"numRunning", len(c.runningTests),
		"longestRunning", detailsStr,
	}

	c.logger.Info("Progress update", logFields...)
}

// Stop stops the progress indicator
func (c *consoleProgressIndicator) Stop() {
	if c.ticker != nil {
		c.ticker.Stop()
	}
	close(c.stopCh)
}

// Helper function that formats running tests into a display string
func formatRunningTests(runningTests map[string]time.Time, maxShow int) string {
	if len(runningTests) == 0 {
		return ""
	}

	// Sort running tests by duration (longest first)
	type runningTest struct {
		name     string
		duration time.Duration
	}

	var running []runningTest
	now := time.Now()
	for testName, startTime := range runningTests {
		running = append(running, runningTest{
			name:     testName,
			duration: now.Sub(startTime),
		})
	}

	// Sort by duration (longest running first)
	sort.Slice(running, func(i, j int) bool {
		return running[i].duration > running[j].duration
	})

	// Format running tests string (limit to maxShow)
	var runningStrs []string
	for i, test := range running {
		if i >= maxShow {
			break
		}
		duration := test.duration.Truncate(time.Second)
		runningStrs = append(runningStrs, fmt.Sprintf("%s (%v)", test.name, duration))
	}

	// Add indicator for additional tests not shown
	if len(running) > maxShow {
		runningStrs = append(runningStrs, fmt.Sprintf("+%d more", len(running)-maxShow))
	}

	return strings.Join(runningStrs, ", ")
}
