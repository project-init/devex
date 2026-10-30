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
	"time"
)

var formats = []string{"table", "md", "csv", "html"}

// splitColumn holds the share of slices under 75% of the threshold, from 75% up to it, and at or over it.
const splitColumn = "<75% / 75-100% / >=THRESHOLD"

var columns = []string{
	"VERDICT", "SLO", "THRESHOLD", "TYPICAL", "WORST", "HEADROOM", splitColumn, "TO THRESHOLD", "TREND",
	"GOOD", "WINDOW", "TARGET", "BUDGET LEFT", "BURN", "ACTION",
}

// narrowColumns are the ones a risk call needs. The rest explain a verdict, so they show with --wide, in CSV, and
// behind the HTML page's toggle.
var narrowColumns = []string{
	"VERDICT", "SLO", "THRESHOLD", "TYPICAL", splitColumn, "TO THRESHOLD", "BUDGET LEFT", "BURN", "ACTION",
}

// splitAt is where splitColumn sits in columns. CSV splits it in three, so a spreadsheet can sort on each share.
var (
	splitAt    = slices.Index(columns, splitColumn)
	csvColumns = slices.Concat(columns[:splitAt], []string{"<75% THRESHOLD", "75-100% THRESHOLD", ">=THRESHOLD"}, columns[splitAt+1:])
)

type reportInfo struct {
	Group string
	At    time.Time // where every window ends
	// MinEvents is --min-events, which the HTML page names in what LOW DATA means.
	MinEvents float64
	All       bool // include HEALTHY, NO DATA, and quiet LOW DATA rows
	Wide      bool // show every column in the table and markdown
}

// sortResults puts the most urgent first: by verdict, then by the raw verdict (which orders the LOW DATA rows), then
// by the fastest burn, the most slices over and near the threshold, and the least headroom. The name breaks a tie,
// so the order does not hang on the order the source answered in.
func sortResults(rs []result) {
	slices.SortFunc(rs, func(a, b result) int {
		return cmp.Or(
			cmp.Compare(b.Verdict, a.Verdict),
			cmp.Compare(b.Raw, a.Raw),
			cmp.Compare(b.Burn, a.Burn),
			cmp.Compare(b.Over, a.Over),
			cmp.Compare(b.Near, a.Near),
			cmp.Compare(a.Headroom, b.Headroom),
			cmp.Compare(a.SLO.Name(), b.SLO.Name()),
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

// hasSlices is true for a Timeslices SLO that had data, the rows whose slice columns are filled in.
func hasSlices(r result) bool {
	return r.SLO.Budgeting == timeslices && r.HasBudget
}

func row(r result) []string {
	threshold, mid := "-", make([]string, 6)
	if r.SLO.Budgeting == timeslices {
		threshold = num(r.SLO.Threshold)
	}
	if hasSlices(r) {
		under, near, over := split(r)
		mid = []string{
			num(r.Typical),
			num(r.Worst),
			pct(r.Headroom),
			fmt.Sprintf("%s / %s / %s%%", share(under), share(near), share(over)),
			toThreshold(r),
			trend(r),
		}
	}
	return slices.Concat([]string{r.Verdict.String(), r.SLO.Name(), threshold}, mid,
		[]string{good(r), windowText(r.SLO.Window)}, budget(r), []string{actionText(r)})
}

// good is how many slices (or events) were good out of all of them.
func good(r result) string {
	if !r.HasBudget {
		return ""
	}
	return fmt.Sprintf("%s of %s %s", thousands(r.Good), thousands(r.Total), counted(r.SLO))
}

func counted(s slo) string {
	if s.Budgeting == occurrences {
		return "events"
	}
	return "slices"
}

// budget is the TARGET, BUDGET LEFT, and BURN cells. BUDGET LEFT and BURN are empty when there was nothing to count.
func budget(r result) []string {
	if !r.HasBudget {
		return []string{pct(r.SLO.Target), "", ""}
	}
	return []string{pct(r.SLO.Target), pct(r.BudgetLeft), burn(r.Burn)}
}

func burn(v float64) string {
	return strconv.FormatFloat(v, 'f', 2, 64) + "x"
}

func csvRow(r result) []string {
	cells := row(r)
	shares := []string{"", "", ""}
	if hasSlices(r) {
		under, near, over := split(r)
		shares = []string{pct(under), pct(near), pct(over)}
	}
	return slices.Concat(cells[:splitAt], shares, cells[splitAt+1:])
}

// split is the row's three splitColumn shares.
func split(r result) (under, near, over float64) {
	return 1 - r.Near - r.Over, r.Near, r.Over
}

// share is a percentage with no sign, to one decimal.
func share(v float64) string {
	return strconv.FormatFloat(v*100, 'f', 1, 64)
}

func pct(v float64) string {
	return strconv.FormatFloat(v*100, 'f', 1, 64) + "%"
}

// num writes a value in the indicator's own unit: whole from 100 up, and to three figures under it.
func num(v float64) string {
	if math.Abs(v) >= 100 {
		return strconv.FormatFloat(v, 'f', 0, 64)
	}
	return strconv.FormatFloat(v, 'g', 3, 64)
}

// toThreshold is when the daily trend reaches the threshold. It is empty when the trend falls, holds, or gets there
// past forecastDays, so a filled cell always needs a look.
func toThreshold(r result) string {
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
	case math.Abs(r.Slope) < r.SLO.Threshold/1000: // under 0.1% of the threshold a day
		return "flat"
	}
	if r.Slope > 0 {
		return "+" + num(r.Slope) + "/day"
	}
	return num(r.Slope) + "/day"
}

type action int

const (
	actNone    action = iota // healthy, and the threshold fits
	actTighten               // healthy with room to spare: the threshold could be lower
	actWait                  // too little data to act on
	actCheck                 // no data at all
	actWatch                 // the typical slice runs near the threshold
	actTrend                 // the daily trend climbs toward the threshold
	actBurning               // the error budget is nearly spent
	actSpent                 // the error budget is spent
)

// advise picks what to do about a row from its verdict and the numbers behind it.
func advise(r result) action {
	switch r.Verdict {
	case breaching:
		return actSpent
	case atRisk:
		if r.Burn >= riskBurn {
			return actBurning
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
	}
	return actCheck
}

// actionText says what to do about a row, with the numbers that call for it.
func actionText(r result) string {
	switch advise(r) {
	case actSpent:
		return fmt.Sprintf("Fix now: %s of %s were bad, and the %s target allows %s. The error budget is spent (burn %s).",
			pct((r.Total-r.Good)/r.Total), counted(r.SLO), pct(r.SLO.Target), pct(1-r.SLO.Target), burn(r.Burn))
	case actBurning:
		return fmt.Sprintf("Look before the budget runs out: it burns at %s, with %s of it left.", burn(r.Burn), pct(r.BudgetLeft))
	case actTrend:
		// The typical slice is the middle of the window, so it trails a climbing SLO: the forecast starts from today.
		if r.DaysToRed == 0 {
			return fmt.Sprintf("Find what changed: the daily median is ~%s today, already at the %s threshold, and climbs %s/day.",
				num(r.TrendNow), num(r.SLO.Threshold), num(r.Slope))
		}
		return fmt.Sprintf("Find what changed: the daily median is ~%s today and climbs %s/day, reaching the %s threshold %s.",
			num(r.TrendNow), num(r.Slope), num(r.SLO.Threshold), toThreshold(r))
	case actWatch:
		return fmt.Sprintf("Look before it pages: the typical slice is %s of the threshold, so it has little room left.",
			pct(r.Typical/r.SLO.Threshold))
	case actTighten:
		return fmt.Sprintf("No action. The threshold could drop to %s.", num(suggestedThreshold(r.Worst)))
	case actNone:
		return "No action."
	case actWait:
		return fmt.Sprintf("Too few %s (%s) to act on; the numbers read %s. Check again once the window holds more.",
			counted(r.SLO), thousands(r.Total), r.Raw)
	}
	return "No data in the window. Check the SLO's queries, and that what they measure still reports."
}

// suggestedThreshold is the lowest threshold, to two figures, that keeps worst under its 75% line.
func suggestedThreshold(worst float64) float64 {
	v := worst / nearRatio
	if v <= 0 {
		return 0
	}
	scale := math.Pow(10, math.Floor(math.Log10(v))-1)
	return math.Ceil(v/scale) * scale
}

// thousands writes a whole number with commas: 2250657 is 2,250,657.
func thousands(v float64) string {
	s := strconv.FormatFloat(math.Round(v), 'f', 0, 64)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

func summary(rs []result, info reportInfo) string {
	counts := map[verdict]int{}
	tight, spent := 0, 0
	for _, r := range rs {
		counts[r.Verdict]++
		if r.CanTight {
			tight++
		}
		if r.HasBudget && r.Burn >= 1 {
			spent++
		}
	}
	s := fmt.Sprintf("%d SLOs of %s, each over its own window to %s: %d breaching, %d at risk, %d watch, %d healthy (%d could tighten their threshold), %d low data, %d no data.",
		len(rs), info.Group, info.At.UTC().Format(time.RFC3339),
		counts[breaching], counts[atRisk], counts[watch], counts[healthy], tight, counts[lowData], counts[noData])
	if spent > 0 {
		s += fmt.Sprintf("\n%d of the %d spent their error budget over their window.", spent, len(rs))
	}
	if counts[noData] > 0 {
		s += fmt.Sprintf("\nNO DATA is %d of the %d: their queries returned nothing in the window.", counts[noData], len(rs))
	}
	if !info.All && counts[healthy]+counts[lowData]+counts[noData] > 0 {
		s += "\nPass --all to list HEALTHY, NO DATA, and quiet LOW DATA rows."
	}
	if !info.Wide {
		s += " Pass --wide for WORST, HEADROOM, TREND, GOOD, WINDOW, and TARGET."
	}
	return s
}
