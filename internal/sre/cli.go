package sre

import (
	"fmt"
	"os"

	"github.com/project-init/devex/internal/sre/config"
	"github.com/project-init/devex/internal/sre/keygen"
	"github.com/project-init/devex/internal/sre/postgres"
	"github.com/project-init/devex/internal/sre/release"
	"github.com/spf13/cobra"
)

const configFlag = "configDir"

func Execute() error {
	// Register tools (subcommands)
	var sreConfigFile string
	rootCmd := &cobra.Command{
		Use:           "sre <tool> [args]",
		Short:         "sre is a toolbox CLI for site reliability operations",
		SilenceUsage:  true,                  // don't print usage on errors by default
		SilenceErrors: true,                  // we’ll print errors ourselves
		Args:          cobra.MinimumNArgs(1), // requires <tool> unless help is requested
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			path, err := resolveConfigPath(sreConfigFile)
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
			// This only runs if Args passes, so this is mostly a guardrail.
			return cmd.Help()
		},
	}
	rootCmd.PersistentFlags().StringVar(&sreConfigFile, configFlag, "", "config directory (default is .sre)")

	// Sub Commands (Tools)
	rootCmd.AddCommand(keygen.Command())
	rootCmd.AddCommand(postgres.Command())
	rootCmd.AddCommand(release.Command())

	// Example Tools
	rootCmd.AddCommand(echoCmd())
	rootCmd.AddCommand(versionCmd())

	if err := rootCmd.Execute(); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "Error:", err)
		_ = rootCmd.Usage()
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

	defaultPath := ".sre"
	if _, err := os.Stat(defaultPath); err != nil {
		return "", fmt.Errorf("no config found: pass --%s or create %s", configFlag, defaultPath)
	}
	return defaultPath, nil
}
