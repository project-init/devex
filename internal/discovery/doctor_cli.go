package discovery

import (
	"context"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/project-init/devex/internal/discovery/config"
	"github.com/project-init/devex/internal/discovery/doctor"
	"github.com/project-init/devex/internal/discovery/provider"
	"github.com/project-init/devex/internal/discovery/skill"
	"github.com/project-init/devex/internal/ui"
	"github.com/spf13/cobra"
)

func doctorCommand() *cobra.Command {
	var configurationPath string
	var harnessValues []string
	var online bool
	command := &cobra.Command{
		Use:         "doctor [project-directory]",
		Short:       "Check discovery prerequisites without changing the project",
		Args:        cobra.MaximumNArgs(1),
		Annotations: map[string]string{ui.JSONAnnotation: "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			u := ui.New(cmd)
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
			if online {
				err := u.Spin(cmd.Context(), "Checking each target online", func(ctx context.Context) error {
					report.Checks = append(report.Checks, onlineChecks(ctx, report.ConfigPath, func(target config.Target) (provider.Adapter, error) {
						return newAdapter(target, true)
					})...)
					return ctx.Err() // Ctrl-C is an interruption, not a failed check
				})
				if err != nil {
					return err
				}
			}
			return printDoctorReport(u, report)
		},
	}
	command.Flags().BoolVar(&online, "online", false, "also sign in to each target and check its project and issue types")
	command.Flags().StringVar(&configurationPath, "config", defaultConfigPath, "target configuration file")
	command.Flags().StringSliceVar(&harnessValues, "harness", []string{"all"}, "AI harness: codex, claude, cursor, or all")
	return command
}

// onlineChecks signs in to each configured target, confirms its project is reachable, and checks
// the issue types its configuration names exist there.
func onlineChecks(ctx context.Context, configPath string, adapterFor func(config.Target) (provider.Adapter, error)) []doctor.Check {
	configuration, err := config.Load(configPath)
	if err != nil {
		return nil // doctor's offline checks already report an unreadable configuration
	}
	var checks []doctor.Check
	for _, name := range slices.Sorted(maps.Keys(configuration.Targets)) {
		checks = append(checks, targetChecks(ctx, name, configuration.Targets[name], adapterFor)...)
	}
	return checks
}

func targetChecks(ctx context.Context, name string, target config.Target, adapterFor func(config.Target) (provider.Adapter, error)) []doctor.Check {
	access := doctor.Check{Name: "online:" + name, Severity: doctor.SeverityFail}
	adapter, err := adapterFor(target)
	if err != nil {
		access.Detail = err.Error()
		return []doctor.Check{access}
	}
	var checks []doctor.Check
	if checker, ok := adapter.(provider.AccessChecker); ok {
		if access.Detail, err = checker.CheckAccess(ctx, target); err != nil {
			access.Detail, access.Remedy = err.Error(), "check the target's credentials and that they can see its project"
			return []doctor.Check{access}
		}
		access.Severity = doctor.SeverityPass
		checks = append(checks, access)
	}
	checker, ok := adapter.(provider.IssueTypeChecker)
	if !ok {
		return checks
	}
	types := doctor.Check{Name: "issue-types:" + name, Severity: doctor.SeverityFail}
	switch missing, err := checker.MissingIssueTypes(ctx, target); {
	case err != nil:
		types.Detail = err.Error()
	case len(missing) > 0:
		types.Detail = "the project has no issue type " + strings.Join(missing, ", ")
		types.Remedy = "fix jira.kind_mapping to name issue types the project has"
	default:
		types.Severity, types.Detail = doctor.SeverityPass, "the project has every issue type kind_mapping names"
	}
	return append(checks, types)
}

func printDoctorReport(u *ui.UI, report doctor.Report) error {
	if u.JSON() {
		if err := u.WriteJSON(doctorJSON(report)); err != nil {
			return err
		}
		if report.FailureCount() > 0 {
			return fmt.Errorf("discovery doctor found %d problem(s)", report.FailureCount())
		}
		return nil
	}
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

func doctorJSON(report doctor.Report) map[string]any {
	return map[string]any{
		"ready":    report.FailureCount() == 0,
		"failures": report.FailureCount(),
		"flags":    report.FlagCount(),
		"warnings": report.WarningCount(),
		"checks":   report.Checks,
	}
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
