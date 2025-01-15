package integration_tests

import (
	"fmt"
	"net/http"
	"net/url"
)

type DynamicAuthClient struct {
	token string
	url   string
}

func NewDynamicAuthClient(url string, token string) (*DynamicAuthClient, error) {
	return &DynamicAuthClient{
		token: token,
		url:   url,
	}, nil
}

func send(req *http.Request) error {
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request to the admin api: %w", err)
	}

	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("invalid response received from the admin api: expected %d, received %d", http.StatusOK, res.StatusCode)
	}

	return nil
}

func (c *DynamicAuthClient) PutKey(secret string) error {
	putUrl, err := url.JoinPath(c.url, "admin/keys", secret)
	if err != nil {
		return fmt.Errorf("failed to create the admin api url: %w", err)
	}
	req, err := http.NewRequest(http.MethodPut, putUrl, nil)
	if err != nil {
		return fmt.Errorf("failed to create new request: %w", err)
	}
	req.Header["Authorization"] = []string{fmt.Sprintf("Bearer %s", c.token)}

	return send(req)
}

func (c *DynamicAuthClient) DeleteKey(secret string) error {
	putUrl, err := url.JoinPath(c.url, "admin/keys", secret)
	if err != nil {
		return fmt.Errorf("failed to create the admin api url: %w", err)
	}
	req, err := http.NewRequest(http.MethodDelete, putUrl, nil)
	if err != nil {
		return fmt.Errorf("failed to create new request: %w", err)
	}
	req.Header["Authorization"] = []string{fmt.Sprintf("Bearer %s", c.token)}

	return send(req)
}
