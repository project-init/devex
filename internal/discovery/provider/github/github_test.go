package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"

	gh "github.com/google/go-github/v74/github"
	"github.com/project-init/devex/internal/discovery/config"
	"github.com/project-init/devex/internal/discovery/domain"
	"github.com/project-init/devex/internal/discovery/provider"
	"github.com/project-init/gommon/pkg/githubclient"
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
		if body.GetTitle() != "Implement audit logs" || !strings.HasPrefix(body.GetBody(), "Parent: https://example.test/1\n<!-- devex-sync: ") {
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

func TestResolveScansOnceAndLabelsADiscoveryPublishedBeforeLabels(t *testing.T) {
	// Build the marker through the helper so the pattern and the format cannot drift apart.
	idempotencyMarker := marker("audit", "WI-001")
	var labeled []string
	var created *gh.Label
	client := newTestClient(t, func(request *http.Request) *http.Response {
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/repos/project-init/devex/issues":
			if request.URL.Query().Get("labels") != "" {
				t.Errorf("listed by label %q before the label existed", request.URL.Query().Get("labels"))
			}
			return jsonResponse(`[{"id":42,"number":7,"html_url":"https://github.test/issues/7","body":"` + idempotencyMarker + `"},` +
				`{"id":43,"number":8,"body":"` + marker("other", "WI-001") + `"}]`)
		case request.Method == http.MethodPost && request.URL.Path == "/repos/project-init/devex/issues/7/labels":
			var body []string
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			labeled = append(labeled, body...)
			return jsonResponse("[" + documentedLabel(t) + "]")
		case request.Method == http.MethodPatch && request.URL.Path == "/repos/project-init/devex/labels/discovery:audit":
			return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(strings.NewReader(`{"message":"Not Found"}`))}
		case request.Method == http.MethodPost && request.URL.Path == "/repos/project-init/devex/labels":
			var label gh.Label
			if err := json.NewDecoder(request.Body).Decode(&label); err != nil {
				t.Fatal(err)
			}
			created = &label
			return jsonResponse(documentedLabel(t))
		default:
			t.Fatalf("request = %s %s", request.Method, request.URL.Path)
			return nil
		}
	})
	published, err := NewWithClient(client).Resolve(
		context.Background(),
		githubTarget(),
		&provider.Plan{DiscoveryID: "audit"},
		[]provider.Operation{{ItemID: "WI-001", IdempotencyKey: idempotencyMarker}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if remote, found := published[idempotencyMarker]; !found || remote.Key != "7" {
		t.Fatalf("published = %#v", published)
	}
	if strings.Join(labeled, ",") != "discovery:audit" {
		t.Errorf("labeled = %q, want only issue #7 given the discovery label", labeled)
	}
	if created == nil || created.GetName() != "discovery:audit" || created.GetDescription() != labelDescription("audit") {
		t.Errorf("created label = %+v, want it marked complete after the backfill", created)
	}
}

func TestResolveFinishesAnInterruptedBackfill(t *testing.T) {
	// The label exists, but its description records no finished backfill.
	idempotencyMarker := marker("audit", "WI-001")
	var edited *gh.Label
	client := newLabelClient(t, labelIncomplete, func(request *http.Request) *http.Response {
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/repos/project-init/devex/issues":
			if request.URL.Query().Get("labels") != "" {
				t.Errorf("trusted an incomplete label")
			}
			return jsonResponse(`[{"id":42,"number":7,"body":"` + idempotencyMarker + `"}]`)
		case request.Method == http.MethodPost && request.URL.Path == "/repos/project-init/devex/issues/7/labels":
			return jsonResponse("[" + documentedLabel(t) + "]")
		case request.Method == http.MethodPatch && request.URL.Path == "/repos/project-init/devex/labels/discovery:audit":
			var label gh.Label
			if err := json.NewDecoder(request.Body).Decode(&label); err != nil {
				t.Fatal(err)
			}
			edited = &label
			return jsonResponse(documentedLabel(t))
		default:
			t.Fatalf("request = %s %s", request.Method, request.URL.Path)
			return nil
		}
	})
	if _, err := NewWithClient(client).Resolve(context.Background(), githubTarget(), &provider.Plan{DiscoveryID: "audit"},
		[]provider.Operation{{ItemID: "WI-001", IdempotencyKey: idempotencyMarker}}); err != nil {
		t.Fatal(err)
	}
	if edited == nil || edited.GetDescription() != labelDescription("audit") {
		t.Errorf("edited label = %+v, want it marked complete", edited)
	}
}

func TestResolveListsOnlyTheDiscoveryLabel(t *testing.T) {
	idempotencyMarker := marker("audit", "WI-001")
	client := newLabelClient(t, labelComplete, func(request *http.Request) *http.Response {
		if request.Method != http.MethodGet || request.URL.Path != "/repos/project-init/devex/issues" || request.URL.Query().Get("labels") != "discovery:audit" {
			t.Fatalf("request = %s %s?%s, want only the labeled listing", request.Method, request.URL.Path, request.URL.RawQuery)
		}
		return jsonResponse(`[{"id":42,"number":7,"html_url":"https://github.test/issues/7","body":"` + idempotencyMarker + `"}]`)
	})
	published, err := NewWithClient(client).Resolve(context.Background(), githubTarget(), &provider.Plan{DiscoveryID: "audit"},
		[]provider.Operation{{ItemID: "WI-001", IdempotencyKey: idempotencyMarker}})
	if err != nil || published[idempotencyMarker].Key != "7" {
		t.Errorf("Resolve() = %#v, %v", published, err)
	}
}

func TestResolveScansWhenItCannotReadTheLabel(t *testing.T) {
	idempotencyMarker := marker("audit", "WI-001")
	client := newLabelClient(t, labelUnreadable, func(request *http.Request) *http.Response {
		switch {
		case strings.HasSuffix(request.URL.Path, "/issues/7/labels"):
			return jsonResponse("[" + documentedLabel(t) + "]") // the backfill
		case request.Method != http.MethodGet:
			return jsonResponse(documentedLabel(t)) // the backfill records the label
		}
		if request.URL.Query().Get("labels") != "" {
			t.Errorf("listing = %s, want the full scan", request.URL.RawQuery)
		}
		return jsonResponse(`[{"id":42,"number":7,"html_url":"https://github.test/issues/7","body":"` + idempotencyMarker + `"}]`)
	})
	published, err := NewWithClient(client).Resolve(context.Background(), githubTarget(), &provider.Plan{DiscoveryID: "audit"},
		[]provider.Operation{{ItemID: "WI-001", IdempotencyKey: idempotencyMarker}})
	if err != nil || published[idempotencyMarker].Key != "7" {
		t.Errorf("Resolve() = %#v, %v; want the scan to find the issue", published, err)
	}
}

func TestDiscoveryLabelFitsGitHubsLimit(t *testing.T) {
	long := strings.Repeat("a", 80)
	if got := discoveryLabel(long); len(got) != 50 || got == discoveryLabel(long+"b") {
		t.Errorf("discoveryLabel(80 chars) = %q (%d), want 50 characters that still tell IDs apart", got, len(got))
	}
	if got := discoveryLabel("audit"); got != "discovery:audit" {
		t.Errorf("discoveryLabel(audit) = %q", got)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

// documentedLabel is GitHub's documented example label, the body of every label response.
func documentedLabel(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("testdata/label.golden")
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

type labelState int

const (
	labelAbsent     labelState = iota // a discovery published before devex labeled its issues
	labelIncomplete                   // the label exists, but a backfill never finished
	labelComplete                     // the label marks every issue of the discovery
	labelUnreadable                   // GitHub fails the label lookup
)

// newTestClient serves handler, answering a discovery label lookup with 404.
func newTestClient(t *testing.T, handler func(*http.Request) *http.Response) *gh.Client {
	t.Helper()
	return newLabelClient(t, labelAbsent, handler)
}

// newLabelClient is newTestClient with the discovery label in state, served as GitHub's
// documented example label renamed for the discovery.
func newLabelClient(t *testing.T, state labelState, handler func(*http.Request) *http.Response) *gh.Client {
	t.Helper()
	httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		const labels = "/repos/project-init/devex/labels/"
		if request.Method == http.MethodGet && strings.HasPrefix(request.URL.Path, labels) {
			switch state {
			case labelAbsent:
				return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(strings.NewReader(`{"message":"Not Found"}`))}, nil
			case labelUnreadable:
				return &http.Response{StatusCode: http.StatusBadGateway, Body: io.NopCloser(strings.NewReader(`{"message":"Server Error"}`))}, nil
			}
			data, err := os.ReadFile("testdata/label.golden")
			if err != nil {
				t.Fatal(err)
			}
			var label map[string]any
			if err := json.Unmarshal(data, &label); err != nil {
				t.Fatal(err)
			}
			label["name"] = strings.TrimPrefix(request.URL.Path, labels)
			if state == labelComplete {
				label["description"] = labelDescription(strings.TrimPrefix(request.URL.Path, labels+"discovery:"))
			}
			data, _ = json.Marshal(label)
			return jsonResponse(string(data)), nil
		}
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

func TestGitHubAuthErrorKeepsATimeout(t *testing.T) {
	timeout := fmt.Errorf("gh auth token did not finish within 15s: %w", githubclient.ErrAuthTimedOut)
	err := githubAuthError(timeout)
	if err != timeout || !errors.Is(err, githubclient.ErrAuthTimedOut) {
		t.Fatalf("err = %v", err)
	}
}

func TestGitHubAuthErrorRewritesUnavailable(t *testing.T) {
	err := githubAuthError(githubclient.ErrAuthUnavailable)
	if errors.Is(err, githubclient.ErrAuthUnavailable) || !strings.Contains(err.Error(), "gh auth login") {
		t.Fatalf("err = %v", err)
	}
}

func TestNewRejectsARelativeGitHubAPIURL(t *testing.T) {
	_, err := New(false, config.Target{GitHub: &config.GitHubTarget{APIURL: "api.github.com"}})
	if err == nil || !strings.Contains(err.Error(), "absolute http or https url") {
		t.Fatalf("err = %v", err)
	}
}

func TestResolveKeepsGoingWhenItCannotLabel(t *testing.T) {
	// GitHub refuses labels to a token without push access; the scan still found the issue.
	idempotencyMarker := marker("audit", "WI-001")
	client := newTestClient(t, func(request *http.Request) *http.Response {
		if request.Method == http.MethodGet && request.URL.Path == "/repos/project-init/devex/issues" {
			return jsonResponse(`[{"id":42,"number":7,"body":"` + idempotencyMarker + `"}]`)
		}
		return &http.Response{StatusCode: http.StatusForbidden, Body: io.NopCloser(strings.NewReader(`{"message":"Must have admin rights to Repository."}`))}
	})
	published, err := NewWithClient(client).Resolve(context.Background(), githubTarget(), &provider.Plan{DiscoveryID: "audit"},
		[]provider.Operation{{ItemID: "WI-001", IdempotencyKey: idempotencyMarker}})
	if err != nil || published[idempotencyMarker].Key != "7" {
		t.Errorf("Resolve() = %#v, %v; want the scanned issue despite the refused label", published, err)
	}
}

func TestBackfillSkipsIssuesAlreadyLabeled(t *testing.T) {
	idempotencyMarker := marker("audit", "WI-001")
	client := newLabelClient(t, labelIncomplete, func(request *http.Request) *http.Response {
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/repos/project-init/devex/issues":
			return jsonResponse(`[{"id":42,"number":7,"labels":[{"name":"discovery:audit"}],"body":"` + idempotencyMarker + `"}]`)
		case request.Method == http.MethodPatch:
			return jsonResponse(documentedLabel(t))
		default:
			t.Fatalf("request = %s %s, want no label added to an already labeled issue", request.Method, request.URL.Path)
			return nil
		}
	})
	if _, err := NewWithClient(client).Resolve(context.Background(), githubTarget(), &provider.Plan{DiscoveryID: "audit"},
		[]provider.Operation{{ItemID: "WI-001", IdempotencyKey: idempotencyMarker}}); err != nil {
		t.Fatal(err)
	}
}

func TestDiscoveryOfReadsTheMarker(t *testing.T) {
	if id, ok := discoveryOf(marker("audit/2026", "WI-001")); !ok || id != "audit/2026" {
		t.Errorf("discoveryOf() = %q, %v", id, ok)
	}
	if _, ok := discoveryOf("plain/text"); ok {
		t.Error("discoveryOf(plain/text) = ok, want a marker required")
	}
}

func TestExecuteLabelsEveryCreateWithItsDiscovery(t *testing.T) {
	// The plan came from a devex that never listed the discovery label.
	var labels []string
	client := newTestClient(t, func(request *http.Request) *http.Response {
		if strings.HasSuffix(request.URL.Path, "/labels") {
			t.Fatal("Execute relabeled an issue that kept its labels")
		}
		var body gh.IssueRequest
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		labels = *body.Labels
		return jsonResponse(`{"id":42,"number":7,"labels":[{"name":"discovery:audit"},{"name":"security"}]}`)
	})
	_, err := NewWithClient(client).Execute(context.Background(), githubTarget(), provider.Operation{
		ID: "create-WI-001", Action: provider.ActionCreateIssue, ItemID: "WI-001", IdempotencyKey: marker("audit", "WI-001"),
		Fields: map[string]any{"title": "Audit logs", "body": "", "labels": []any{"security"}},
	}, nil)
	if err != nil || strings.Join(labels, ",") != "discovery:audit,security" {
		t.Errorf("labels = %q, %v; want the discovery label added", labels, err)
	}
}

func TestExecuteAddsBackADiscoveryLabelGitHubDropped(t *testing.T) {
	var added []string
	client := newTestClient(t, func(request *http.Request) *http.Response {
		if request.URL.Path == "/repos/project-init/devex/issues/7/labels" {
			if err := json.NewDecoder(request.Body).Decode(&added); err != nil {
				t.Fatal(err)
			}
			return jsonResponse("[" + documentedLabel(t) + "]")
		}
		return jsonResponse(`{"id":42,"number":7,"labels":[]}`)
	})
	_, err := NewWithClient(client).Execute(context.Background(), githubTarget(), provider.Operation{
		ID: "create-WI-001", Action: provider.ActionCreateIssue, ItemID: "WI-001", IdempotencyKey: marker("audit", "WI-001"),
		Fields: map[string]any{"title": "Audit logs", "body": "", "labels": []any{"security"}},
	}, nil)
	if err != nil || strings.Join(added, ",") != "discovery:audit" {
		t.Errorf("added labels = %q, %v; want the dropped discovery label added back", added, err)
	}
}

func TestExecuteKeepsTheCreateWhenGitHubRefusesTheLabel(t *testing.T) {
	client := newTestClient(t, func(request *http.Request) *http.Response {
		if request.URL.Path == "/repos/project-init/devex/issues/7/labels" {
			return &http.Response{StatusCode: http.StatusForbidden, Body: io.NopCloser(strings.NewReader(`{"message":"Must have admin rights to Repository."}`))}
		}
		return jsonResponse(`{"id":42,"number":7,"labels":[]}`)
	})
	remote, err := NewWithClient(client).Execute(context.Background(), githubTarget(), provider.Operation{
		ID: "create-WI-001", Action: provider.ActionCreateIssue, ItemID: "WI-001", IdempotencyKey: marker("audit", "WI-001"),
		Fields: map[string]any{"title": "Audit logs", "body": "", "labels": []any{}},
	}, nil)
	if err != nil || remote.Key != "7" {
		t.Errorf("Execute() = %#v, %v; want the created issue recorded so a rerun does not create it again", remote, err)
	}
}
