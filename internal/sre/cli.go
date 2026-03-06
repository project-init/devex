package sre

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:           "sre <tool> [args]",
	Short:         "sre is a toolbox CLI for site reliability operations",
	SilenceUsage:  true,                  // don't print usage on errors by default
	SilenceErrors: true,                  // we’ll print errors ourselves
	Args:          cobra.MinimumNArgs(1), // requires <tool> unless help is requested
	RunE: func(cmd *cobra.Command, args []string) error {
		// This only runs if Args passes, so this is mostly a guardrail.
		return cmd.Help()
	},
}

func Execute() error {
	// Register tools (subcommands)
	rootCmd.AddCommand(echoCmd())
	rootCmd.AddCommand(versionCmd())

	if err := rootCmd.Execute(); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "Error:", err)
		_ = rootCmd.Usage()
		return err
	}
	return nil
}
