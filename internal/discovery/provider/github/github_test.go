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
	published, err := NewWithClient(client).Resolve(
		context.Background(),
		githubTarget(),
		&provider.Plan{},
		[]provider.Operation{{ItemID: "WI-001", IdempotencyKey: idempotencyMarker}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if remote, found := published[idempotencyMarker]; !found || remote.Key != "7" {
		t.Fatalf("published = %#v", published)
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

func TestBodyLinksTheDiscoveryDocument(t *testing.T) {
	item := domain.WorkItem{
		ID:                 "WI-001",
		Kind:               domain.KindTask,
		Title:              "First",
		Description:        "First.",
		AcceptanceCriteria: []string{"It works."},
	}
	documentURL := "https://github.com/project-init/devex/blob/main/docs/audit/discovery.md"

	input := provider.PlanInput{DocumentURL: documentURL}

	body := bodyForItem(item, input, "<!-- marker -->")
	if !strings.Contains(body, "Discovery: "+documentURL) {
		t.Fatalf("body = %q, want the document URL", body)
	}

	// Without a URL the footer names a file the reader cannot open, so it is dropped entirely.
	if body := bodyForItem(item, provider.PlanInput{}, "<!-- marker -->"); strings.Contains(body, "Discovery:") {
		t.Fatalf("body = %q, want no discovery footer", body)
	}
}

// The initiative is the entry point a reader lands on, so it carries the link back to the request.
func TestBodyLinksTheTrackingIssueFromInitiatives(t *testing.T) {
	input := provider.PlanInput{TrackingURL: "https://jira.test/browse/DEVEX-42"}
	initiative := domain.WorkItem{
		ID:          "INIT-001",
		Kind:        domain.KindInitiative,
		Title:       "Deliver audit logs",
		Description: "Deliver audit logs.",
	}

	body := bodyForItem(initiative, input, "<!-- marker -->")
	if !strings.Contains(body, "Tracking issue: "+input.TrackingURL) {
		t.Fatalf("body = %q, want the tracking issue", body)
	}

	task := initiative
	task.ID = "WI-001"
	task.Kind = domain.KindTask
	if body := bodyForItem(task, input, "<!-- marker -->"); strings.Contains(body, "Tracking issue:") {
		t.Fatalf("body = %q, want no tracking link on a task", body)
	}
}
