package access

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"github.com/project-init/devex/internal/sre/config"
	"github.com/project-init/devex/internal/ui"
	"github.com/spf13/cobra"
)

const envFlag = "env"

func Command() *cobra.Command {
	var envValue string
	cmd := &cobra.Command{
		Use:   "access [environment]",
		Short: "Open psql on a configured postgres environment",
		Long: "Open psql on an environment from the sre config, named as an argument or with --env, or chosen from a " +
			"list on a terminal. The password, or an RDS IAM token signed for the environment's port and region, " +
			"reaches psql through PGPASSWORD, never its command line.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, ok := config.GetConfig(cmd.Context())
			if !ok || cfg == nil {
				return fmt.Errorf("config not loaded")
			}
			u := ui.New(cmd)
			requested := envValue
			if len(args) == 1 {
				if requested != "" && requested != args[0] {
					return fmt.Errorf("environment given twice: %q as an argument and %q with --env", args[0], requested)
				}
				requested = args[0]
			}

			if _, ok := cfg.Postgres.Environments[requested]; len(args) == 1 && !ok {
				return fmt.Errorf("unknown environment %q; configured: %v", requested, slices.Sorted(maps.Keys(cfg.Postgres.Environments)))
			}
			environment, err := selectEnvironment(cmd.Context(), u, requested, cfg.Postgres.Environments)
			if err != nil {
				return err
			}
			envCfg := cfg.Postgres.Environments[environment]
			u.Status("Connecting to %s (%s as %s)", environment, envCfg.Host, envCfg.UserName)
			psqlCfg, err := loadPGAccessEnvironment(cmd.Context(), envCfg, loadAWSConfig)
			if err != nil {
				return fmt.Errorf("environment %q: %w", environment, err)
			}
			if err := runPsql(psqlCfg); err != nil {
				return fmt.Errorf("environment %q: %w", environment, err)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&envValue, envFlag, "", "environment to connect to (prompts when omitted)")
	return cmd
}

// selectEnvironment returns the --env value when given, and otherwise prompts for one.
func selectEnvironment(ctx context.Context, u *ui.UI, flagValue string, environments map[string]config.PostgresEnvironmentConfig) (string, error) {
	names := slices.Sorted(maps.Keys(environments))
	if len(names) == 0 {
		return "", fmt.Errorf("no postgres environments configured")
	}
	options := make([]ui.Option, len(names))
	for index, name := range names {
		options[index] = ui.Option{Label: name, Value: name}
	}
	return u.Choose(ctx, "Select environment", options, names[0], envFlag, flagValue)
}
