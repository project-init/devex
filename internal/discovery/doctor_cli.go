package discovery

import (
	"fmt"
	"io"
	"os"
	"strings"

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
	color := doctorColorEnabled(output)
	printDoctorGroup(output, "Passed", checksWithSeverity(report.Checks, doctor.SeverityPass), color)
	flagged := append(
		checksWithSeverity(report.Checks, doctor.SeverityFlag),
		checksWithSeverity(report.Checks, doctor.SeverityWarn)...,
	)
	printDoctorGroup(output, "Flagged", flagged, color)
	printDoctorGroup(output, doctorHeading("Failures", doctor.SeverityFail, color), checksWithSeverity(report.Checks, doctor.SeverityFail), color)
	failures := report.FailureCount()
	flags := report.FlagCount()
	warnings := report.WarningCount()
	if failures > 0 {
		return fmt.Errorf("discovery doctor found %d problem(s), %d flag(s), and %d warning(s)", failures, flags, warnings)
	}
	_, _ = fmt.Fprintf(output, "Discovery is ready (%d flag(s), %d warning(s)).\n", flags, warnings)
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

func printDoctorGroup(output io.Writer, heading string, checks []doctor.Check, color bool) {
	if len(checks) == 0 {
		return
	}
	_, _ = fmt.Fprintln(output, heading)
	for _, check := range checks {
		lines := strings.Split(check.Detail, "\n")
		_, _ = fmt.Fprintf(output, "  %s %s: %s\n", doctorLabel(check.Severity, color), check.Name, lines[0])
		for _, line := range lines[1:] {
			_, _ = fmt.Fprintf(output, "         %s\n", line)
		}
		if check.Remedy != "" {
			_, _ = fmt.Fprintf(output, "         fix: %s\n", check.Remedy)
		}
	}
	_, _ = fmt.Fprintln(output)
}

func doctorColorEnabled(output io.Writer) bool {
	if os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		return false
	}
	file, ok := output.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func doctorLabel(severity doctor.Severity, color bool) string {
	label := "[" + string(severity) + "]"
	if !color {
		return label
	}
	switch severity {
	case doctor.SeverityPass:
		return "\x1b[32m" + label + "\x1b[0m"
	case doctor.SeverityFlag, doctor.SeverityWarn:
		return "\x1b[33m" + label + "\x1b[0m"
	case doctor.SeverityFail:
		return "\x1b[1;31m" + label + "\x1b[0m"
	default:
		return label
	}
}

func doctorHeading(heading string, severity doctor.Severity, color bool) string {
	if color && severity == doctor.SeverityFail {
		return "\x1b[1;31m" + heading + "\x1b[0m"
	}
	return heading
}
