package slos

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"testing"
	"time"
)

// fakeQuerier records each batch and answers each query through answer, matched on the full query string.
// A batch of more than maxQueries times out, when maxQueries is set.
type fakeQuerier struct {
	batches    [][]string
	answer     func(q string) []point
	maxQueries int
}

func (f *fakeQuerier) query(_ context.Context, qs []string, _, _ time.Time) ([][]point, error) {
	f.batches = append(f.batches, qs)
	if f.maxQueries > 0 && len(qs) > f.maxQueries {
		return nil, errTimeout
	}
	out := make([][]point, len(qs))
	for i, q := range qs {
		out[i] = f.answer(q)
	}
	return out, nil
}

// fakeClient answers queries like fakeQuerier and lists names as the metrics Datadog has seen.
type fakeClient struct {
	*fakeQuerier
	names  []string
	listed int
}

func (f *fakeClient) metricNames(context.Context, time.Time) ([]string, error) {
	f.listed++
	return f.names, nil
}

var quietProgress = progress{w: io.Discard}

// TestFetchAndScore asks for the RPCs in batches, scores an RPC with no p95 data as NO DATA, and gives each RPC the
// numbers of its own queries.
func TestFetchAndScore(t *testing.T) {
	at := time.Unix(1_700_000_000, 0)
	pt := func(v float64) []point { return []point{{T: at, V: v}} }
	eps := []endpoint{
		{Method: "/acme.v1.Service/Quiet", Prefix: "svc", Metric: "quiet", Scope: "*", SLO: 100, Rollup: 15 * time.Minute},
		{Method: "/acme.v1.Service/Slow", Prefix: "svc", Metric: "slow", Scope: "*", SLO: 100, Rollup: 15 * time.Minute},
		{Method: "/acme.v1.Service/Busy", Prefix: "svc", Metric: "busy", Scope: "*", SLO: 100, Rollup: 15 * time.Minute},
	}
	answers := map[string][]point{
		// Slow's p95 is under its SLO, and only its max is over: the max must reach the over share.
		endpointQueries(eps[1])[qP95]:   pt(90),
		endpointQueries(eps[1])[qMax]:   pt(200),
		endpointQueries(eps[1])[qCount]: pt(200),
		endpointQueries(eps[2])[qP95]:   pt(30),
		endpointQueries(eps[2])[qCount]: pt(300),
	}
	f := &fakeQuerier{answer: func(q string) []point { return answers[q] }}

	got, err := fetchAndScore(context.Background(), f, eps, at, at, 2, quietProgress)
	if err != nil {
		t.Fatalf("fetchAndScore: %v", err)
	}
	// No request carries more than the batch, and every query is asked once.
	var asked []string
	for _, b := range f.batches {
		if len(b) > 2*queriesPerEndpoint {
			t.Errorf("a request carried %d queries, want at most 2 RPCs' worth", len(b))
		}
		asked = append(asked, b...)
	}
	var all []string
	for _, ep := range eps {
		q := endpointQueries(ep)
		all = append(all, q[:]...)
	}
	if slices.Sort(asked); !slices.Equal(asked, slices.Sorted(slices.Values(all))) {
		t.Errorf("asked %d queries, want each of the %d once", len(asked), len(all))
	}

	type row struct {
		name      string
		v         verdict
		p95, reqs float64
	}
	var rows []row
	for _, r := range got {
		rows = append(rows, row{r.Endpoint.Name(), r.Verdict, r.P95, r.Requests})
	}
	want := []row{
		{"Service.Quiet", noData, 0, 0},
		{"Service.Slow", atRisk, 90, 200},
		{"Service.Busy", healthy, 30, 300},
	}
	if !slices.Equal(rows, want) {
		t.Errorf("rows\n got %+v\nwant %+v", rows, want)
	}
	if got[1].RedShare == 0 {
		t.Error("Slow's max is over its SLO, but its over share is 0")
	}
}

// TestFetchAndScoreSplitsATimedOutBatch asks again in smaller batches when Datadog times out, and still scores every
// RPC once, in order. One RPC that times out on its own is an error.
func TestFetchAndScoreSplitsATimedOutBatch(t *testing.T) {
	var eps []endpoint
	for i := range 5 {
		eps = append(eps, endpoint{Method: fmt.Sprintf("/acme.v1.Service/RPC%d", i), Prefix: "svc", Metric: fmt.Sprintf("rpc%d", i), Scope: "*", SLO: 100, Rollup: 15 * time.Minute})
	}
	f := &fakeQuerier{answer: func(string) []point { return nil }, maxQueries: 2 * queriesPerEndpoint}
	got, err := fetchAndScore(context.Background(), f, eps, time.Time{}, time.Time{}, 5, quietProgress)
	if err != nil {
		t.Fatalf("fetchAndScore: %v", err)
	}
	var names []string
	for _, r := range got {
		names = append(names, r.Endpoint.Name())
	}
	if want := []string{"Service.RPC0", "Service.RPC1", "Service.RPC2", "Service.RPC3", "Service.RPC4"}; !slices.Equal(names, want) {
		t.Errorf("rows = %v, want %v", names, want)
	}

	f.maxQueries = queriesPerEndpoint - 1
	if _, err := fetchAndScore(context.Background(), f, eps[:1], time.Time{}, time.Time{}, 5, quietProgress); !errors.Is(err, errTimeout) {
		t.Errorf("one RPC that times out: error = %v, want errTimeout", err)
	}
}
