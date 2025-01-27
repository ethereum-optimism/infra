package metrics

import (
	"errors"
	"regexp"
	"testing"
)

func TestErrToLabel(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{
			name: "nil error",
			err:  nil,
		},
		{
			name: "simple error",
			err:  errors.New("test error"),
		},
		{
			name: "error with special chars",
			err:  errors.New("test@error#123"),
		},
		{
			name: "error with multiple spaces",
			err:  errors.New("test   error"),
		},
		{
			name: "error with multiple underscores",
			err:  errors.New("test__error"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := errToLabel(tt.err)
			validLabelRegex := regexp.MustCompile(`[a-zA-Z_][a-zA-Z0-9_]*`)
			if !validLabelRegex.MatchString(result) {
				t.Errorf("errLabel() = %v, is not a valid Prometheus label", result)
			}
		})
	}
}

func TestRecordError(t *testing.T) {
	// just test that it doesn't panic
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("RecordError panic'd")
		}
	}()

	RecordError("test_error")
}

func TestRecordErrorDetails(t *testing.T) {
	// Test with nil error
	RecordErrorDetails("test", nil)

	// Test with actual error
	RecordErrorDetails("test", errors.New("sample error"))
}

func TestRecordValidation(t *testing.T) {
	// Test various validation scenarios
	RecordValidation("test-network", "validator1", "test", "pass", nil)
	RecordValidation("test-network", "validator2", "test", "fail", errors.New("validation error"))
}

func TestRecordAcceptance(t *testing.T) {
	// Test acceptance scenarios
	RecordAcceptance("test-network", "pass", nil)
	RecordAcceptance("test-network", "fail", errors.New("acceptance error"))
}
