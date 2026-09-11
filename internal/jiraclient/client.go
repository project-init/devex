package jiraclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Client handles communication with Jira Rest API
type Client struct {
	client  *http.Client
	email   string
	baseURL string
	token   string
}

// NewClient creates new Jira API client
// Accepts custom *http.Client, uses DefaultClient if nil [i.e base URL https://my-domain.atlassian.net and auth creds ]
func NewClient(httpClient *http.Client, baseURL string, email string, token string) *Client {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &Client{
		client:  httpClient,
		baseURL: strings.TrimSuffix(baseURL, "/"),
		email:   email,
		token:   token,
	}
}

// NewRequest creates an API request
// A relative path can be provided, e.g. "/rest/api/3/issue"
func (c *Client) NewRequest(ctx context.Context, method, path string, body any) (*http.Request, error) {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(encoded)
	}

	reqURL := c.baseURL + path
	request, err := http.NewRequestWithContext(ctx, method, reqURL, reader)
	if err != nil {
		return nil, err
	}

	request.SetBasicAuth(c.email, c.token)
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}

	return request, nil
}

// Do executes an API request and unmarshals the response body into responseTarget.
func (c *Client) Do(request *http.Request, responseTarget any) error {
	response, err := c.client.Do(request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		bodyBytes, _ := io.ReadAll(io.LimitReader(response.Body, 32*1024))
		return &httpStatusError{StatusCode: response.StatusCode, Body: string(bodyBytes)}
	}

	if responseTarget == nil || response.StatusCode == http.StatusNoContent {
		return nil
	}

	if err := json.NewDecoder(response.Body).Decode(responseTarget); err != nil {
		return fmt.Errorf("decode Jira response: %w", err)
	}
	return nil
}

type httpStatusError struct {
	StatusCode int
	Body       string
}

func (e *httpStatusError) Error() string {
	return fmt.Sprintf("Jira returned HTTP %d: %s", e.StatusCode, e.Body)
}
