package errtrack

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/project-init/devex/internal/sre/config"
	"github.com/project-init/devex/internal/sre/errtrack/report"
	"github.com/spf13/cobra"
)

// options are the flags of the errtrack command.
type options struct {
	// Env overrides the deployment environment. The provider reads one from
	// the error's own event, so this is only needed when that is missing or
	// wrong.
	Env string

	// DatadogSite overrides the site resolved from the pasted link and the
	// environment. See datadogBaseURL for the precedence.
	DatadogSite string

	// Out writes the report to a file instead of stdout.
	Out string
}

// deps holds everything run reaches outside the process, so a test can swap
// each one and never open a socket or read a clock.
//
// now is here because fetched_at would otherwise change on every run and no
// test could assert on the output.
type deps struct {
	getenv    func(string) string
	newClient func(baseURL, apiKey, appKey string) issueFetcher
	stdout    io.Writer
	stderr    io.Writer
	now       func() time.Time
}

func Command() *cobra.Command {
	var o options

	cmd := &cobra.Command{
		Use:   "errtrack <link>",
		Short: "Pull Datadog error metadata down as JSON from an error link",
		Long: `Take a Datadog Error Tracking link and write the metadata behind it as JSON, ready to hand
to a coding agent or a ticket-creation step.

Export DD_API_KEY and DD_APP_KEY first. The app key needs access to Error Tracking. The site is
read from the link itself, so an org on a site other than datadoghq.com needs nothing extra;
DD_SITE or --datadog-site override it.`,
		Args:        cobra.ExactArgs(1),
		Annotations: map[string]string{config.OptionalAnnotation: "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
			defer stop()

			return run(ctx, o, args[0], deps{
				getenv: os.Getenv,
				newClient: func(baseURL, apiKey, appKey string) issueFetcher {
					return newDatadogClient(baseURL, apiKey, appKey)
				},
				stdout: cmd.OutOrStdout(),
				stderr: cmd.ErrOrStderr(),
				now:    time.Now,
			})
		},
	}

	cmd.Flags().StringVar(&o.Env, "env", "",
		"Deployment environment to record, instead of the one on the error, e.g. production")
	cmd.Flags().StringVar(&o.DatadogSite, "datadog-site", "",
		"Datadog site of the org, instead of the link's own host or DD_SITE, e.g. datadoghq.eu")
	cmd.Flags().StringVarP(&o.Out, "out", "o", "",
		"Write the report to this file, only once the run succeeds, instead of to stdout")

	return cmd
}

// run resolves the link, the credentials and the site, then fetches.
//
// The link is validated before the credentials are read: it is the argument the
// user just typed and needs no setup to check, so a bad paste should be
// reported as a bad paste rather than as missing keys.
func run(ctx context.Context, o options, link string, d deps) error {
	target, err := ParseTarget(link)
	if err != nil {
		return err
	}

	apiKey, appKey, err := loadKeys(d.getenv)
	if err != nil {
		return err
	}

	baseURL := datadogBaseURL(o.DatadogSite, target.Site, d.getenv)
	if note := siteConflict(o.DatadogSite, target.Site, d.getenv); note != "" {
		// A note nobody can read is not worth failing the run over.
		_, _ = fmt.Fprintln(d.stderr, note)
	}

	resp, err := d.newClient(baseURL, apiKey, appKey).fetchIssue(ctx, target.IssueID)
	if err != nil {
		return err
	}

	rep := toReport(resp, mapInput{
		Site:        cmp.Or(target.Site, siteOfBaseURL(baseURL)),
		EnvOverride: o.Env,
		FetchedAt:   d.now().UTC(),
	})

	return writeReport(d, o.Out, rep)
}

// siteOfBaseURL recovers the site from an API host, so a report built from a
// bare issue id still carries a link back to Datadog.
func siteOfBaseURL(baseURL string) string {
	return strings.TrimPrefix(strings.TrimPrefix(baseURL, "https://"), "api.")
}

// writeReport writes the document to stdout, or to out when it is set.
//
// Indented because a person reads this as often as a program does, and newline
// terminated so a shell prompt lands on its own line.
//
// run calls this last and the encode happens before the file is touched, so a
// run that fails leaves whatever was in out from last time rather than
// truncating it to nothing.
func writeReport(d deps, out string, rep report.Report) error {
	encoded, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return fmt.Errorf("encode the report: %w", err)
	}
	encoded = append(encoded, '\n')

	if out == "" {
		if _, err := d.stdout.Write(encoded); err != nil {
			return fmt.Errorf("write the report: %w", err)
		}

		return nil
	}

	if err := os.WriteFile(out, encoded, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", out, err)
	}
	// On stderr, so --out still composes with a pipe.
	_, _ = fmt.Fprintln(d.stderr, "Wrote", out)

	return nil
}
