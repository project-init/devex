package discovery

import (
	"fmt"
	"os"
	"strings"

	"github.com/project-init/devex/internal/discovery/doctor"
	"github.com/project-init/devex/internal/discovery/skill"
	"github.com/project-init/devex/internal/ui"
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
			return printDoctorReport(ui.New(cmd), report)
		},
	}
	command.Flags().StringVar(&configurationPath, "config", defaultConfigPath, "target configuration file")
	command.Flags().StringSliceVar(&harnessValues, "harness", []string{"all"}, "AI harness: codex, claude, cursor, or all")
	return command
}

func printDoctorReport(u *ui.UI, report doctor.Report) error {
	printDoctorGroup(u, u.Heading("Passed"), checksWithSeverity(report.Checks, doctor.SeverityPass))
	flagged := append(
		checksWithSeverity(report.Checks, doctor.SeverityFlag),
		checksWithSeverity(report.Checks, doctor.SeverityWarn)...,
	)
	printDoctorGroup(u, u.Heading("Flagged"), flagged)
	printDoctorGroup(u, u.Bad("Failures"), checksWithSeverity(report.Checks, doctor.SeverityFail))
	failures := report.FailureCount()
	flags := report.FlagCount()
	warnings := report.WarningCount()
	if failures > 0 {
		return fmt.Errorf("discovery doctor found %d problem(s), %d flag(s), and %d warning(s)", failures, flags, warnings)
	}
	u.Printf("Discovery is ready (%d flag(s), %d warning(s)).\n", flags, warnings)
	return nil
}

func checksWithSeverity(checks []doctor.Check, severity doctor.Severity) []doctor.Check {
	result := make([]doctor.Check, 0)
	for _, check := range checks {
		if check.Severity == severity {
			result = append(result, check)
		}
	}
	return result
}

func printDoctorGroup(u *ui.UI, heading string, checks []doctor.Check) {
	if len(checks) == 0 {
		return
	}
	u.Printf("%s\n", heading)
	for _, check := range checks {
		lines := strings.Split(check.Detail, "\n")
		u.Printf("  %s %s: %s\n", doctorLabel(u, check.Severity), check.Name, lines[0])
		for _, line := range lines[1:] {
			u.Printf("         %s\n", line)
		}
		if check.Remedy != "" {
			u.Printf("         fix: %s\n", check.Remedy)
		}
	}
	u.Printf("\n")
}

func doctorLabel(u *ui.UI, severity doctor.Severity) string {
	label := "[" + string(severity) + "]"
	switch severity {
	case doctor.SeverityPass:
		return u.Good(label)
	case doctor.SeverityFlag, doctor.SeverityWarn:
		return u.Warning(label)
	case doctor.SeverityFail:
		return u.Bad(label)
	default:
		return label
	}
}
