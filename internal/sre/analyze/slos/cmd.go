package slos

import (
	"bytes"
	"cmp"
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
	"github.com/project-init/gommon/pkg/datadog"
	"github.com/spf13/cobra"
)

// The defaults every team shares, so a run needs only --group. .sre/slos.yaml overrides them. The module has no
// default: it is the one go.mod requires that ships defaultFile.
const (
	defaultFile = "sre/configuration/slos.gen.yaml"
	// The same metric carries uat, so an unscoped run would mix it into the percentiles.
	defaultScope = "environment:production"
	// Used when go.mod does not require --module, so a repository that is not a Go consumer of it, or not Go at all,
	// still gets a report. A repository that does require it reads the SLOs its code was built against.
	defaultVersion = "latest"
)

// options holds the flags that run reads.
type options struct {
	Group         string
	Module        string
	ProtosVersion string
	MetricPrefix  string
	Tags          []string
	Days          int
	DaysSet       bool   // --days was given, so it cannot be mixed with --since
	Since         string // YYYY-MM-DD; overrides Days
	Rollup        time.Duration
	Format        string
	All, Wide     bool
	Batch         int
	MinRequests   float64
	Out           string // empty writes to stdout
	DryRun        bool
}

// deps holds what run reaches outside the process, so a test can swap each one.
type deps struct {
	getenv    func(string) string
	download  downloader
	goMod     string
	modCache  func(context.Context) (string, error)
	newClient func(site, apiKey, appKey string) (client, error)
	stdout    io.Writer
	stderr    io.Writer
	live      bool // stderr is a terminal, so progress can redraw one line
	now       func() time.Time
}

func Command() *cobra.Command {
	var o options
	cmd := &cobra.Command{
		Use:   "slos",
		Short: "Rank RPCs by how close they run to their response time SLO",
		Long: `Read each RPC's response time SLO from the SLO file in the protos module, then score how close the
RPC ran to it in Datadog over the window. --group picks the team whose RPCs to score, and nothing has to be
set up in the repository the command runs in.

The protos module is the one go.mod requires that ships ` + defaultFile + `, at the version go.mod
requires. In a repository with no go.mod, --module names it.

Each RPC gets a verdict from its typical bucket's p95 against the SLO, the estimated share of its
requests that took the SLO or longer, or 75% of it or more, and the trend of its daily p95.
Datadog keeps only each bucket's median, p95, and max, so the shares are estimated between them. The report sorts the most urgent first and
gives each RPC an action.

Export DD_API_KEY and DD_APP_KEY first. The app key needs the timeseries_query scope.

Which app serves each RPC is read from Datadog, because the protos do not know it: one group's RPCs can
come from several apps, and an RPC no app reports reads NO DATA.`,
		Annotations: map[string]string{config.OptionalAnnotation: "true"},
		Args:        cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// The command is config-optional, so a repository with no .sre runs on the defaults alone.
			var slos config.SLOsConfiguration
			if cfg, ok := config.GetConfig(cmd.Context()); ok && cfg != nil {
				slos = cfg.SLOs
			}
			o.DaysSet = cmd.Flags().Changed("days")

			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
			defer stop()
			return run(ctx, o, slos, deps{
				getenv:   os.Getenv,
				download: goModDownload,
				goMod:    "go.mod",
				modCache: goModCache,
				newClient: func(site, apiKey, appKey string) (client, error) {
					return newDatadogClient(site, apiKey, appKey)
				},
				stdout: cmd.OutOrStdout(),
				stderr: cmd.ErrOrStderr(),
				live:   isTerminal(os.Stderr),
				now:    time.Now,
			})
		},
	}

	cmd.Flags().StringVar(&o.Group, "group", "", "Team whose RPCs to score, as a services key of the SLO file, e.g. gigapi")
	cmd.Flags().StringVar(&o.Module, "module", "", "Read the SLO file from this module instead of the one go.mod requires that ships it, e.g. github.com/acme/protos")
	cmd.Flags().StringVar(&o.ProtosVersion, "protos-version", "", "Read the SLO file from this release instead of the one go.mod requires, e.g. v1.2.3")
	cmd.Flags().StringVar(&o.MetricPrefix, "metric-prefix", "", "Pin the metric namespace instead of reading from Datadog which app serves each RPC")
	cmd.Flags().StringSliceVar(&o.Tags, "tag", nil, "Score only the RPCs carrying one of these x-slo-tags, e.g. p0,p1")
	cmd.Flags().IntVar(&o.Days, "days", 30, "Window to score, in days back from now")
	cmd.Flags().StringVar(&o.Since, "since", "", "Score from this UTC date (YYYY-MM-DD) to now instead of --days, e.g. the day of a rollout")
	cmd.Flags().DurationVar(&o.Rollup, "rollup", 15*time.Minute, "Bucket size; match it to the response time monitors' window")
	cmd.Flags().StringVar(&o.Format, "format", "table", "Output format: "+strings.Join(formats, ", "))
	cmd.Flags().StringVar(&o.Out, "out", "", "Write the report to this file, only once the run succeeds, instead of to stdout")
	cmd.Flags().BoolVar(&o.All, "all", false, "Also list HEALTHY, NO DATA, and quiet LOW DATA rows (csv and html always list them)")
	cmd.Flags().BoolVar(&o.Wide, "wide", false, "Show every column in the table and markdown (csv and html always show them)")
	cmd.Flags().IntVar(&o.Batch, "batch", 8, "RPCs per Datadog request; each RPC costs five queries")
	cmd.Flags().Float64Var(&o.MinRequests, "min-requests", 100, "Requests in the window below which a row is LOW DATA")
	cmd.Flags().BoolVar(&o.DryRun, "dry-run", false, "Print the Datadog queries and exit")

	return cmd
}

func run(ctx context.Context, o options, cfg config.SLOsConfiguration, d deps) error {
	site := cmp.Or(cfg.Datadog.Site, "datadoghq.com")
	if !slices.Contains(datadog.Sites, site) {
		return fmt.Errorf("slos.datadog.site %q is not a Datadog site: use one of %s", site, strings.Join(datadog.Sites, ", "))
	}
	if !slices.Contains(formats, o.Format) {
		return fmt.Errorf("--format %q: use %s", o.Format, strings.Join(formats, ", "))
	}
	if o.Rollup < time.Minute || o.Batch < 1 {
		return errors.New("--rollup must be at least 1m, and --batch at least 1")
	}
	to := d.now()
	from, span, err := window(o, to)
	if err != nil {
		return err
	}
	cfg.File = cmp.Or(cfg.File, defaultFile)
	if cfg.Module = cmp.Or(o.Module, cfg.Module); cfg.Module == "" {
		if cfg.Module, err = findSLOModule(ctx, d.goMod, cfg.File, d.modCache); err != nil {
			return err
		}
	}
	cfg.Version = cmp.Or(cfg.Version, defaultVersion)
	group := cmp.Or(o.Group, cfg.Group)
	prefix := cmp.Or(o.MetricPrefix, cfg.MetricPrefix)
	scope := cmp.Or(cfg.Datadog.Scope, defaultScope)

	data, ref, err := readSLOFile(ctx, cfg, d.goMod, o.ProtosVersion, d.download)
	if err != nil {
		return err
	}
	eps, err := parseSLOs(data, group, scope, o.Rollup)
	if err != nil {
		return fmt.Errorf("%s %s: %w", ref, cfg.File, err)
	}
	if eps = filterByTags(eps, o.Tags); len(eps) == 0 {
		return fmt.Errorf("no RPC of %s carries %s", group, strings.Join(o.Tags, " or "))
	}

	apiKey, appKey, err := loadKeys(d.getenv)
	if err != nil {
		return err
	}
	dd, err := d.newClient(site, apiKey, appKey)
	if err != nil {
		return err
	}
	if prefix != "" {
		eps = pinPrefixes(eps, prefix)
	} else {
		names, err := dd.metricNames(ctx, to.Add(-prefixWindow))
		if err != nil {
			return fmt.Errorf("list the metrics that name which app serves each RPC: %w", err)
		}
		eps = resolvePrefixes(eps, names)
	}
	if o.DryRun {
		return printQueries(d.stdout, eps)
	}

	results, err := fetchAndScore(ctx, dd, eps, from, to, o.Batch, progress{w: d.stderr, live: d.live})
	if err != nil {
		return err
	}
	unweighted := 0
	for i := range results {
		results[i].gate(o.MinRequests)
		if r := results[i]; r.Counted && !r.Weighted && r.Verdict != noData {
			unweighted++
		}
	}
	if unweighted > 0 {
		_, _ = fmt.Fprintf(d.stderr, "warning: %d endpoints have request counts that do not line up with their p95 buckets, so their numbers weight every bucket the same\n", unweighted)
	}
	info := reportInfo{Group: group, Prefixes: prefixesOf(eps), Scope: scope, Window: span, Rollup: shortDuration(o.Rollup),
		MinRequests: o.MinRequests, All: o.All, Wide: o.Wide}
	return writeOutput(d, o.Out, o.Format, results, info)
}

// window turns --days or --since into the start of the window and the words the summary uses for it.
func window(o options, now time.Time) (time.Time, string, error) {
	if o.Since == "" {
		if o.Days < 1 {
			return time.Time{}, "", errors.New("--days must be at least 1")
		}
		return now.AddDate(0, 0, -o.Days), fmt.Sprintf("last %dd", o.Days), nil
	}
	if o.DaysSet {
		return time.Time{}, "", errors.New("use --days or --since, not both")
	}
	from, err := time.Parse(time.DateOnly, o.Since)
	if err != nil {
		return time.Time{}, "", fmt.Errorf("--since %q: want a date as YYYY-MM-DD", o.Since)
	}
	if !from.Before(now) {
		return time.Time{}, "", fmt.Errorf("--since %s is not in the past", o.Since)
	}
	return from, "since " + o.Since, nil
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

func printQueries(w io.Writer, eps []endpoint) error {
	for _, ep := range eps {
		slo := "none"
		if ep.SLO > 0 {
			slo = ms(ep.SLO)
		}
		if ep.Prefix == "" {
			if _, err := fmt.Fprintf(w, "# %s  SLO %s  no app reports this RPC\n", ep.Name(), slo); err != nil {
				return err
			}
			continue
		}
		if _, err := fmt.Fprintf(w, "# %s  SLO %s\n", ep.Name(), slo); err != nil {
			return err
		}
		for _, q := range endpointQueries(ep) {
			if _, err := fmt.Fprintln(w, q); err != nil {
				return err
			}
		}
	}
	return nil
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
