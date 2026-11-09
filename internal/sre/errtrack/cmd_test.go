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
)

// testNow is a fixed clock, so fetched_at is the same on every run and the
// golden report can be compared at all.
var testNow = time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)

// fakeFetcher stands in for Datadog, recording what the command constructed it
// with so a test can check the resolved base URL without a request being made.
type fakeFetcher struct {
	resp issueResponse
	err  error

	gotBaseURL string
	calls      int
}

func (f *fakeFetcher) fetchIssue(_ context.Context, _ string) (issueResponse, error) {
	f.calls++

	return f.resp, f.err
}

// testDeps builds deps that reach nothing outside the test: a map-backed
// environment, buffers for both streams, a frozen clock and a fake client.
func testDeps(env map[string]string, f *fakeFetcher) (deps, *bytes.Buffer, *bytes.Buffer) {
	var stdout, stderr bytes.Buffer

	return deps{
		getenv: func(k string) string { return env[k] },
		newClient: func(baseURL, _, _ string) issueFetcher {
			f.gotBaseURL = baseURL

			return f
		},
		stdout: &stdout,
		stderr: &stderr,
		now:    func() time.Time { return testNow },
	}, &stdout, &stderr
}

func keyEnv() map[string]string {
	return map[string]string{apiKeyEnv: "api", appKeyEnv: "app"}
}

// TestRunOrdersItsFailures is the one thing about run worth pinning: the link
// is validated before the credentials are read. Reversed, someone who pastes a
// Grafana URL with no keys exported is told to go configure Datadog
// credentials — chasing a problem that was never theirs.
func TestRunOrdersItsFailures(t *testing.T) {
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

// TestRunKeepsStdoutClean matters because stdout is the JSON document: a stray
// write there corrupts a `| jq` or `| claude` pipeline, and a half-written
// document on failure is worse than none. Notes go to stderr instead.
func TestRunKeepsStdoutClean(t *testing.T) {
	t.Run("a failed fetch writes nothing", func(t *testing.T) {
		sentinel := errors.New("datadog said no")
		f := &fakeFetcher{err: sentinel}
		d, stdout, _ := testDeps(keyEnv(), f)

		if err := run(context.Background(), options{}, issuePageLink, d); !errors.Is(err, sentinel) {
			t.Errorf("run() error = %v, want it to wrap %v", err, sentinel)
		}
		if stdout.Len() != 0 {
			t.Errorf("stdout = %q, want nothing when the fetch fails", stdout)
		}
	})

	t.Run("a site disagreement goes to stderr", func(t *testing.T) {
		f := &fakeFetcher{resp: loadFixture(t)}
		env := map[string]string{apiKeyEnv: "api", appKeyEnv: "app", siteEnv: defaultSite}
		d, stdout, stderr := testDeps(env, f)

		euLink := "https://app.datadoghq.eu/error-tracking/issue/" + issueID
		if err := run(context.Background(), options{}, euLink, d); err != nil {
			t.Fatalf("run() error = %v", err)
		}

		// The link wins, and the keys most likely belong to the org the
		// environment names, so the disagreement is worth saying out loud.
		if f.gotBaseURL != "https://api.datadoghq.eu" {
			t.Errorf("baseURL = %q, want the link's site", f.gotBaseURL)
		}
		if stderr.Len() == 0 {
			t.Error("no note about the site disagreement")
		}
		if !json.Valid(stdout.Bytes()) {
			t.Errorf("stdout is not a clean document: %q", stdout)
		}
	})
}

// TestRunWritesToAFile covers --out, including the reason the encode happens
// before the file is opened: a failed run should leave the last good report in
// place rather than truncating it to nothing.
func TestRunWritesToAFile(t *testing.T) {
	const previous = `{"previous":"report"}`

	t.Run("writes on success", func(t *testing.T) {
		f := &fakeFetcher{resp: loadFixture(t)}
		d, stdout, stderr := testDeps(keyEnv(), f)
		path := filepath.Join(t.TempDir(), "report.json")

		if err := run(context.Background(), options{Out: path}, issuePageLink, d); err != nil {
			t.Fatalf("run() error = %v", err)
		}

		written, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read the written report: %v", err)
		}
		if !json.Valid(written) {
			t.Errorf("the file is not valid JSON: %s", written)
		}
		// Nothing on stdout, so --out still composes with a pipe.
		if stdout.Len() != 0 {
			t.Errorf("stdout = %q, want nothing when --out is set", stdout)
		}
		if !strings.Contains(stderr.String(), path) {
			t.Errorf("stderr = %q, want it to name the file written", stderr)
		}
	})

	t.Run("leaves the file alone on failure", func(t *testing.T) {
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
	})
}
