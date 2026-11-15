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
		Use:   "access",
		Short: "Handle postgres access via psql",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, ok := config.GetConfig(cmd.Context())
			if !ok || cfg == nil {
				return fmt.Errorf("config not loaded")
			}
			u := ui.New(cmd)

			environment, err := selectEnvironment(cmd.Context(), u, envValue, cfg.Postgres.Environments)
			if err != nil {
				return err
			}

			u.Status("Using environment %s", environment)
			psqlCfg, err := loadPGAccessEnvironment(cmd.Context(), u, cfg.Postgres.Environments[environment])
			if err != nil {
				return err
			}

			return runPsql(psqlCfg)
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
