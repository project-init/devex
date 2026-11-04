package slos

import (
	"maps"
	"math"
	"slices"
	"time"
)

type verdict int

// Ordered from least to most urgent, so a report sorts on the value.
const (
	noData verdict = iota
	lowData
	healthy
	watch
	atRisk
	breaching
)

func (v verdict) String() string {
	return [...]string{"NO DATA", "LOW DATA", "HEALTHY", "WATCH", "AT RISK", "BREACHING"}[v]
}

const (
	// A burn this fast or faster spends most of the error budget over the window.
	riskBurn = 0.75
	// A slice at this share of the threshold or more is near it, and so is a typical slice this high.
	nearRatio = 0.75
	// A rising trend that reaches the threshold within this many days is at risk even if it has not crossed yet.
	riskDays = 30.0
	// A trend that reaches the threshold further out than this is too far to forecast, so the report leaves it out.
	forecastDays = 90.0
	// A trend needs this many days that had data.
	minTrendDays = 7
	// A healthy SLO whose worst slices stay under this share of the threshold has room to tighten it.
	tightenRatio = 0.5
)

// result scores one SLO over its window.
type result struct {
	SLO slo

	// HasBudget is true when the SLO had slices (or events) to count against its target. Good and Total count the
	// good ones and all of them; a slice with no data counts as neither.
	HasBudget   bool
	Good, Total float64
	// Burn is how fast the error budget is spent: the bad share over the share the target allows. 1 spends exactly
	// the budget over the window, 2 spends it in half the window. BudgetLeft is 1 - Burn, negative once overspent.
	Burn, BudgetLeft float64

	// The rest reads a Timeslices SLO's slice values against its threshold.
	Typical  float64 // the median slice
	Worst    float64 // 1 in 20 slices was this high or higher
	Headroom float64 // (threshold - Worst) / threshold; negative when over
	// Near and Over are the shares of slices at nearRatio of the threshold up to it, and at or over it.
	Near, Over float64

	HasTrend  bool
	Slope     float64 // change in the daily median, in the indicator's unit per day
	TrendNow  float64 // the daily median on the trend line's last day, where the forecast starts from
	DaysToRed float64 // when the trend reaches the threshold; 0 if already there, -1 if never

	Verdict verdict
	// Raw is the verdict the numbers give on their own. It differs from Verdict only on a LOW DATA row.
	Raw      verdict
	CanTight bool
}

// score scores one SLO from the series its source read. An SLO with fewer than a day of slices, or fewer than
// minEvents events, is LOW DATA: a few bad ones out of a handful are not a trend.
func score(s slo, ser series, minEvents float64) result {
	r := result{SLO: s, DaysToRed: -1}
	if s.Budgeting == occurrences {
		r.setBudget(sum(ser.Good), sum(ser.Total))
	} else {
		r.readSlices(ser.Value)
	}
	if !r.HasBudget {
		return r // NO DATA
	}
	r.classify()
	few := r.Total < float64(sliceCount(day, s.Slice))
	if s.Budgeting == occurrences {
		few = r.Total < minEvents
	}
	if few {
		r.Verdict, r.CanTight = lowData, false
	}
	return r
}

// setBudget fills in the error budget from good out of total, against the SLO's target.
func (r *result) setBudget(good, total float64) {
	if total <= 0 {
		return
	}
	r.HasBudget, r.Good, r.Total = true, good, total
	r.Burn = (total - good) / total / (1 - r.SLO.Target)
	r.BudgetLeft = 1 - r.Burn
}

// readSlices counts a slice good when its value is under the threshold, and reads how the slices sit against it.
func (r *result) readSlices(values []point) {
	if len(values) == 0 {
		return
	}
	threshold := r.SLO.Threshold
	sorted := make([]float64, len(values))
	var good, near float64
	for i, p := range values {
		sorted[i] = p.V
		switch {
		case p.V < threshold*nearRatio:
			good++
		case p.V < threshold:
			good++
			near++
		}
	}
	slices.Sort(sorted)
	n := float64(len(values))
	r.setBudget(good, n)
	r.Near, r.Over = near/n, (n-good)/n
	r.Typical, r.Worst = quantile(sorted, 0.5), quantile(sorted, 0.95)
	r.Headroom = (threshold - r.Worst) / threshold
	r.trend(values)
}

// classify gives the verdict: a spent or nearly spent budget first, then, for a threshold, a trend that is about to
// cross it and a typical slice that runs near it.
func (r *result) classify() {
	r.Verdict = healthy
	switch {
	case r.Burn >= 1:
		r.Verdict = breaching
	case r.Burn >= riskBurn, r.trendingToRed():
		r.Verdict = atRisk
	case r.SLO.Budgeting == timeslices && r.Typical >= r.SLO.Threshold*nearRatio:
		r.Verdict = watch
	}
	r.Raw = r.Verdict
	r.CanTight = r.Verdict == healthy && r.SLO.Budgeting == timeslices && r.Worst < r.SLO.Threshold*tightenRatio
}

func (r result) trendingToRed() bool {
	return r.HasTrend && r.DaysToRed >= 0 && r.DaysToRed <= riskDays
}

// sliceCount is how many slices of size slice fit in d.
func sliceCount(d, slice time.Duration) int {
	if slice <= 0 {
		return 0
	}
	return int(d / slice)
}

// trend fits a line through each day's median slice. A day's median ignores the lone spikes that would swing a fit
// through raw slices.
func (r *result) trend(values []point) {
	byDay := map[time.Time][]float64{}
	for _, p := range values {
		d := p.T.UTC().Truncate(day)
		byDay[d] = append(byDay[d], p.V)
	}
	if len(byDay) < minTrendDays {
		return
	}
	days := slices.SortedFunc(maps.Keys(byDay), time.Time.Compare)

	xs := make([]float64, len(days))
	ys := make([]float64, len(days))
	for i, d := range days {
		v := byDay[d]
		slices.Sort(v)
		xs[i] = d.Sub(days[0]).Hours() / 24
		ys[i] = quantile(v, 0.5)
	}
	slope, intercept := fitLine(xs, ys)
	r.HasTrend, r.Slope = true, slope

	now := intercept + slope*xs[len(xs)-1]
	r.TrendNow = now
	switch {
	case now >= r.SLO.Threshold:
		r.DaysToRed = 0
	case slope > 0:
		r.DaysToRed = (r.SLO.Threshold - now) / slope
	}
}

// fitLine is an ordinary least squares fit of y = intercept + slope*x.
func fitLine(xs, ys []float64) (slope, intercept float64) {
	n := float64(len(xs))
	var sx, sy, sxx, sxy float64
	for i := range xs {
		sx += xs[i]
		sy += ys[i]
		sxx += xs[i] * xs[i]
		sxy += xs[i] * ys[i]
	}
	den := n*sxx - sx*sx
	if den == 0 {
		return 0, sy / n
	}
	slope = (n*sxy - sx*sy) / den
	return slope, (sy - slope*sx) / n
}

// quantile reads q from sorted values, interpolating between neighbours.
func quantile(sorted []float64, q float64) float64 {
	if len(sorted) == 0 {
		return math.NaN()
	}
	pos := q * float64(len(sorted)-1)
	lo := int(math.Floor(pos))
	hi := int(math.Ceil(pos))
	return sorted[lo] + (sorted[hi]-sorted[lo])*(pos-float64(lo))
}

func sum(pts []point) float64 {
	var s float64
	for _, p := range pts {
		s += p.V
	}
	return s
}
