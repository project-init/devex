package sre

import (
	"github.com/project-init/devex/internal/configdir"
	"github.com/project-init/devex/internal/sre/analyze"
	"github.com/project-init/devex/internal/sre/config"
	"github.com/project-init/devex/internal/sre/dependencies"
	"github.com/project-init/devex/internal/sre/keygen"
	"github.com/project-init/devex/internal/sre/postgres"
	"github.com/project-init/devex/internal/sre/release"
	"github.com/spf13/cobra"
)

const defaultConfigDir = ".sre"

func Command() *cobra.Command {
	// Register tools (subcommands)
	var sreConfigFile string
	var rootCmd *cobra.Command
	rootCmd = &cobra.Command{
		Use:           "sre <tool> [args]",
		Short:         "sre is a toolbox CLI for site reliability operations",
		SilenceUsage:  true, // don’t print usage on errors by default
		SilenceErrors: true, // we’ll print errors ourselves
		// NoArgs rejects an unknown tool name instead of printing help and exiting 0.
		Args: cobra.NoArgs,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if cmd == rootCmd {
				return nil
			}
			path, err := configdir.Resolve(sreConfigFile, defaultConfigDir)
			if err != nil {
				if sreConfigFile == "" && configOptional(cmd) {
					return nil
				}

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
	configdir.AddFlag(rootCmd, &sreConfigFile, defaultConfigDir)

	// Sub Commands (Tools)
	rootCmd.AddCommand(analyze.Command())
	rootCmd.AddCommand(dependencies.Command())
	rootCmd.AddCommand(keygen.Command())
	rootCmd.AddCommand(postgres.Command())
	rootCmd.AddCommand(release.Command())

	return rootCmd
}

func configOptional(cmd *cobra.Command) bool {
	for c := cmd; c != nil; c = c.Parent() {
		if c.Annotations[config.OptionalAnnotation] == "true" {
			return true
		}
	}

	return false
}
