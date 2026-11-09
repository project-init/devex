package errtrack

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/project-init/devex/internal/sre/errtrack/report"
)

// testNow is a fixed clock, so fetched_at is the same on every run and a test
// can assert on the output.
var testNow = time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)

// fakeFetcher stands in for Datadog. It records what the command constructed it
// with, so a test can assert the resolved base URL and credentials without a
// request ever being made.
type fakeFetcher struct {
	resp issueResponse
	err  error

	gotBaseURL, gotAPIKey, gotAppKey string
	gotIssueID                       string
	calls                            int
}

func (f *fakeFetcher) fetchIssue(_ context.Context, issueID string) (issueResponse, error) {
	f.calls++
	f.gotIssueID = issueID

	return f.resp, f.err
}

// newClient returns a func matching deps.newClient that records its arguments.
func (f *fakeFetcher) newClient() func(baseURL, apiKey, appKey string) issueFetcher {
	return func(baseURL, apiKey, appKey string) issueFetcher {
		f.gotBaseURL, f.gotAPIKey, f.gotAppKey = baseURL, apiKey, appKey

		return f
	}
}

// testDeps builds deps that reach nothing outside the test: a map-backed
// environment, buffers for both streams, a frozen clock, and a fake client.
func testDeps(env map[string]string, f *fakeFetcher) (deps, *bytes.Buffer, *bytes.Buffer) {
	var stdout, stderr bytes.Buffer

	return deps{
		getenv:    func(k string) string { return env[k] },
		newClient: f.newClient(),
		stdout:    &stdout,
		stderr:    &stderr,
		now:       func() time.Time { return testNow },
	}, &stdout, &stderr
}

func keyEnv() map[string]string {
	return map[string]string{apiKeyEnv: "api", appKeyEnv: "app"}
}

// fixtureFetcher answers with the sanitised real response.
func fixtureFetcher(t *testing.T) *fakeFetcher {
	t.Helper()

	return &fakeFetcher{resp: loadFixture(t)}
}

func TestRunWritesTheReport(t *testing.T) {
	f := fixtureFetcher(t)
	d, stdout, stderr := testDeps(keyEnv(), f)

	if err := run(context.Background(), options{}, issuePageLink, d); err != nil {
		t.Fatalf("run() error = %v", err)
	}

	t.Run("the client was built for the resolved site and keys", func(t *testing.T) {
		if f.gotBaseURL != "https://api.datadoghq.com" {
			t.Errorf("baseURL = %q", f.gotBaseURL)
		}
		if f.gotAPIKey != "api" || f.gotAppKey != "app" {
			t.Errorf("keys = %q, %q", f.gotAPIKey, f.gotAppKey)
		}
		if f.gotIssueID != issueID {
			t.Errorf("issueID = %q, want %q", f.gotIssueID, issueID)
		}
		if f.calls != 1 {
			t.Errorf("fetchIssue called %d times, want 1", f.calls)
		}
	})

	t.Run("stdout is the report and nothing else", func(t *testing.T) {
		var rep report.Report
		dec := json.NewDecoder(bytes.NewReader(stdout.Bytes()))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&rep); err != nil {
			t.Fatalf("stdout is not a clean report document: %v\n%s", err, stdout)
		}
		if rep.Summary == "" || rep.Error.Service != "login-ui-app" {
			t.Errorf("report looks wrong: %+v", rep)
		}
		if !rep.FetchedAt.Equal(testNow) {
			t.Errorf("FetchedAt = %v, want the injected clock %v", rep.FetchedAt, testNow)
		}
	})

	t.Run("output is indented and newline terminated", func(t *testing.T) {
		// A person reads this as often as a program does, and a trailing
		// newline keeps a shell prompt on its own line.
		if !strings.Contains(stdout.String(), "\n  \"source\"") {
			t.Error("output is not indented")
		}
		if !strings.HasSuffix(stdout.String(), "}\n") {
			t.Errorf("output does not end in a newline: %q", stdout.String()[stdout.Len()-10:])
		}
	})

	t.Run("nothing on stderr for a clean run", func(t *testing.T) {
		if stderr.Len() != 0 {
			t.Errorf("stderr = %q", stderr)
		}
	})
}

// TestRunChecksTheLinkBeforeTheKeys pins the order. The link is the argument
// the user just typed and needs no setup to validate, so a bad paste has to be
// reported as a bad paste — not as missing credentials, which sends someone
// configuring an environment that was never the problem.
func TestRunChecksTheLinkBeforeTheKeys(t *testing.T) {
	f := &fakeFetcher{}
	d, _, _ := testDeps(nil, f) // no keys either

	err := run(context.Background(), options{}, "https://example.com/nope", d)
	if err == nil {
		t.Fatal("run() error = nil, want one")
	}
	if !strings.Contains(err.Error(), "not a Datadog host") {
		t.Errorf("error is not about the link:\n  %v", err)
	}
	if strings.Contains(err.Error(), apiKeyEnv) {
		t.Errorf("error blames the credentials for a bad link:\n  %v", err)
	}
	if f.calls != 0 {
		t.Error("fetched despite a bad link")
	}
}

func TestRunReportsMissingKeys(t *testing.T) {
	f := &fakeFetcher{}
	d, _, _ := testDeps(map[string]string{apiKeyEnv: "api"}, f)

	err := run(context.Background(), options{}, issuePageLink, d)
	if err == nil {
		t.Fatal("run() error = nil, want one")
	}
	if !strings.Contains(err.Error(), appKeyEnv) {
		t.Errorf("error does not name the missing key:\n  %v", err)
	}
	if f.calls != 0 {
		t.Error("fetched without credentials")
	}
}

// TestRunPropagatesFetchFailure checks a Datadog failure reaches the user and
// leaves stdout untouched, so a failed run cannot emit half a document into a
// pipeline.
func TestRunPropagatesFetchFailure(t *testing.T) {
	sentinel := errors.New("datadog said no")
	f := &fakeFetcher{err: sentinel}
	d, stdout, _ := testDeps(keyEnv(), f)

	err := run(context.Background(), options{}, issuePageLink, d)
	if !errors.Is(err, sentinel) {
		t.Errorf("run() error = %v, want it to wrap %v", err, sentinel)
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want nothing when the fetch fails", stdout)
	}
}

func TestRunSiteResolution(t *testing.T) {
	euLink := "https://app.datadoghq.eu/error-tracking/issue/" + issueID

	tests := map[string]struct {
		opts     options
		link     string
		env      map[string]string
		wantBase string
		wantSite string // the site the issue link in the report is built on
		wantNote bool
	}{
		"site comes from the link": {
			link: euLink, env: keyEnv(),
			wantBase: "https://api.datadoghq.eu", wantSite: "datadoghq.eu",
		},
		"flag overrides the link": {
			opts: options{DatadogSite: "us3.datadoghq.com"},
			link: euLink, env: keyEnv(),
			wantBase: "https://api.us3.datadoghq.com", wantSite: "datadoghq.eu",
		},

		// The keys in the environment most likely belong to the org the
		// environment names, so this disagreement is worth saying out loud
		// rather than resolving silently into a 403.
		"link and environment disagree": {
			link:     euLink,
			env:      map[string]string{apiKeyEnv: "api", appKeyEnv: "app", siteEnv: defaultSite},
			wantBase: "https://api.datadoghq.eu", wantSite: "datadoghq.eu", wantNote: true,
		},

		// A bare id carries no host, so the environment decides — and the
		// report's issue link has to be built from that same site.
		"a bare id falls back to the environment": {
			link:     issueID,
			env:      map[string]string{apiKeyEnv: "api", appKeyEnv: "app", siteEnv: "datadoghq.eu"},
			wantBase: "https://api.datadoghq.eu", wantSite: "datadoghq.eu",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			f := fixtureFetcher(t)
			d, stdout, stderr := testDeps(tt.env, f)

			if err := run(context.Background(), tt.opts, tt.link, d); err != nil {
				t.Fatalf("run() error = %v", err)
			}
			if f.gotBaseURL != tt.wantBase {
				t.Errorf("baseURL = %q, want %q", f.gotBaseURL, tt.wantBase)
			}

			var rep report.Report
			if err := json.Unmarshal(stdout.Bytes(), &rep); err != nil {
				t.Fatalf("decode report: %v", err)
			}
			if want := "https://app." + tt.wantSite + "/error-tracking/issue/"; !strings.HasPrefix(rep.Links.Issue, want) {
				t.Errorf("Links.Issue = %q, want it to start %q", rep.Links.Issue, want)
			}

			note := stderr.String()
			if tt.wantNote && note == "" {
				t.Error("expected a note about the site disagreement on stderr")
			}
			if !tt.wantNote && note != "" {
				t.Errorf("unexpected stderr output: %q", note)
			}
		})
	}
}

func TestRunEnvOverrideReachesTheReport(t *testing.T) {
	f := fixtureFetcher(t)
	d, stdout, _ := testDeps(keyEnv(), f)

	if err := run(context.Background(), options{Env: "staging"}, issuePageLink, d); err != nil {
		t.Fatalf("run() error = %v", err)
	}

	var rep report.Report
	if err := json.Unmarshal(stdout.Bytes(), &rep); err != nil {
		t.Fatalf("decode report: %v", err)
	}
	if rep.Error.Environment == nil || *rep.Error.Environment != "staging" {
		t.Errorf("Environment = %v, want the --env override", rep.Error.Environment)
	}
}

func TestRunWritesToAFile(t *testing.T) {
	f := fixtureFetcher(t)
	d, stdout, stderr := testDeps(keyEnv(), f)
	path := filepath.Join(t.TempDir(), "report.json")

	if err := run(context.Background(), options{Out: path}, issuePageLink, d); err != nil {
		t.Fatalf("run() error = %v", err)
	}

	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the written report: %v", err)
	}

	var rep report.Report
	if err := json.Unmarshal(written, &rep); err != nil {
		t.Fatalf("the file is not a report: %v", err)
	}
	if rep.Error.Service != "login-ui-app" {
		t.Errorf("report looks wrong: %+v", rep.Error)
	}

	// Nothing on stdout, so --out composes with a pipe; the confirmation goes
	// to stderr instead.
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want nothing when --out is set", stdout)
	}
	if !strings.Contains(stderr.String(), path) {
		t.Errorf("stderr = %q, want it to name the file written", stderr)
	}
}

// TestRunLeavesTheFileAloneOnFailure is the reason the encode happens before
// the file is opened: a failed run should leave yesterday's good report in
// place rather than truncating it to nothing.
func TestRunLeavesTheFileAloneOnFailure(t *testing.T) {
	const previous = `{"previous":"report"}`

	path := filepath.Join(t.TempDir(), "report.json")
	if err := os.WriteFile(path, []byte(previous), 0o644); err != nil {
		t.Fatal(err)
	}

	f := &fakeFetcher{err: errors.New("datadog said no")}
	d, _, _ := testDeps(keyEnv(), f)

	if err := run(context.Background(), options{Out: path}, issuePageLink, d); err == nil {
		t.Fatal("run() error = nil, want the fetch failure")
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the file was removed: %v", err)
	}
	if string(after) != previous {
		t.Errorf("file = %q, want it untouched at %q", after, previous)
	}
}
