package root

import (
	"fmt"
	"os"

	"github.com/project-init/devex/internal/components"
	"github.com/project-init/devex/internal/contributions"
	"github.com/project-init/devex/internal/discovery"
	"github.com/project-init/devex/internal/localize"
	"github.com/project-init/devex/internal/sre"
	"github.com/project-init/devex/internal/version"
	"github.com/project-init/devex/internal/workplan"
	gommoncmd "github.com/project-init/gommon/pkg/cmd"
	"github.com/spf13/cobra"
)

// Execute runs devex and prints any error to stderr. The first Ctrl-C or SIGTERM cancels the
// command's context; a second one terminates the process.
func Execute() error {
	ctx := gommoncmd.SetupMainContext()
	if err := newRootCmd().ExecuteContext(ctx); err != nil {
		if ctx.Err() != nil {
			err = fmt.Errorf("cancelled: %w", err)
		}
		_, _ = fmt.Fprintln(os.Stderr, "Error:", err)
		return err
	}
	return nil
}

func newRootCmd() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:           "devex",
		Short:         "devex is a toolbox CLI for developer experience operations",
		Version:       version.String(),
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	rootCmd.AddCommand(versionCmd())
	rootCmd.AddCommand(sre.Command())
	rootCmd.AddCommand(localize.Command())
	rootCmd.AddCommand(workplan.Command())
	rootCmd.AddCommand(contributions.Command())
	rootCmd.AddCommand(components.Command())
	rootCmd.AddCommand(discovery.Command())

	return rootCmd
}

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the devex version",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "devex version %s\n", version.String())
		},
	}
}
