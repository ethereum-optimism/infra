package nat

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/log"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDefaultTestScheduler_RunOnce tests the scheduler in run-once mode
func TestDefaultTestScheduler_RunOnce(t *testing.T) {
	// Setup
	logger := log.New()
	callCount := 0

	scheduler := &DefaultTestScheduler{
		interval: 100 * time.Millisecond,
		runOnce:  true,
		logger:   logger,
		done:     make(chan struct{}),
	}

	// Register a test callback
	scheduler.RegisterCallback(func() error {
		callCount++
		return nil
	})

	// Start the scheduler
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	err := scheduler.Start(ctx)
	require.NoError(t, err)

	// In run-once mode, the callback should be called exactly once immediately
	assert.Equal(t, 1, callCount, "Expected callback to be called exactly once")

	// Wait a bit to make sure no more calls happen
	time.Sleep(200 * time.Millisecond)

	// Call count should still be 1
	assert.Equal(t, 1, callCount, "Expected callback to be called exactly once")
}

// TestDefaultTestScheduler_Periodic tests the scheduler in periodic mode
func TestDefaultTestScheduler_Periodic(t *testing.T) {
	// Setup
	logger := log.New()

	// Use a channel to synchronize and count callback executions
	callChan := make(chan struct{}, 10) // Buffer to avoid blocking
	expectedCalls := 4                  // We want to verify exactly 4 calls

	scheduler := &DefaultTestScheduler{
		interval: 10 * time.Millisecond, // Use a short interval for faster test execution
		runOnce:  false,
		logger:   logger,
		done:     make(chan struct{}),
	}

	// Register a test callback that signals the channel
	scheduler.RegisterCallback(func() error {
		callChan <- struct{}{}
		return nil
	})

	// Create a context with cancel function to stop the test
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start the scheduler
	err := scheduler.Start(ctx)
	require.NoError(t, err)

	// Wait for exactly the expected number of calls
	for i := 0; i < expectedCalls; i++ {
		select {
		case <-callChan:
			// Got a callback execution
		case <-time.After(1 * time.Second): // Safety timeout
			t.Fatalf("Timed out waiting for callback execution %d/%d", i+1, expectedCalls)
		}
	}

	// Stop the scheduler
	err = scheduler.Stop()
	require.NoError(t, err)

	// Verify no more calls happen after stopping
	// Wait a short time to catch any potential extra calls
	extraCallCount := 0
	select {
	case <-callChan:
		extraCallCount++
	case <-time.After(50 * time.Millisecond):
		// No more calls, which is expected
	}
	assert.Equal(t, 0, extraCallCount, "Expected no more calls after stopping")

	// Wait for shutdown
	err = scheduler.WaitForShutdown(ctx)
	assert.NoError(t, err)
}

// TestDefaultTestScheduler_CallbackError tests error handling in the callback
func TestDefaultTestScheduler_CallbackError(t *testing.T) {
	// Setup
	logger := log.New()
	expectedError := errors.New("test callback error")

	scheduler := &DefaultTestScheduler{
		interval: 100 * time.Millisecond,
		runOnce:  true,
		logger:   logger,
		done:     make(chan struct{}),
	}

	// Register a callback that returns an error
	scheduler.RegisterCallback(func() error {
		return expectedError
	})

	// Start the scheduler - with run-once mode, this should call the callback immediately
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	// The error from the callback should be returned
	err := scheduler.Start(ctx)
	assert.Error(t, err)
	assert.Equal(t, expectedError, err)
}

// TestDefaultTestScheduler_NoCallback tests that an error is returned when no callback is registered
func TestDefaultTestScheduler_NoCallback(t *testing.T) {
	// Setup
	logger := log.New()

	scheduler := &DefaultTestScheduler{
		interval: 100 * time.Millisecond,
		runOnce:  true,
		logger:   logger,
		done:     make(chan struct{}),
	}

	// Start without registering a callback
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	// Should return an error
	err := scheduler.Start(ctx)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "callback must be registered")
}

// TestDefaultTestScheduler_AlreadyStopped tests that Stop() is idempotent
func TestDefaultTestScheduler_AlreadyStopped(t *testing.T) {
	// Setup
	logger := log.New()

	scheduler := &DefaultTestScheduler{
		interval: 100 * time.Millisecond,
		runOnce:  true,
		logger:   logger,
		done:     make(chan struct{}),
	}

	// Register a test callback
	scheduler.RegisterCallback(func() error {
		return nil
	})

	// Stop without starting
	err := scheduler.Stop()
	assert.NoError(t, err, "Stop should be idempotent")

	// Stop again
	err = scheduler.Stop()
	assert.NoError(t, err, "Second stop should also succeed")
}

// TestDefaultTestScheduler_WaitForShutdown tests waiting for goroutines to exit
func TestDefaultTestScheduler_WaitForShutdown(t *testing.T) {
	// Setup
	logger := log.New()

	scheduler := &DefaultTestScheduler{
		interval: 100 * time.Millisecond,
		runOnce:  false,
		logger:   logger,
		done:     make(chan struct{}),
	}

	// Register a test callback
	scheduler.RegisterCallback(func() error {
		return nil
	})

	// Start the scheduler
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	err := scheduler.Start(ctx)
	require.NoError(t, err)

	// Stop the scheduler
	err = scheduler.Stop()
	require.NoError(t, err)

	// Wait for shutdown - should succeed since we've stopped
	err = scheduler.WaitForShutdown(ctx)
	assert.NoError(t, err, "WaitForShutdown should succeed after stopping")
}
