package slos

import (
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

// oneSLO is one Timeslices SLO, of service orders.
const oneSLO = `apiVersion: openslo/v1
kind: SLO
metadata: {name: a}
spec:
  service: orders
  indicator:
    spec:
      thresholdMetric: {metricSource: {type: Datadog, spec: {query: "avg:a{*}"}}}
  timeWindow: [{duration: 30d, isRolling: true}]
  budgetingMethod: Timeslices
  objectives: [{op: lt, value: 500, target: 0.95, timeSliceWindow: 15m}]
`

func readTestdata(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/openslo.yaml")
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// TestParseOpenSLO converts each SLO document of the group into devex's own type, in file order, and skips the
// Service document. A threshold reads its query, value, and time slice; a ratio reads its good and total queries.
func TestParseOpenSLO(t *testing.T) {
	got, err := parseOpenSLO(readTestdata(t), "orders")
	if err != nil {
		t.Fatalf("parseOpenSLO: %v", err)
	}
	want := []slo{
		{ID: "orders-checkout-latency", Display: "Checkout", Service: "orders", Tags: []string{"p0"}, Budgeting: timeslices, Threshold: 800,
			Target: 0.99, Window: 7 * day, Slice: 5 * time.Minute, Indicator: indicator{Query: "avg:acme.checkout.response_time.95percentile{env:production}"}},
		{ID: "orders-nightly-export", Service: "orders", Tags: []string{"p1", "batch"}, Budgeting: occurrences, Target: 0.9, Window: 28 * day,
			Slice: occurrencesSlice, Indicator: indicator{Good: "sum:acme.export.succeeded{env:production}", Total: "sum:acme.export.runs{env:production}"}},
	}
	if !slices.EqualFunc(got, want, func(a, b slo) bool {
		return a.ID == b.ID && a.Display == b.Display && a.Service == b.Service && slices.Equal(a.Tags, b.Tags) &&
			a.Budgeting == b.Budgeting && a.Threshold == b.Threshold && a.Target == b.Target && a.Window == b.Window &&
			a.Slice == b.Slice && a.Indicator == b.Indicator
	}) {
		t.Errorf("SLOs\n got %+v\nwant %+v", got, want)
	}
	if got[0].Name() != "Checkout" || got[1].Name() != "orders-nightly-export" {
		t.Errorf("names = %q, %q; want the displayName when set, and the name otherwise", got[0].Name(), got[1].Name())
	}
}

// TestPickGroup needs --group only when the file holds more than one service, and names them when it is missing or
// unknown.
func TestPickGroup(t *testing.T) {
	if _, err := parseOpenSLO(readTestdata(t), ""); err == nil || !strings.Contains(err.Error(), "inventory, orders") {
		t.Errorf("no group in a file of two services: error = %v, want one that names both", err)
	}
	if _, err := parseOpenSLO(readTestdata(t), "billing"); err == nil || !strings.Contains(err.Error(), "inventory, orders") {
		t.Errorf("an unknown group: error = %v, want one that names both", err)
	}
	if slos, err := parseOpenSLO([]byte(oneSLO), ""); err != nil || len(slos) != 1 {
		t.Errorf("no group in a file of one service = %d SLOs, %v; want its SLO", len(slos), err)
	}
}

// TestFilterByTags keeps the SLOs that carry any of the tags, and every SLO when none is given.
func TestFilterByTags(t *testing.T) {
	slos := []slo{{ID: "a", Tags: []string{"p0"}}, {ID: "b", Tags: []string{"p1", "batch"}}, {ID: "c"}}
	ids := func(ss []slo) []string {
		var out []string
		for _, s := range ss {
			out = append(out, s.ID)
		}
		return out
	}
	if got := ids(filterByTags(slos, []string{"batch", "p0"})); !slices.Equal(got, []string{"a", "b"}) {
		t.Errorf("tags batch,p0 keep %v, want a, b", got)
	}
	if got := ids(filterByTags(slos, nil)); !slices.Equal(got, []string{"a", "b", "c"}) {
		t.Errorf("no tags keep %v, want every SLO", got)
	}
}

// TestParseOpenSLORefusesWhatItDoesNotRead fails on anything outside the subset, with what to change, rather than
// scoring it wrong.
func TestParseOpenSLORefusesWhatItDoesNotRead(t *testing.T) {
	threshold := oneSLO
	cases := []struct {
		name, from, to, want string
	}{
		{name: "another apiVersion", from: "openslo/v1\n", to: "openslo/v2alpha\n", want: "devex reads openslo/v1"},
		{name: "an indicatorRef", from: "  indicator:\n    spec:\n      thresholdMetric: {metricSource: {type: Datadog, spec: {query: \"avg:a{*}\"}}}\n", to: "  indicatorRef: shared\n", want: "inline"},
		{name: "a calendar window", from: "isRolling: true", to: "isRolling: false", want: "rolling"},
		{name: "a month window", from: "duration: 30d", to: "duration: 1M", want: `"1M"`},
		{name: "two objectives", from: "objectives: [{", to: "objectives: [{target: 0.9}, {", want: "2 objectives"},
		{name: "a target over 1", from: "target: 0.95", to: "target: 95", want: "between 0 and 1"},
		{name: "op gt", from: "op: lt", to: "op: gt", want: "op: lt"},
		{name: "another source", from: "type: Datadog", to: "type: Prometheus", want: "devex reads datadog"},
		{name: "a query split by a tag", from: `query: "avg:a{*}"`, to: `query: "avg:a{*} by {host}"`, want: "split by a tag"},
		{name: "a query with its own rollup", from: `query: "avg:a{*}"`, to: `query: "avg:a{*}.rollup(avg, 60)"`, want: "leave them out"},
		{name: "no query", from: `query: "avg:a{*}"`, to: `query: ""`, want: "no metricSource.spec.query"},
		{name: "RatioTimeslices", from: "budgetingMethod: Timeslices", to: "budgetingMethod: RatioTimeslices", want: "Timeslices and Occurrences"},
		{name: "Occurrences without a ratioMetric", from: "budgetingMethod: Timeslices", to: "budgetingMethod: Occurrences", want: "needs a ratioMetric"},
		{name: "a ratio given as bad", from: "thresholdMetric: {metricSource: {type: Datadog, spec: {query: \"avg:a{*}\"}}}\n  timeWindow: [{duration: 30d, isRolling: true}]\n  budgetingMethod: Timeslices", to: "ratioMetric: {good: {metricSource: {type: Datadog, spec: {query: g}}}, bad: {metricSource: {type: Datadog, spec: {query: b}}}, total: {metricSource: {type: Datadog, spec: {query: t}}}}\n  timeWindow: [{duration: 30d, isRolling: true}]\n  budgetingMethod: Occurrences", want: "does not read bad"},
		{name: "no service", from: "service: orders", to: "service: \"\"", want: "spec.service"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !strings.Contains(threshold, tc.from) {
				t.Fatalf("the base SLO has no %q to replace", tc.from)
			}
			_, err := parseOpenSLO([]byte(strings.Replace(threshold, tc.from, tc.to, 1)), "orders")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want one that says %q", err, tc.want)
			}
		})
	}
}
