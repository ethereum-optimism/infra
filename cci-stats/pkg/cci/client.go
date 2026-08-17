// Package cci builds the CircleCI client this program talks to the API with.
package cci

import (
	"fmt"
	"net/http"
	"time"

	"github.com/axelKingsley/go-circleci"
)

// NewClient returns a CircleCI client that retries transient API failures. It is
// the only way this program builds a client, so no call site has to remember to
// ask for the retries.
func NewClient(token string) (*circleci.Client, error) {
	return newClient(circleci.DefaultAddress, token)
}

// Bounds one attempt, so a connection CircleCI accepts but never answers cannot
// hold the run open: the runner's context has no deadline, and the CronJob
// forbids concurrent runs, so one hung request stops every later run too.
//
// On the transport rather than http.Client.Timeout, which would cover all the
// attempts together and shrink the budget with each one.
const responseHeaderTimeout = 30 * time.Second

func newClient(address, token string) (*circleci.Client, error) {
	transport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, fmt.Errorf("http.DefaultTransport is a %T, not an *http.Transport", http.DefaultTransport)
	}
	transport = transport.Clone()
	transport.ResponseHeaderTimeout = responseHeaderTimeout

	cfg := circleci.DefaultConfig()
	cfg.Address = address
	cfg.Token = token
	cfg.HTTPClient = &http.Client{Transport: newRetryTransport(transport)}

	client, err := circleci.NewClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create circleci client: %w", err)
	}
	return client, nil
}
