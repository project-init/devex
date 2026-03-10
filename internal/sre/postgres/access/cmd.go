package access

import (
	"fmt"
	"sort"

	"github.com/manifoldco/promptui"
	"github.com/project-init/devex/internal/sre/config"
	"github.com/spf13/cobra"
)

func Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "access",
		Short: "Handle postgres access via psql",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, ok := config.GetConfig(cmd.Context())
			if !ok || cfg == nil {
				return fmt.Errorf("config not loaded")
			}

			environment, err := selectEnvironment(cfg.Postgres.Environments)
			if err != nil {
				return err
			}

			fmt.Printf("%s chosen. Loading environment config...\n", environment)
			environmentConfig, found := cfg.Postgres.Environments[environment]
			if !found {
				return fmt.Errorf("environment %s not found in cfg %+v", environment, cfg.Postgres)
			}

			psqlCfg, err := loadPGAccessEnvironment(environmentConfig)
			if err != nil {
				return err
			}

			err = runPsql(psqlCfg)
			if err != nil {
				return err
			}
			return nil
		},
	}
	return cmd
}

func selectEnvironment(environments map[string]config.PostgresEnvironmentConfig) (string, error) {
	environmentNames := make([]string, 0, len(environments))
	for environmentName := range environments {
		environmentNames = append(environmentNames, environmentName)
	}

	sort.Strings(environmentNames)

	argPrompt := promptui.Select{
		Label: "Select Environment",
		Items: environmentNames,
	}

	_, argument, err := argPrompt.Run()
	if err != nil {
		return "", err
	}

	return argument, nil
}
