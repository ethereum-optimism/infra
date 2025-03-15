package nat

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ethereum/go-ethereum/log"
)

// TestScheduler is responsible for scheduling periodic test runs.
type TestScheduler interface {
	Start(ctx context.Context) error
	Stop() error
	RegisterCallback(func() error)
	WaitForShutdown(ctx context.Context) error
	Stopped() bool
}

// DefaultTestScheduler implements the TestScheduler interface.
type DefaultTestScheduler struct {
	interval time.Duration
	runOnce  bool
	logger   log.Logger
	callback func() error

	running atomic.Bool
	done    chan struct{}
	wg      sync.WaitGroup
}

// NewDefaultTestScheduler creates a new DefaultTestScheduler.
func NewDefaultTestScheduler(interval time.Duration, runOnce bool, logger log.Logger) *DefaultTestScheduler {
	return &DefaultTestScheduler{
		interval: interval,
		runOnce:  runOnce,
		logger:   logger,
		done:     make(chan struct{}),
	}
}

// RegisterCallback registers the callback to be called when tests should run.
func (s *DefaultTestScheduler) RegisterCallback(callback func() error) {
	s.callback = callback
}

// Start starts the scheduler.
func (s *DefaultTestScheduler) Start(ctx context.Context) error {
	if s.callback == nil {
		return errors.New("callback must be registered before starting scheduler")
	}

	s.done = make(chan struct{})
	s.running.Store(true)

	if s.runOnce {
		s.logger.Info("Starting scheduler in run-once mode")
		return s.callback()
	}

	s.logger.Info("Starting scheduler in continuous mode", "interval", s.interval)

	// Run tests immediately on startup
	err := s.callback()
	if err != nil {
		return err
	}

	// Start a goroutine for periodic test execution
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.logger.Debug("Starting periodic test runner goroutine", "interval", s.interval)

		for {
			select {
			case <-time.After(s.interval):
				// Check if we should still be running
				if !s.running.Load() {
					s.logger.Debug("Service stopped, exiting periodic test runner")
					return
				}

				// Run tests
				s.logger.Info("Running periodic tests")
				if err := s.callback(); err != nil {
					s.logger.Error("Error running periodic tests", "error", err)
				}
				s.logger.Info("Test run interval", "interval", s.interval)

			case <-s.done:
				s.logger.Debug("Done signal received, stopping periodic test runner")
				return

			case <-ctx.Done():
				s.logger.Debug("Context canceled, stopping periodic test runner")
				s.running.Store(false)
				return
			}
		}
	}()

	return nil
}

// Stop stops the scheduler.
func (s *DefaultTestScheduler) Stop() error {
	// Check if we're already stopped
	if !s.running.Load() {
		s.logger.Debug("Scheduler already stopped, nothing to do")
		return nil
	}

	// Update running state first to prevent new test runs
	s.running.Store(false)

	// Signal goroutines to exit
	s.logger.Debug("Sending done signal to goroutines")
	close(s.done)

	return nil
}

// Stopped returns true if the scheduler is stopped.
func (s *DefaultTestScheduler) Stopped() bool {
	return !s.running.Load()
}

// WaitForShutdown blocks until all goroutines have terminated.
func (s *DefaultTestScheduler) WaitForShutdown(ctx context.Context) error {
	s.logger.Debug("Waiting for all goroutines to terminate")

	// Create a channel that will be closed when the WaitGroup is done
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()

	// Wait for either WaitGroup completion or context expiration
	select {
	case <-done:
		s.logger.Debug("All goroutines terminated successfully")
		return nil
	case <-ctx.Done():
		s.logger.Warn("Timed out waiting for goroutines to terminate", "error", ctx.Err())
		return ctx.Err()
	}
}
