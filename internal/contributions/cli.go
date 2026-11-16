package contributions

import (
	"time"

	"github.com/project-init/devex/internal/contributions/collection"
	"github.com/project-init/devex/internal/contributions/config"
	"github.com/project-init/devex/internal/contributions/signal"
	"github.com/project-init/devex/internal/ui"
	"github.com/spf13/cobra"
)

func Command() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:           "contributions",
		Short:         "contributions is a toolbox CLI for gathering and signaling contributions",
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	rootCmd.AddCommand(collectCmd())
	rootCmd.AddCommand(signalCmd())

	return rootCmd
}

func collectCmd() *cobra.Command {
	return &cobra.Command{
		Use:           "collect <config_file>",
		Short:         "Gather all pull requests per the definition in the config file and store them in the prs directory",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.NewConfigFromYaml(args[0])
			if err != nil {
				return err
			}

			u := ui.New(cmd)
			start := time.Now()
			if err := collection.Run(cmd.Context(), u, cfg); err != nil {
				return err
			}
			u.Success("Collected pull requests into %s in %s", cfg.OutputDirectories.Prs, time.Since(start).Round(time.Second))
			return nil
		},
	}
}

func signalCmd() *cobra.Command {
	return &cobra.Command{
		Use:           "signal <config_file>",
		Short:         "Create a signal output per the definition in the config file and store it in the signals directory",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.NewConfigFromYaml(args[0])
			if err != nil {
				return err
			}

			paths, err := signal.Run(cfg)
			if err != nil {
				return err
			}
			u := ui.New(cmd)
			for _, path := range paths {
				u.Success("Wrote %s", path)
			}
			return nil
		},
	}
}
