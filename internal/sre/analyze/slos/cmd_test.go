package slos

import (
	"bytes"
	"context"
	"encoding/csv"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

var testOptions = options{SLOFile: "testdata/openslo.yaml", Group: "orders", Format: "csv", Batch: 8, MinEvents: 100}

// testNow is the clock of every run.
var testNow = time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)

// testDeps has the Datadog keys in the env and answers every query through q. stdout collects the report.
func testDeps(q *fakeQuerier) (deps, *bytes.Buffer) {
	env := map[string]string{apiKeyEnv: "api", appKeyEnv: "app"}
	var stdout bytes.Buffer
	return deps{
		getenv:    func(k string) string { return env[k] },
		newClient: func(string, string, string) querier { return q },
		stdout:    &stdout,
		stderr:    io.Discard,
		now:       func() time.Time { return testNow },
	}, &stdout
}

func noAnswers() *fakeQuerier {
	return &fakeQuerier{answer: func(string) []point { return nil }}
}

// sloColumn is the SLO name of each csv row.
func sloColumn(t *testing.T, stdout *bytes.Buffer) []string {
	t.Helper()
	records, err := csv.NewReader(stdout).ReadAll()
	if err != nil || len(records) == 0 {
		t.Fatalf("read the csv: %v", err)
	}
	var out []string
	for _, r := range records[1:] {
		out = append(out, r[slices.Index(csvColumns, "SLO")])
	}
	slices.Sort(out)
	return out
}

// TestRunScoresTheGroupsSLOs gives every SLO of the group a row, and none of another group.
func TestRunScoresTheGroupsSLOs(t *testing.T) {
	d, stdout := testDeps(noAnswers())
	if err := run(context.Background(), testOptions, d); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got, want := sloColumn(t, stdout), []string{"Checkout", "orders-nightly-export"}; !slices.Equal(got, want) {
		t.Errorf("rows = %v, want %v", got, want)
	}
}

// TestRunTag scores only the SLOs that carry one of the tags, and says so when none does.
func TestRunTag(t *testing.T) {
	d, stdout := testDeps(noAnswers())
	o := testOptions
	o.Tags = []string{"batch"}
	if err := run(context.Background(), o, d); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := sloColumn(t, stdout); !slices.Equal(got, []string{"orders-nightly-export"}) {
		t.Errorf("rows = %v, want the one SLO tagged batch", got)
	}
	o.Tags = []string{"p9"}
	if err := run(context.Background(), o, d); err == nil || !strings.Contains(err.Error(), "p9") {
		t.Errorf("no SLO tagged p9: error = %v, want one that says so", err)
	}
}

// TestRunEndsEveryWindowAtAt reads every SLO up to --at, or now when it is not set, and refuses a time in the future.
func TestRunEndsEveryWindowAtAt(t *testing.T) {
	for _, at := range []string{"", "2026-10-01T00:00:00Z"} {
		q := noAnswers()
		d, _ := testDeps(q)
		o := testOptions
		o.At = at
		if err := run(context.Background(), o, d); err != nil {
			t.Fatalf("--at %q: %v", at, err)
		}
		want := testNow
		if at != "" {
			want, _ = time.Parse(time.RFC3339, at)
		}
		for _, r := range q.requests {
			if !r.to.Equal(want) {
				t.Errorf("--at %q: a request ends at %v, want %v", at, r.to, want)
			}
		}
	}
	for at, wantErr := range map[string]string{"2026-12-01T00:00:00Z": "future", "2026-10-01": "RFC 3339"} {
		d, _ := testDeps(noAnswers())
		o := testOptions
		o.At = at
		if err := run(context.Background(), o, d); err == nil || !strings.Contains(err.Error(), wantErr) {
			t.Errorf("--at %s: error = %v, want one that says %q", at, err, wantErr)
		}
	}
}

// TestDryRunPrintsTheQueriesAndAsksNothing needs no keys, and asks Datadog nothing.
func TestDryRunPrintsTheQueriesAndAsksNothing(t *testing.T) {
	q := noAnswers()
	d, stdout := testDeps(q)
	d.getenv = func(string) string { return "" }
	o := testOptions
	o.DryRun = true
	if err := run(context.Background(), o, d); err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if len(q.requests) != 0 {
		t.Errorf("requests = %d, want none on a dry run", len(q.requests))
	}
	for _, want := range []string{"Checkout", "avg:acme.checkout.response_time.95percentile{env:production}.rollup(avg, 300)", "orders-nightly-export"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("the dry run should print %s:\n%s", want, stdout)
		}
	}
}

// TestOutWritesOnlyOnSuccess leaves the last report in --out when a run fails, and replaces it when one succeeds.
func TestOutWritesOnlyOnSuccess(t *testing.T) {
	out := filepath.Join(t.TempDir(), "slo-risk.html")
	if err := os.WriteFile(out, []byte("last report"), 0o644); err != nil {
		t.Fatal(err)
	}
	o := testOptions
	o.Out, o.Format = out, "html"

	d, _ := testDeps(noAnswers())
	d.getenv = func(string) string { return "" }
	if err := run(context.Background(), o, d); err == nil {
		t.Fatal("run with no keys succeeded, want an error")
	}
	if got, _ := os.ReadFile(out); string(got) != "last report" {
		t.Errorf("after a failed run, --out holds %q, want the last report untouched", got)
	}

	d, _ = testDeps(noAnswers())
	if err := run(context.Background(), o, d); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got, _ := os.ReadFile(out); !strings.Contains(string(got), "Checkout") {
		t.Errorf("after a run that succeeds, --out holds %q, want the HTML report", got)
	}
}

// TestRunSendsTheKeysToTheOrgsSite builds the API host from --datadog-site, then DD_SITE, then DATADOG_SITE, and
// falls back to Datadog's US1 site, so an org on US1 sets nothing.
func TestRunSendsTheKeysToTheOrgsSite(t *testing.T) {
	cases := []struct {
		name string
		flag string
		env  map[string]string
		want string
	}{
		{name: "default", want: "https://api.datadoghq.com"},
		{name: "DATADOG_SITE", env: map[string]string{"DATADOG_SITE": "us3.datadoghq.com"}, want: "https://api.us3.datadoghq.com"},
		{name: "DD_SITE over DATADOG_SITE", env: map[string]string{"DD_SITE": "datadoghq.eu", "DATADOG_SITE": "us3.datadoghq.com"}, want: "https://api.datadoghq.eu"},
		{name: "flag over DD_SITE", flag: "ap1.datadoghq.com", env: map[string]string{"DD_SITE": "datadoghq.eu"}, want: "https://api.ap1.datadoghq.com"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			q := noAnswers()
			d, _ := testDeps(q)
			env := map[string]string{apiKeyEnv: "api", appKeyEnv: "app"}
			for k, v := range tc.env {
				env[k] = v
			}
			d.getenv = func(k string) string { return env[k] }
			var got string
			d.newClient = func(baseURL, _, _ string) querier {
				got = baseURL
				return q
			}
			o := testOptions
			o.DatadogSite = tc.flag
			if err := run(context.Background(), o, d); err != nil {
				t.Fatalf("run: %v", err)
			}
			if got != tc.want {
				t.Errorf("base URL = %q, want %q", got, tc.want)
			}
		})
	}
}
