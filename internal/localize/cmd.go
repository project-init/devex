package localize

import (
	"github.com/project-init/devex/internal/configdir"
	"github.com/project-init/devex/internal/localize/audit"
	"github.com/project-init/devex/internal/localize/config"
	"github.com/project-init/devex/internal/localize/html"
	"github.com/project-init/devex/internal/localize/mobile"
	"github.com/project-init/devex/internal/localize/translate"
	"github.com/spf13/cobra"
)

const defaultConfigDir = ".localize"

func Command() *cobra.Command {
	var localizeConfigFile string
	var cmd *cobra.Command
	cmd = &cobra.Command{
		Use:           "localize <tool> [args]",
		Short:         "localize is a toolbox CLI for localization management",
		Long:          "Manage translations for projects using the 'gotext' workflow: audit catalogs for missing translations, fill them with Gemini, render a review report, and build mobile bundles.",
		SilenceUsage:  true,
		SilenceErrors: true,
		// NoArgs rejects an unknown tool name instead of printing help and exiting 0.
		Args: cobra.NoArgs,
		PersistentPreRunE: func(current *cobra.Command, args []string) error {
			if current == cmd {
				return nil
			}
			path, err := configdir.Resolve(localizeConfigFile, defaultConfigDir)
			if err != nil {
				return err
			}

			cfg, err := config.LoadConfig(path)
			if err != nil {
				return err
			}

			current.SetContext(config.WithConfig(current.Context(), cfg))
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}
	configdir.AddFlag(cmd, &localizeConfigFile, defaultConfigDir)

	cmd.AddCommand(audit.Command())
	cmd.AddCommand(html.Command())
	cmd.AddCommand(mobile.Command())
	cmd.AddCommand(translate.Command())

	return cmd
}
