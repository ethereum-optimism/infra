package proxyd

import (
	"crypto/tls"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"syscall"
	"time"
)

type ProbeSpec struct {
	FailureThreshold int
	SuccessThreshold int
	Period           time.Duration
	Timeout          time.Duration
}

// borrowed from https://github.com/kubernetes/kubernetes/blob/b53b9fb5573323484af9a19cf3f5bfe80760abba/pkg/probe/dialer_others.go#L37
// probeDialer is a dialer that sets the SO_LINGER option to 1 second.
func probeDialer() *net.Dialer {
	dialer := &net.Dialer{
		Control: func(network, address string, c syscall.RawConn) error {
			return c.Control(func(fd uintptr) {
				syscall.SetsockoptLinger(int(fd), syscall.SOL_SOCKET, syscall.SO_LINGER, &syscall.Linger{Onoff: 1, Linger: 1})
			})
		},
	}
	return dialer
}

var defaultTransport = http.DefaultTransport.(*http.Transport)

func doHTTPProbe(req *http.Request, client *http.Client) (bool, string) {
	res, err := client.Do(req)
	if err != nil {
		// Convert errors into failures to catch timeouts.
		return false, err.Error()
	}
	defer res.Body.Close()
	if _, err = io.ReadAll(res.Body); err != nil {
		return false, err.Error()
	}
	if res.StatusCode >= http.StatusOK && res.StatusCode < http.StatusBadRequest {
		if res.StatusCode >= http.StatusMultipleChoices { // Redirect
			return false, fmt.Sprintf("HTTP Probe result is a redirect: %s", res.Status)
		}
		return true, ""
	}
	return false, fmt.Sprintf("HTTP probe failed with statuscode: %d", res.StatusCode)
}

type ProbeWorker struct {
	// Channel for stopping the probe.
	stopCh chan struct{}

	// Describes the probe configuration (read-only)
	spec ProbeSpec

	transport *http.Transport
	req       *http.Request

	// A callback function to pass probe result to
	resultHandler func(bool, string)

	// The last probe result for this worker.
	lastResult bool
	// How many times in a row the probe has returned the same result.
	resultRun int
}

func NewProbeWorker(
	probeUrl string,
	probeSpec ProbeSpec,
	resultHandler func(bool, string),
) (*ProbeWorker, error) {

	u, err := url.Parse(probeUrl)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest("GET", u.String(), nil)
	if err != nil {
		return nil, err
	}

	req.Header = http.Header{
		"User-Agent": {"proxyd-probe"},
		"Accept":     {"*/*"},
	}

	transport := &http.Transport{
		TLSClientConfig:     &tls.Config{InsecureSkipVerify: true},
		TLSHandshakeTimeout: defaultTransport.TLSHandshakeTimeout,
		DisableKeepAlives:   true,
		DisableCompression:  true,
		DialContext:         probeDialer().DialContext,
		IdleConnTimeout:     defaultTransport.IdleConnTimeout,
	}

	return &ProbeWorker{
		stopCh:        make(chan struct{}, 1), // Buffer so stop() can be non-blocking.
		spec:          probeSpec,
		resultHandler: resultHandler,
		transport:     transport,
		req:           req,
	}, nil
}

func (w *ProbeWorker) run() {
	probeTickerPeriod := w.spec.Period

	// first wait period is random to avoid simultaneous probes
	time.Sleep(time.Duration(rand.Float64() * float64(probeTickerPeriod)))

	probeTicker := time.NewTicker(probeTickerPeriod)

	defer func() {
		// Clean up.
		probeTicker.Stop()

	}()

probeLoop:
	for {
		w.doProbe()
		// Wait for next probe tick.
		select {
		case <-w.stopCh:
			break probeLoop
		case <-probeTicker.C:
			// continue
		}
	}
}

// Stop stops the probe worker. It is safe to call Stop multiple times.
func (w *ProbeWorker) Stop() {
	select {
	case w.stopCh <- struct{}{}:
	default: // Non-blocking.
	}
}

// Start starts the probe worker.
func (w *ProbeWorker) Start() {
	go w.run()
}

func (w *ProbeWorker) doProbe() {
	// Note, exec probe does NOT have access to pod environment variables or downward API

	client := &http.Client{
		Timeout:   w.spec.Timeout,
		Transport: w.transport,
	}

	result, message := doHTTPProbe(w.req, client)

	if w.lastResult == result {
		w.resultRun++
	} else {
		w.lastResult = result
		w.resultRun = 1
	}

	if (!result && w.resultRun < int(w.spec.FailureThreshold)) ||
		(result && w.resultRun < int(w.spec.SuccessThreshold)) {
		// Success or failure is below threshold - leave the probe state unchanged.
	}

	w.resultHandler(result, message)

}
