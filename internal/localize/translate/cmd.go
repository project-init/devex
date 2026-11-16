package translate

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"

	"github.com/project-init/devex/internal/localize/config"
	"github.com/project-init/devex/internal/ui"
	"github.com/spf13/cobra"
)

const (
	defaultBatchSize   = 50
	defaultConcurrency = 4
)

func Command() *cobra.Command {
	var locales []string
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "translate",
		Short: "Fill missing translations in every locale with Gemini",
		Long: "Find each locale's missing or empty messages by comparing its messages.gotext.json with en-US, translate them " +
			"with Gemini following the rubric at localize.rubricPath, and write the translations back. A translation whose " +
			"placeholders differ from the source is rejected. Needs GEMINI_API_KEY.",
		Args:        cobra.NoArgs,
		Annotations: map[string]string{ui.JSONAnnotation: "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, ok := config.GetConfig(cmd.Context())
			if !ok || cfg == nil {
				return fmt.Errorf("config not loaded")
			}
			return runCommand(cmd.Context(), ui.New(cmd), cfg.Localize, locales, dryRun, os.Getenv,
				func(ctx context.Context, apiKey, modelName string) (model, error) {
					return newGemini(ctx, apiKey, modelName)
				})
		},
	}
	cmd.Flags().StringSliceVar(&locales, "locale", nil, "translate only these locales, e.g. es-US,fr-FR")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print proposed translations and leave catalogs unchanged")
	return cmd
}

type newModelFunc func(ctx context.Context, apiKey, modelName string) (model, error)

func runCommand(ctx context.Context, u *ui.UI, cfg config.LocalizeConfiguration, locales []string, dryRun bool, getenv func(string) string, newModel newModelFunc) error {
	if cfg.LocalesDir == "" {
		return fmt.Errorf("missing configuration: localize.localesDir")
	}
	opts := options{
		dryRun:      dryRun,
		batchSize:   valueOr(cfg.Translate.BatchSize, defaultBatchSize),
		concurrency: valueOr(cfg.Translate.Concurrency, defaultConcurrency),
	}
	if cfg.RubricPath != "" {
		rubric, err := os.ReadFile(cfg.RubricPath)
		if err != nil {
			return fmt.Errorf("read rubric: %w", err)
		}
		opts.rubric = string(rubric)
	}

	plans, err := plan(cfg.LocalesDir, locales)
	if err != nil {
		return err
	}
	var m model
	var progress *ui.Progress
	if work := workCount(plans); work > 0 {
		apiKey := getenv("GEMINI_API_KEY")
		if apiKey == "" {
			return errNoAPIKey
		}
		if m, err = newModel(ctx, apiKey, cmp.Or(cfg.Translate.Model, defaultModel)); err != nil {
			return err
		}
		progress = u.Progress("Translating", work)
	}
	results, err := run(ctx, cfg.LocalesDir, plans, m, opts, func(n int) {
		if progress != nil {
			progress.Add(n, "")
		}
	})
	if progress != nil {
		progress.Done() // before the report, so the summary doesn't print under the bar
	}
	if results == nil {
		return err
	}
	return errors.Join(report(u, results, dryRun), err)
}

// report prints the results and returns an error when the model's translations were rejected,
// so scripts see the failure in text and JSON mode alike.
func report(u *ui.UI, results []result, dryRun bool) error {
	var translated, rejected int
	for _, r := range results {
		translated += len(r.Translated)
		rejected += len(r.Rejected)
	}
	var rejectedErr error
	if rejected > 0 {
		rejectedErr = fmt.Errorf("rejected %s; rerun to retry", translations(rejected))
	}
	if u.JSON() {
		if err := u.WriteJSON(struct {
			DryRun  bool     `json:"dryRun"`
			Locales []result `json:"locales"`
		}{dryRun, results}); err != nil {
			return err
		}
		return rejectedErr
	}
	if dryRun {
		for _, result := range results {
			if len(result.Translated) == 0 {
				continue
			}
			u.Printf("%s\n", u.Heading(result.Locale))
			rows := make([][]string, len(result.Translated))
			for index, proposal := range result.Translated {
				rows[index] = []string{proposal.Source, proposal.Translation}
			}
			u.Table([]string{"Source", "Proposed translation"}, rows)
			u.Printf("\n")
		}
	}
	rows := make([][]string, len(results))
	for index, result := range results {
		rows[index] = []string{result.Locale, strconv.Itoa(len(result.Translated)), strconv.Itoa(len(result.Rejected)), strconv.Itoa(len(result.Skipped))}
		for _, rejection := range result.Rejected {
			u.Warn("%s: rejected %q: %s", result.Locale, rejection.ID, rejection.Reason)
		}
	}
	u.Table([]string{"Locale", "Translated", "Rejected", "Skipped"}, rows)
	switch {
	case dryRun:
		u.Status("Dry run: no catalogs changed.")
	case translated > 0:
		u.Success("Wrote %s.", translations(translated))
	case rejected == 0:
		u.Success("Every locale is already translated.")
	}
	return rejectedErr
}

func translations(count int) string {
	if count == 1 {
		return "1 translation"
	}
	return strconv.Itoa(count) + " translations"
}

func valueOr(value, fallback int) int {
	if value > 0 {
		return value
	}
	return fallback
}
