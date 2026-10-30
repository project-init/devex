package slos

import (
	_ "embed"
	"fmt"
	"html/template"
	"io"
	"math"
	"slices"
	"strconv"
	"strings"
)

//go:embed report.html.tmpl
var htmlSource string

var htmlReport = template.Must(template.New("report").Parse(htmlSource))

type htmlPage struct {
	Group, Scope, Window, Rollup string
	Columns                      []htmlColumn
	Tiles                        []htmlTile
	Requests, Near, Over         string // empty when no row with an SLO had traffic
	Total                        *htmlSplit
	Sections                     []htmlSection
}

type htmlColumn struct {
	Name, Tip string
	// Extra columns are not in narrowColumns, so they hide until the page's toggle shows every column.
	Extra bool
}

type htmlTile struct {
	Label, Class, Info string
	Count              int
}

// htmlSection holds the rows of one verdict, in sort order.
type htmlSection struct {
	Label, Class, Info string
	Open               bool
	Flagged            int // LOW DATA rows whose numbers alone read worse than HEALTHY
	Rows               []htmlRow
}

// htmlSplit draws the split column as a bar, with each share as its width, and the panel that opens over the bar.
type htmlSplit struct {
	Under, Near, Over float64 // percent
	Shares            []htmlShare
	Of                string // the requests the counts are out of; empty when they were not counted
}

// htmlShare is one line of the panel: the range, its share, and about how many requests that is.
type htmlShare struct {
	Class, Range, Pct, Count string
}

type htmlRow struct {
	Class string
	// Reads is the raw verdict of a LOW DATA row whose numbers alone call for a look, so it can still stand out.
	Reads, ReadsClass string
	Cells             []htmlCell
}

type htmlCell struct {
	Text  string
	Class string // "near" or "over" colors the cell
	Extra bool
	Split *htmlSplit // set on the split column of a row with shares
}

// sectionOrder is most urgent first, the order sortResults puts the rows in.
var sectionOrder = []verdict{breaching, atRisk, watch, healthy, noSLO, lowData, noData}

func verdictClass(v verdict) string {
	return [...]string{"no-data", "low-data", "no-slo", "healthy", "watch", "at-risk", "breaching"}[v]
}

// writeHTML writes one self-contained page with every row in rs, which must be sorted.
func writeHTML(w io.Writer, rs []result, info reportInfo) error {
	return htmlReport.Execute(w, buildPage(rs, info))
}

// columnTips explain the columns whose name alone does not say how they are worked out.
func columnTips(rollup string) map[string]string {
	return map[string]string{
		"TYPICAL P95": fmt.Sprintf("In a normal %s window, 95 of every 100 requests were this fast or faster.", rollup),
		"P95 WORST":   "The same, for the slowest 5% of windows.",
		splitColumn:   "Out of every 100 requests: how many took under 75% of the SLO, 75-100%, and the SLO or longer. Estimated.",
		"TO SLO":      fmt.Sprintf("When the p95 reaches the SLO if it keeps climbing. Empty if it isn't climbing, or it's over %.0f days away.", forecastDays),
		"APP":         "The app that reports the RPC's metrics. A group's RPCs can come from several apps, and \"-\" means no app reports this one.",
	}
}

// verdictInfo says what puts a row under a verdict in words, with the lines the scoring uses. A p95 at or over a line
// is the same as 5 of every 100 requests at or over it, so the text counts requests and never says p95 for that.
func verdictInfo(v verdict, info reportInfo) string {
	near, watchN, breachN, when := pct0(nearRatio), per100(watchShare), per100(breachShare), period(info.Window)
	switch v {
	case breaching:
		return fmt.Sprintf("Too slow now. In a normal %s window, %s or more of every 100 requests take longer than the SLO. Or that's true across all requests %s.", info.Rollup, breachN, when)
	case atRisk:
		return fmt.Sprintf("Close to the SLO all the time. In a normal %s window, %s or more of every 100 requests take at least %s of the SLO. Or the p95 is climbing and will reach the SLO within %.0f days.", info.Rollup, watchN, near, riskDays)
	case watch:
		return fmt.Sprintf("Close to the SLO in bursts. Normal windows are fine, but across all requests %s, %s or more of every 100 took at least %s of the SLO.", when, watchN, near)
	case healthy:
		return fmt.Sprintf("Fewer than %s of every 100 requests took at least %s of the SLO, in normal windows and across all requests %s.", watchN, near, when)
	case noSLO:
		return "No response time SLO is set, so there's nothing to measure against. The action suggests one."
	case lowData:
		return fmt.Sprintf("Under %.0f requests %s, too few to trust. If the numbers alone look worse than HEALTHY, a second label says so.", info.MinRequests, when)
	}
	return fmt.Sprintf("No requests %s, or no app reports the metric at all. Check the RPC is instrumented, still gets traffic, and that the scope is right.", when)
}

func pct0(v float64) string {
	return strconv.FormatFloat(v*100, 'f', 0, 64) + "%"
}

// per100 is a share as a count out of every 100 requests.
func per100(v float64) string {
	return strconv.FormatFloat(v*100, 'f', -1, 64)
}

// period puts the report's window in a sentence: "last 30d" reads "in the last 30 days", and "since 2026-11-03" stays.
func period(window string) string {
	if days, ok := strings.CutPrefix(window, "last "); ok {
		return "in the last " + strings.TrimSuffix(days, "d") + " days"
	}
	return window
}

// groupLabel names the group and the apps that report it, e.g. "gigapi (gigapi, cubby)".
func groupLabel(info reportInfo) string {
	if len(info.Prefixes) == 0 {
		return info.Group
	}
	return info.Group + " (" + strings.Join(info.Prefixes, ", ") + ")"
}

// buildPage gives each verdict a section, and HEALTHY, LOW DATA, and NO DATA start folded.
func buildPage(rs []result, info reportInfo) htmlPage {
	page := htmlPage{Group: groupLabel(info), Scope: info.Scope, Window: info.Window, Rollup: info.Rollup}
	tips := columnTips(info.Rollup)
	for _, c := range columns {
		page.Columns = append(page.Columns, htmlColumn{Name: c, Tip: tips[c], Extra: !slices.Contains(narrowColumns, c)})
	}
	if requests, near, over := totals(rs); requests > 0 {
		page.Requests = strconv.FormatFloat(requests, 'f', 0, 64)
		page.Near, page.Over = pct(near/requests), pct(over/requests)
		page.Total = newSplit(1-near/requests, (near-over)/requests, over/requests, requests,
			[3]string{"under 75% of their SLO", "75-100% of their SLO", "their SLO or longer"})
	}
	for _, v := range sectionOrder {
		sec := htmlSection{Label: v.String(), Class: verdictClass(v), Info: verdictInfo(v, info), Open: v != healthy && v != lowData && v != noData}
		for _, r := range rs {
			if r.Verdict == v {
				sec.Rows = append(sec.Rows, htmlRowFor(r))
				if v == lowData && !quiet(r) {
					sec.Flagged++
				}
			}
		}
		page.Tiles = append(page.Tiles, htmlTile{Label: sec.Label, Class: sec.Class, Info: sec.Info, Count: len(sec.Rows)})
		if len(sec.Rows) > 0 {
			page.Sections = append(page.Sections, sec)
		}
	}
	return page
}

func htmlRowFor(r result) htmlRow {
	out := htmlRow{Class: verdictClass(r.Verdict)}
	if r.Verdict == lowData && !quiet(r) {
		out.Reads, out.ReadsClass = r.Raw.String(), verdictClass(r.Raw)
	}
	for i, text := range row(r) {
		c := htmlCell{Text: text, Class: cellClass(r, columns[i]), Extra: !slices.Contains(narrowColumns, columns[i])}
		if columns[i] == splitColumn && text != "" {
			under, near, over := split(r)
			requests := r.Requests
			if !r.Counted {
				requests = 0
			}
			line, slo := ms(r.Endpoint.SLO*nearRatio), ms(r.Endpoint.SLO)
			c.Split = newSplit(under, near, over, requests, [3]string{"under " + line, line + "-" + slo, slo + " or longer"})
		}
		out.Cells = append(out.Cells, c)
	}
	return out
}

// newSplit builds the bar and its panel from the three shares. requests is 0 when they were not counted, and the
// panel then shows the shares alone.
func newSplit(under, near, over, requests float64, ranges [3]string) *htmlSplit {
	s := &htmlSplit{Under: under * 100, Near: near * 100, Over: over * 100}
	for i, share := range []float64{under, near, over} {
		sh := htmlShare{Class: [...]string{"u", "n", "o"}[i], Range: ranges[i], Pct: pct(share)}
		if requests > 0 {
			// The shares are estimates, so their counts round to three figures. The total is a real count.
			sh.Count = "≈ " + thousands(roundFigures(share*requests, 3))
		}
		s.Shares = append(s.Shares, sh)
	}
	if requests > 0 {
		s.Of = thousands(requests)
	}
	return s
}

// roundFigures rounds v to n significant figures: 2113456 to 2 figures is 2100000.
func roundFigures(v float64, n int) float64 {
	if v <= 0 {
		return 0
	}
	scale := math.Pow(10, math.Floor(math.Log10(v))-float64(n-1))
	return math.Round(v/scale) * scale
}

// thousands writes a whole number with commas: 2250657 is 2,250,657.
func thousands(v float64) string {
	s := strconv.FormatFloat(math.Round(v), 'f', 0, 64)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

// cellClass colors a number by the line it crossed: over the SLO, or at 75% of it or more. The split column draws its
// own colors.
func cellClass(r result, col string) string {
	if r.Buckets == 0 || r.Endpoint.SLO == 0 {
		return ""
	}
	var over, near bool
	switch col {
	case "TYPICAL P95", "P95 WORST":
		v := r.P95
		if col == "P95 WORST" {
			v = r.P95Worst
		}
		over, near = v >= r.Endpoint.SLO, v >= r.Endpoint.SLO*nearRatio
	case "HEADROOM":
		over, near = r.Headroom <= 0, r.Headroom <= 1-nearRatio
	case "TO SLO":
		over, near = r.HasTrend && r.DaysToRed == 0, r.trendingToRed()
	default:
		return ""
	}
	switch {
	case over:
		return "over"
	case near:
		return "near"
	}
	return ""
}
