package slos

import (
	"math"
	"testing"
	"time"
)

var (
	t0      = time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	quarter = 15 * time.Minute
	ep400   = endpoint{Method: "/acme.orders.v1.OrdersService/GetOrder", SLO: 400}
)

// buckets spreads vals over 15m buckets from t0, so 96 values fill one day.
func buckets(vals ...float64) []point {
	pts := make([]point, len(vals))
	for i, v := range vals {
		pts[i] = point{T: t0.Add(time.Duration(i) * quarter), V: v}
	}
	return pts
}

// daily puts one bucket at noon on each day, so each day's median is the value given.
func daily(vals ...float64) []point {
	pts := make([]point, len(vals))
	for i, v := range vals {
		pts[i] = point{T: t0.AddDate(0, 0, i).Add(12 * time.Hour), V: v}
	}
	return pts
}

// TestVerdictThresholds starts each verdict exactly at its line: the typical p95 on the SLO or its 75% line, or a
// share of requests on its share.
func TestVerdictThresholds(t *testing.T) {
	slo := ep400.SLO
	cases := []struct {
		name           string
		p95, red, near float64
		want           verdict
	}{
		{name: "a typical p95 on the SLO is breaching", p95: slo, want: breaching},
		{name: "a typical p95 just under the SLO is at risk", p95: slo - 1e-9, want: atRisk},
		{name: "a typical p95 on the 75% line is at risk", p95: slo * nearRatio, want: atRisk},
		{name: "a typical p95 just under the 75% line is healthy", p95: slo*nearRatio - 1e-9, want: healthy},
		{name: "exactly the breach share over the SLO is breaching with a low p95", red: breachShare, near: breachShare, want: breaching},
		{name: "just under the breach share is only watch", red: breachShare - 1e-9, near: breachShare, want: watch},
		{name: "exactly the watch share near the SLO is watch", near: watchShare, want: watch},
		{name: "just under the watch share is healthy", near: watchShare - 1e-9, want: healthy},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := result{Endpoint: ep400, DaysToRed: -1, P95: tc.p95, RedShare: tc.red, NearShare: tc.near}
			r.classify()
			if r.Verdict != tc.want {
				t.Errorf("verdict = %v, want %v (typical p95 %v, over share %v, near share %v)", r.Verdict, tc.want, r.P95, r.RedShare, r.NearShare)
			}
		})
	}
}

// TestOverShareCountsSlowRequestsOnly counts only a bucket's slow requests as over the SLO, not every request in a
// bucket whose p95 reached it.
func TestOverShareCountsSlowRequestsOnly(t *testing.T) {
	slo := ep400.SLO
	// One busy bucket whose p95 sits on the SLO: by definition 1 in 20 of its requests took that long.
	r := score(ep400, buckets(slo), buckets(100), buckets(2*slo), buckets(1000), nil)
	if math.Abs(r.RedShare-0.05) > 1e-12 || r.Verdict != breaching {
		t.Errorf("over share, verdict = %v, %v; want 0.05, BREACHING", r.RedShare, r.Verdict)
	}
	// A bucket whose slowest request was under the SLO had none over it.
	if r := score(ep400, buckets(200), buckets(100), buckets(slo-1), buckets(1000), nil); r.RedShare != 0 {
		t.Errorf("over share with max under the SLO = %v, want 0", r.RedShare)
	}
}

// TestTailShare estimates a bucket's share at a line from its median, p95, and max.
func TestTailShare(t *testing.T) {
	tail := func(median, p95, max, line float64) float64 {
		return tailShare(bucketKnots(buckets(p95), buckets(median), buckets(max))[0], line)
	}
	cases := []struct {
		name                   string
		median, p95, max, line float64
		want                   float64
	}{
		{name: "a line on the median is half", median: 100, p95: 300, max: 500, line: 100, want: 0.5},
		{name: "a line on the p95 is 1 in 20", median: 100, p95: 300, max: 500, line: 300, want: 0.05},
		{name: "halfway from the p95 to the max is half of 1 in 20", median: 100, p95: 300, max: 500, line: 400, want: 0.025},
		{name: "a line on or past the max is none", median: 100, p95: 300, max: 500, line: 500, want: 0},
		{name: "a p95 under the median is lifted to it", median: 400, p95: 300, max: 500, line: 450, want: 0.025},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tail(tc.median, tc.p95, tc.max, tc.line); math.Abs(got-tc.want) > 1e-12 {
				t.Errorf("tail(median %v, p95 %v, max %v) at %v = %v, want %v", tc.median, tc.p95, tc.max, tc.line, got, tc.want)
			}
		})
	}
}

// TestSharesWeighByRequests counts requests, not buckets: slow buckets that served few requests weigh little in the
// shares and in P95 WORST.
func TestSharesWeighByRequests(t *testing.T) {
	// 10 slow buckets with 1 request each, then 90 fast buckets with 100 each.
	vals, counts := make([]float64, 100), make([]float64, 100)
	for i := range vals {
		vals[i], counts[i] = 100, 100
		if i < 10 {
			vals[i], counts[i] = ep400.SLO, 1
		}
	}
	weighted := score(ep400, buckets(vals...), nil, nil, buckets(counts...), nil)
	even := score(ep400, buckets(vals...), nil, nil, nil, nil)
	// The slow buckets are 10 of 9010 requests, but 10 of 100 buckets.
	if want := even.RedShare * 100 / 9010; math.Abs(weighted.RedShare-want) > 1e-12 || even.RedShare == 0 {
		t.Errorf("over share by requests = %v, want %v: by buckets it is %v", weighted.RedShare, want, even.RedShare)
	}
	if weighted.P95Worst != 100 || even.P95Worst != ep400.SLO {
		t.Errorf("P95 WORST by requests, by buckets = %v, %v; want 100, %v", weighted.P95Worst, even.P95Worst, ep400.SLO)
	}
}

// TestTrendThreshold makes a rising RPC at risk when its daily p95 reaches the SLO within riskDays, and not later.
func TestTrendThreshold(t *testing.T) {
	// Rising 10ms a day; the last day sets how far it is from the 400ms SLO.
	rising := func(last float64) []point {
		vals := make([]float64, minTrendDays)
		for i := range vals {
			vals[i] = last - float64(len(vals)-1-i)*10
		}
		return daily(vals...)
	}
	cases := []struct {
		name string
		last float64
		want verdict
	}{
		{name: "reaching the SLO in exactly riskDays is at risk", last: ep400.SLO - riskDays*10, want: atRisk},
		{name: "reaching the SLO one day later is healthy", last: ep400.SLO - (riskDays+1)*10, want: healthy},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := score(ep400, rising(tc.last), nil, nil, nil, nil)
			if r.Verdict != tc.want {
				t.Errorf("verdict = %v, want %v (days to SLO %v)", r.Verdict, tc.want, r.DaysToRed)
			}
			// The forecast starts from the last day, not from the typical p95 in the middle of the window.
			if math.Abs(r.TrendNow-tc.last) > 1e-9 {
				t.Errorf("trend today = %v, want the last day's %v", r.TrendNow, tc.last)
			}
		})
	}
}

// TestGateAtMinRequests marks a row LOW DATA below minRequests and keeps what its numbers read.
func TestGateAtMinRequests(t *testing.T) {
	const minRequests = 100
	slow := buckets(2 * ep400.SLO)
	cases := []struct {
		name     string
		ep       endpoint
		requests float64
		want     verdict
	}{
		{name: "exactly minRequests keeps the verdict", ep: ep400, requests: minRequests, want: breaching},
		{name: "one under minRequests is low data", ep: ep400, requests: minRequests - 1, want: lowData},
		{name: "an RPC with no SLO stays NO SLO", ep: endpoint{Method: "/acme.orders.v1.OrdersService/NoSLO"}, requests: 1, want: noSLO},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := score(tc.ep, slow, nil, nil, buckets(tc.requests), nil)
			raw := r.Verdict
			r.gate(minRequests)
			if r.Verdict != tc.want || r.Raw != raw {
				t.Errorf("after gate(%d) with %v requests: verdict, raw = %v, %v; want %v, %v", minRequests, tc.requests, r.Verdict, r.Raw, tc.want, raw)
			}
		})
	}
}

// TestNoSLOStillReportsTraffic scores an RPC with no SLO as NO SLO, with its p95 filled in so the report can suggest
// one.
func TestNoSLOStillReportsTraffic(t *testing.T) {
	r := score(endpoint{Method: "/acme.inventory.v1.InventoryService/ReserveItem"}, buckets(120, 120), nil, nil, buckets(50, 50), nil)
	if r.Verdict != noSLO || r.P95 != 120 || r.Requests != 100 {
		t.Errorf("verdict, p95, requests = %v, %v, %v; want NO SLO, 120, 100", r.Verdict, r.P95, r.Requests)
	}
	// With no traffic too, it still needs an SLO more than a look at its traffic.
	if r := score(endpoint{Method: "/acme.inventory.v1.InventoryService/ReserveItem"}, nil, nil, nil, nil, nil); r.Verdict != noSLO {
		t.Errorf("no SLO and no traffic: verdict = %v, want NO SLO", r.Verdict)
	}
}
