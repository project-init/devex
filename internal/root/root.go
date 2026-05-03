package root

import (
	"fmt"
	"os"

	"github.com/project-init/devex/internal/components"
	"github.com/project-init/devex/internal/contributions"
	"github.com/project-init/devex/internal/sre"
	"github.com/project-init/devex/internal/workplan"
	"github.com/spf13/cobra"
)

func Execute() error {
	rootCmd := &cobra.Command{
		Use:           "devex",
		Short:         "devex is a toolbox CLI for developer experience operations",
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	// Add subcommands
	rootCmd.AddCommand(sre.Command())
	rootCmd.AddCommand(workplan.Command())
	rootCmd.AddCommand(contributions.Command())
	rootCmd.AddCommand(components.Command())

	if err := rootCmd.Execute(); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "Error:", err)
		return err
	}
	return nil
}
