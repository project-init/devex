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
// The bias filter is why this is worth a test: Datadog ignores an unrecognised
// filter rather than rejecting it, so getting it wrong returns samples with no
// stack frames — which reads as "this error has no stack trace" rather than
// "my request was malformed". Nothing about calling the CLI would reveal that.
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

	// The id comes out of a pasted link, so it has to be escaped.
	if escaped := issuePath("../../admin/secrets"); strings.Contains(escaped, "../") {
		t.Errorf("issuePath() = %q, want the id escaped", escaped)
	}
}

func TestFetchIssue(t *testing.T) {
	body, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	var gotQuery string
	var gotHeaders http.Header

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery, gotHeaders = r.URL.RawQuery, r.Header
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	resp, err := newDatadogClient(srv.URL, "api-key", "app-key").fetchIssue(context.Background(), issueID)
	if err != nil {
		t.Fatalf("fetchIssue() error = %v", err)
	}

	// Percent-encoded brackets are what Datadog actually receives, and it
	// honours them — a real response came back reporting the bias.
	if !strings.Contains(gotQuery, "filter%5Bsamples.biases%5D="+gitMetadataBias) {
		t.Errorf("query = %q, want the bias filter", gotQuery)
	}
	if gotHeaders.Get("DD-API-KEY") != "api-key" || gotHeaders.Get("DD-APPLICATION-KEY") != "app-key" {
		t.Errorf("auth headers = %q, %q", gotHeaders.Get("DD-API-KEY"), gotHeaders.Get("DD-APPLICATION-KEY"))
	}

	sample, ok := resp.sample()
	if !ok || resp.Data.ID != issueID || sample.Event.Env != "production" {
		t.Errorf("decode lost something: id=%q sample=%v env=%q", resp.Data.ID, ok, sample.Event.Env)
	}
}

// TestExplain checks that each Datadog failure says what to do about it.
//
// Without these, a wrong site and a key missing a scope are both "HTTP 403",
// and the site — the likeliest cause, and the thing nobody checks — is never
// mentioned. Exercised directly rather than through a test server, which has
// no Datadog site to report.
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
		"404 points at the org, not only the issue": {
			sentinel: gerror.ErrNotFound,
			wantInfo: []string{"deleted", site},
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
			if !strings.Contains(got.Error(), "HTTP 999") || !errors.Is(got, tt.sentinel) {
				t.Errorf("explain lost the original error:\n  %v", got)
			}
		})
	}

	// Anything explain has no advice for must pass through untouched.
	sentinel := errors.New("something else entirely")
	if got := explain(sentinel, site); !errors.Is(got, sentinel) {
		t.Errorf("explain() = %v, want the original error", got)
	}
}

// TestSampleSelection covers which sample a report is built from. Only the
// git_metadata one can carry frames, so it has to win even when another comes
// back first.
func TestSampleSelection(t *testing.T) {
	other := included{Type: sampleType, Attributes: sampleAttributes{Biases: []string{"latest"}, Service: "other"}}
	wanted := included{Type: sampleType, Attributes: sampleAttributes{Biases: []string{gitMetadataBias}, Service: "wanted"}}

	if got, _ := (issueResponse{Included: []included{other, wanted}}).sample(); got.Service != "wanted" {
		t.Errorf("sample = %q, want the git_metadata one even when it is not first", got.Service)
	}
	if got, _ := (issueResponse{Included: []included{other}}).sample(); got.Service != "other" {
		t.Errorf("sample = %q, want a fallback to any sample", got.Service)
	}
	if _, found := (issueResponse{Included: []included{{Type: "error_tracking_issue_case"}}}).sample(); found {
		t.Error("found a sample in a resource that is not one")
	}
}
