package slos

import (
	"context"
	"io"
	"slices"
	"strings"
	"testing"
	"time"
)

// fakeQuerier records each request and answers each query through answer, matched on the full query string. A
// request of more than maxQueries times out, when maxQueries is set.
type fakeQuerier struct {
	requests   []request
	answer     func(q string) []point
	maxQueries int
}

type request struct {
	queries  []string
	from, to time.Time
}

func (f *fakeQuerier) query(_ context.Context, qs []string, from, to time.Time) ([][]point, error) {
	f.requests = append(f.requests, request{queries: qs, from: from, to: to})
	if f.maxQueries > 0 && len(qs) > f.maxQueries {
		return nil, errTimeout
	}
	out := make([][]point, len(qs))
	for i, q := range qs {
		out[i] = f.answer(q)
	}
	return out, nil
}

var quietProgress = progress{w: io.Discard}

// thresholdSLO is a 15m Timeslices SLO on query q over window.
func thresholdSLO(q string, window time.Duration) slo {
	return slo{ID: q, Budgeting: timeslices, Threshold: 100, Target: 0.95, Window: window, Slice: 15 * time.Minute, Indicator: indicator{Query: q}}
}

// TestFetchAndScoreReadsEachSLOOverItsOwnWindow asks for each SLO over its own window ending at, at most batch SLOs
// and one window per request, and scores each SLO on its own answer.
func TestFetchAndScoreReadsEachSLOOverItsOwnWindow(t *testing.T) {
	at := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
	slos := []slo{
		thresholdSLO("month-a", 30*day), thresholdSLO("week", 7*day), thresholdSLO("month-b", 30*day), thresholdSLO("month-c", 30*day),
	}
	bad := slicesOf(repeat(twoDays, 500)...)
	f := &fakeQuerier{answer: func(q string) []point {
		if strings.HasPrefix(q, "month-b") {
			return bad
		}
		return nil
	}}

	got, err := fetchAndScore(context.Background(), datadogSource{dd: f}, slos, at, 2, 0, quietProgress)
	if err != nil {
		t.Fatalf("fetchAndScore: %v", err)
	}

	var asked []string
	for _, r := range f.requests {
		if len(r.queries) > 2 {
			t.Errorf("a request carried %d SLOs, want at most the batch of 2", len(r.queries))
		}
		if !r.to.Equal(at) {
			t.Errorf("a request ends at %v, want at %v", r.to, at)
		}
		for _, q := range r.queries {
			window := 30 * day
			if strings.HasPrefix(q, "week") {
				window = 7 * day
			}
			if !r.from.Equal(at.Add(-window)) {
				t.Errorf("%s was read from %v, want its own window back from at: %v", q, r.from, at.Add(-window))
			}
			asked = append(asked, strings.SplitN(q, ".", 2)[0])
		}
	}
	slices.Sort(asked)
	if want := []string{"month-a", "month-b", "month-c", "week"}; !slices.Equal(asked, want) {
		t.Errorf("asked %v, want each SLO once: %v", asked, want)
	}
	for _, r := range got {
		if want := map[bool]verdict{true: breaching, false: noData}[r.SLO.ID == "month-b"]; r.Verdict != want {
			t.Errorf("%s = %v, want %v from its own answer", r.SLO.ID, r.Verdict, want)
		}
	}
}

// TestFetchAndScoreSplitsATimedOutBatch asks again in halves when a request times out, down to one SLO, and still
// scores every SLO once.
func TestFetchAndScoreSplitsATimedOutBatch(t *testing.T) {
	var slos []slo
	for _, q := range []string{"a", "b", "c", "d", "e"} {
		slos = append(slos, thresholdSLO(q, 30*day))
	}
	f := &fakeQuerier{answer: func(string) []point { return nil }, maxQueries: 1}

	got, err := fetchAndScore(context.Background(), datadogSource{dd: f}, slos, time.Now(), 5, 0, quietProgress)
	if err != nil {
		t.Fatalf("fetchAndScore: %v", err)
	}
	if len(got) != len(slos) {
		t.Errorf("scored %d SLOs, want %d", len(got), len(slos))
	}
}
