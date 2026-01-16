package proxyd

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestProbeWorker(t *testing.T) {
	tests := []struct {
		name           string
		serverResponse int
		expectedResult bool
		redirectURL    string // Optional redirect URL
	}{
		{
			name:           "successful healthcheck",
			serverResponse: http.StatusOK,
			expectedResult: true,
		},
		{
			name:           "failed healthcheck - 500",
			serverResponse: http.StatusInternalServerError,
			expectedResult: false,
		},
		{
			name:           "failed healthcheck - 503 service unavailable",
			serverResponse: http.StatusServiceUnavailable,
			expectedResult: false,
		},
		{
			name:           "failed healthcheck - 400 bad request",
			serverResponse: http.StatusBadRequest,
			expectedResult: false,
		},
		{
			name:           "failed healthcheck - 401 unauthorized",
			serverResponse: http.StatusUnauthorized,
			expectedResult: false,
		},
		{
			name:           "failed healthcheck - 403 forbidden",
			serverResponse: http.StatusForbidden,
			expectedResult: false,
		},
		{
			name:           "failed healthcheck - 404 not found",
			serverResponse: http.StatusNotFound,
			expectedResult: false,
		},
		{
			name:           "redirect healthcheck",
			serverResponse: http.StatusTemporaryRedirect,
			expectedResult: false,
			redirectURL:    "http://example.com/redirect",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a test server
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tt.redirectURL != "" {
					http.Redirect(w, r, tt.redirectURL, tt.serverResponse)
					return
				}
				w.WriteHeader(tt.serverResponse)
			}))
			defer server.Close()

			// Create a channel to receive probe results
			resultChan := make(chan bool, 1)
			messageChan := make(chan string, 1)

			// Create probe spec
			spec := ProbeSpec{
				FailureThreshold: 1,
				SuccessThreshold: 1,
				Period:           1 * time.Second,
				Timeout:          1 * time.Second,
			}

			// Create probe worker
			worker, err := NewProbeWorker(
				"test-backend",
				server.URL,
				spec,
				func(result bool, message string) {
					resultChan <- result
					messageChan <- message
				},
				nil,
			)
			if err != nil {
				t.Fatalf("Failed to create probe worker: %v", err)
			}

			// Start the worker
			worker.Start()
			defer worker.Stop()

			// Wait for the first probe result
			select {
			case result := <-resultChan:
				if result != tt.expectedResult {
					t.Errorf("Expected result %v, got %v", tt.expectedResult, result)
				}
				// For redirects, verify the error message contains "redirect"
				if tt.serverResponse == http.StatusFound {
					message := <-messageChan
					if message == "" || !containsRedirectMessage(message) {
						t.Errorf("Expected redirect message, got: %s", message)
					}
				}
			case <-time.After(2 * time.Second):
				t.Fatal("Timeout waiting for probe result")
			}
		})
	}
}

func containsRedirectMessage(message string) bool {
	return message == "HTTP Probe result is a redirect: 302 Found"
}

func TestProbeWorkerTimeout(t *testing.T) {
	// Create a test server that hangs
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second) // Longer than the timeout
	}))
	defer server.Close()

	// Create a channel to receive probe results
	resultChan := make(chan bool, 1)
	messageChan := make(chan string, 1)

	// Create probe spec with short timeout
	spec := ProbeSpec{
		FailureThreshold: 1,
		SuccessThreshold: 1,
		Period:           1 * time.Second,
		Timeout:          1 * time.Second,
	}

	// Create probe worker
	worker, err := NewProbeWorker(
		"test-backend",
		server.URL,
		spec,
		func(result bool, message string) {
			resultChan <- result
			messageChan <- message
		},
		nil,
	)
	if err != nil {
		t.Fatalf("Failed to create probe worker: %v", err)
	}

	// Start the worker
	worker.Start()
	defer worker.Stop()

	// Wait for the first probe result
	select {
	case result := <-resultChan:
		if result != false {
			t.Errorf("Expected timeout to result in false, got %v", result)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for probe result")
	}
}

func TestProbeWorkerThresholdBehavior(t *testing.T) {
	t.Run("failure threshold prevents premature unhealthy marking", func(t *testing.T) {
		var requestCount atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			count := requestCount.Add(1)
			if count <= 2 {
				w.WriteHeader(http.StatusServiceUnavailable)
			} else {
				w.WriteHeader(http.StatusOK)
			}
		}))
		defer server.Close()

		var callCount atomic.Int32
		resultChan := make(chan bool, 10)

		spec := ProbeSpec{
			FailureThreshold: 3,
			SuccessThreshold: 1,
			Period:           50 * time.Millisecond,
			Timeout:          1 * time.Second,
		}

		worker, err := NewProbeWorker(
			"test-backend",
			server.URL,
			spec,
			func(result bool, message string) {
				callCount.Add(1)
				resultChan <- result
			},
			nil,
		)
		if err != nil {
			t.Fatalf("Failed to create probe worker: %v", err)
		}

		worker.Start()
		defer worker.Stop()

		// Wait for probes to complete (first 2 fail, 3rd succeeds)
		time.Sleep(300 * time.Millisecond)

		// Should only have received success callback since failures didn't meet threshold
		select {
		case result := <-resultChan:
			if !result {
				t.Errorf("Expected first callback to be success (threshold not met for failures)")
			}
		case <-time.After(500 * time.Millisecond):
			t.Fatal("Timeout waiting for probe result")
		}
	})

	t.Run("success threshold prevents premature healthy marking", func(t *testing.T) {
		var requestCount atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requestCount.Add(1)
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		var callCount atomic.Int32
		resultChan := make(chan bool, 10)

		spec := ProbeSpec{
			FailureThreshold: 1,
			SuccessThreshold: 3,
			Period:           50 * time.Millisecond,
			Timeout:          1 * time.Second,
		}

		worker, err := NewProbeWorker(
			"test-backend",
			server.URL,
			spec,
			func(result bool, message string) {
				callCount.Add(1)
				resultChan <- result
			},
			nil,
		)
		if err != nil {
			t.Fatalf("Failed to create probe worker: %v", err)
		}

		worker.Start()
		defer worker.Stop()

		// Wait for at least 3 successful probes
		time.Sleep(250 * time.Millisecond)

		// First callback should only come after 3 consecutive successes
		select {
		case result := <-resultChan:
			if !result {
				t.Errorf("Expected success after meeting threshold")
			}
			// Verify we needed 3 probes
			if requestCount.Load() < 3 {
				t.Errorf("Expected at least 3 requests before success callback, got %d", requestCount.Load())
			}
		case <-time.After(500 * time.Millisecond):
			t.Fatal("Timeout waiting for probe result")
		}
	})

	t.Run("failure threshold triggers after consecutive failures", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
		}))
		defer server.Close()

		resultChan := make(chan bool, 10)

		spec := ProbeSpec{
			FailureThreshold: 3,
			SuccessThreshold: 1,
			Period:           50 * time.Millisecond,
			Timeout:          1 * time.Second,
		}

		worker, err := NewProbeWorker(
			"test-backend",
			server.URL,
			spec,
			func(result bool, message string) {
				resultChan <- result
			},
			nil,
		)
		if err != nil {
			t.Fatalf("Failed to create probe worker: %v", err)
		}

		worker.Start()
		defer worker.Stop()

		// Wait for 3 consecutive failures
		select {
		case result := <-resultChan:
			if result {
				t.Errorf("Expected failure after meeting threshold")
			}
		case <-time.After(500 * time.Millisecond):
			t.Fatal("Timeout waiting for probe result")
		}
	})
}

func TestProbeWorkerDNSFailure(t *testing.T) {
	resultChan := make(chan bool, 1)
	messageChan := make(chan string, 1)

	spec := ProbeSpec{
		FailureThreshold: 1,
		SuccessThreshold: 1,
		Period:           100 * time.Millisecond,
		Timeout:          1 * time.Second,
	}

	worker, err := NewProbeWorker(
		"test-backend",
		"http://invalid-domain.com/health",
		spec,
		func(result bool, message string) {
			resultChan <- result
			messageChan <- message
		},
		nil,
	)
	if err != nil {
		t.Fatalf("Failed to create probe worker: %v", err)
	}

	worker.Start()
	defer worker.Stop()

	select {
	case result := <-resultChan:
		if result {
			t.Errorf("Expected DNS failure to result in false, got true")
		}
		message := <-messageChan
		if !strings.Contains(message, "no such host") && !strings.Contains(message, "dial") {
			t.Errorf("Expected DNS error message, got: %s", message)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Timeout waiting for probe result")
	}
}

func TestProbeWorkerConnectionRefused(t *testing.T) {
	resultChan := make(chan bool, 1)
	messageChan := make(chan string, 1)

	spec := ProbeSpec{
		FailureThreshold: 1,
		SuccessThreshold: 1,
		Period:           100 * time.Millisecond,
		Timeout:          1 * time.Second,
	}

	// Use localhost with a port that's unlikely to be listening
	worker, err := NewProbeWorker(
		"test-backend",
		"http://127.0.0.1:59999/health",
		spec,
		func(result bool, message string) {
			resultChan <- result
			messageChan <- message
		},
		nil,
	)
	if err != nil {
		t.Fatalf("Failed to create probe worker: %v", err)
	}

	worker.Start()
	defer worker.Stop()

	select {
	case result := <-resultChan:
		if result {
			t.Errorf("Expected connection refused to result in false, got true")
		}
		message := <-messageChan
		if !strings.Contains(message, "connection refused") && !strings.Contains(message, "connect") {
			t.Errorf("Expected connection refused error message, got: %s", message)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for probe result")
	}
}
