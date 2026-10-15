// Package cci builds the CircleCI client this program talks to the API with.
package cci

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"time"

	"github.com/axelKingsley/go-circleci"
)

// Client adds the one paginated CircleCI endpoint that go-circleci cannot
// currently express while preserving access to the rest of the upstream
// client.
type Client struct {
	*circleci.Client
	listWorkflowJobsPage func(context.Context, string, string) (*circleci.WorkflowJobList, error)
}

// NewClient returns a CircleCI client that retries transient API failures. It is
// the only way this program builds a client, so no call site has to remember to
// ask for the retries.
func NewClient(token string) (*Client, error) {
	return newClient(circleci.DefaultAddress, token)
}

// Bounds one attempt, so a connection CircleCI accepts but never answers cannot
// hold the run open: the runner's context has no deadline, and the CronJob
// forbids concurrent runs, so one hung request stops every later run too.
//
// On the transport rather than http.Client.Timeout, which would cover all the
// attempts together and shrink the budget with each one.
const responseHeaderTimeout = 30 * time.Second

func newClient(address, token string) (*Client, error) {
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

	baseURL, err := url.Parse(address)
	if err != nil {
		return nil, fmt.Errorf("invalid circleci address: %w", err)
	}
	baseURL.Path = path.Join(baseURL.Path, circleci.DefaultBasePath) + "/"

	wrapped := &Client{Client: client}
	wrapped.listWorkflowJobsPage = func(ctx context.Context, workflowID, pageToken string) (*circleci.WorkflowJobList, error) {
		u := *baseURL
		u.Path = path.Join(baseURL.Path, "workflow", workflowID, "job")
		if pageToken != "" {
			q := u.Query()
			q.Set("page-token", pageToken)
			u.RawQuery = q.Encode()
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create jobs request: %w", err)
		}
		req.Header.Set("Circle-Token", token)
		req.Header.Set("Accept", "application/json")

		resp, err := cfg.HTTPClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch jobs page: %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
			return nil, fmt.Errorf("failed to fetch jobs page: circleci returned %s", resp.Status)
		}

		page := &circleci.WorkflowJobList{}
		if err := json.NewDecoder(resp.Body).Decode(page); err != nil {
			return nil, fmt.Errorf("failed to decode jobs page: %w", err)
		}
		return page, nil
	}
	return wrapped, nil
}

// ListWorkflowJobsPage fetches one workflow-jobs page. Clients assembled in
// service tests fall back to go-circleci for their only (unpaginated) page.
func (c *Client) ListWorkflowJobsPage(ctx context.Context, workflowID, pageToken string) (*circleci.WorkflowJobList, error) {
	if c.listWorkflowJobsPage != nil {
		return c.listWorkflowJobsPage(ctx, workflowID, pageToken)
	}
	if pageToken != "" {
		return nil, fmt.Errorf("workflow jobs pagination is not configured")
	}
	return c.Workflows.ListWorkflowJobs(ctx, workflowID)
}
