package runner

import (
	"sync"

	"github.com/ethereum-optimism/infra/op-acceptor/logging"
)

// jsonStore implements JSONStore interface using FileLogger's RawJSONSink
type jsonStore struct {
	rawJSONSink *logging.RawJSONSink
	storage     map[string][]byte
	mu          sync.RWMutex
}

// NewJSONStore creates a new JSON store
func NewJSONStore(fileLogger *logging.FileLogger) JSONStore {
	store := &jsonStore{
		storage: make(map[string][]byte),
	}

	if fileLogger != nil {
		// Try to get the RawJSONSink from the fileLogger
		if sink, ok := fileLogger.GetSinkByType(RawJSONSinkType); ok {
			if rawJSONSink, ok := sink.(*logging.RawJSONSink); ok {
				store.rawJSONSink = rawJSONSink
			}
		}
	}

	return store
}

// Store stores raw JSON output for a test
func (s *jsonStore) Store(testID string, rawJSON []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Store in local map for retrieval
	s.storage[testID] = rawJSON

	// Also store in the RawJSONSink if available
	if s.rawJSONSink != nil {
		s.rawJSONSink.StoreRawJSON(testID, rawJSON)
	}
}

// Get retrieves raw JSON output for a test
func (s *jsonStore) Get(testID string) ([]byte, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// First try local storage
	if data, exists := s.storage[testID]; exists {
		return data, true
	}

	// Fallback to RawJSONSink if available
	if s.rawJSONSink != nil {
		return s.rawJSONSink.GetRawJSON(testID)
	}

	return nil, false
}
