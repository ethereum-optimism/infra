package nat

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTestFailedError(t *testing.T) {
	// Create a TestFailedError
	msg := "some tests failed"
	err := NewTestFailedError(msg)

	// Verify it implements the error interface
	assert.Equal(t, msg, err.Error())

	// Test error type detection with type assertion
	var asError error = err
	_, ok := asError.(*TestFailedError)
	assert.True(t, ok, "should be detectable as *TestFailedError")

	// Test that it's not detected as another error type
	var otherErr error = errors.New("other error")
	_, ok = otherErr.(*TestFailedError)
	assert.False(t, ok, "random error should not be detected as *TestFailedError")
}
