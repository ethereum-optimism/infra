package proxyd

import (
	"reflect"
	"testing"
	"time"
)

func TestMillisecondsToDuration(t *testing.T) {
	tests := []struct {
		name     string
		ms       int
		expected time.Duration
	}{
		{"zero milliseconds", 0, 0 * time.Millisecond},
		{"one millisecond", 1, 1 * time.Millisecond},
		{"hundred milliseconds", 100, 100 * time.Millisecond},
		{"one second", 1000, 1 * time.Second},
		{"five seconds", 5000, 5 * time.Second},
		{"negative milliseconds", -100, -100 * time.Millisecond},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := millisecondsToDuration(tt.ms)
			if result != tt.expected {
				t.Errorf("millisecondsToDuration(%d) = %v, want %v", tt.ms, result, tt.expected)
			}
		})
	}
}

func TestSecondsToDuration(t *testing.T) {
	tests := []struct {
		name     string
		seconds  int
		expected time.Duration
	}{
		{"zero seconds", 0, 0 * time.Second},
		{"one second", 1, 1 * time.Second},
		{"five seconds", 5, 5 * time.Second},
		{"thirty seconds", 30, 30 * time.Second},
		{"negative seconds", -10, -10 * time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := secondsToDuration(tt.seconds)
			if result != tt.expected {
				t.Errorf("secondsToDuration(%d) = %v, want %v", tt.seconds, result, tt.expected)
			}
		})
	}
}

func TestMillisecondsToDurationIntegration(t *testing.T) {
	// Test that the duration actually works with time functions
	ms := 50
	duration := millisecondsToDuration(ms)

	start := time.Now()
	time.Sleep(duration)
	elapsed := time.Since(start)

	// Allow some tolerance for timing (within 10ms)
	tolerance := 10 * time.Millisecond
	if elapsed < duration-tolerance || elapsed > duration+tolerance {
		t.Errorf("Sleep duration was %v, expected approximately %v", elapsed, duration)
	}
}

func TestParseCommaSeparatedList(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{"happy path", "key1,key2,key3", []string{"key1", "key2", "key3"}},
		{"trims whitespace", "  key1  ,  key2  ", []string{"key1", "key2"}},
		{"filters empty strings", "key1,,key2", []string{"key1", "key2"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseCommaSeparatedList(tt.input)
			if !reflect.DeepEqual(result, tt.expected) {
				t.Errorf("parseCommaSeparatedList(%q) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}
