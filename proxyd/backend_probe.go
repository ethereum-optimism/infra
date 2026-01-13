// backend_probe.go implements HTTP health check probing for backend servers.
//
// The probe system is inspired by Kubernetes liveness/readiness probes and provides:
//   - Periodic HTTP health checks against a configurable endpoint
//   - Configurable success/failure thresholds to prevent flapping
//   - Async operation via a background goroutine per backend
//
// # Usage
//
// When a backend is configured with a probe_url, a ProbeWorker runs in the background,
// periodically checking the endpoint. The backend is only marked unhealthy after
// FailureThreshold consecutive failures, and only marked healthy after SuccessThreshold
// consecutive successes. This threshold behavior prevents health status from flapping
// due to transient network issues.
//
// # Configuration
//
// ProbeSpec controls the probe behavior:
//   - FailureThreshold: consecutive failures before marking unhealthy (default: 1)
//   - SuccessThreshold: consecutive successes before marking healthy (default: 2)
//   - Period: interval between probes (default: 4s)
//   - Timeout: HTTP request timeout per probe (default: 4s)
//
// # HTTP Probe Behavior
//
// The probe sends a GET request to the configured URL. Success is determined by:
//   - 2xx status codes: success
//   - 3xx (redirects), 4xx, 5xx, or connection errors: failure
//
// The probe uses a custom dialer with SO_LINGER set (borrowed from Kubernetes) to ensure
// clean connection teardown, and disables keep-alives to get fresh connection state each probe.
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
				_ = syscall.SetsockoptLinger(int(fd), syscall.SOL_SOCKET, syscall.SO_LINGER, &syscall.Linger{Onoff: 1, Linger: 1})
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
	stopCh        chan struct{}
	spec          ProbeSpec
	transport     *http.Transport
	req           *http.Request
	resultHandler func(bool, string)
	lastResult    bool
	resultRun     int
	backendName   string
}

func NewProbeWorker(
	backendName string,
	probeUrl string,
	probeSpec ProbeSpec,
	insecureSkipVerify bool,
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
		TLSClientConfig:     &tls.Config{InsecureSkipVerify: insecureSkipVerify},
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
		backendName:   backendName,
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

func (w *ProbeWorker) Stop() {
	select {
	case w.stopCh <- struct{}{}:
	default: // Non-blocking.
	}
}

func (w *ProbeWorker) Start() {
	go w.run()
}

func (w *ProbeWorker) doProbe() {
	client := &http.Client{
		Timeout:   w.spec.Timeout,
		Transport: w.transport,
	}

	start := time.Now()
	result, message := doHTTPProbe(w.req, client)
	duration := time.Since(start)

	RecordBackendProbeDuration(w.backendName, duration)
	RecordBackendProbeCheck(w.backendName, result)

	if w.lastResult == result {
		w.resultRun++
	} else {
		w.lastResult = result
		w.resultRun = 1
	}

	if (!result && w.resultRun < int(w.spec.FailureThreshold)) ||
		(result && w.resultRun < int(w.spec.SuccessThreshold)) {
		// Success or failure is below threshold - leave the probe state unchanged.
		return
	}

	RecordBackendProbeHealthy(w.backendName, result)
	w.resultHandler(result, message)
}
