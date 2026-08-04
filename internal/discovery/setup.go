package discovery

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/project-init/devex/internal/discovery/config"
	"github.com/project-init/devex/internal/discovery/doctor"
	"github.com/project-init/devex/internal/discovery/skill"
	"github.com/spf13/cobra"
)

func setupCommand() *cobra.Command {
	var configurationPath string
	var harnessValues []string
	var yes bool
	command := &cobra.Command{
		Use:   "setup [project-directory]",
		Short: "Interactively set up discovery in a consuming project",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			projectDirectory := "."
			if len(args) == 1 {
				projectDirectory = args[0]
			}
			return runSetup(cmd, projectDirectory, configurationPath, harnessValues, yes)
		},
	}
	command.Flags().StringVar(&configurationPath, "config", defaultConfigPath, "target configuration file")
	command.Flags().StringSliceVar(&harnessValues, "harness", nil, "AI harness: codex, claude, cursor, or all")
	command.Flags().BoolVarP(&yes, "yes", "y", false, "install missing skill and configuration files without prompting")
	return command
}

func runSetup(
	cmd *cobra.Command,
	projectDirectory string,
	configurationPath string,
	harnessValues []string,
	yes bool,
) error {
	projectRoot, err := filepath.Abs(projectDirectory)
	if err != nil {
		return fmt.Errorf("resolve project directory %s: %w", projectDirectory, err)
	}
	interactive := isInteractive(cmd.InOrStdin(), cmd.OutOrStdout())
	prompts := &promptSession{reader: bufio.NewReader(cmd.InOrStdin()), output: cmd.OutOrStdout()}
	harnesses, err := setupHarnesses(projectRoot, harnessValues, yes, interactive, prompts)
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
		install := yes
		if interactive && !yes {
			install, err = prompts.confirm("Install the missing run-discovery skill for " + harnessList(missing) + "?")
			if err != nil {
				return err
			}
		}
		if install {
			results, err := skill.Install(projectRoot, missing, false)
			if err != nil {
				return err
			}
			printInstallResults(cmd.OutOrStdout(), results)
		}
	}
	if len(modified) > 0 && interactive {
		replace, err := prompts.confirm("Installed skill files differ from this devex release for " + harnessList(modified) + ". Replace them?")
		if err != nil {
			return err
		}
		if replace {
			results, err := skill.Install(projectRoot, modified, true)
			if err != nil {
				return err
			}
			printInstallResults(cmd.OutOrStdout(), results)
		}
	}
	resolvedConfigurationPath := configurationPath
	if !filepath.IsAbs(resolvedConfigurationPath) {
		resolvedConfigurationPath = filepath.Join(projectRoot, resolvedConfigurationPath)
	}
	if _, statErr := os.Stat(resolvedConfigurationPath); errors.Is(statErr, os.ErrNotExist) {
		create := yes
		if interactive && !yes {
			create, err = prompts.confirm("Create " + resolvedConfigurationPath + "?")
			if err != nil {
				return err
			}
		}
		if create {
			if err := config.WriteExample(resolvedConfigurationPath, false); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "CREATED discovery configuration: %s\n", resolvedConfigurationPath)
		}
	} else if statErr != nil {
		return fmt.Errorf("inspect discovery configuration %s: %w", resolvedConfigurationPath, statErr)
	}
	report, err := doctor.Run(projectRoot, resolvedConfigurationPath, harnesses, os.LookupEnv)
	if err != nil {
		return err
	}
	return printDoctorReport(cmd.OutOrStdout(), report)
}

func setupHarnesses(
	projectRoot string,
	values []string,
	yes bool,
	interactive bool,
	prompts *promptSession,
) ([]skill.Harness, error) {
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
	if yes {
		return skill.AllHarnesses(), nil
	}
	if interactive {
		return prompts.chooseHarnesses()
	}
	return skill.AllHarnesses(), nil
}

type promptSession struct {
	reader *bufio.Reader
	output io.Writer
}

func (p *promptSession) confirm(question string) (bool, error) {
	_, _ = fmt.Fprintf(p.output, "%s [y/N] ", question)
	answer, err := p.reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	answer = strings.ToLower(strings.TrimSpace(answer))
	return answer == "y" || answer == "yes", nil
}

func (p *promptSession) chooseHarnesses() ([]skill.Harness, error) {
	_, _ = fmt.Fprint(p.output, "AI harnesses to install [all/codex/claude/cursor] (all): ")
	answer, err := p.reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	answer = strings.TrimSpace(answer)
	if answer == "" {
		answer = "all"
	}
	return skill.ParseHarnesses([]string{answer})
}

func isInteractive(input io.Reader, output io.Writer) bool {
	inputFile, inputOK := input.(*os.File)
	outputFile, outputOK := output.(*os.File)
	if !inputOK || !outputOK {
		return false
	}
	inputInfo, inputErr := inputFile.Stat()
	outputInfo, outputErr := outputFile.Stat()
	return inputErr == nil && outputErr == nil &&
		inputInfo.Mode()&os.ModeCharDevice != 0 && outputInfo.Mode()&os.ModeCharDevice != 0
}

func harnessList(harnesses []skill.Harness) string {
	values := make([]string, 0, len(harnesses))
	for _, harness := range harnesses {
		values = append(values, string(harness))
	}
	return strings.Join(values, ", ")
}

func printInstallResults(output io.Writer, results []skill.InstallResult) {
	for _, result := range results {
		_, _ = fmt.Fprintf(
			output,
			"%s run-discovery skill for %s: %s\n",
			strings.ToUpper(string(result.State)),
			result.Harness,
			result.Path,
		)
	}
}
