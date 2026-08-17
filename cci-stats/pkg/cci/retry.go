package cci

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"log/slog"
	"math/rand/v2"
	"net"
	"net/http"
	"strconv"
	"time"
)

// Defaults for an hourly job: CircleCI's 5xx bursts have lasted seconds, and a
// run of a few minutes still has the rest of the hour spare.
const (
	defaultMaxAttempts = 5
	defaultBaseDelay   = 500 * time.Millisecond
	defaultMaxDelay    = 15 * time.Second
	// Past maxDelay, because a Retry-After says when the server will serve the
	// request. The bound only stops an absurd value stranding the run.
	defaultMaxRetryAfter = 2 * time.Minute
)

// Draining a body to the end returns its connection to the pool. CircleCI's
// error bodies are one JSON message, so the limit only guards against an
// unexpectedly large one.
const discardLimit = 64 << 10

// retryTransport retries a request whose failure may not recur.
//
// This is the last place that still has the status code: the client turns a
// failed response into an error carrying only the body's message, so a 502
// reaches the caller indistinguishable from a permanent failure. Sitting here
// also covers every call, including any added later.
type retryTransport struct {
	next          http.RoundTripper
	maxAttempts   int
	baseDelay     time.Duration
	maxDelay      time.Duration
	maxRetryAfter time.Duration
	// Fields so that tests do not wait out real backoff.
	sleep  func(ctx context.Context, d time.Duration) error
	jitter func(d time.Duration) time.Duration
}

func newRetryTransport(next http.RoundTripper) *retryTransport {
	return &retryTransport{
		next:          next,
		maxAttempts:   defaultMaxAttempts,
		baseDelay:     defaultBaseDelay,
		maxDelay:      defaultMaxDelay,
		maxRetryAfter: defaultMaxRetryAfter,
		sleep:         sleep,
		jitter:        jitter,
	}
}

func (t *retryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx := req.Context()

	for attempt := 1; ; attempt++ {
		attemptReq, err := replay(req, attempt)
		if err != nil {
			return nil, err
		}

		resp, err := t.next.RoundTrip(attemptReq)
		last := attempt >= t.maxAttempts
		switch {
		case err != nil:
			if last || !retryableErr(ctx, err) || !replayable(req) {
				return nil, err
			}
			slog.Warn("retrying failed CircleCI request",
				"url", req.URL.Path, "attempt", attempt, "err", err)
		case !retryableStatus(resp.StatusCode):
			return resp, nil
		case last || !replayable(req):
			// Handed back untouched, for the caller to read the failure off.
			return resp, nil
		default:
			slog.Warn("retrying failed CircleCI request",
				"url", req.URL.Path, "attempt", attempt, "status", resp.StatusCode)
		}

		delay := t.backoff(attempt)
		if resp != nil {
			delay = t.delayFor(resp, delay)
			discard(resp)
		}
		if err := t.sleep(ctx, delay); err != nil {
			return nil, err
		}
	}
}

// delayFor waits at least as long as the backoff. A Retry-After on its own can
// be zero, either because the server said so or because an HTTP-date a few
// seconds past reads as no wait, which would resend at once and unjittered.
func (t *retryTransport) delayFor(resp *http.Response, backoff time.Duration) time.Duration {
	after, ok := retryAfter(resp)
	if !ok {
		return backoff
	}
	return max(min(after, t.maxRetryAfter), backoff)
}

func (t *retryTransport) backoff(attempt int) time.Duration {
	delay := t.baseDelay
	for range attempt - 1 {
		delay *= 2
		if delay >= t.maxDelay {
			return t.jitter(t.maxDelay)
		}
	}
	return t.jitter(delay)
}

// retryableStatus reports whether the same request could succeed later. A 4xx
// describes the request itself, and 404 is routine: a job with no test metadata
// reports as not found, once per job.
func retryableStatus(status int) bool {
	if status == http.StatusTooManyRequests {
		return true
	}
	// 501 is permanent, unlike the rest of 5xx.
	return status >= 500 && status != http.StatusNotImplemented
}

func retryableErr(ctx context.Context, err error) bool {
	// The pipeline pool cancels its siblings as soon as one of them fails.
	if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}

	// Settled answers, so retrying only spends the budget to reach the same one.
	var dns *net.DNSError
	if errors.As(err, &dns) && dns.IsNotFound {
		return false
	}
	var cert *tls.CertificateVerificationError
	return !errors.As(err, &cert)
}

// replayable reports whether the request can be sent again. A body without
// GetBody was consumed by the first attempt, so resending it would send nothing.
// The method matters because a gateway 502 says nothing about whether the origin
// applied the request: replaying a POST could apply it twice.
func replayable(req *http.Request) bool {
	if req.Body != nil && req.Body != http.NoBody && req.GetBody == nil {
		return false
	}
	switch req.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace,
		http.MethodPut, http.MethodDelete:
		return true
	default:
		return false
	}
}

// replay clones the request, which RoundTrip must not modify. The first attempt
// carries the caller's own body, so the transport underneath closes it as the
// contract requires; later attempts get a fresh reader.
func replay(req *http.Request, attempt int) (*http.Request, error) {
	out := req.Clone(req.Context())
	if attempt == 1 || req.GetBody == nil {
		return out, nil
	}
	body, err := req.GetBody()
	if err != nil {
		return nil, err
	}
	out.Body = body
	return out, nil
}

// retryAfter reads the Retry-After header in either of its forms. A value in the
// past reads as no wait rather than a negative one.
func retryAfter(resp *http.Response) (time.Duration, bool) {
	val := resp.Header.Get("Retry-After")
	if val == "" {
		return 0, false
	}
	if secs, err := strconv.Atoi(val); err == nil {
		return max(time.Duration(secs)*time.Second, 0), true
	}
	if date, err := http.ParseTime(val); err == nil {
		return max(time.Until(date), 0), true
	}
	slog.Warn("ignoring unreadable Retry-After header", "value", val)
	return 0, false
}

// discard drains and closes a response that will not be returned, so its
// connection goes back to the pool.
func discard(resp *http.Response) {
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, discardLimit))
	_ = resp.Body.Close()
}

func sleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// jitter spreads concurrent retries over the second half of the window, so the
// fetch pool does not resend everything at once. The floor keeps a delay from
// collapsing to near zero.
func jitter(d time.Duration) time.Duration {
	return d/2 + rand.N(d/2+1)
}
