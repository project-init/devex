package slos

import (
	"cmp"
	"encoding/csv"
	"fmt"
	"io"
	"math"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"
)

var formats = []string{"table", "md", "csv", "html"}

// splitColumn holds the share of requests under 75% of the SLO, from 75% up to it, and at or over it.
const splitColumn = "<75% / 75-100% / >=SLO"

// APP is last, so the csv column arithmetic below keeps working; narrowColumns puts it where it reads best.
var columns = []string{
	"VERDICT", "ENDPOINT", "SLO", "MEDIAN", "TYPICAL P95", "P95 WORST", "HEADROOM", splitColumn, "TO SLO", "TREND",
	"REQUESTS", "FAILED", "ACTION", "APP",
}

// narrowColumns are the ones a risk call needs. The rest explain a verdict, so they show with --wide, in CSV, and
// behind the HTML page's toggle.
var narrowColumns = []string{"VERDICT", "ENDPOINT", "APP", "SLO", "TYPICAL P95", splitColumn, "TO SLO", "REQUESTS", "ACTION"}

// csvColumns split the split column in three, so a spreadsheet can sort on each share.
var csvColumns = slices.Concat(columns[:7], []string{"<75% SLO", "75-100% SLO", ">=SLO"}, columns[8:])

type reportInfo struct {
	Group string
	// Prefixes are the apps that report the group's RPCs, e.g. [gigapi]. A group served by several apps has several.
	Prefixes []string
	Scope    string // e.g. "environment:production"
	Window   string // e.g. "last 30d" or "since 2026-11-03"
	Rollup   string
	// MinRequests is --min-requests, which the HTML page names in what LOW DATA means.
	MinRequests float64
	All         bool // include HEALTHY, NO DATA, and quiet LOW DATA rows
	Wide        bool // show every column in the table and markdown
}

// sortResults puts the most urgent first: by verdict, then by the raw verdict (which orders the LOW DATA rows), then
// by the share over the SLO, then by the share near it, then by the least headroom. The name breaks a tie, so the
// order does not hang on the order Datadog answered in.
func sortResults(rs []result) {
	slices.SortFunc(rs, func(a, b result) int {
		return cmp.Or(
			cmp.Compare(b.Verdict, a.Verdict),
			cmp.Compare(b.Raw, a.Raw),
			cmp.Compare(b.RedShare, a.RedShare),
			cmp.Compare(b.NearShare, a.NearShare),
			cmp.Compare(a.Headroom, b.Headroom),
			cmp.Compare(a.Endpoint.Name(), b.Endpoint.Name()),
		)
	})
}

func writeReport(w io.Writer, format string, rs []result, info reportInfo) error {
	sortResults(rs)
	shown := rs
	if !info.All {
		shown = slices.DeleteFunc(slices.Clone(rs), quiet)
	}
	cols := narrowColumns
	if info.Wide {
		cols = columns
	}

	switch format {
	case "csv":
		// CSV is for a spreadsheet, so it always holds every row and no summary.
		cw := csv.NewWriter(w)
		if err := cw.Write(csvColumns); err != nil {
			return err
		}
		for _, r := range rs {
			if err := cw.Write(csvRow(r)); err != nil {
				return err
			}
		}
		cw.Flush()
		return cw.Error()
	case "html":
		return writeHTML(w, rs, info)
	case "md":
		_, _ = fmt.Fprintf(w, "| %s |\n|%s\n", strings.Join(cols, " | "), strings.Repeat("---|", len(cols)))
		for _, r := range shown {
			_, _ = fmt.Fprintf(w, "| %s |\n", strings.Join(pick(cols, row(r)), " | "))
		}
		_, err := fmt.Fprintf(w, "\n%s\n", summary(rs, info))
		return err
	case "table":
		_, _ = fmt.Fprintln(w, "Risk Assessment Table:")
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		_, _ = fmt.Fprintln(tw, strings.Join(cols, "\t"))
		for _, r := range shown {
			_, _ = fmt.Fprintln(tw, strings.Join(pick(cols, row(r)), "\t"))
		}
		if err := tw.Flush(); err != nil {
			return err
		}
		_, err := fmt.Fprintf(w, "\n%s\n", summary(rs, info))
		return err
	default:
		return fmt.Errorf("unknown format %q: use %s", format, strings.Join(formats, ", "))
	}
}

// pick takes the cells for cols out of a full row.
func pick(cols, cells []string) []string {
	out := make([]string, len(cols))
	for i, c := range cols {
		out[i] = cells[slices.Index(columns, c)]
	}
	return out
}

// quiet rows need no look: HEALTHY, NO DATA, and a LOW DATA row whose numbers alone read HEALTHY.
func quiet(r result) bool {
	switch r.Verdict {
	case healthy, noData:
		return true
	case lowData:
		return r.Raw <= healthy
	}
	return false
}

func row(r result) []string {
	out := []string{r.Verdict.String(), r.Endpoint.Name(), ms(r.Endpoint.SLO)}
	if r.Endpoint.SLO == 0 {
		out[2] = "-"
	}
	if r.Buckets == 0 {
		return append(out, "", "", "", "", "", "", "", "", "", actionText(r), app(r))
	}
	if r.Verdict == noSLO {
		return append(out, ms(r.Median), ms(r.P95), ms(r.P95Worst), "", "", "", "",
			strconv.FormatFloat(r.Requests, 'f', 0, 64), failed(r), actionText(r), app(r))
	}
	under, near, over := split(r)
	return append(out,
		ms(r.Median),
		ms(r.P95),
		ms(r.P95Worst),
		pct(r.Headroom),
		fmt.Sprintf("%s / %s / %s%%", share(under), share(near), share(over)),
		toSLO(r),
		trend(r),
		strconv.FormatFloat(r.Requests, 'f', 0, 64),
		failed(r),
		actionText(r),
		app(r),
	)
}

// app names the app that reports the RPC. It is empty when none does, which is what makes the row NO DATA.
func app(r result) string {
	if r.Endpoint.Prefix == "" {
		return "-"
	}
	return r.Endpoint.Prefix
}

func csvRow(r result) []string {
	cells := row(r)
	shares := []string{"", "", ""}
	if r.Buckets > 0 && r.Verdict != noSLO {
		under, near, over := split(r)
		shares = []string{pct(under), pct(near), pct(over)}
	}
	return slices.Concat(cells[:7], shares, cells[8:])
}

// split is the share of requests under 75% of the SLO, from 75% up to the SLO, and at or over it.
func split(r result) (under, near, over float64) {
	return 1 - r.NearShare, r.NearShare - r.RedShare, r.RedShare
}

// share is a percentage with no sign, to one decimal.
func share(v float64) string {
	return strconv.FormatFloat(v*100, 'f', 1, 64)
}

func ms(v float64) string {
	return strconv.FormatFloat(v, 'f', 0, 64) + "ms"
}

func pct(v float64) string {
	return strconv.FormatFloat(v*100, 'f', 1, 64) + "%"
}

// toSLO is when the daily p95 trend reaches the SLO. It is empty when the trend falls, holds, or gets there past
// forecastDays, so a filled cell always needs a look.
func toSLO(r result) string {
	switch {
	case !r.HasTrend || r.DaysToRed < 0 || r.DaysToRed > forecastDays:
		return ""
	case r.DaysToRed == 0:
		return "now"
	}
	return fmt.Sprintf("in %.0fd", math.Ceil(r.DaysToRed))
}

func trend(r result) string {
	switch {
	case !r.HasTrend:
		return "-"
	case math.Abs(r.Slope) < 0.05: // would print as +0.0 or -0.0
		return "flat"
	}
	return fmt.Sprintf("%+.1fms/day", r.Slope)
}

// failed shows the failure count and rate. Failures get no verdict of their own: the verdict is about latency.
func failed(r result) string {
	rate := r.failRate()
	if math.IsNaN(rate) {
		return strconv.FormatFloat(r.Failed, 'f', 0, 64)
	}
	return fmt.Sprintf("%.0f (%.2f%%)", r.Failed, rate*100)
}

type action int

const (
	actNone    action = iota // healthy, and the SLO fits
	actTighten               // healthy with room to spare: the SLO could be lower
	actWait                  // too few requests to act on
	actCheck                 // no traffic at all
	actSetSLO                // no SLO to score against
	actWatch                 // some requests run near the SLO
	actTrend                 // the daily p95 climbs toward the SLO
	actLook                  // the typical bucket runs near the SLO
	actFix                   // the typical bucket runs over the SLO, or too many requests do
)

// advise picks what to do about a row from its verdict and the numbers behind it.
func advise(r result) action {
	switch r.Verdict {
	case breaching:
		return actFix
	case atRisk:
		if r.P95 >= r.Endpoint.SLO*nearRatio {
			return actLook
		}
		return actTrend
	case watch:
		return actWatch
	case healthy:
		if r.CanTight {
			return actTighten
		}
		return actNone
	case lowData:
		return actWait
	case noSLO:
		return actSetSLO
	}
	return actCheck
}

// actionText says what to do about a row, with the numbers that call for it.
func actionText(r result) string {
	var s string
	switch advise(r) {
	case actFix:
		s = fmt.Sprintf("Fix now: the typical p95 is %s of the SLO, and %s of requests ran over it. Speed up the slow path, or raise the SLO to %s or more.",
			pct(r.P95/r.Endpoint.SLO), pct(r.RedShare), ms(roundUp(r.P95Worst)))
	case actLook:
		s = fmt.Sprintf("Look before it pages: the typical p95 is %s of the SLO, so it has little room left.",
			pct(r.P95/r.Endpoint.SLO))
	case actTrend:
		// The typical p95 is the middle of the window, so it trails a climbing RPC: the forecast starts from today.
		if r.DaysToRed == 0 {
			s = fmt.Sprintf("Find what changed: the daily p95 is ~%s today, already at the %s SLO, and climbs %.1fms/day.",
				ms(r.TrendNow), ms(r.Endpoint.SLO), r.Slope)
		} else {
			s = fmt.Sprintf("Find what changed: the daily p95 is ~%s today and climbs %.1fms/day, reaching the %s SLO %s.",
				ms(r.TrendNow), r.Slope, ms(r.Endpoint.SLO), toSLO(r))
		}
	case actWatch:
		s = fmt.Sprintf("Keep an eye on it: %s of requests ran at 75%% of the SLO or more.", pct(r.NearShare))
	case actTighten:
		s = fmt.Sprintf("No action. The SLO could drop to %s.", ms(suggestedSLO(r.P95Worst)))
	case actNone:
		s = "No action."
	case actWait:
		return fmt.Sprintf("Too few requests (%.0f) to act on; the numbers read %s. Rerun with more --days.", r.Requests, r.Raw)
	case actSetSLO:
		if r.Buckets == 0 {
			return "Give the RPC a response time SLO. It had no traffic in the window."
		}
		return fmt.Sprintf("Give the RPC a response time SLO: %s keeps P95 WORST under 75%% of it.", ms(suggestedSLO(r.P95Worst)))
	case actCheck:
		if r.Endpoint.Prefix == "" {
			return "No app reports this RPC's metrics. Instrument it, or drop its x-slo-* comments if it is gone."
		}
		return "No requests in the window. Check the endpoint still gets traffic, and that the scope is right."
	}
	if r.FewBuckets {
		s += fmt.Sprintf(" Noisy: %d busy buckets.", r.Buckets)
	}
	return s
}

// suggestedSLO is the lowest SLO that keeps p95Worst under its 75% line, rounded up to 10ms.
func suggestedSLO(p95Worst float64) float64 {
	return roundUp(p95Worst / nearRatio)
}

func roundUp(v float64) float64 {
	return math.Ceil(v/10) * 10
}

// totals sums the requests of every row with an SLO, and how many of them ran near it and over it.
func totals(rs []result) (requests, near, over float64) {
	for _, r := range rs {
		if r.Verdict == noSLO || r.Buckets == 0 {
			continue
		}
		requests += r.Requests
		near += r.Requests * r.NearShare
		over += r.Requests * r.RedShare
	}
	return requests, near, over
}

func summary(rs []result, info reportInfo) string {
	counts := map[verdict]int{}
	tight := 0
	for _, r := range rs {
		counts[r.Verdict]++
		if r.CanTight {
			tight++
		}
	}
	where := info.Group
	if len(info.Prefixes) > 0 {
		where += " (" + strings.Join(info.Prefixes, ", ") + ")"
	}
	s := fmt.Sprintf("%d endpoints of %s in %s, %s, %s buckets: %d breaching, %d at risk, %d watch, %d healthy (%d could tighten their SLO), %d low data, %d no SLO, %d no data.",
		len(rs), where, info.Scope, info.Window, info.Rollup,
		counts[breaching], counts[atRisk], counts[watch], counts[healthy], tight, counts[lowData], counts[noSLO], counts[noData])
	if requests, near, over := totals(rs); requests > 0 {
		s += fmt.Sprintf("\nOf %.0f requests, %s ran at 75%% of their SLO or more, and %s over it.", requests, pct(near/requests), pct(over/requests))
	}
	if counts[noData] > 0 {
		s += fmt.Sprintf("\nNO DATA is %d of the %d: no app reported the metric, so the RPC is either uninstrumented or had no traffic.", counts[noData], len(rs))
	}
	if !info.All && counts[healthy]+counts[lowData]+counts[noData] > 0 {
		s += "\nPass --all to list HEALTHY, NO DATA, and quiet LOW DATA rows."
	}
	if !info.Wide {
		s += " Pass --wide for MEDIAN, P95 WORST, HEADROOM, TREND, and FAILED."
	}
	return s
}
