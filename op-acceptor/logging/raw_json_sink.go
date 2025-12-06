package logging

import (
	"fmt"
	"io"
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

	// Store the original raw JSON output from go test -json on disk to avoid keeping it in memory.
	mu            sync.Mutex
	rawJSONEvents map[string]string // Map of [test-id] -> temp file path with raw JSON events
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

	// Retrieve the stored path for this test without removing it yet so other consumers can access it.
	path := s.getPath(result.Metadata.ID)
	if path != "" {
		file, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("failed to open raw JSON file %s: %w", path, err)
		}
		defer func() {
			_ = file.Close()
		}()

		if _, err := io.Copy(asyncFileWriterAdapter{writer: writer}, file); err != nil {
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
func (s *RawJSONSink) StoreRawJSON(testID string, rawJSON []byte) error {
	if len(rawJSON) == 0 {
		return nil
	}

	tmpFile, err := s.createTempRawFile(testID)
	if err != nil {
		return err
	}

	if _, err := tmpFile.Write(rawJSON); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpFile.Name())
		return fmt.Errorf("failed to write raw JSON: %w", err)
	}

	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmpFile.Name())
		return fmt.Errorf("failed to close raw JSON file: %w", err)
	}

	s.storePath(testID, tmpFile.Name())
	return nil
}

// StoreRawJSONFromFile copies an existing file into the sink-managed storage.
func (s *RawJSONSink) StoreRawJSONFromFile(testID, sourcePath string) error {
	src, err := os.Open(sourcePath)
	if err != nil {
		return fmt.Errorf("failed to open raw JSON source %s: %w", sourcePath, err)
	}
	defer func() {
		_ = src.Close()
	}()

	tmpFile, err := s.createTempRawFile(testID)
	if err != nil {
		return err
	}

	if _, err := io.Copy(tmpFile, src); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpFile.Name())
		return fmt.Errorf("failed to copy raw JSON: %w", err)
	}

	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmpFile.Name())
		return fmt.Errorf("failed to close raw JSON file: %w", err)
	}

	s.storePath(testID, tmpFile.Name())
	return nil
}

// GetRawJSON retrieves the raw JSON output for a test ID
// Returns the raw JSON bytes and a boolean indicating if the test ID was found
func (s *RawJSONSink) GetRawJSON(testID string) ([]byte, bool) {
	path := s.getPath(testID)
	if path == "" {
		return nil, false
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	return data, true
}

// WriteRawJSONTo streams the stored raw JSON into the provided writer.
func (s *RawJSONSink) WriteRawJSONTo(testID string, w io.Writer) (bool, error) {
	path := s.getPath(testID)
	if path == "" {
		return false, nil
	}

	file, err := os.Open(path)
	if err != nil {
		return false, fmt.Errorf("failed to open raw JSON file %s: %w", path, err)
	}
	defer func() {
		_ = file.Close()
	}()

	if _, err := io.Copy(w, file); err != nil {
		return false, fmt.Errorf("failed to copy raw JSON: %w", err)
	}
	return true, nil
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

	s.cleanupStoredFiles()
	return nil
}

func (s *RawJSONSink) createTempRawFile(testID string) (*os.File, error) {
	prefix := fmt.Sprintf("raw-json-%s-", safeFilename(testID))
	tmpFile, err := os.CreateTemp("", prefix)
	if err != nil {
		return nil, fmt.Errorf("failed to create temp raw JSON file: %w", err)
	}
	return tmpFile, nil
}

func (s *RawJSONSink) storePath(testID, path string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.rawJSONEvents == nil {
		s.rawJSONEvents = make(map[string]string)
	}
	s.rawJSONEvents[testID] = path
}

func (s *RawJSONSink) getPath(testID string) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.rawJSONEvents == nil {
		return ""
	}
	return s.rawJSONEvents[testID]
}

func (s *RawJSONSink) deletePath(testID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.rawJSONEvents == nil {
		return
	}

	if path, ok := s.rawJSONEvents[testID]; ok {
		_ = os.Remove(path)
		delete(s.rawJSONEvents, testID)
	}
}

// DeleteRawJSON removes the stored raw JSON for a test once all consumers are done with it.
func (s *RawJSONSink) DeleteRawJSON(testID string) {
	s.deletePath(testID)
}

func (s *RawJSONSink) cleanupStoredFiles() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.rawJSONEvents == nil {
		return
	}

	for testID, path := range s.rawJSONEvents {
		_ = os.Remove(path)
		delete(s.rawJSONEvents, testID)
	}
}
