package github

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	gh "github.com/google/go-github/v74/github"
	"github.com/project-init/devex/internal/discovery/config"
	"github.com/project-init/devex/internal/discovery/domain"
	"github.com/project-init/devex/internal/discovery/provider"
)

func TestExecuteCreatesIssue(t *testing.T) {
	client := newTestClient(t, func(request *http.Request) *http.Response {
		if request.Method != http.MethodPost || request.URL.Path != "/repos/project-init/devex/issues" {
			t.Fatalf("request = %s %s", request.Method, request.URL.Path)
		}
		var body gh.IssueRequest
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.GetTitle() != "Implement audit logs" || body.GetBody() != "Parent: https://example.test/1" {
			t.Fatalf("body = %#v", body)
		}
		return jsonResponse(`{"id":42,"number":7,"html_url":"https://github.test/issues/7"}`)
	})
	adapter := NewWithClient(client)
	remote, err := adapter.Execute(context.Background(), githubTarget(), provider.Operation{
		ID:     "create-WI-001",
		ItemID: "WI-001",
		Fields: map[string]any{
			"title":  "Implement audit logs",
			"body":   "Parent: {{remote:INIT-001}}",
			"labels": []any{"security", "feature"},
		},
	}, map[domain.ItemID]provider.RemoteRef{
		"INIT-001": {URL: "https://example.test/1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if remote.ID != "42" || remote.Key != "7" {
		t.Fatalf("remote = %#v", remote)
	}
}

func TestLookupFindsIdempotencyMarker(t *testing.T) {
	// Build the marker through the helper so the pattern and the format cannot drift apart.
	idempotencyMarker := marker("audit", "WI-001")
	client := newTestClient(t, func(_ *http.Request) *http.Response {
		return jsonResponse(
			`[{"id":42,"number":7,"html_url":"https://github.test/issues/7","body":"` + idempotencyMarker + `"}]`,
		)
	})
	remote, err := NewWithClient(client).Lookup(
		context.Background(),
		githubTarget(),
		idempotencyMarker,
	)
	if err != nil {
		t.Fatal(err)
	}
	if remote == nil || remote.Key != "7" {
		t.Fatalf("remote = %#v", remote)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func newTestClient(t *testing.T, handler func(*http.Request) *http.Response) *gh.Client {
	t.Helper()
	httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return handler(request), nil
	})}
	client := gh.NewClient(httpClient)
	baseURL, _ := url.Parse("https://api.github.test/")
	client.BaseURL = baseURL
	return client
}

func jsonResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func githubTarget() config.Target {
	return config.Target{
		Provider: "github",
		GitHub: &config.GitHubTarget{
			Owner:      "project-init",
			Repository: "devex",
		},
	}
}
