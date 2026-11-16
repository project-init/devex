package workplan

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
)

// deprecationNotice completes Cobra's `Command "<name>" is deprecated, ` prefix.
const deprecationNotice = "use `devex discovery init` and `devex discovery publish` instead; a future release removes it"

func Command() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:           "workplan",
		Short:         "workplan is a toolbox CLI for managing work plans",
		Deprecated:    deprecationNotice,
		SilenceUsage:  true,
		SilenceErrors: true,
		// Cobra prints Deprecated only for the command that runs, and marking the leaves
		// would hide them from `devex workplan --help`, so the parent warns for them.
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Command %q is deprecated, %s\n", cmd.CommandPath(), deprecationNotice)
		},
	}

	rootCmd.AddCommand(generateCmd())
	rootCmd.AddCommand(publishCmd())

	return rootCmd
}

func generateCmd() *cobra.Command {
	return &cobra.Command{
		Use:           "generate <workplans_directory> <workplan_name>",
		Short:         "Generate a workplan and problem markdown based on the current template(s)",
		Args:          cobra.ExactArgs(2),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return GenerateFiles(args[0], args[1])
		},
	}
}

func publishCmd() *cobra.Command {
	return &cobra.Command{
		Use:           "publish <workplan_path>",
		Short:         "Publish a workplan to JIRA",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return PublishWorkPlanToJira(context.Background(), args[0])
		},
	}
}
