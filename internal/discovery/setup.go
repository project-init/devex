package discovery

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/project-init/devex/internal/discovery/config"
	"github.com/project-init/devex/internal/discovery/doctor"
	"github.com/project-init/devex/internal/discovery/skill"
	"github.com/project-init/devex/internal/ui"
	"github.com/spf13/cobra"
)

const harnessFlag = "harness"

func setupCommand() *cobra.Command {
	var configurationPath string
	var harnessValues []string
	command := &cobra.Command{
		Use:   "setup [project-directory]",
		Short: "Interactively set up discovery in a consuming project",
		Long: "Install the run-discovery skill and discovery configuration, then report readiness. " +
			"--yes installs missing files without prompting; --harness picks the AI harnesses.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			projectDirectory := "."
			if len(args) == 1 {
				projectDirectory = args[0]
			}
			return runSetup(cmd, projectDirectory, configurationPath, harnessValues)
		},
	}
	command.Flags().StringVar(&configurationPath, "config", defaultConfigPath, "target configuration file")
	command.Flags().StringSliceVar(&harnessValues, harnessFlag, nil, "AI harness: codex, claude, cursor, or all")
	return command
}

func runSetup(cmd *cobra.Command, projectDirectory string, configurationPath string, harnessValues []string) error {
	u := ui.New(cmd)
	ctx := cmd.Context()
	projectRoot, err := filepath.Abs(projectDirectory)
	if err != nil {
		return fmt.Errorf("resolve project directory %s: %w", projectDirectory, err)
	}
	harnesses, err := setupHarnesses(ctx, u, projectRoot, harnessValues)
	if err != nil {
		return err
	}
	statuses, err := skill.Inspect(projectRoot, harnesses)
	if err != nil {
		return err
	}
	var missing []skill.Harness
	var modified []skill.Harness
	for _, status := range statuses {
		switch status.State {
		case skill.StateMissing:
			missing = append(missing, status.Harness)
		case skill.StateModified:
			modified = append(modified, status.Harness)
		}
	}
	if len(missing) > 0 {
		install, err := u.Confirm(ctx, "Install the missing run-discovery skill for "+harnessList(missing)+"?")
		if err != nil {
			return err
		}
		if install {
			results, err := skill.Install(projectRoot, missing, false)
			if err != nil {
				return err
			}
			printInstallResults(u, results)
		}
	}
	if len(modified) > 0 {
		if err := replaceModifiedSkills(ctx, u, projectRoot, modified); err != nil {
			return err
		}
	}
	resolvedConfigurationPath := configurationPath
	if !filepath.IsAbs(resolvedConfigurationPath) {
		resolvedConfigurationPath = filepath.Join(projectRoot, resolvedConfigurationPath)
	}
	if _, statErr := os.Stat(resolvedConfigurationPath); errors.Is(statErr, os.ErrNotExist) {
		create, err := u.Confirm(ctx, "Create "+resolvedConfigurationPath+"?")
		if err != nil {
			return err
		}
		if create {
			if err := config.WriteExample(resolvedConfigurationPath, false); err != nil {
				return err
			}
			u.Success("Created discovery configuration: %s", resolvedConfigurationPath)
		}
	} else if statErr != nil {
		return fmt.Errorf("inspect discovery configuration %s: %w", resolvedConfigurationPath, statErr)
	}
	report, err := doctor.Run(projectRoot, resolvedConfigurationPath, harnesses, os.LookupEnv)
	if err != nil {
		return err
	}
	return printDoctorReport(u, report)
}

// replaceModifiedSkills offers to overwrite skill files the project edited. --yes installs
// missing files only, so replacing edited ones always asks, and only on a terminal.
func replaceModifiedSkills(ctx context.Context, u *ui.UI, projectRoot string, modified []skill.Harness) error {
	if !u.Interactive() || u.Yes() {
		u.Warn("installed skill files differ from this devex release for %s; run `devex discovery install-skill %s --harness %s --force` to replace them",
			harnessList(modified), projectRoot, strings.ReplaceAll(harnessList(modified), " ", ""))
		return nil
	}
	replace, err := u.Confirm(ctx, "Installed skill files differ from this devex release for "+harnessList(modified)+". Replace them?")
	if err != nil || !replace {
		return err
	}
	results, err := skill.Install(projectRoot, modified, true)
	if err != nil {
		return err
	}
	printInstallResults(u, results)
	return nil
}

func setupHarnesses(ctx context.Context, u *ui.UI, projectRoot string, values []string) ([]skill.Harness, error) {
	if len(values) > 0 {
		return skill.ParseHarnesses(values)
	}
	detected, err := skill.DetectHarnesses(projectRoot)
	if err != nil {
		return nil, err
	}
	if len(detected) > 0 {
		return detected, nil
	}
	if u.Yes() {
		return skill.AllHarnesses(), nil
	}
	choice, err := u.Select(ctx, "AI harnesses to install", []ui.Option{
		{Label: "all", Value: "all"},
		{Label: "codex", Value: string(skill.HarnessCodex)},
		{Label: "claude", Value: string(skill.HarnessClaude)},
		{Label: "cursor", Value: string(skill.HarnessCursor)},
	}, "all", harnessFlag)
	if err != nil {
		return nil, err
	}
	return skill.ParseHarnesses([]string{choice})
}

func harnessList(harnesses []skill.Harness) string {
	values := make([]string, 0, len(harnesses))
	for _, harness := range harnesses {
		values = append(values, string(harness))
	}
	return strings.Join(values, ", ")
}

func printInstallResults(u *ui.UI, results []skill.InstallResult) {
	for _, result := range results {
		u.Success("%s run-discovery skill for %s: %s", strings.ToUpper(string(result.State)), result.Harness, result.Path)
	}
}
