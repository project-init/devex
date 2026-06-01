package localize

import (
	"fmt"
	"os"

	"github.com/project-init/devex/internal/localize/audit"
	"github.com/project-init/devex/internal/localize/config"
	"github.com/project-init/devex/internal/localize/translate"
	"github.com/spf13/cobra"
)

const configFlag = "configDir"
const defaultConfigDir = ".localize"

func Command() *cobra.Command {
	var localizeConfigFile string
	cmd := &cobra.Command{
		Use:           "localize <tool> [args]",
		Short:         "localize is a toolbox CLI for localization management",
		Long:          "Manage translations for projects using the 'gotext' workflow. Detect missing keys and translate them using the Gemini LLM.",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          requireToolArg,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			path, err := resolveConfigPath(localizeConfigFile)
			if err != nil {
				return err
			}

			cfg, err := config.LoadConfig(path)
			if err != nil {
				return err
			}

			cmd.SetContext(config.WithConfig(cmd.Context(), cfg))
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}
	cmd.PersistentFlags().StringVar(&localizeConfigFile, configFlag, "", fmt.Sprintf("config directory (default is %s)", defaultConfigDir))

	cmd.AddCommand(audit.Command())
	cmd.AddCommand(translate.Command())

	return cmd
}

func requireToolArg(cmd *cobra.Command, args []string) error {
	if err := cobra.MinimumNArgs(1)(cmd, args); err != nil {
		_ = cmd.Usage()
		return err
	}
	return nil
}

func resolveConfigPath(explicit string) (string, error) {
	if explicit != "" {
		if _, err := os.Stat(explicit); err != nil {
			return "", fmt.Errorf("config dir %q not found: %w", explicit, err)
		}
		return explicit, nil
	}

	if _, err := os.Stat(defaultConfigDir); err != nil {
		return "", fmt.Errorf("no config found: pass --%s or create %s", configFlag, defaultConfigDir)
	}
	return defaultConfigDir, nil
}
