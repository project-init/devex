package doctor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/project-init/devex/internal/discovery/config"
	"github.com/project-init/devex/internal/discovery/skill"
)

func TestRunReportsMissingPrerequisites(t *testing.T) {
	project := t.TempDir()
	report, err := Run(project, filepath.Join(".sre", "discovery.yaml"), []skill.Harness{skill.HarnessCodex}, emptyEnv)
	if err != nil {
		t.Fatal(err)
	}
	if report.FailureCount() != 2 {
		t.Fatalf("failures = %d, checks = %#v", report.FailureCount(), report.Checks)
	}
	if report.WarningCount() != 1 {
		t.Fatalf("warnings = %d, checks = %#v", report.WarningCount(), report.Checks)
	}
	for _, check := range report.Checks {
		if check.Name == "skill:codex" && !strings.Contains(check.Remedy, project) {
			t.Fatalf("skill remedy does not target project: %q", check.Remedy)
		}
	}
}

func TestRunValidatesConfigurationSkillsAndCredentials(t *testing.T) {
	project := t.TempDir()
	if err := os.Mkdir(filepath.Join(project, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := skill.Install(project, []skill.Harness{skill.HarnessCodex}, false); err != nil {
		t.Fatal(err)
	}
	configurationPath := filepath.Join(project, ".sre", "discovery.yaml")
	if err := config.WriteExample(configurationPath, false); err != nil {
		t.Fatal(err)
	}
	report, err := Run(project, configurationPath, []skill.Harness{skill.HarnessCodex}, emptyEnv)
	if err != nil {
		t.Fatal(err)
	}
	if report.FailureCount() != 2 {
		t.Fatalf("placeholder failures = %d, checks = %#v", report.FailureCount(), report.Checks)
	}
	configured := `targets:
  github-project:
    provider: github
    github:
      owner: project-init
      repository: product
  jira-project:
    provider: jira
    jira:
      base_url: https://company.atlassian.net
      project_key: PRODUCT
      kind_mapping:
        initiative: Epic
`
	if err := os.WriteFile(configurationPath, []byte(configured), 0o644); err != nil {
		t.Fatal(err)
	}
	environment := map[string]string{
		"GITHUB_TOKEN": "set",
		"JIRA_EMAIL":   "set",
		"JIRA_API_KEY": "set",
	}
	report, err = Run(project, configurationPath, []skill.Harness{skill.HarnessCodex}, func(name string) (string, bool) {
		value, exists := environment[name]
		return value, exists
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.FailureCount() != 0 || report.WarningCount() != 0 {
		t.Fatalf("report = %#v", report)
	}
}

func emptyEnv(string) (string, bool) { return "", false }

func TestShellQuote(t *testing.T) {
	if got, want := shellQuote("a'b $HOME"), `'a'"'"'b $HOME'`; got != want {
		t.Fatalf("shellQuote() = %q, want %q", got, want)
	}
}
