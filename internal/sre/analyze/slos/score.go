package slos

import (
	"cmp"
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
	noSLO // the RPC has no response time SLO, so there is nothing to score it against
	healthy
	watch
	atRisk
	breaching
)

func (v verdict) String() string {
	return [...]string{"NO DATA", "LOW DATA", "NO SLO", "HEALTHY", "WATCH", "AT RISK", "BREACHING"}[v]
}

const (
	// 1 in 20 requests or more took the SLO or longer: the p95 over the whole window is over the SLO, even when the
	// typical bucket's is not.
	breachShare = 0.05
	// A rising trend that reaches the SLO within this many days is at risk even if it has not crossed yet.
	riskDays = 30.0
	// A trend that reaches the SLO further out than this is too far to forecast, so the report leaves it out.
	forecastDays = 90.0
	// A request that took this share of the SLO or longer is near the SLO, and so is a typical p95 this high.
	nearRatio = 0.75
	// 1 in 20 requests or more near the SLO, or over it.
	watchShare = 0.05
	// Fewer busy buckets than one full day of 15m buckets makes the percentiles noisy.
	minBuckets = 96
	// A trend needs this many days that had traffic.
	minTrendDays = 7
	// A healthy RPC whose worst p95 stays under this share of its SLO has room to tighten the SLO.
	tightenRatio = 0.5
)

// result scores one RPC.
type result struct {
	Endpoint endpoint

	// Buckets counts the rollup buckets that had traffic. Datadog returns no point for an empty bucket.
	Buckets  int
	Requests float64
	Failed   float64
	Counted  bool // the count query returned a series, so Requests is a real count
	// Weighted is true when the counts lined up with the p95 buckets. Every number from Median to RedShare then
	// weights each bucket by its requests, so a quiet night bucket counts for little. Without it, every busy bucket
	// counts the same.
	Weighted bool

	Median   float64 // the typical request's bucket median
	P95      float64 // the typical request's bucket p95: half the requests were served in a bucket this slow or slower
	P95Worst float64 // 1 in 20 requests was served in a bucket with a p95 this slow or slower

	Headroom float64 // (SLO - P95Worst) / SLO; negative when over

	// NearShare and RedShare estimate the share of requests that took at least nearRatio of the SLO, and at least the
	// SLO. Datadog keeps only each bucket's median, p95, and max, so tailShare estimates each bucket's share.
	NearShare float64
	RedShare  float64

	HasTrend  bool
	Slope     float64 // change in the daily p95, in the metric's unit per day
	TrendNow  float64 // the daily p95 on the trend line's last day, where the forecast starts from
	DaysToRed float64 // when the trend reaches the SLO; 0 if already there, -1 if never

	Verdict verdict
	// Raw is the verdict the numbers give on their own. It differs from Verdict only on a LOW DATA row.
	Raw        verdict
	FewBuckets bool
	CanTight   bool
}

func (r result) failRate() float64 {
	if r.Requests == 0 {
		return math.NaN()
	}
	return r.Failed / r.Requests
}

func score(ep endpoint, p95, median, maxes, count, failed []point) result {
	r := result{Endpoint: ep, DaysToRed: -1, Requests: sum(count), Failed: sum(failed), Counted: len(count) > 0}
	if len(p95) == 0 {
		r.Verdict = noData
		if ep.SLO == 0 {
			r.Verdict = noSLO
		}
		r.Raw = r.Verdict
		return r
	}

	w := weights(p95, count)
	r.Weighted = w != nil
	r.Buckets = len(p95)
	r.FewBuckets = r.Buckets < minBuckets
	r.P95 = weightedQuantile(p95, w, 0.5)
	r.P95Worst = weightedQuantile(p95, w, 0.95)
	if len(median) > 0 {
		r.Median = weightedQuantile(median, weights(median, count), 0.5)
	}
	if ep.SLO == 0 {
		r.Verdict, r.Raw = noSLO, noSLO
		return r
	}
	r.Headroom = (ep.SLO - r.P95Worst) / ep.SLO
	knots := bucketKnots(p95, median, maxes)
	r.NearShare = shareAtLeast(knots, w, ep.SLO*nearRatio)
	r.RedShare = shareAtLeast(knots, w, ep.SLO)
	r.trend(p95)
	r.classify()
	return r
}

// classify gives the verdict from the typical p95, the shares, and the trend. A monitor already pages on each bucket
// over the SLO, so the verdict asks how the typical bucket runs, not whether one ever crossed.
func (r *result) classify() {
	slo := r.Endpoint.SLO
	switch {
	case r.P95 >= slo, r.RedShare >= breachShare:
		r.Verdict = breaching
	case r.P95 >= slo*nearRatio, r.trendingToRed():
		r.Verdict = atRisk
	case r.NearShare >= watchShare:
		r.Verdict = watch
	default:
		r.Verdict = healthy
	}
	r.Raw = r.Verdict
	r.CanTight = r.Verdict == healthy && !r.FewBuckets && r.P95Worst < r.Endpoint.SLO*tightenRatio
}

func (r result) trendingToRed() bool {
	return r.HasTrend && r.DaysToRed >= 0 && r.DaysToRed <= riskDays
}

// gate marks a row LOW DATA when it served fewer than minRequests requests: one slow call out of a few is not a
// trend. A row with no count series is left alone, because a missing count says nothing about the traffic.
func (r *result) gate(minRequests float64) {
	if r.Verdict == noData || r.Verdict == noSLO || !r.Counted || r.Requests >= minRequests {
		return
	}
	r.Verdict = lowData
	r.CanTight = false
}

// trend fits a line through each day's median p95. A day's median ignores the lone spikes that would swing a fit
// through raw buckets.
func (r *result) trend(p95 []point) {
	byDay := map[time.Time][]float64{}
	for _, p := range p95 {
		day := p.T.UTC().Truncate(24 * time.Hour)
		byDay[day] = append(byDay[day], p.V)
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
	r.HasTrend = true
	r.Slope = slope

	now := intercept + slope*xs[len(xs)-1]
	r.TrendNow = now
	switch {
	case now >= r.Endpoint.SLO:
		r.DaysToRed = 0
	case slope > 0:
		r.DaysToRed = (r.Endpoint.SLO - now) / slope
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

// weightedQuantile reads q from pts, each weighted by w; a nil w falls back to quantile. Each sorted point sits at
// the middle of its share of the total weight, and q interpolates between those midpoints, clamped to the first
// and last point. A point with no weight is dropped.
func weightedQuantile(pts []point, w []float64, q float64) float64 {
	if w == nil {
		vals := values(pts)
		slices.Sort(vals)
		return quantile(vals, q)
	}
	type vw struct{ v, w float64 }
	var s []vw
	var total float64
	for i, p := range pts {
		if w[i] > 0 {
			s = append(s, vw{p.V, w[i]})
			total += w[i]
		}
	}
	if len(s) == 0 {
		return math.NaN()
	}
	slices.SortFunc(s, func(a, b vw) int { return cmp.Compare(a.v, b.v) })
	var cum, prevMid float64
	for i, e := range s {
		mid := (cum + e.w/2) / total
		cum += e.w
		if mid >= q {
			if i == 0 {
				return e.v
			}
			return s[i-1].v + (e.v-s[i-1].v)*(q-prevMid)/(mid-prevMid)
		}
		prevMid = mid
	}
	return s[len(s)-1].v
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

// weights gives each p95 bucket the request count of the count bucket at the same time. It returns nil, which
// weights every bucket the same, when there are no counts to use.
func weights(p95, count []point) []float64 {
	countAt := byTime(count)
	w := make([]float64, len(p95))
	var total float64
	for i, p := range p95 {
		w[i] = countAt[p.T.Unix()]
		total += w[i]
	}
	if total == 0 {
		return nil
	}
	return w
}

// knot is one point a bucket's latencies are known at: tail of the bucket's requests took v or longer.
type knot struct{ v, tail float64 }

// bucketKnots gives each p95 bucket the points its latencies are known at: half its requests took the median or
// longer, 1 in 20 the p95, and none more than the max. A bucket with no median or max has no knot for it. Averaging
// percentiles across hosts can leave them out of order, so each knot is raised to the one before it.
func bucketKnots(p95, median, maxes []point) [][]knot {
	medianAt, maxAt := byTime(median), byTime(maxes)
	out := make([][]knot, len(p95))
	for i, p := range p95 {
		ks := []knot{{0, 1}}
		v := 0.0
		if m, ok := medianAt[p.T.Unix()]; ok {
			v = math.Max(v, m)
			ks = append(ks, knot{v, 0.5})
		}
		v = math.Max(v, p.V)
		ks = append(ks, knot{v, 0.05})
		if m, ok := maxAt[p.T.Unix()]; ok && m > v {
			ks = append(ks, knot{m, 0})
		}
		out[i] = ks
	}
	return out
}

// tailShare estimates the share of a bucket's requests that took line or longer. Between two knots it assumes the
// requests spread evenly; past the last knot it is 0. A line on a knot takes that knot's share, so a line on the p95
// is 1 in 20.
func tailShare(ks []knot, line float64) float64 {
	for i, k := range ks {
		if k.v < line {
			continue
		}
		if k.v == line || i == 0 {
			return k.tail
		}
		prev := ks[i-1]
		return prev.tail + (k.tail-prev.tail)*(line-prev.v)/(k.v-prev.v)
	}
	return 0
}

// shareAtLeast is the estimated share of requests that took line or longer, each bucket weighted by w. A nil w
// weights every bucket as 1.
func shareAtLeast(buckets [][]knot, w []float64, line float64) float64 {
	var hit, total float64
	for i, ks := range buckets {
		wi := 1.0
		if w != nil {
			wi = w[i]
		}
		total += wi
		hit += wi * tailShare(ks, line)
	}
	return hit / total
}

func byTime(pts []point) map[int64]float64 {
	out := make(map[int64]float64, len(pts))
	for _, p := range pts {
		out[p.T.Unix()] = p.V
	}
	return out
}

func values(pts []point) []float64 {
	out := make([]float64, len(pts))
	for i, p := range pts {
		out[i] = p.V
	}
	return out
}

func sum(pts []point) float64 {
	var s float64
	for _, p := range pts {
		s += p.V
	}
	return s
}
