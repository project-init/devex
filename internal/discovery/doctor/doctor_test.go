package doctor

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/project-init/devex/internal/discovery/config"
	"github.com/project-init/devex/internal/discovery/skill"
	"github.com/project-init/gommon/pkg/githubclient"
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
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	relativeProject, err := filepath.Rel(workingDirectory, project)
	if err != nil {
		t.Fatal(err)
	}
	wantRemedy := fmt.Sprintf("devex discovery install-skill %s --harness codex", shellQuote(relativeProject))
	var gotRemedy string
	for _, check := range report.Checks {
		if check.Name == "skill:codex" {
			gotRemedy = check.Remedy
		}
	}
	if gotRemedy != wantRemedy {
		t.Fatalf("skill remedy = %q, want %q", gotRemedy, wantRemedy)
	}
}

func TestRunFormatsModifiedSkillRemedyWithProjectBeforeFlags(t *testing.T) {
	project := t.TempDir()
	if _, err := skill.Install(project, []skill.Harness{skill.HarnessCodex}, false); err != nil {
		t.Fatal(err)
	}
	skillPath := filepath.Join(project, ".agents", "skills", "run-discovery", "SKILL.md")
	if err := os.WriteFile(skillPath, []byte("modified\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	report, err := Run(project, filepath.Join(".sre", "discovery.yaml"), []skill.Harness{skill.HarnessCodex}, emptyEnv)
	if err != nil {
		t.Fatal(err)
	}
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	relativeProject, err := filepath.Rel(workingDirectory, project)
	if err != nil {
		t.Fatal(err)
	}
	wantRemedy := fmt.Sprintf(
		"review the differences, then run devex discovery install-skill %s --harness codex --force",
		shellQuote(relativeProject),
	)
	var gotRemedy string
	for _, check := range report.Checks {
		if check.Name == "skill:codex" {
			gotRemedy = check.Remedy
		}
	}
	if gotRemedy != wantRemedy {
		t.Fatalf("skill remedy = %q, want %q", gotRemedy, wantRemedy)
	}
}

func TestRunFailsWhenNoHarnessHasTheSkill(t *testing.T) {
	project := t.TempDir()
	report, err := Run(project, filepath.Join(".sre", "discovery.yaml"), skill.AllHarnesses(), emptyEnv)
	if err != nil {
		t.Fatal(err)
	}
	if report.FailureCount() != 4 {
		t.Fatalf("failures = %d, checks = %#v", report.FailureCount(), report.Checks)
	}
	if report.FlagCount() != 0 {
		t.Fatalf("flags = %d, checks = %#v", report.FlagCount(), report.Checks)
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
	report, err = Run(project, configurationPath, skill.AllHarnesses(), func(name string) (string, bool) {
		value, exists := environment[name]
		return value, exists
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.FailureCount() != 0 || report.WarningCount() != 0 {
		t.Fatalf("report = %#v", report)
	}
	if report.FlagCount() != 2 {
		t.Fatalf("flags = %d, checks = %#v", report.FlagCount(), report.Checks)
	}
}

func TestMain(m *testing.M) {
	githubAuth = func() error { return githubclient.ErrAuthUnavailable }
	os.Exit(m.Run())
}

func TestGitHubCredentialCheck(t *testing.T) {
	previous := githubAuth
	t.Cleanup(func() { githubAuth = previous })

	configuration := &config.File{
		Targets: map[string]config.Target{
			"github-project": {Provider: "github"},
		},
	}

	tests := []struct {
		name     string
		token    string
		authErr  error
		detail   string
		remedy   string
		severity Severity
	}{
		{
			name:     "token",
			token:    "set",
			detail:   "GITHUB_TOKEN is set",
			severity: SeverityPass,
		},
		{
			name:     "cli",
			detail:   "GitHub CLI auth is available",
			severity: SeverityPass,
		},
		{
			name:     "missing",
			authErr:  githubclient.ErrAuthUnavailable,
			detail:   "GitHub auth is not available; planning still works, but publish apply requires it",
			remedy:   "run `gh auth login` or set GITHUB_TOKEN before publishing",
			severity: SeverityWarn,
		},
		{
			name:     "timeout",
			authErr:  githubclient.ErrAuthTimedOut,
			detail:   "gh auth token timed out; planning still works, but publish apply requires it",
			remedy:   "set GITHUB_TOKEN, or retry after the gh prompt finishes",
			severity: SeverityWarn,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			githubAuth = func() error { return test.authErr }
			report := Report{}
			appendCredentialChecks(&report, configuration, func(name string) (string, bool) {
				if name == "GITHUB_TOKEN" && test.token != "" {
					return test.token, true
				}
				return "", false
			})
			if len(report.Checks) != 1 {
				t.Fatalf("checks = %#v", report.Checks)
			}
			check := report.Checks[0]
			if check.Name != "credentials:github" || check.Severity != test.severity || check.Detail != test.detail || check.Remedy != test.remedy {
				t.Fatalf("check = %#v", check)
			}
		})
	}
}

func emptyEnv(string) (string, bool) { return "", false }

func TestShellQuote(t *testing.T) {
	if got, want := shellQuote("a'b $HOME"), `'a'"'"'b $HOME'`; got != want {
		t.Fatalf("shellQuote() = %q, want %q", got, want)
	}
}

func TestRelativeProjectArgumentUsesCurrentDirectory(t *testing.T) {
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if got := relativeProjectArgument(workingDirectory); got != "." {
		t.Fatalf("relativeProjectArgument() = %q, want .", got)
	}
}
