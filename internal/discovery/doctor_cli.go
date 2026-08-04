package discovery

import (
	"fmt"
	"io"
	"os"

	"github.com/project-init/devex/internal/discovery/doctor"
	"github.com/project-init/devex/internal/discovery/skill"
	"github.com/spf13/cobra"
)

func doctorCommand() *cobra.Command {
	var configurationPath string
	var harnessValues []string
	command := &cobra.Command{
		Use:   "doctor [project-directory]",
		Short: "Check discovery prerequisites without changing the project",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			projectDirectory := "."
			if len(args) == 1 {
				projectDirectory = args[0]
			}
			harnesses, err := skill.ParseHarnesses(harnessValues)
			if err != nil {
				return err
			}
			report, err := doctor.Run(projectDirectory, configurationPath, harnesses, os.LookupEnv)
			if err != nil {
				return err
			}
			return printDoctorReport(cmd.OutOrStdout(), report)
		},
	}
	command.Flags().StringVar(&configurationPath, "config", defaultConfigPath, "target configuration file")
	command.Flags().StringSliceVar(&harnessValues, "harness", []string{"all"}, "AI harness: codex, claude, cursor, or all")
	return command
}

func printDoctorReport(output io.Writer, report doctor.Report) error {
	for _, check := range report.Checks {
		_, _ = fmt.Fprintf(output, "%-4s %-24s %s\n", check.Severity, check.Name, check.Detail)
		if check.Remedy != "" {
			_, _ = fmt.Fprintf(output, "     fix: %s\n", check.Remedy)
		}
	}
	failures := report.FailureCount()
	warnings := report.WarningCount()
	if failures > 0 {
		return fmt.Errorf("discovery doctor found %d problem(s) and %d warning(s)", failures, warnings)
	}
	_, _ = fmt.Fprintf(output, "Discovery is ready (%d warning(s)).\n", warnings)
	return nil
}
