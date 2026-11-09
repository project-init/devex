package errtrack

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	gerror "github.com/project-init/gommon/pkg/errors"
)

// TestIssuePath pins the request the whole tool depends on.
//
// The bias filter is the part worth guarding: Datadog ignores an unrecognised
// filter rather than rejecting it, so getting this wrong returns samples with
// no stack frames — which reads as "this error has no stack trace" rather than
// "my request was malformed".
func TestIssuePath(t *testing.T) {
	got := issuePath(issueID)

	base, rawQuery, found := strings.Cut(got, "?")
	if !found {
		t.Fatalf("issuePath() = %q, want a query string", got)
	}
	if want := "/api/v2/error-tracking/issues/" + issueID; base != want {
		t.Errorf("path = %q, want %q", base, want)
	}

	q, err := url.ParseQuery(rawQuery)
	if err != nil {
		t.Fatalf("query does not parse: %v", err)
	}
	if q.Get("include") != "samples" {
		t.Errorf("include = %q, want samples", q.Get("include"))
	}
	if q.Get("filter[samples.biases]") != gitMetadataBias {
		t.Errorf("bias filter = %q, want %q", q.Get("filter[samples.biases]"), gitMetadataBias)
	}

	// Brackets are percent-encoded by Encode. A real response came back with
	// biases: ["git_metadata"], so Datadog honours that form.
	if !strings.Contains(rawQuery, "filter%5Bsamples.biases%5D") {
		t.Errorf("brackets are not percent-encoded: %q", rawQuery)
	}
}

// TestIssuePathEscapesTheID matters because the id comes out of a pasted link.
func TestIssuePathEscapesTheID(t *testing.T) {
	got := issuePath("../../admin/secrets")

	if strings.Contains(got, "../") {
		t.Errorf("issuePath() = %q, want the id escaped", got)
	}
	if !strings.HasPrefix(got, "/api/v2/error-tracking/issues/") {
		t.Errorf("issuePath() = %q, want the endpoint prefix intact", got)
	}
}

func TestFetchIssue(t *testing.T) {
	body, err := os.ReadFile("testdata/issue.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	var gotPath, gotQuery string
	var gotHeaders http.Header

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery, gotHeaders = r.URL.Path, r.URL.RawQuery, r.Header
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	c := newDatadogClient(srv.URL, "api-key", "app-key")

	resp, err := c.fetchIssue(context.Background(), issueID)
	if err != nil {
		t.Fatalf("fetchIssue() error = %v", err)
	}

	t.Run("request", func(t *testing.T) {
		if want := "/api/v2/error-tracking/issues/" + issueID; gotPath != want {
			t.Errorf("path = %q, want %q", gotPath, want)
		}
		// The server decodes the percent-encoded brackets, so this is what
		// Datadog actually receives.
		if !strings.Contains(gotQuery, "filter%5Bsamples.biases%5D="+gitMetadataBias) {
			t.Errorf("query = %q, want the bias filter", gotQuery)
		}
		if gotHeaders.Get("DD-API-KEY") != "api-key" {
			t.Errorf("DD-API-KEY = %q", gotHeaders.Get("DD-API-KEY"))
		}
		if gotHeaders.Get("DD-APPLICATION-KEY") != "app-key" {
			t.Errorf("DD-APPLICATION-KEY = %q", gotHeaders.Get("DD-APPLICATION-KEY"))
		}
	})

	t.Run("decode", func(t *testing.T) {
		if resp.Data.ID != issueID {
			t.Errorf("Data.ID = %q, want %q", resp.Data.ID, issueID)
		}
		if resp.Data.Attributes.Service != "login-ui-app" {
			t.Errorf("Service = %q", resp.Data.Attributes.Service)
		}
		sample, ok := resp.sample()
		if !ok {
			t.Fatal("no sample decoded from the fixture")
		}
		if !sample.hasBias(gitMetadataBias) {
			t.Errorf("sample biases = %v, want %s", sample.Biases, gitMetadataBias)
		}
		if sample.Event.Env != "production" {
			t.Errorf("event env = %q, want production", sample.Event.Env)
		}
	})
}

// TestFetchIssueReportsAFailure checks a non-2xx carries through with the issue
// id attached, rather than surfacing as a bare HTTP status.
func TestFetchIssueReportsAFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"errors":["Forbidden"]}`))
	}))
	defer srv.Close()

	_, err := newDatadogClient(srv.URL, "api-key", "app-key").fetchIssue(context.Background(), issueID)
	if err == nil {
		t.Fatal("fetchIssue() error = nil, want one for a 403")
	}
	if !strings.Contains(err.Error(), issueID) {
		t.Errorf("error does not name the issue:\n  %v", err)
	}
	if !strings.Contains(err.Error(), "403") {
		t.Errorf("error does not carry the status:\n  %v", err)
	}
}

// TestSampleSelection covers which sample a report is built from. Only the
// git_metadata one can carry frames, so it has to win even when another sample
// comes back first.
func TestSampleSelection(t *testing.T) {
	other := included{Type: sampleType, Attributes: sampleAttributes{Biases: []string{"latest"}, Service: "other"}}
	wanted := included{Type: sampleType, Attributes: sampleAttributes{Biases: []string{gitMetadataBias}, Service: "wanted"}}
	unrelated := included{Type: "error_tracking_issue_case", Attributes: sampleAttributes{Service: "case"}}

	tests := map[string]struct {
		included    []included
		wantService string
		wantFound   bool
	}{
		"prefers the git_metadata sample": {
			included: []included{other, wanted}, wantService: "wanted", wantFound: true,
		},
		"already first": {
			included: []included{wanted, other}, wantService: "wanted", wantFound: true,
		},
		"falls back to any sample": {
			included: []included{other}, wantService: "other", wantFound: true,
		},
		"skips resources that are not samples": {
			included: []included{unrelated}, wantFound: false,
		},
		"nothing included": {
			wantFound: false,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got, found := issueResponse{Included: tt.included}.sample()
			if found != tt.wantFound {
				t.Fatalf("found = %v, want %v", found, tt.wantFound)
			}
			if got.Service != tt.wantService {
				t.Errorf("sample service = %q, want %q", got.Service, tt.wantService)
			}
		})
	}
}

// TestExplain checks that each Datadog failure says what to do about it.
//
// Without these, a wrong site and a key missing a scope are both "HTTP 403",
// and the site — the likeliest cause and the thing nobody checks — is never
// mentioned.
//
// explain is exercised directly rather than through a test server, because a
// test server has no Datadog site to report.
func TestExplain(t *testing.T) {
	const site = "datadoghq.eu"

	tests := map[string]struct {
		sentinel error
		wantInfo []string
	}{
		"401 points at the keys and the org": {
			sentinel: gerror.ErrPermissionDenied,
			wantInfo: []string{apiKeyEnv, appKeyEnv, site},
		},
		"403 points at the scope and the org": {
			sentinel: gerror.ErrForbidden,
			wantInfo: []string{"Error Tracking", site},
		},
		"404 points at the org rather than only the issue": {
			sentinel: gerror.ErrNotFound,
			wantInfo: []string{"deleted", site},
		},
		"a timeout says what to do next": {
			sentinel: gerror.ErrTimeout,
			wantInfo: []string{"retry"},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			// Wrapping the sentinel is what gommon's status error does, so
			// errors.Is matches the same way it will in production.
			original := fmt.Errorf("Datadog returned HTTP 999: %w", tt.sentinel)

			got := explain(original, site)

			for _, want := range tt.wantInfo {
				if !strings.Contains(got.Error(), want) {
					t.Errorf("message does not mention %q:\n  %v", want, got)
				}
			}
			// The original is wrapped, so the status and Datadog's own body
			// are still available to anyone who wants them.
			if !strings.Contains(got.Error(), "HTTP 999") {
				t.Errorf("explain lost the original message:\n  %v", got)
			}
			if !errors.Is(got, tt.sentinel) {
				t.Errorf("explain broke the sentinel chain for %v", tt.sentinel)
			}
		})
	}
}

// TestExplainPassesThroughUnknownErrors keeps explain from swallowing anything
// it has no advice for.
func TestExplainPassesThroughUnknownErrors(t *testing.T) {
	sentinel := errors.New("something else entirely")

	if got := explain(sentinel, "datadoghq.com"); !errors.Is(got, sentinel) {
		t.Errorf("explain() = %v, want the original error", got)
	}
}
