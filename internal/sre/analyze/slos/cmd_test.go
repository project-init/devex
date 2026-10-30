package slos

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/project-init/devex/internal/sre/config"
	"gopkg.in/yaml.v3"
)

var testOptions = options{Days: 30, Rollup: 15 * time.Minute, Format: "table", Batch: 8, MinRequests: 100}

func testConfig(group string) config.SLOsConfiguration {
	return config.SLOsConfiguration{Module: testModule, File: "slos.gen.yaml", Group: group}
}

// testMetricNames is what Datadog reports for testdata/slos.gen.yaml: the orders RPCs come from the acme app, and
// GetOrder also comes from the orders-api app. Nothing reports InventoryService.GetItem.
var testMetricNames = []string{
	"acme.acmeServicesOrdersV1OrdersServiceGetOrder" + p95Suffix,
	"orders-api.acmeServicesOrdersV1OrdersServiceGetOrder" + p95Suffix,
	"acme.acmeServicesOrdersV1OrdersServiceListOrders" + p95Suffix,
	"acme.somethingElse.response_time.count",
}

// testDeps serves testdata/slos.gen.yaml as the pinned release, with the Datadog keys in the env, and answers every
// query through q. stdout collects the report.
func testDeps(t *testing.T, q *fakeQuerier) (deps, *bytes.Buffer) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "slos.gen.yaml"), readTestdata(t), 0o644); err != nil {
		t.Fatal(err)
	}
	goModPath := filepath.Join(dir, "go.mod")
	if err := os.WriteFile(goModPath, []byte("module example.com/app\n\nrequire github.com/acme/protos v1.0.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{apiKeyEnv: "api", appKeyEnv: "app"}
	var stdout bytes.Buffer
	return deps{
		getenv:   func(k string) string { return env[k] },
		download: func(context.Context, string, string) (string, string, error) { return dir, "v1.0.0", nil },
		goMod:    goModPath,
		newClient: func(string, string, string) (client, error) {
			return &fakeClient{fakeQuerier: q, names: testMetricNames}, nil
		},
		stdout: &stdout,
		stderr: io.Discard,
		now:    time.Now,
	}, &stdout
}

// TestRunGivesEveryEntryARow scores every RPC of the group, with or without an SLO and with or without data, and
// gives an RPC two apps report one row per app.
func TestRunGivesEveryEntryARow(t *testing.T) {
	q := &fakeQuerier{answer: func(string) []point { return nil }}
	d, stdout := testDeps(t, q)
	o := testOptions
	o.Format = "csv"
	if err := run(context.Background(), o, testConfig("orders"), d); err != nil {
		t.Fatalf("run: %v", err)
	}
	var rows []string
	for _, r := range csvRows(t, stdout) {
		rows = append(rows, r[1])
	}
	var file struct {
		Services map[string][]struct{ Method string } `yaml:"services"`
	}
	if err := yaml.Unmarshal(readTestdata(t), &file); err != nil {
		t.Fatal(err)
	}
	var want []string
	for _, e := range file.Services["orders"] {
		want = append(want, endpoint{Method: e.Method}.Name())
		// GetOrder is reported by both acme and orders-api, so it gets a row for each.
		if strings.HasSuffix(e.Method, "/GetOrder") {
			want = append(want, endpoint{Method: e.Method}.Name())
		}
	}
	slices.Sort(rows)
	slices.Sort(want)
	if !slices.Equal(rows, want) {
		t.Errorf("rows = %v, want one for each orders RPC in the file: %v", rows, want)
	}
}

// csvRows runs the report as csv and returns its rows after the header.
func csvRows(t *testing.T, stdout *bytes.Buffer) [][]string {
	t.Helper()
	records, err := csv.NewReader(stdout).ReadAll()
	if err != nil || len(records) == 0 {
		t.Fatalf("read the csv: %v", err)
	}
	return records[1:]
}

// TestRunGroupErrors names the groups to choose from when --group is missing or unknown, and says so when the group
// owns no service, rather than writing an empty report.
func TestRunGroupErrors(t *testing.T) {
	cases := map[string]struct{ group, want string }{
		"missing": {group: "", want: "pass --group"},
		"unknown": {group: "ordrs", want: `no group "ordrs"`},
		"empty":   {group: "shipping", want: "has no RPCs"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			q := &fakeQuerier{answer: func(string) []point { return nil }}
			d, _ := testDeps(t, q)
			o := testOptions
			o.Format = "csv"
			err := run(context.Background(), o, testConfig(tc.group), d)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("run = %v, want an error that says %q", err, tc.want)
			}
			if tc.group != "shipping" && !strings.Contains(err.Error(), "inventory, orders, shipping") {
				t.Errorf("run error %q should list the groups in the file", err)
			}
			if len(q.batches) != 0 {
				t.Errorf("batches = %d, want no queries", len(q.batches))
			}
		})
	}
}

// TestRunNoAppReportsTheRPC gives an RPC no app reports a NO DATA row and no query, so an uninstrumented group costs
// nothing to report on.
func TestRunNoAppReportsTheRPC(t *testing.T) {
	q := &fakeQuerier{answer: func(string) []point { return nil }}
	d, stdout := testDeps(t, q)
	o := testOptions
	o.Format = "csv"
	if err := run(context.Background(), o, testConfig("inventory"), d); err != nil {
		t.Fatalf("run: %v", err)
	}
	rows := csvRows(t, stdout)
	if len(q.batches) != 0 {
		t.Errorf("batches = %d, want no queries for an RPC no app reports", len(q.batches))
	}
	if len(rows) != 1 || rows[0][0] != noData.String() || rows[0][len(rows[0])-1] != "-" {
		t.Errorf("rows = %v, want one NO DATA row with no app", rows)
	}
}

// TestRunVersionWithoutAGoModRequire reads the latest release when go.mod does not require the module, so a
// repository that is not a Go consumer of the protos still gets a report, and --protos-version pins it instead.
func TestRunVersionWithoutAGoModRequire(t *testing.T) {
	cases := map[string]struct{ goMod, flag, want string }{
		"go.mod requires it":    {goMod: "module example.com/app\n\nrequire github.com/acme/protos v1.0.0\n", want: "v1.0.0"},
		"go.mod does not":       {goMod: "module example.com/app\n", want: defaultVersion},
		"no go.mod at all":      {want: defaultVersion},
		"--protos-version wins": {goMod: "module example.com/app\n\nrequire github.com/acme/protos v1.0.0\n", flag: "v2.3.4", want: "v2.3.4"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "slos.gen.yaml"), readTestdata(t), 0o644); err != nil {
				t.Fatal(err)
			}
			goModPath := filepath.Join(dir, "go.mod")
			if tc.goMod != "" {
				if err := os.WriteFile(goModPath, []byte(tc.goMod), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			var asked string
			d, _ := testDeps(t, &fakeQuerier{answer: func(string) []point { return nil }})
			d.goMod = goModPath
			d.download = func(_ context.Context, _, version string) (string, string, error) {
				asked = version
				return dir, "v9.9.9", nil
			}
			o := testOptions
			o.Format, o.ProtosVersion = "csv", tc.flag
			if err := run(context.Background(), o, testConfig("inventory"), d); err != nil {
				t.Fatalf("run: %v", err)
			}
			if asked != tc.want {
				t.Errorf("downloaded %q, want %q", asked, tc.want)
			}
		})
	}
}

// TestRunModuleSource reads the SLO file from --module, then slos.module, and finds it from go.mod when neither is
// set.
func TestRunModuleSource(t *testing.T) {
	cases := map[string]struct{ flag, config, want string }{
		"--module wins":           {flag: "github.com/flag/protos", config: "github.com/config/protos", want: "github.com/flag/protos"},
		"slos.module over go.mod": {config: "github.com/config/protos", want: "github.com/config/protos"},
		"found from go.mod":       {want: testModule},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			d, _ := testDeps(t, &fakeQuerier{answer: func(string) []point { return nil }})
			cache := t.TempDir()
			cached := filepath.Join(cache, testModule+"@v1.0.0")
			if err := os.MkdirAll(cached, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(cached, "slos.gen.yaml"), readTestdata(t), 0o644); err != nil {
				t.Fatal(err)
			}
			d.modCache = func(context.Context) (string, error) { return cache, nil }
			download := d.download
			var asked string
			d.download = func(ctx context.Context, path, version string) (string, string, error) {
				asked = path
				return download(ctx, path, version)
			}
			o := testOptions
			o.Format, o.Module = "csv", tc.flag
			cfg := testConfig("inventory")
			cfg.Module = tc.config
			if err := run(context.Background(), o, cfg, d); err != nil {
				t.Fatalf("run: %v", err)
			}
			if asked != tc.want {
				t.Errorf("read the SLO file from %q, want %q", asked, tc.want)
			}
		})
	}
}

// TestRunPinnedPrefixSkipsTheMetricList uses --metric-prefix as given and never lists the metrics.
func TestRunPinnedPrefixSkipsTheMetricList(t *testing.T) {
	q := &fakeQuerier{answer: func(string) []point { return nil }}
	d, _ := testDeps(t, q)
	var c *fakeClient
	d.newClient = func(string, string, string) (client, error) {
		c = &fakeClient{fakeQuerier: q, names: testMetricNames}
		return c, nil
	}
	o := testOptions
	o.MetricPrefix = "pinned"
	if err := run(context.Background(), o, testConfig("inventory"), d); err != nil {
		t.Fatalf("run: %v", err)
	}
	if c.listed != 0 {
		t.Errorf("listed the metrics %d times, want none with --metric-prefix", c.listed)
	}
	if len(q.batches) != 1 || !strings.Contains(q.batches[0][0], "pinned.acmeServicesInventoryV1InventoryServiceGetItem") {
		t.Errorf("queries = %v, want them under the pinned prefix", q.batches)
	}
}

// TestRunTagFilter scores only the RPCs carrying the tag.
func TestRunTagFilter(t *testing.T) {
	q := &fakeQuerier{answer: func(string) []point { return nil }}
	d, stdout := testDeps(t, q)
	o := testOptions
	o.Format, o.Tags = "csv", []string{"p0"}
	if err := run(context.Background(), o, testConfig("orders"), d); err != nil {
		t.Fatalf("run: %v", err)
	}
	for _, r := range csvRows(t, stdout) {
		if r[1] != "OrdersService.GetOrder" {
			t.Errorf("row %v, want only the p0 RPC", r)
		}
	}
}

// TestRunUsesTheEvaluationWindowAsTheRollup reads the RPC's own x-slo-evaluation-window instead of --rollup.
func TestRunUsesTheEvaluationWindowAsTheRollup(t *testing.T) {
	q := &fakeQuerier{answer: func(string) []point { return nil }}
	d, _ := testDeps(t, q)
	o := testOptions
	if err := run(context.Background(), o, testConfig("orders"), d); err != nil {
		t.Fatalf("run: %v", err)
	}
	var got []string
	for _, batch := range q.batches {
		for _, query := range batch {
			if strings.Contains(query, "ListOrders") && strings.Contains(query, "95percentile") {
				got = append(got, query)
			}
		}
	}
	// ListOrders sets evaluationWindow: 1h, where --rollup is 15m.
	if len(got) != 1 || !strings.Contains(got[0], "rollup(avg, 3600)") {
		t.Errorf("ListOrders p95 queries = %v, want one rolled up to its 1h evaluation window", got)
	}
}

// TestDryRunPrintsTheQueriesAndAsksNothing lists every RPC's queries, under the app that reports it, without
// running one.
func TestDryRunPrintsTheQueriesAndAsksNothing(t *testing.T) {
	q := &fakeQuerier{answer: func(string) []point { t.Error("a dry run ran a query"); return nil }}
	d, stdout := testDeps(t, q)
	o := testOptions
	o.DryRun = true
	if err := run(context.Background(), o, testConfig("orders"), d); err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if len(q.batches) != 0 {
		t.Errorf("batches = %d, want none on a dry run", len(q.batches))
	}
	for _, want := range []string{"OrdersService.GetOrder", "OrdersService.ListOrders", "orders-api.acmeServicesOrdersV1OrdersServiceGetOrder"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("the dry run should print %s:\n%s", want, stdout)
		}
	}
}

// TestRunWithoutKeysNamesBoth stops before any request when the Datadog keys are not exported.
func TestRunWithoutKeysNamesBoth(t *testing.T) {
	d, _ := testDeps(t, &fakeQuerier{answer: func(string) []point { return nil }})
	d.getenv = func(string) string { return "" }
	err := run(context.Background(), testOptions, testConfig("orders"), d)
	if err == nil || !strings.Contains(err.Error(), apiKeyEnv) || !strings.Contains(err.Error(), appKeyEnv) {
		t.Errorf("run = %v, want an error naming %s and %s", err, apiKeyEnv, appKeyEnv)
	}
}

// TestRunRefusesBadInputBeforeAnyRequest refuses a site that is not Datadog's before it reads a key, since the keys
// go to api.<site>.
func TestRunRefusesBadInputBeforeAnyRequest(t *testing.T) {
	cases := []struct {
		name   string
		cfg    func(*config.SLOsConfiguration)
		format string
		want   string
	}{
		{name: "unknown site", cfg: func(c *config.SLOsConfiguration) { c.Datadog.Site = "example.com" }, format: "table", want: "slos.datadog.site"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, _ := testDeps(t, nil)
			d.download = func(context.Context, string, string) (string, string, error) {
				t.Error("run downloaded the module, want it to stop first")
				return "", "", errors.New("stop")
			}
			cfg := testConfig("orders")
			tc.cfg(&cfg)
			o := testOptions
			o.Format = tc.format
			if err := run(context.Background(), o, cfg, d); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("run = %v, want an error that names %q", err, tc.want)
			}
		})
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

	d, _ := testDeps(t, &fakeQuerier{answer: func(string) []point { return nil }})
	d.getenv = func(string) string { return "" }
	if err := run(context.Background(), o, testConfig("orders"), d); err == nil {
		t.Fatal("run with no keys succeeded, want an error")
	}
	if got, _ := os.ReadFile(out); string(got) != "last report" {
		t.Errorf("after a failed run, --out holds %q, want the last report untouched", got)
	}

	d, _ = testDeps(t, &fakeQuerier{answer: func(string) []point { return nil }})
	if err := run(context.Background(), o, testConfig("orders"), d); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got, _ := os.ReadFile(out); !strings.Contains(string(got), "OrdersService.GetOrder") {
		t.Errorf("after a run that succeeds, --out holds %q, want the HTML report", got)
	}
}

func TestWindow(t *testing.T) {
	now := time.Date(2026, 11, 10, 15, 0, 0, 0, time.UTC)
	cases := []struct {
		name      string
		o         options
		wantFrom  time.Time
		wantWords string
		wantErr   string
	}{
		{name: "since starts at that UTC midnight", o: options{Days: 30, Since: "2026-11-03"}, wantFrom: time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC), wantWords: "since 2026-11-03"},
		{name: "since and an explicit days clash", o: options{Days: 7, DaysSet: true, Since: "2026-11-03"}, wantErr: "not both"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			from, words, err := window(tc.o, now)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("window(%+v) error = %v, want %q", tc.o, err, tc.wantErr)
				}
				return
			}
			if err != nil || !from.Equal(tc.wantFrom) || words != tc.wantWords {
				t.Errorf("window(%+v) = %v, %q, %v; want %v, %q", tc.o, from, words, err, tc.wantFrom, tc.wantWords)
			}
		})
	}
}
