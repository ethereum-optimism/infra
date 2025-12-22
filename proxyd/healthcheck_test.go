package proxyd

import (
	"net/http"
	"net/http/httptest"
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
			name:           "failed healthcheck",
			serverResponse: http.StatusInternalServerError,
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
				server.URL,
				spec,
				func(result bool, message string) {
					resultChan <- result
					messageChan <- message
				},
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
		server.URL,
		spec,
		func(result bool, message string) {
			resultChan <- result
			messageChan <- message
		},
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
