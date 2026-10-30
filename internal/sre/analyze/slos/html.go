package slos

import (
	_ "embed"
	"fmt"
	"html/template"
	"io"
	"slices"
	"strconv"
	"time"
)

//go:embed report.html.tmpl
var htmlSource string

var htmlReport = template.Must(template.New("report").Parse(htmlSource))

type htmlPage struct {
	Group, At string
	Columns   []htmlColumn
	Tiles     []htmlTile
	Sections  []htmlSection
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
	Of                string // the slices the counts are out of
}

// htmlShare is one line of the panel: the range, its share, and how many slices that is.
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
	Split *htmlSplit // set on the split column of a row with slices
}

// sectionOrder is most urgent first, the order sortResults puts the rows in.
var sectionOrder = []verdict{breaching, atRisk, watch, healthy, lowData, noData}

func verdictClass(v verdict) string {
	return [...]string{"no-data", "low-data", "healthy", "watch", "at-risk", "breaching"}[v]
}

// writeHTML writes one self-contained page with every row in rs, which must be sorted.
func writeHTML(w io.Writer, rs []result, info reportInfo) error {
	return htmlReport.Execute(w, buildPage(rs, info))
}

// columnTips explain the columns whose name alone does not say how they are worked out.
var columnTips = map[string]string{
	"THRESHOLD":    "A slice is good when its value is under this, in the indicator's own unit.",
	"TYPICAL":      "The median slice: half the slices in the window were this high or higher.",
	"WORST":        "1 in 20 slices was this high or higher.",
	"HEADROOM":     "How far WORST is under the threshold, as a share of it. Negative when over.",
	splitColumn:    "Out of every 100 slices: how many were under 75% of the threshold, 75-100%, and at it or over.",
	"TO THRESHOLD": fmt.Sprintf("When the daily median reaches the threshold if it keeps climbing. Empty if it isn't climbing, or it's over %.0f days away.", forecastDays),
	"GOOD":         "The good slices (or events) out of all of them. A slice with no data counts as neither.",
	"WINDOW":       "The SLO's own time window, which its error budget covers. It ends where the report does.",
	"TARGET":       "The share of slices (or events) that must be good.",
	"BUDGET LEFT":  "How much of the error budget the target allows is not spent yet, over the SLO's window. Negative once overspent.",
	"BURN":         "How fast the error budget is spent. 1x spends exactly the budget over the window; 2x spends it in half the window.",
}

// verdictInfo says what puts a row under a verdict in words, with the lines the scoring uses.
func verdictInfo(v verdict, info reportInfo) string {
	near := strconv.FormatFloat(nearRatio*100, 'f', 0, 64) + "%"
	switch v {
	case breaching:
		return "The error budget is spent: more slices (or events) were bad over the window than the target allows."
	case atRisk:
		return fmt.Sprintf("The budget burns at %s or faster, so most of it is gone. Or the daily median is climbing and will reach the threshold within %.0f days.", burn(riskBurn), riskDays)
	case watch:
		return fmt.Sprintf("The budget holds, but the typical slice runs at %s of the threshold or more, so it has little room left.", near)
	case healthy:
		return "The budget holds, and the typical slice is well under the threshold."
	case lowData:
		return fmt.Sprintf("Under a day of slices, or under %.0f events, too few to trust. If the numbers alone look worse than HEALTHY, a second label says so.", info.MinEvents)
	}
	return "The SLO's queries returned nothing in the window. Check they are right, and that what they measure still reports."
}

// buildPage gives each verdict a section, and HEALTHY, LOW DATA, and NO DATA start folded.
func buildPage(rs []result, info reportInfo) htmlPage {
	page := htmlPage{Group: info.Group, At: info.At.UTC().Format(time.RFC3339)}
	for _, c := range columns {
		page.Columns = append(page.Columns, htmlColumn{Name: c, Tip: columnTips[c], Extra: !slices.Contains(narrowColumns, c)})
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
		if columns[i] == splitColumn && hasSlices(r) {
			c.Split = newSplit(r)
		}
		out.Cells = append(out.Cells, c)
	}
	return out
}

// newSplit builds the bar and its panel from the row's three shares of slices.
func newSplit(r result) *htmlSplit {
	under, near, over := split(r)
	line, threshold := num(r.SLO.Threshold*nearRatio), num(r.SLO.Threshold)
	ranges := [3]string{"under " + line, line + "-" + threshold, threshold + " or more"}
	s := &htmlSplit{Under: under * 100, Near: near * 100, Over: over * 100, Of: thousands(r.Total)}
	for i, sh := range []float64{under, near, over} {
		s.Shares = append(s.Shares, htmlShare{Class: [...]string{"u", "n", "o"}[i], Range: ranges[i], Pct: pct(sh), Count: thousands(sh * r.Total)})
	}
	return s
}

// cellClass colors a number by the line it crossed: over the threshold, or at 75% of it or more. The split column
// draws its own colors.
func cellClass(r result, col string) string {
	if !r.HasBudget {
		return ""
	}
	if col == "BUDGET LEFT" || col == "BURN" {
		return lineClass(r.Burn >= 1, r.Burn >= riskBurn)
	}
	if r.SLO.Budgeting != timeslices {
		return ""
	}
	t := r.SLO.Threshold
	switch col {
	case "TYPICAL":
		return lineClass(r.Typical >= t, r.Typical >= t*nearRatio)
	case "WORST":
		return lineClass(r.Worst >= t, r.Worst >= t*nearRatio)
	case "HEADROOM":
		return lineClass(r.Headroom <= 0, r.Headroom <= 1-nearRatio)
	case "TO THRESHOLD":
		return lineClass(r.HasTrend && r.DaysToRed == 0, r.trendingToRed())
	}
	return ""
}

func lineClass(over, near bool) string {
	switch {
	case over:
		return "over"
	case near:
		return "near"
	}
	return ""
}
