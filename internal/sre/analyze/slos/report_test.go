package slos

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"math/rand/v2"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// mixedResults has every verdict, several rows each, with ties on the numbers so every sort key gets used.
func mixedResults() []result {
	rng := rand.New(rand.NewPCG(1, 2))
	var rs []result
	for i := range 60 {
		v := verdict(i % len(sectionOrder))
		r := result{
			SLO:      slo{ID: fmt.Sprintf("slo-%02d", i), Threshold: 200, Target: 0.95},
			Verdict:  v,
			Raw:      v,
			Burn:     float64(rng.IntN(3)) / 2,
			Over:     float64(rng.IntN(3)) / 10,
			Near:     float64(rng.IntN(3)) / 10,
			Headroom: float64(rng.IntN(3)) / 10,
		}
		if v != noData {
			r.HasBudget, r.Good, r.Total = true, 190, 200
		}
		if v == lowData {
			r.Raw = verdict(rng.IntN(int(breaching) + 1))
		}
		rs = append(rs, r)
	}
	return rs
}

// TestSortResults puts the most urgent verdict first, and within one verdict the faster burn first. The order does not
// depend on the order the source answered in.
func TestSortResults(t *testing.T) {
	want := mixedResults()
	sortResults(want)
	for i := 1; i < len(want); i++ {
		a, b := want[i-1], want[i]
		if a.Verdict < b.Verdict || (a.Verdict == b.Verdict && a.Raw == b.Raw && a.Burn < b.Burn) {
			t.Fatalf("row %d (%v, burn %v) sorts above row %d (%v, burn %v)", i-1, a.Verdict, a.Burn, i, b.Verdict, b.Burn)
		}
	}
	rng := rand.New(rand.NewPCG(3, 4))
	for range 5 {
		got := mixedResults()
		rng.Shuffle(len(got), func(i, j int) { got[i], got[j] = got[j], got[i] })
		sortResults(got)
		if !slices.EqualFunc(got, want, func(a, b result) bool { return a.SLO.Name() == b.SLO.Name() }) {
			t.Fatalf("a shuffled input sorts to a different order")
		}
	}
}

// TestActionSaysWhyAtRisk tells a burning budget apart from a trend toward the threshold, and starts the forecast
// from today's median, not the typical slice, which trails a climbing SLO.
func TestActionSaysWhyAtRisk(t *testing.T) {
	burning := result{SLO: latency, Verdict: atRisk, HasBudget: true, Burn: 0.8, BudgetLeft: 0.2}
	if got := actionText(burning); !strings.Contains(got, "0.80x") || !strings.Contains(got, "20.0%") {
		t.Errorf("burning action = %q, want the burn and the budget left", got)
	}
	climbing := result{SLO: slo{Threshold: 800}, Verdict: atRisk, Typical: 469, HasTrend: true, Slope: 12.6, TrendNow: 645, DaysToRed: (800 - 645) / 12.6}
	got := actionText(climbing)
	for _, want := range []string{"645", "12.6/day", "800", "in 13d"} {
		if !strings.Contains(got, want) || strings.Contains(got, "469") {
			t.Errorf("trend action = %q, want today's median and the climb (%s), not the typical slice", got, want)
		}
	}
}

// TestTighten suggests the lowest threshold, to two figures, that keeps the worst slice under its 75% line.
func TestTighten(t *testing.T) {
	r := result{SLO: latency, Verdict: healthy, HasBudget: true, CanTight: true, Worst: 31.2}
	if got := actionText(r); !strings.Contains(got, "drop to 42") {
		t.Errorf("action = %q, want a threshold of 42 (31.2 / 0.75 = 41.6, up to two figures)", got)
	}
}

// TestWriteReportHidesQuietRows leaves HEALTHY, NO DATA, and a LOW DATA row that reads HEALTHY out of the table and
// markdown unless --all is given. CSV always lists every row.
func TestWriteReportHidesQuietRows(t *testing.T) {
	rs := func() []result {
		return []result{
			{SLO: slo{ID: "fine"}, Verdict: healthy},
			{SLO: slo{ID: "slow"}, Verdict: breaching},
			{SLO: slo{ID: "gone"}, Verdict: noData},
			{SLO: slo{ID: "rare"}, Verdict: lowData, Raw: breaching},
			{SLO: slo{ID: "hush"}, Verdict: lowData, Raw: healthy},
		}
	}
	needLook := map[string]bool{"slow": true, "rare": true}
	for _, format := range []string{"table", "md"} {
		for _, all := range []bool{false, true} {
			var buf bytes.Buffer
			if err := writeReport(&buf, format, rs(), reportInfo{All: all}); err != nil {
				t.Fatalf("%s: %v", format, err)
			}
			for _, r := range rs() {
				name := r.SLO.Name()
				if shown := regexp.MustCompile(`\b` + name + `\b`).MatchString(buf.String()); shown != (all || needLook[name]) {
					t.Errorf("%s --all=%v: %s shown = %v, want %v", format, all, name, shown, all || needLook[name])
				}
			}
		}
	}
	rows := csvRecords(t, rs())
	if len(rows) != len(rs())+1 {
		t.Errorf("csv has %d rows, want a header and every SLO", len(rows))
	}
}

func csvRecords(t *testing.T, rs []result) [][]string {
	t.Helper()
	var buf bytes.Buffer
	if err := writeReport(&buf, "csv", rs, reportInfo{}); err != nil {
		t.Fatalf("csv: %v", err)
	}
	recs, err := csv.NewReader(&buf).ReadAll()
	if err != nil {
		t.Fatalf("read csv: %v", err)
	}
	return recs
}

// TestCSVSplitsTheShares gives each share of slices its own column, and the three add up to every slice.
func TestCSVSplitsTheShares(t *testing.T) {
	r := score(latency, series{Value: slicesOf(slices3(70, 20, 10)...)}, 0)
	recs := csvRecords(t, []result{r})
	got := map[string]string{}
	for i, col := range recs[0] {
		got[col] = recs[1][i]
	}
	if got["<75% THRESHOLD"] != "70.0%" || got["75-100% THRESHOLD"] != "20.0%" || got[">=THRESHOLD"] != "10.0%" {
		t.Errorf("shares = %s, %s, %s; want 70.0%%, 20.0%%, 10.0%%", got["<75% THRESHOLD"], got["75-100% THRESHOLD"], got[">=THRESHOLD"])
	}
}

// TestHTMLPage lists every row once, in sort order, under a section per verdict, most urgent first. The urgent
// sections start open and the quiet ones folded, and a number on or past a line is colored.
func TestHTMLPage(t *testing.T) {
	rs := mixedResults()
	sortResults(rs)
	page := buildPage(rs, reportInfo{})

	var sections, rows []string
	folded := map[string]bool{}
	for _, sec := range page.Sections {
		sections = append(sections, sec.Label)
		folded[sec.Label] = !sec.Open
		for _, r := range sec.Rows {
			rows = append(rows, r.Cells[slices.Index(columns, "SLO")].Text)
		}
	}
	if i, j := slices.Index(sections, "BREACHING"), slices.Index(sections, "HEALTHY"); i < 0 || j < 0 || i > j {
		t.Errorf("sections = %v, want BREACHING before HEALTHY", sections)
	}
	for label, want := range map[string]bool{"BREACHING": false, "AT RISK": false, "HEALTHY": true, "LOW DATA": true, "NO DATA": true} {
		if folded[label] != want {
			t.Errorf("section %s folded = %v, want %v", label, folded[label], want)
		}
	}
	var want []string
	for _, r := range rs {
		want = append(want, r.SLO.Name())
	}
	if !slices.Equal(rows, want) {
		t.Errorf("rows\n got %v\nwant %v", rows, want)
	}

	// The typical slice sits exactly on the 75% line and the worst exactly on the threshold: on a line has reached it.
	hot := result{SLO: latency, Verdict: watch, HasBudget: true, Total: 100, Good: 100, Typical: 75, Worst: 100}
	color := map[string]string{}
	for i, c := range htmlRowFor(hot).Cells {
		color[columns[i]] = c.Class
	}
	if color["TYPICAL"] != "near" || color["WORST"] != "over" {
		t.Errorf("TYPICAL on the 75%% line and WORST on the threshold should be near and over; got %v", color)
	}
}

// TestSplitPanelCountsSlices opens a panel over the split bar with each range, its share, and how many slices that is.
func TestSplitPanelCountsSlices(t *testing.T) {
	r := score(slo{ID: "list", Budgeting: timeslices, Threshold: 600, Target: 0.95, Slice: latency.Slice},
		series{Value: slicesOf(append(append(repeat(90, 100), repeat(6, 500)...), repeat(4, 600)...)...)}, 0)
	panel := htmlRowFor(r).Cells[slices.Index(columns, splitColumn)].Split
	want := []htmlShare{
		{Class: "u", Range: "under 450", Pct: "90.0%", Count: "90"},
		{Class: "n", Range: "450-600", Pct: "6.0%", Count: "6"},
		{Class: "o", Range: "600 or more", Pct: "4.0%", Count: "4"},
	}
	if panel == nil || !slices.Equal(panel.Shares, want) || panel.Of != "100" {
		t.Errorf("panel = %+v\nwant %+v of 100", panel, want)
	}
}

// TestToThreshold forecasts only a rising trend that reaches the threshold within forecastDays, so the cell is empty
// unless it needs a look.
func TestToThreshold(t *testing.T) {
	cases := []struct {
		name string
		r    result
		want string
	}{
		{name: "no trend is empty", r: result{DaysToRed: -1}, want: ""},
		{name: "a trend that never gets there is empty", r: result{HasTrend: true, DaysToRed: -1}, want: ""},
		{name: "a trend at the threshold is now", r: result{HasTrend: true, DaysToRed: 0}, want: "now"},
		{name: "part of a day rounds up", r: result{HasTrend: true, DaysToRed: 12.2}, want: "in 13d"},
		{name: "exactly forecastDays is shown", r: result{HasTrend: true, DaysToRed: forecastDays}, want: "in 90d"},
		{name: "past forecastDays is empty", r: result{HasTrend: true, DaysToRed: forecastDays + 0.5}, want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := toThreshold(tc.r); got != tc.want {
				t.Errorf("toThreshold = %q, want %q", got, tc.want)
			}
		})
	} // A slope under 0.1% of the threshold a day would print as +0 or -0.
	if got := trend(result{SLO: slo{Threshold: 500}, HasTrend: true, Slope: -0.4}); got != "flat" {
		t.Errorf("trend of -0.4/day against 500 = %q, want flat", got)
	}
}

// TestWriteHTMLIsSelfContained loads nothing from the network, so the file opens anywhere.
func TestWriteHTMLIsSelfContained(t *testing.T) {
	var buf bytes.Buffer
	if err := writeReport(&buf, "html", mixedResults(), reportInfo{}); err != nil {
		t.Fatalf("html: %v", err)
	}
	if regexp.MustCompile(`(?i)(src|href)\s*=|@import|url\(`).MatchString(buf.String()) {
		t.Error("the page should load nothing from outside the file")
	}
}
