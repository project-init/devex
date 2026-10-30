package slos

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"slices"
	"strings"
	"time"

	"github.com/project-init/devex/internal/sre/config"
	"github.com/spf13/cobra"
)

// options holds the flags that run reads.
type options struct {
	SLOFile     string
	Group       string
	Tags        []string
	At          string // RFC 3339; empty is now
	DatadogSite string
	Format      string
	All, Wide   bool
	Batch       int
	MinEvents   float64
	Out         string // empty writes to stdout
	DryRun      bool
}

// deps holds what run reaches outside the process, so a test can swap each one.
type deps struct {
	getenv    func(string) string
	newClient func(baseURL, apiKey, appKey string) querier
	stdout    io.Writer
	stderr    io.Writer
	live      bool // stderr is a terminal, so progress can redraw one line
	now       func() time.Time
}

func Command() *cobra.Command {
	var o options
	cmd := &cobra.Command{
		Use:   "slos",
		Short: "Score OpenSLO SLOs against Datadog, with an error budget for each",
		Long: `Read the SLOs in an OpenSLO v1 file and score each one in Datadog over its own time window, ending now.
--group picks the service whose SLOs to score when the file holds more than one, and nothing has to be set up
in the repository the command runs in.

Every SLO gets an error budget from its target, and a verdict from how fast it burns. A Timeslices SLO also
reads its slices against the threshold: how the typical and worst slices sit, and the trend of its daily
median. The report sorts the most urgent first and gives each SLO an action.

Export DD_API_KEY and DD_APP_KEY first. The app key needs the timeseries_query scope. An org on a Datadog
site other than datadoghq.com sets DD_SITE or passes --datadog-site.`,
		Annotations: map[string]string{config.OptionalAnnotation: "true"},
		Args:        cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
			defer stop()
			return run(ctx, o, deps{
				getenv: os.Getenv,
				newClient: func(baseURL, apiKey, appKey string) querier {
					return newDatadogClient(baseURL, apiKey, appKey)
				},
				stdout: cmd.OutOrStdout(),
				stderr: cmd.ErrOrStderr(),
				live:   isTerminal(os.Stderr),
				now:    time.Now,
			})
		},
	}

	cmd.Flags().StringVar(&o.SLOFile, "slo-file", "", "OpenSLO v1 file to score, e.g. slos/openslo.yaml")
	_ = cmd.MarkFlagRequired("slo-file")
	cmd.Flags().StringVar(&o.Group, "group", "", "Score the SLOs of this spec.service, when the file holds more than one, e.g. orders")
	cmd.Flags().StringSliceVar(&o.Tags, "tag", nil, "Score only the SLOs whose tags label has one of these, e.g. p0,p1")
	cmd.Flags().StringVar(&o.At, "at", "", "End every window here instead of now, in RFC 3339, so a report can be reproduced, e.g. 2026-11-02T00:00:00Z")
	cmd.Flags().StringVar(&o.DatadogSite, "datadog-site", "", "Datadog site of the org instead of DD_SITE, or datadoghq.com when neither is set, e.g. datadoghq.eu")
	cmd.Flags().StringVar(&o.Format, "format", "table", "Output format: "+strings.Join(formats, ", "))
	cmd.Flags().StringVar(&o.Out, "out", "", "Write the report to this file, only once the run succeeds, instead of to stdout")
	cmd.Flags().BoolVar(&o.All, "all", false, "Also list HEALTHY, NO DATA, and quiet LOW DATA rows (csv and html always list them)")
	cmd.Flags().BoolVar(&o.Wide, "wide", false, "Show every column in the table and markdown (csv and html always show them)")
	cmd.Flags().IntVar(&o.Batch, "batch", 8, "SLOs per Datadog request")
	cmd.Flags().Float64Var(&o.MinEvents, "min-events", 100, "Events in the window below which an Occurrences SLO is LOW DATA")
	cmd.Flags().BoolVar(&o.DryRun, "dry-run", false, "Print the Datadog queries and exit")

	return cmd
}

func run(ctx context.Context, o options, d deps) error {
	if !slices.Contains(formats, o.Format) {
		return fmt.Errorf("--format %q: use %s", o.Format, strings.Join(formats, ", "))
	}
	if o.Batch < 1 {
		return errors.New("--batch must be at least 1")
	}
	at, err := endOfWindows(o.At, d.now())
	if err != nil {
		return err
	}

	data, err := os.ReadFile(o.SLOFile)
	if err != nil {
		return err
	}
	slos, err := parseOpenSLO(data, o.Group)
	if err != nil {
		return fmt.Errorf("%s: %w", o.SLOFile, err)
	}
	if slos = filterByTags(slos, o.Tags); len(slos) == 0 {
		return fmt.Errorf("no SLO in %s carries %s", o.SLOFile, strings.Join(o.Tags, " or "))
	}

	src := datadogSource{}
	if o.DryRun {
		return printQueries(d.stdout, src, slos)
	}
	apiKey, appKey, err := loadKeys(d.getenv)
	if err != nil {
		return err
	}
	src.dd = d.newClient(datadogBaseURL(o.DatadogSite, d.getenv), apiKey, appKey)

	results, err := fetchAndScore(ctx, src, slos, at, o.Batch, o.MinEvents, progress{w: d.stderr, live: d.live})
	if err != nil {
		return err
	}
	info := reportInfo{Group: slos[0].Service, At: at, MinEvents: o.MinEvents, All: o.All, Wide: o.Wide}
	return writeOutput(d, o.Out, o.Format, results, info)
}

// endOfWindows is --at, or now when it is not set. It must not be in the future, where there is no data yet.
func endOfWindows(at string, now time.Time) (time.Time, error) {
	if at == "" {
		return now, nil
	}
	t, err := time.Parse(time.RFC3339, at)
	if err != nil {
		return time.Time{}, fmt.Errorf("--at %q: want RFC 3339, e.g. 2026-11-02T00:00:00Z", at)
	}
	if t.After(now) {
		return time.Time{}, fmt.Errorf("--at %s is in the future", at)
	}
	return t, nil
}

// writeOutput writes the report to stdout, or to out when it is set. run calls it last, so a run that fails leaves
// the last report in out, not an empty file.
func writeOutput(d deps, out, format string, rs []result, info reportInfo) error {
	if out == "" {
		return writeReport(d.stdout, format, rs, info)
	}
	var buf bytes.Buffer
	if err := writeReport(&buf, format, rs, info); err != nil {
		return err
	}
	if err := os.WriteFile(out, buf.Bytes(), 0o644); err != nil {
		return err
	}
	_, _ = fmt.Fprintln(d.stderr, "Wrote", out)
	return nil
}

func printQueries(w io.Writer, src metricSource, slos []slo) error {
	for _, s := range slos {
		head := fmt.Sprintf("# %s  under %s in %s of %s slices, over %s", s.Name(), num(s.Threshold), pct(s.Target), shortDuration(s.Slice), windowText(s.Window))
		if s.Budgeting == occurrences {
			head = fmt.Sprintf("# %s  %s of events good, over %s", s.Name(), pct(s.Target), windowText(s.Window))
		}
		if _, err := fmt.Fprintln(w, head); err != nil {
			return err
		}
		for _, q := range src.queries(s) {
			if _, err := fmt.Fprintln(w, q); err != nil {
				return err
			}
		}
	}
	return nil
}

// windowText writes a window in whole days when it is one, e.g. 30d, and as a duration otherwise, e.g. 12h.
func windowText(d time.Duration) string {
	if d%day == 0 {
		return fmt.Sprintf("%dd", d/day)
	}
	return shortDuration(d)
}

// isTerminal is true when f is a terminal, where a "\r" can redraw one progress line. A file or a pipe gets the
// final count only.
func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// shortDuration drops the zero units time.Duration prints: 15m0s -> 15m, 1h0m0s -> 1h.
func shortDuration(d time.Duration) string {
	s := d.String()
	if strings.HasSuffix(s, "m0s") {
		s = strings.TrimSuffix(s, "0s")
	}
	if strings.HasSuffix(s, "h0m") {
		s = strings.TrimSuffix(s, "0m")
	}
	return s
}
