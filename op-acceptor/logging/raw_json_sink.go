package logging

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/ethereum-optimism/infra/op-acceptor/types"
)

const rawGoEventsLog = "raw_go_events.log"

// RawJSONSink writes test results in the raw Go JSON test output format
// that matches what `go test -json` would produce.
// This is useful for feeding to tools like gotestsum that expect this format.
type RawJSONSink struct {
	logger *FileLogger

	// Store the original raw JSON output from go test -json
	mu            sync.Mutex
	rawJSONEvents map[string][]byte // Map of [test-id] -> []raw JSON events
}

// GoTestEvent represents an event in the go test JSON output
// Matches the format described in Go's test2json package
type GoTestEvent struct {
	Time    time.Time `json:"Time"`
	Action  string    `json:"Action"`
	Package string    `json:"Package"`
	Test    string    `json:"Test,omitempty"`
	Output  string    `json:"Output,omitempty"`
	Elapsed float64   `json:"Elapsed,omitempty"`
}

// Consume writes the raw JSON output for a test to the raw_go_events.log file
func (s *RawJSONSink) Consume(result *types.TestResult, runID string) error {
	// Get the raw_go_events.log file path for this runID
	baseDir, err := s.logger.GetDirectoryForRunID(runID)
	if err != nil {
		return err
	}

	// Create the raw events file path
	rawEventsFile := filepath.Join(baseDir, rawGoEventsLog)

	// Get or create the async writer
	writer, err := s.logger.getAsyncWriter(rawEventsFile)
	if err != nil {
		return err
	}

	// Initialize if needed
	s.mu.Lock()
	if s.rawJSONEvents == nil {
		s.rawJSONEvents = make(map[string][]byte)
	}
	// Get the raw JSON events for this test
	rawJSON, exists := s.rawJSONEvents[result.Metadata.ID]
	s.mu.Unlock()

	if exists && len(rawJSON) > 0 {
		// Write the raw JSON events directly to the file
		if err := writer.Write(rawJSON); err != nil {
			return fmt.Errorf("failed to write raw JSON events: %w", err)
		}
	}

	return nil
}

// GetRawEventsFileForRunID returns the path to the raw_go_events.log file for the given runID
func (s *RawJSONSink) GetRawEventsFileForRunID(runID string) (string, error) {
	baseDir, err := s.logger.GetDirectoryForRunID(runID)
	if err != nil {
		return "", err
	}
	return filepath.Join(baseDir, rawGoEventsLog), nil
}

// StoreRawJSON stores the raw JSON output for a test
// This must be called by the test runner to provide the original JSON data
func (s *RawJSONSink) StoreRawJSON(testID string, rawJSON []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.rawJSONEvents == nil {
		s.rawJSONEvents = make(map[string][]byte)
	}
	s.rawJSONEvents[testID] = rawJSON
}

// GetRawJSON retrieves the raw JSON output for a test ID
// Returns the raw JSON bytes and a boolean indicating if the test ID was found
func (s *RawJSONSink) GetRawJSON(testID string) ([]byte, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.rawJSONEvents == nil {
		return nil, false
	}

	rawJSON, exists := s.rawJSONEvents[testID]
	if !exists {
		return nil, false
	}

	// Return a copy to avoid race conditions
	result := make([]byte, len(rawJSON))
	copy(result, rawJSON)
	return result, true
}

// Complete creates the results directory
func (s *RawJSONSink) Complete(runID string) error {
	// Create the directory for results
	baseDir, err := s.logger.GetDirectoryForRunID(runID)
	if err != nil {
		return err
	}

	resultsDir := filepath.Join(baseDir, "results")
	if err := os.MkdirAll(resultsDir, 0755); err != nil {
		return fmt.Errorf("failed to create results directory: %w", err)
	}

	return nil
}
