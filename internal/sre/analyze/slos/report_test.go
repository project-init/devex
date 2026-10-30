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

// mixedResults has every verdict, several rows each, with ties on the shares so every sort key gets used.
func mixedResults() []result {
	rng := rand.New(rand.NewPCG(1, 2))
	var rs []result
	for i := range 60 {
		v := verdict(i % len(sectionOrder))
		r := result{
			Endpoint:  endpoint{Method: fmt.Sprintf("/acme.v1.Service/RPC%02d", i), SLO: 200},
			Verdict:   v,
			Raw:       v,
			RedShare:  float64(rng.IntN(3)) / 10,
			NearShare: float64(rng.IntN(3)) / 10,
			Headroom:  float64(rng.IntN(3)) / 10,
			Requests:  500,
			Buckets:   200,
		}
		switch v {
		case noData:
			r.Buckets = 0
		case lowData:
			r.Raw = verdict(rng.IntN(int(breaching) + 1))
		}
		rs = append(rs, r)
	}
	return rs
}

// TestSortResults puts the most urgent verdict first, and within one verdict the row with more requests over the SLO
// first. The order does not depend on the order Datadog answered in.
func TestSortResults(t *testing.T) {
	want := mixedResults()
	sortResults(want)
	for i := 1; i < len(want); i++ {
		a, b := want[i-1], want[i]
		if a.Verdict < b.Verdict || (a.Verdict == b.Verdict && a.Raw == b.Raw && a.RedShare < b.RedShare) {
			t.Fatalf("row %d (%v, %v over) sorts above row %d (%v, %v over)", i-1, a.Verdict, a.RedShare, i, b.Verdict, b.RedShare)
		}
	}
	rng := rand.New(rand.NewPCG(3, 4))
	for range 5 {
		got := mixedResults()
		rng.Shuffle(len(got), func(i, j int) { got[i], got[j] = got[j], got[i] })
		sortResults(got)
		if !slices.EqualFunc(got, want, func(a, b result) bool { return a.Endpoint.Name() == b.Endpoint.Name() }) {
			t.Fatalf("a shuffled input sorts to a different order")
		}
	}
}

// TestAdvise splits a verdict by why it was given: near the SLO or only trending toward it, and healthy with room to
// tighten or not.
func TestAdvise(t *testing.T) {
	cases := []struct {
		name string
		r    result
		want action
	}{
		{name: "at risk with the typical p95 near the SLO is look", r: result{Endpoint: endpoint{SLO: 400}, Verdict: atRisk, P95: 300}, want: actLook},
		{name: "at risk on the trend alone is find what changed", r: result{Endpoint: endpoint{SLO: 400}, Verdict: atRisk, P95: 299, HasTrend: true}, want: actTrend},
		{name: "healthy with room is tighten", r: result{Verdict: healthy, CanTight: true}, want: actTighten},
		{name: "healthy is no action", r: result{Verdict: healthy}, want: actNone},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := advise(tc.r); got != tc.want {
				t.Errorf("advise(%v) = %d, want %d", tc.r.Verdict, got, tc.want)
			}
		})
	}
}

// TestTotals weighs each RPC's shares by its requests, and leaves out the rows with no SLO.
func TestTotals(t *testing.T) {
	rs := []result{
		{Endpoint: endpoint{SLO: 200}, Verdict: watch, Buckets: 1, Requests: 900, NearShare: 0.5, RedShare: 0.1},
		{Endpoint: endpoint{SLO: 200}, Verdict: healthy, Buckets: 1, Requests: 100},
		{Endpoint: endpoint{}, Verdict: noSLO, Buckets: 1, Requests: 5000},
	}
	// 450 of 1000 requests near, 90 over.
	if p := buildPage(rs, reportInfo{}); p.Requests != "1000" || p.Near != "45.0%" || p.Over != "9.0%" {
		t.Errorf("totals = %s requests, %s near, %s over; want 1000, 45.0%%, 9.0%%", p.Requests, p.Near, p.Over)
	}
}

// TestWriteReportHidesQuietRows leaves HEALTHY, NO DATA, and a LOW DATA row that reads HEALTHY out of the table and
// markdown unless -all is given. CSV always lists every row.
func TestWriteReportHidesQuietRows(t *testing.T) {
	rs := func() []result {
		return []result{
			{Endpoint: endpoint{Method: "/acme.v1.Service/Fine", SLO: 200}, Verdict: healthy, Buckets: 500},
			{Endpoint: endpoint{Method: "/acme.v1.Service/Slow", SLO: 200}, Verdict: breaching, Buckets: 500},
			{Endpoint: endpoint{Method: "/acme.v1.Service/Gone", SLO: 200}, Verdict: noData},
			{Endpoint: endpoint{Method: "/acme.v1.Service/Rare", SLO: 200}, Verdict: lowData, Raw: breaching, Requests: 3, Buckets: 1},
			{Endpoint: endpoint{Method: "/acme.v1.Service/Hush", SLO: 200}, Verdict: lowData, Raw: healthy, Requests: 3, Buckets: 1},
			{Endpoint: endpoint{Method: "/acme.v1.Service/Unset"}, Verdict: noSLO, Buckets: 1},
		}
	}
	// Without --all, only the rows that need a look: a LOW DATA row that reads worse than HEALTHY is one.
	needLook := map[string]bool{"Service.Slow": true, "Service.Rare": true, "Service.Unset": true}
	for _, format := range []string{"table", "md"} {
		for _, all := range []bool{false, true} {
			var buf bytes.Buffer
			if err := writeReport(&buf, format, rs(), reportInfo{All: all}); err != nil {
				t.Fatalf("%s: %v", format, err)
			}
			for _, r := range rs() {
				name := r.Endpoint.Name()
				if shown := strings.Contains(buf.String(), name); shown != (all || needLook[name]) {
					t.Errorf("%s --all=%v: %s shown = %v, want %v", format, all, name, shown, all || needLook[name])
				}
			}
		}
	}
	var buf bytes.Buffer
	if err := writeReport(&buf, "csv", rs(), reportInfo{}); err != nil {
		t.Fatalf("csv: %v", err)
	}
	for _, r := range rs() {
		if !strings.Contains(buf.String(), r.Endpoint.Name()) {
			t.Errorf("csv is missing %s", r.Endpoint.Name())
		}
	}
}

// TestHTMLPage lists every row once, in sort order, under a section per verdict, most urgent first. The urgent
// sections start open and the quiet ones folded, and a number on or past a line is colored.
func TestHTMLPage(t *testing.T) {
	rs := mixedResults()
	// P95 sits exactly on the 75% line and P95 WORST exactly on the SLO: a number on a line has reached it.
	hot := result{Endpoint: endpoint{Method: "/acme.v1.Service/Hot", SLO: 100}, Verdict: breaching, Raw: breaching,
		Buckets: 200, Requests: 900, P95: 75, P95Worst: 100, RedShare: 0.5, NearShare: 0.6}
	rs = append(rs, hot)
	sortResults(rs)
	page := buildPage(rs, reportInfo{})

	var sections, rows []string
	folded := map[string]bool{}
	for _, sec := range page.Sections {
		sections = append(sections, sec.Label)
		folded[sec.Label] = !sec.Open
		for _, r := range sec.Rows {
			rows = append(rows, r.Cells[slices.Index(columns, "ENDPOINT")].Text)
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
		want = append(want, r.Endpoint.Name())
	}
	if !slices.Equal(rows, want) {
		t.Errorf("rows\n got %v\nwant %v", rows, want)
	}

	color := map[string]string{}
	for _, c := range htmlRowFor(hot).Cells[:len(columns)] {
		color[c.Text] = c.Class
	}
	if color["75ms"] != "near" || color["100ms"] != "over" {
		t.Errorf("TYPICAL P95 on the 75%% line and P95 WORST on the SLO should be near and over; got %v", color)
	}
}

// TestTrendActionStartsFromToday names today's p95 next to the climb and the SLO, so the forecast adds up from the
// numbers the row shows. The typical p95 trails a climbing RPC.
func TestTrendActionStartsFromToday(t *testing.T) {
	r := result{Endpoint: endpoint{SLO: 800}, Verdict: atRisk, P95: 469, HasTrend: true, Slope: 12.6, TrendNow: 645, DaysToRed: (800 - 645) / 12.6}
	if got, want := actionText(r), "the daily p95 is ~645ms today and climbs 12.6ms/day, reaching the 800ms SLO in 13d."; !strings.Contains(got, want) {
		t.Errorf("action = %q, want it to say %q", got, want)
	}
	r.TrendNow, r.DaysToRed = 810, 0
	if got, want := actionText(r), "the daily p95 is ~810ms today, already at the 800ms SLO"; !strings.Contains(got, want) {
		t.Errorf("action = %q, want it to say %q", got, want)
	}
}

// TestSplitPanelCountsRequests opens a panel over the split bar with each range in milliseconds, its share, and about
// how many requests that is. A row whose requests were not counted shows the shares alone.
func TestSplitPanelCountsRequests(t *testing.T) {
	r := result{Endpoint: endpoint{Method: "/acme.v1.Service/List", SLO: 600}, Verdict: watch, Buckets: 1, Counted: true,
		Requests: 2250657, NearShare: 0.061, RedShare: 0.028}
	panel := func(r result) *htmlSplit { return htmlRowFor(r).Cells[slices.Index(columns, splitColumn)].Split }
	want := []htmlShare{
		{Class: "u", Range: "under 450ms", Pct: "93.9%", Count: "≈ 2,110,000"},
		{Class: "n", Range: "450ms-600ms", Pct: "3.3%", Count: "≈ 74,300"},
		{Class: "o", Range: "600ms or longer", Pct: "2.8%", Count: "≈ 63,000"},
	}
	if got := panel(r); !slices.Equal(got.Shares, want) || got.Of != "2,250,657" {
		t.Errorf("panel = %+v of %q\nwant %+v of 2,250,657", got.Shares, got.Of, want)
	}
	r.Counted = false
	if got := panel(r); got.Of != "" || got.Shares[0].Count != "" || got.Shares[0].Pct != "93.9%" {
		t.Errorf("panel with no request count = %+v of %q, want the shares alone", got.Shares, got.Of)
	}
}

// TestToSLO forecasts only a rising trend that reaches the SLO within forecastDays, so the cell is empty unless it
// needs a look.
func TestToSLO(t *testing.T) {
	cases := []struct {
		name string
		r    result
		want string
	}{
		{name: "no trend is empty", r: result{DaysToRed: -1}, want: ""},
		{name: "a trend that never reaches the SLO is empty", r: result{HasTrend: true, DaysToRed: -1}, want: ""},
		{name: "a trend at the SLO is now", r: result{HasTrend: true, DaysToRed: 0}, want: "now"},
		{name: "part of a day rounds up", r: result{HasTrend: true, DaysToRed: 12.2}, want: "in 13d"},
		{name: "exactly forecastDays is shown", r: result{HasTrend: true, DaysToRed: forecastDays}, want: "in 90d"},
		{name: "past forecastDays is empty", r: result{HasTrend: true, DaysToRed: forecastDays + 0.5}, want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := toSLO(tc.r); got != tc.want {
				t.Errorf("toSLO = %q, want %q", got, tc.want)
			}
		})
	}
	if got := trend(result{HasTrend: true, Slope: -0.04}); got != "flat" {
		t.Errorf("trend of -0.04ms/day = %q, want flat, not -0.0ms/day", got)
	}
}

// TestCSVSplitsTheShares gives each share its own column, and the three add up to every request.
func TestCSVSplitsTheShares(t *testing.T) {
	rs := []result{{Endpoint: endpoint{Method: "/acme.v1.Service/Hot", SLO: 100}, Verdict: watch, Buckets: 1, NearShare: 0.3, RedShare: 0.1}}
	var buf bytes.Buffer
	if err := writeReport(&buf, "csv", rs, reportInfo{}); err != nil {
		t.Fatalf("csv: %v", err)
	}
	recs, err := csv.NewReader(&buf).ReadAll()
	if err != nil {
		t.Fatalf("read csv: %v", err)
	}
	got := map[string]string{}
	for i, col := range recs[0] {
		got[col] = recs[1][i]
	}
	if got["<75% SLO"] != "70.0%" || got["75-100% SLO"] != "20.0%" || got[">=SLO"] != "10.0%" {
		t.Errorf("shares = %s, %s, %s; want 70.0%%, 20.0%%, 10.0%%", got["<75% SLO"], got["75-100% SLO"], got[">=SLO"])
	}
}

// TestWriteHTMLIsSelfContained loads nothing from the network, so the file opens anywhere.
func TestWriteHTMLIsSelfContained(t *testing.T) {
	rs := []result{{Endpoint: endpoint{Method: "/acme.v1.Service/Hot", SLO: 100}, Verdict: breaching, Buckets: 1}}
	var buf bytes.Buffer
	if err := writeReport(&buf, "html", rs, reportInfo{}); err != nil {
		t.Fatalf("html: %v", err)
	}
	if regexp.MustCompile(`(?i)(src|href)\s*=|@import|url\(`).MatchString(buf.String()) {
		t.Error("the page should load nothing from outside the file")
	}
}
