package contributions

import (
	"context"
	"fmt"
	"time"

	"github.com/project-init/devex/internal/contributions/collection"
	"github.com/project-init/devex/internal/contributions/config"
	"github.com/project-init/devex/internal/contributions/signal"
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

			fmt.Printf("starting Contributions (collect) At - %s\n", time.Now().String())
			ctx := context.Background()
			err = collection.Run(ctx, cfg)
			if err != nil {
				return err
			}
			fmt.Printf("completed Contributions (collect) At - %s\n", time.Now().String())
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

			fmt.Printf("starting Contributions (signal) At - %s\n", time.Now().String())
			err = signal.Run(cfg)
			if err != nil {
				return err
			}
			fmt.Printf("completed Contributions (signal) At - %s\n", time.Now().String())
			return nil
		},
	}
}
