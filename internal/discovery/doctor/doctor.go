package doctor

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/project-init/devex/internal/discovery/config"
	"github.com/project-init/devex/internal/discovery/skill"
)

type Severity string

const (
	SeverityPass Severity = "PASS"
	SeverityFlag Severity = "FLAG"
	SeverityWarn Severity = "WARN"
	SeverityFail Severity = "FAIL"
)

type Check struct {
	Severity Severity
	Name     string
	Detail   string
	Remedy   string
}

type Report struct {
	ProjectRoot string
	ConfigPath  string
	Checks      []Check
}

func (r Report) FailureCount() int {
	count := 0
	for _, check := range r.Checks {
		if check.Severity == SeverityFail {
			count++
		}
	}
	return count
}

func (r Report) WarningCount() int {
	count := 0
	for _, check := range r.Checks {
		if check.Severity == SeverityWarn {
			count++
		}
	}
	return count
}

func (r Report) FlagCount() int {
	count := 0
	for _, check := range r.Checks {
		if check.Severity == SeverityFlag {
			count++
		}
	}
	return count
}

func Run(
	projectDirectory string,
	configurationPath string,
	harnesses []skill.Harness,
	lookupEnv func(string) (string, bool),
) (Report, error) {
	projectRoot, err := filepath.Abs(projectDirectory)
	if err != nil {
		return Report{}, fmt.Errorf("resolve project directory %s: %w", projectDirectory, err)
	}
	info, err := os.Stat(projectRoot)
	if err != nil {
		return Report{}, fmt.Errorf("inspect project directory %s: %w", projectRoot, err)
	}
	if !info.IsDir() {
		return Report{}, fmt.Errorf("project directory %s is not a directory", projectRoot)
	}
	if !filepath.IsAbs(configurationPath) {
		configurationPath = filepath.Join(projectRoot, configurationPath)
	}
	report := Report{ProjectRoot: projectRoot, ConfigPath: configurationPath}
	report.Checks = append(report.Checks, Check{
		Severity: SeverityPass,
		Name:     "cli",
		Detail:   "devex discovery is available",
	})
	report.Checks = append(report.Checks, Check{
		Severity: SeverityPass,
		Name:     "project",
		Detail:   projectRoot,
	})
	if _, err := os.Stat(filepath.Join(projectRoot, ".git")); err == nil {
		report.Checks = append(report.Checks, Check{Severity: SeverityPass, Name: "git", Detail: "Git metadata found"})
	} else if os.IsNotExist(err) {
		report.Checks = append(report.Checks, Check{
			Severity: SeverityWarn,
			Name:     "git",
			Detail:   "project root does not contain .git metadata",
			Remedy:   "run doctor from the repository root or initialize Git",
		})
	} else {
		return Report{}, fmt.Errorf("inspect Git metadata: %w", err)
	}
	statuses, err := skill.Inspect(projectRoot, harnesses)
	if err != nil {
		return Report{}, err
	}
	projectArgument := relativeProjectArgument(projectRoot)
	hasInstalledSkill := false
	for _, status := range statuses {
		if status.State != skill.StateMissing {
			hasInstalledSkill = true
			break
		}
	}
	for _, status := range statuses {
		check := Check{Name: "skill:" + string(status.Harness), Detail: status.Path}
		switch status.State {
		case skill.StateCurrent:
			check.Severity = SeverityPass
		case skill.StateMissing:
			if hasInstalledSkill {
				check.Severity = SeverityFlag
				check.Detail = "run-discovery is not installed for this additional harness at " + status.Path
			} else {
				check.Severity = SeverityFail
				check.Detail = "run-discovery is not installed at " + status.Path
			}
			check.Remedy = fmt.Sprintf("devex discovery install-skill %s --harness %s", projectArgument, status.Harness)
		case skill.StateModified:
			check.Severity = SeverityFail
			check.Detail = "installed files differ from the bundled run-discovery skill at " + status.Path
			check.Remedy = fmt.Sprintf(
				"review the differences, then run devex discovery install-skill %s --harness %s --force",
				projectArgument,
				status.Harness,
			)
		default:
			return Report{}, fmt.Errorf("unsupported skill state %q", status.State)
		}
		report.Checks = append(report.Checks, check)
	}
	configuration, loadErr := config.Load(configurationPath)
	if loadErr != nil {
		check := Check{Severity: SeverityFail, Name: "configuration", Detail: loadErr.Error()}
		if errors.Is(loadErr, os.ErrNotExist) {
			check.Remedy = "devex discovery config init " + shellQuote(configurationPath)
		} else {
			check.Remedy = "correct the discovery target configuration"
		}
		report.Checks = append(report.Checks, check)
		return report, nil
	}
	configurationDetail := fmt.Sprintf("%s defines %d target(s)", configurationPath, len(configuration.Targets))
	if configuration.DefaultTarget != "" {
		configurationDetail += "; default is " + configuration.DefaultTarget
	}
	if len(configuration.DefaultLabels) > 0 {
		configurationDetail += fmt.Sprintf("; %d default label(s)", len(configuration.DefaultLabels))
	}
	report.Checks = append(report.Checks, Check{
		Severity: SeverityPass,
		Name:     "configuration",
		Detail:   configurationDetail,
	})
	targetNames := make([]string, 0, len(configuration.Targets))
	for name := range configuration.Targets {
		targetNames = append(targetNames, name)
	}
	sort.Strings(targetNames)
	for _, name := range targetNames {
		target := configuration.Targets[name]
		if placeholders := placeholderFields(target); len(placeholders) > 0 {
			report.Checks = append(report.Checks, Check{
				Severity: SeverityFail,
				Name:     "target:" + name,
				Detail:   "example values remain in " + strings.Join(placeholders, ", "),
				Remedy:   "replace the example values in " + configurationPath,
			})
		} else {
			report.Checks = append(report.Checks, Check{
				Severity: SeverityPass,
				Name:     "target:" + name,
				Detail:   target.Provider + " target is configured",
			})
		}
	}
	appendCredentialChecks(&report, configuration, lookupEnv)
	return report, nil
}

func appendCredentialChecks(report *Report, configuration *config.File, lookupEnv func(string) (string, bool)) {
	providers := make(map[string]bool)
	for _, target := range configuration.Targets {
		providers[strings.ToLower(target.Provider)] = true
	}
	if providers["github"] {
		if value, exists := lookupEnv("GITHUB_TOKEN"); exists && value != "" {
			report.Checks = append(report.Checks, Check{Severity: SeverityPass, Name: "credentials:github", Detail: "GITHUB_TOKEN is set"})
		} else {
			report.Checks = append(report.Checks, Check{
				Severity: SeverityWarn,
				Name:     "credentials:github",
				Detail:   "GITHUB_TOKEN is not set; planning still works, but publish apply requires it",
				Remedy:   "set GITHUB_TOKEN in the environment before publishing",
			})
		}
	}
	if providers["jira"] {
		email, emailSet := lookupEnv("JIRA_EMAIL")
		key, keySet := lookupEnv("JIRA_API_KEY")
		if emailSet && email != "" && keySet && key != "" {
			report.Checks = append(report.Checks, Check{Severity: SeverityPass, Name: "credentials:jira", Detail: "JIRA_EMAIL and JIRA_API_KEY are set"})
		} else {
			report.Checks = append(report.Checks, Check{
				Severity: SeverityWarn,
				Name:     "credentials:jira",
				Detail:   "JIRA_EMAIL and JIRA_API_KEY are required by publish apply",
				Remedy:   "set JIRA_EMAIL and JIRA_API_KEY in the environment before publishing",
			})
		}
	}
}

func placeholderFields(target config.Target) []string {
	var fields []string
	if target.GitHub != nil {
		if target.GitHub.Owner == "your-organization" {
			fields = append(fields, "github.owner")
		}
		if target.GitHub.Repository == "your-repository" {
			fields = append(fields, "github.repository")
		}
	}
	if target.Jira != nil {
		if target.Jira.ProjectKey == "PROJECT" {
			fields = append(fields, "jira.project_key")
		}
		if strings.Contains(target.Jira.BaseURL, "example.atlassian.net") {
			fields = append(fields, "jira.base_url")
		}
	}
	return fields
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func relativeProjectArgument(projectRoot string) string {
	workingDirectory, err := os.Getwd()
	if err != nil {
		return shellQuote(projectRoot)
	}
	relative, err := filepath.Rel(workingDirectory, projectRoot)
	if err != nil {
		return shellQuote(projectRoot)
	}
	if relative == "." {
		return relative
	}
	return shellQuote(relative)
}
