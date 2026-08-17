package cci

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"
)

// attempt is one canned outcome for fakeTransport to return.
type attempt struct {
	status  int
	headers http.Header
	err     error
}

type fakeTransport struct {
	attempts []attempt
	// closeRequests closes each request body, as the real transport does.
	closeRequests bool
	// calls records the request as each attempt saw it.
	calls  []*http.Request
	bodies []string
	closed int
}

func (f *fakeTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	f.calls = append(f.calls, req)

	body := ""
	if req.Body != nil {
		b, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		body = string(b)
		if f.closeRequests {
			if err := req.Body.Close(); err != nil {
				return nil, err
			}
		}
	}
	f.bodies = append(f.bodies, body)

	if len(f.calls) > len(f.attempts) {
		return nil, fmt.Errorf("unexpected attempt %d", len(f.calls))
	}
	a := f.attempts[len(f.calls)-1]
	if a.err != nil {
		return nil, a.err
	}

	headers := a.headers
	if headers == nil {
		headers = make(http.Header)
	}
	return &http.Response{
		StatusCode: a.status,
		Header:     headers,
		Body:       &countingBody{Reader: strings.NewReader("upstream body"), transport: f},
	}, nil
}

// countingReader is a request body that records how often it is closed.
type countingReader struct {
	io.Reader
	closed int
}

func (c *countingReader) Close() error {
	c.closed++
	return nil
}

type countingBody struct {
	io.Reader
	transport *fakeTransport
}

func (c *countingBody) Close() error {
	c.transport.closed++
	return nil
}

// testTransport wraps next with retries that neither sleep nor jitter, and
// reports the delays it would have waited.
func testTransport(next http.RoundTripper) (*retryTransport, *[]time.Duration) {
	var delays []time.Duration
	tr := newRetryTransport(next)
	tr.sleep = func(_ context.Context, d time.Duration) error {
		delays = append(delays, d)
		return nil
	}
	tr.jitter = func(d time.Duration) time.Duration { return d }
	return tr, &delays
}

func newTestRequest(t *testing.T) *http.Request {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet,
		"https://circleci.com/api/v2/pipeline/abc", nil)
	if err != nil {
		t.Fatalf("failed to build request: %v", err)
	}
	return req
}

func TestRetriesServerErrorAndReturnsTheEventualSuccess(t *testing.T) {
	fake := &fakeTransport{attempts: []attempt{{status: 502}, {status: 503}, {status: 200}}}
	tr, _ := testTransport(fake)

	resp, err := tr.RoundTrip(newTestRequest(t))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if len(fake.calls) != 3 {
		t.Errorf("made %d attempts, want 3", len(fake.calls))
	}
}

func TestRetriesRateLimit(t *testing.T) {
	fake := &fakeTransport{attempts: []attempt{{status: 429}, {status: 200}}}
	tr, _ := testTransport(fake)

	resp, err := tr.RoundTrip(newTestRequest(t))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestRetriesTransportError(t *testing.T) {
	fake := &fakeTransport{attempts: []attempt{{err: errors.New("connection reset by peer")}, {status: 200}}}
	tr, _ := testTransport(fake)

	resp, err := tr.RoundTrip(newTestRequest(t))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestDoesNotRetryStatusesTheRequestCannotChange(t *testing.T) {
	for _, status := range []int{
		http.StatusBadRequest,
		http.StatusUnauthorized,
		http.StatusForbidden,
		http.StatusNotFound,
		http.StatusNotImplemented,
	} {
		fake := &fakeTransport{attempts: []attempt{{status: status}, {status: 200}}}
		tr, _ := testTransport(fake)

		resp, err := tr.RoundTrip(newTestRequest(t))
		if err != nil {
			t.Fatalf("status %d: unexpected error: %v", status, err)
		}
		if resp.StatusCode != status {
			t.Errorf("status = %d, want %d", resp.StatusCode, status)
		}
		if len(fake.calls) != 1 {
			t.Errorf("status %d: made %d attempts, want 1", status, len(fake.calls))
		}
		if err := resp.Body.Close(); err != nil {
			t.Errorf("failed to close body: %v", err)
		}
	}
}

func TestGivesUpOnTheLastResponseSoTheCallerSeesTheRealFailure(t *testing.T) {
	fake := &fakeTransport{attempts: []attempt{{status: 502}, {status: 502}, {status: 502}}}
	tr, _ := testTransport(fake)
	tr.maxAttempts = 3

	resp, err := tr.RoundTrip(newTestRequest(t))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != 502 {
		t.Errorf("status = %d, want 502", resp.StatusCode)
	}
	if len(fake.calls) != 3 {
		t.Errorf("made %d attempts, want 3", len(fake.calls))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read body: %v", err)
	}
	if string(body) != "upstream body" {
		t.Errorf("body = %q, want the final response left readable for the client", body)
	}
	if err := resp.Body.Close(); err != nil {
		t.Errorf("failed to close body: %v", err)
	}
}

func TestGivesUpOnTheLastTransportError(t *testing.T) {
	boom := errors.New("connection reset by peer")
	fake := &fakeTransport{attempts: []attempt{{err: boom}, {err: boom}, {err: boom}}}
	tr, _ := testTransport(fake)
	tr.maxAttempts = 3

	resp, err := tr.RoundTrip(newTestRequest(t)) // nolint:bodyclose // asserted nil below
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the transport error", err)
	}
	if resp != nil {
		t.Errorf("returned both a response and an error")
	}
	if len(fake.calls) != 3 {
		t.Errorf("made %d attempts, want 3", len(fake.calls))
	}
}

func TestClosesEveryResponseItDiscards(t *testing.T) {
	fake := &fakeTransport{attempts: []attempt{{status: 502}, {status: 502}, {status: 200}}}
	tr, _ := testTransport(fake)

	resp, err := tr.RoundTrip(newTestRequest(t))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fake.closed != 2 {
		t.Errorf("closed %d discarded responses, want 2", fake.closed)
	}
	if err := resp.Body.Close(); err != nil {
		t.Errorf("failed to close body: %v", err)
	}
}

func TestBacksOffExponentiallyUpToTheCap(t *testing.T) {
	fake := &fakeTransport{attempts: []attempt{{status: 502}, {status: 502}, {status: 502}, {status: 502}, {status: 200}}}
	tr, delays := testTransport(fake)
	tr.maxAttempts = 5
	tr.baseDelay = 100 * time.Millisecond
	tr.maxDelay = 300 * time.Millisecond

	resp, err := tr.RoundTrip(newTestRequest(t))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()

	want := []time.Duration{
		100 * time.Millisecond,
		200 * time.Millisecond,
		300 * time.Millisecond,
		300 * time.Millisecond,
	}
	if !slices.Equal(*delays, want) {
		t.Errorf("delays = %v, want %v", *delays, want)
	}
}

func TestHonoursRetryAfter(t *testing.T) {
	tests := []struct {
		name       string
		retryAfter string
		want       time.Duration
	}{
		{
			name:       "as asked, past maxDelay",
			retryAfter: "60",
			want:       time.Minute,
		},
		{
			name:       "capped when it would strand the run",
			retryAfter: "3600",
			want:       2 * time.Minute,
		},
		{
			name:       "ignored when unreadable",
			retryAfter: "soon",
			want:       100 * time.Millisecond,
		},
		{
			name:       "floored by the backoff when it asks for no wait",
			retryAfter: "0",
			want:       100 * time.Millisecond,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			headers := make(http.Header)
			headers.Set("Retry-After", test.retryAfter)
			fake := &fakeTransport{attempts: []attempt{{status: 429, headers: headers}, {status: 200}}}
			tr, delays := testTransport(fake)
			tr.baseDelay = 100 * time.Millisecond
			tr.maxDelay = 15 * time.Second

			resp, err := tr.RoundTrip(newTestRequest(t))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			defer resp.Body.Close()

			if !slices.Equal(*delays, []time.Duration{test.want}) {
				t.Errorf("delays = %v, want [%v]", *delays, test.want)
			}
		})
	}
}

func TestStopsWhenTheContextIsCancelledWhileBackingOff(t *testing.T) {
	fake := &fakeTransport{attempts: []attempt{{status: 502}, {status: 200}}}
	tr, _ := testTransport(fake)
	tr.sleep = func(context.Context, time.Duration) error { return context.Canceled }

	resp, err := tr.RoundTrip(newTestRequest(t)) // nolint:bodyclose // asserted nil below
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if resp != nil {
		t.Errorf("returned both a response and an error")
	}
	if len(fake.calls) != 1 {
		t.Errorf("made %d attempts, want 1", len(fake.calls))
	}
}

func TestDoesNotRetryACancelledRequest(t *testing.T) {
	fake := &fakeTransport{attempts: []attempt{{err: context.Canceled}, {status: 200}}}
	tr, _ := testTransport(fake)

	if _, err := tr.RoundTrip(newTestRequest(t)); !errors.Is(err, context.Canceled) { // nolint:bodyclose // no response with an error
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if len(fake.calls) != 1 {
		t.Errorf("made %d attempts, want 1", len(fake.calls))
	}
}

func TestReplaysARequestBody(t *testing.T) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPut,
		"https://circleci.com/api/v2/project/gh/org/repo/settings", strings.NewReader(`{"key":"value"}`))
	if err != nil {
		t.Fatalf("failed to build request: %v", err)
	}

	fake := &fakeTransport{attempts: []attempt{{status: 503}, {status: 200}}}
	tr, _ := testTransport(fake)

	resp, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()

	want := []string{`{"key":"value"}`, `{"key":"value"}`}
	if !slices.Equal(fake.bodies, want) {
		t.Errorf("bodies = %q, want %q", fake.bodies, want)
	}
}

func TestDoesNotRetryABodyItCannotReplay(t *testing.T) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPut,
		"https://circleci.com/api/v2/project/gh/org/repo/settings", io.NopCloser(strings.NewReader("stream")))
	if err != nil {
		t.Fatalf("failed to build request: %v", err)
	}
	req.GetBody = nil

	fake := &fakeTransport{attempts: []attempt{{status: 503}, {status: 200}}}
	tr, _ := testTransport(fake)

	resp, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != 503 {
		t.Errorf("status = %d, want the unretried 503", resp.StatusCode)
	}
	if len(fake.calls) != 1 {
		t.Errorf("made %d attempts, want 1", len(fake.calls))
	}
	if err := resp.Body.Close(); err != nil {
		t.Errorf("failed to close body: %v", err)
	}
}

func TestDoesNotRetryAMethodThatMayHaveApplied(t *testing.T) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		"https://circleci.com/api/v2/pipeline/abc/continue", strings.NewReader(`{"key":"value"}`))
	if err != nil {
		t.Fatalf("failed to build request: %v", err)
	}

	fake := &fakeTransport{attempts: []attempt{{status: 503}, {status: 200}}}
	tr, _ := testTransport(fake)

	resp, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != 503 {
		t.Errorf("status = %d, want the unretried 503", resp.StatusCode)
	}
	if len(fake.calls) != 1 {
		t.Errorf("made %d attempts, want 1", len(fake.calls))
	}
	if err := resp.Body.Close(); err != nil {
		t.Errorf("failed to close body: %v", err)
	}
}

func TestDoesNotRetryASettledTransportError(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{
			name: "a name that does not resolve",
			err:  &net.DNSError{Err: "no such host", Name: "circleci.invalid", IsNotFound: true},
		},
		{
			name: "a certificate that does not verify",
			err:  &tls.CertificateVerificationError{Err: errors.New("expired")},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fake := &fakeTransport{attempts: []attempt{{err: test.err}, {status: 200}}}
			tr, _ := testTransport(fake)

			if _, err := tr.RoundTrip(newTestRequest(t)); !errors.Is(err, test.err) { // nolint:bodyclose // no response with an error
				t.Fatalf("err = %v, want %v", err, test.err)
			}
			if len(fake.calls) != 1 {
				t.Errorf("made %d attempts, want 1", len(fake.calls))
			}
		})
	}

	// A dropped connection reads as none of the above and is still retried.
	fake := &fakeTransport{attempts: []attempt{{err: &net.DNSError{Err: "server misbehaving"}}, {status: 200}}}
	tr, _ := testTransport(fake)

	resp, err := tr.RoundTrip(newTestRequest(t))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()

	if len(fake.calls) != 2 {
		t.Errorf("made %d attempts, want 2", len(fake.calls))
	}
}

func TestClosesTheCallersBodyOnce(t *testing.T) {
	// RoundTrip owns the body it is handed, and only the first attempt carries it.
	body := &countingReader{Reader: strings.NewReader("payload")}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPut,
		"https://circleci.com/api/v2/project/gh/org/repo/settings", body)
	if err != nil {
		t.Fatalf("failed to build request: %v", err)
	}
	req.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader("payload")), nil
	}

	fake := &fakeTransport{attempts: []attempt{{status: 503}, {status: 200}}, closeRequests: true}
	tr, _ := testTransport(fake)

	resp, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()

	if body.closed != 1 {
		t.Errorf("the caller's body was closed %d times, want 1", body.closed)
	}
}

func TestLeavesTheCallersRequestAlone(t *testing.T) {
	req := newTestRequest(t)
	fake := &fakeTransport{attempts: []attempt{{status: 502}, {status: 200}}}
	tr, _ := testTransport(fake)

	resp, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()

	for i, call := range fake.calls {
		if call == req {
			t.Errorf("attempt %d was handed the caller's own request", i+1)
		}
	}
}

func TestSleepReturnsTheContextError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := sleep(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestJitterStaysWithinTheWindow(t *testing.T) {
	for range 100 {
		got := jitter(time.Second)
		if got < 500*time.Millisecond || got > time.Second {
			t.Fatalf("jitter(1s) = %v, want between 500ms and 1s", got)
		}
	}
}
