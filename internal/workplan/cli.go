package workplan

import (
	"context"

	"github.com/spf13/cobra"
)

func Command() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:           "workplan",
		Short:         "workplan is a toolbox CLI for managing work plans",
		SilenceUsage:  true,
		SilenceErrors: true,
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
