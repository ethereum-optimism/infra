package runner

import "github.com/ethereum-optimism/infra/op-acceptor/logging"

var _ JSONStore = (*jsonStore)(nil)

// jsonStore implements JSONStore interface using FileLogger's RawJSONSink
type jsonStore struct {
	rawJSONSink *logging.RawJSONSink
}

// NewJSONStore creates a new JSON store
func NewJSONStore(fileLogger *logging.FileLogger) JSONStore {
	store := &jsonStore{}

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
func (s *jsonStore) Store(testID string, rawJSON []byte) error {
	if len(rawJSON) == 0 || s.rawJSONSink == nil {
		return nil
	}
	return s.rawJSONSink.StoreRawJSON(testID, rawJSON)
}

// StoreFromFile copies raw JSON from an existing file path
func (s *jsonStore) StoreFromFile(testID, path string) error {
	if s.rawJSONSink == nil {
		return nil
	}
	return s.rawJSONSink.StoreRawJSONFromFile(testID, path)
}
