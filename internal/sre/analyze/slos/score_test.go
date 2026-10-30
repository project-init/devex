package slos

import (
	"math"
	"testing"
	"time"
)

// latency is a Timeslices SLO of 15m slices under 100, 95% good over 30 days.
var latency = slo{ID: "latency", Budgeting: timeslices, Threshold: 100, Target: 0.95, Window: 30 * day, Slice: 15 * time.Minute}

// slicesOf gives each value its own 15m slice, from a fixed start.
func slicesOf(values ...float64) []point {
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	out := make([]point, len(values))
	for i, v := range values {
		out[i] = point{T: start.Add(time.Duration(i) * 15 * time.Minute), V: v}
	}
	return out
}

// repeat is n slices of v.
func repeat(n int, v float64) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = v
	}
	return out
}

// twoDays is two days of 15m slices: enough to score, too few for a trend.
const twoDays = 192

// TestTimeslicesVerdict reads the budget first, then how the slices sit against the threshold.
func TestTimeslicesVerdict(t *testing.T) {
	mixed := func(bad, near int) []float64 {
		v := repeat(twoDays-bad-near, 10)
		v = append(v, repeat(near, 80)...)
		return append(v, repeat(bad, 100)...)
	}
	cases := []struct {
		name   string
		values []float64
		want   verdict
	}{
		{name: "more bad slices than the target allows is breaching", values: mixed(12, 0), want: breaching}, // 6.25% bad, burn 1.25
		{name: "a slice at the threshold counts as bad", values: mixed(10, 0), want: breaching},              // 5.2% bad
		{name: "a burn of 0.75 or more is at risk", values: mixed(8, 0), want: atRisk},                       // 4.2% bad, burn 0.83
		{name: "a typical slice at 75% of the threshold is watch", values: append(repeat(60, 10), repeat(132, 75)...), want: watch},
		{name: "a few slow slices with the budget intact is healthy", values: mixed(2, 20), want: healthy},
		{name: "under a day of slices is low data", values: repeat(95, 100), want: lowData},
		{name: "no slices is no data", values: nil, want: noData},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := score(latency, series{Value: slicesOf(tc.values...)}, 0).Verdict; got != tc.want {
				t.Errorf("verdict = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestLowDataKeepsWhatTheNumbersRead marks too little data LOW DATA, and keeps the verdict it would have had, so the
// report can still flag one that reads BREACHING.
func TestLowDataKeepsWhatTheNumbersRead(t *testing.T) {
	r := score(latency, series{Value: slicesOf(repeat(10, 500)...)}, 0)
	if r.Verdict != lowData || r.Raw != breaching {
		t.Errorf("verdict = %v (reads %v), want LOW DATA that reads BREACHING", r.Verdict, r.Raw)
	}
}

// TestBudget is the bad share over the share the target allows, from the slices or the events.
func TestBudget(t *testing.T) {
	// 3 of 100 slices bad against a 95% target spends 60% of the budget.
	r := score(latency, series{Value: slicesOf(append(repeat(97, 10), repeat(3, 200)...)...)}, 0)
	if r.Good != 97 || r.Total != 100 || math.Abs(r.Burn-0.6) > 1e-9 || math.Abs(r.BudgetLeft-0.4) > 1e-9 {
		t.Errorf("timeslices: good %v of %v, burn %v, left %v; want 97 of 100, 0.6, 0.4", r.Good, r.Total, r.Burn, r.BudgetLeft)
	}

	ratio := slo{ID: "ratio", Budgeting: occurrences, Target: 0.99, Window: 30 * day, Slice: time.Hour}
	r = score(ratio, series{Good: slicesOf(490, 495), Total: slicesOf(500, 500)}, 100)
	if r.Good != 985 || r.Total != 1000 || math.Abs(r.Burn-1.5) > 1e-9 || r.Verdict != breaching {
		t.Errorf("occurrences: good %v of %v, burn %v, %v; want 985 of 1000, 1.5, BREACHING", r.Good, r.Total, r.Burn, r.Verdict)
	}
	if r := score(ratio, series{Good: slicesOf(50), Total: slicesOf(50)}, 100); r.Verdict != lowData {
		t.Errorf("50 events under --min-events 100 = %v, want LOW DATA", r.Verdict)
	}
	if r := score(ratio, series{}, 100); r.Verdict != noData {
		t.Errorf("no events = %v, want NO DATA", r.Verdict)
	}
}

// TestSliceShares counts each slice once: under 75% of the threshold, from 75% up to it, or at it and over.
func TestSliceShares(t *testing.T) {
	values := slices3(70, 20, 10)
	r := score(latency, series{Value: slicesOf(values...)}, 0)
	under, near, over := split(r)
	if math.Abs(under-0.7) > 1e-9 || math.Abs(near-0.2) > 1e-9 || math.Abs(over-0.1) > 1e-9 {
		t.Errorf("split = %v / %v / %v, want 0.7 / 0.2 / 0.1", under, near, over)
	}
}

// slices3 is under, near, and over slices of 10, 80, and 100 against a threshold of 100, in that order.
func slices3(under, near, over int) []float64 {
	return append(append(repeat(under, 10), repeat(near, 80)...), repeat(over, 100)...)
}

// TestTrendReachesTheThreshold forecasts a climbing daily median, and makes it AT RISK only when it gets there within
// riskDays.
func TestTrendReachesTheThreshold(t *testing.T) {
	climb := func(perDay float64) []float64 {
		var v []float64
		for d := range 10 {
			v = append(v, repeat(96, 30+perDay*float64(d))...)
		}
		return v
	}
	cases := []struct {
		name       string
		perDay     float64
		wantRisk   bool
		wantInDays float64
	}{
		{name: "today at 66, climbing 4 a day, reaches 100 in 8.5 days", perDay: 4, wantRisk: true, wantInDays: 8.5},
		{name: "today at 39, climbing 1 a day, is 61 days out", perDay: 1, wantRisk: false, wantInDays: 61},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := score(latency, series{Value: slicesOf(climb(tc.perDay)...)}, 0)
			if !r.HasTrend || math.Abs(r.DaysToRed-tc.wantInDays) > 1e-6 || (r.Verdict == atRisk) != tc.wantRisk {
				t.Errorf("trend %v, in %v days, %v; want in %v days, at risk %v", r.HasTrend, r.DaysToRed, r.Verdict, tc.wantInDays, tc.wantRisk)
			}
		})
	}
	if r := score(latency, series{Value: slicesOf(repeat(6*96, 30)...)}, 0); r.HasTrend {
		t.Error("6 days of data gave a trend, want minTrendDays")
	}
}

// TestCanTighten offers a lower threshold only on a healthy SLO whose worst slices stay under half of it.
func TestCanTighten(t *testing.T) {
	if r := score(latency, series{Value: slicesOf(repeat(twoDays, 49)...)}, 0); !r.CanTight {
		t.Error("worst slice at 49 of 100 cannot tighten, want it can")
	}
	if r := score(latency, series{Value: slicesOf(repeat(twoDays, 50)...)}, 0); r.CanTight {
		t.Error("worst slice at 50 of 100 can tighten, want it cannot")
	}
	if r := score(latency, series{Value: slicesOf(repeat(10, 10)...)}, 0); r.CanTight {
		t.Error("a LOW DATA SLO can tighten, want it cannot")
	}
}
