package config

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadStrictConfiguration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "discovery.yaml")
	content := `default_target: github-devex
default_labels:
  - discovery
  - team-platform
targets:
  github-devex:
    provider: github
    github:
      owner: project-init
      repository: devex
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	configuration, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if configuration.Targets["github-devex"].GitHub.Repository != "devex" {
		t.Fatalf("configuration = %#v", configuration)
	}
	if configuration.DefaultTarget != "github-devex" {
		t.Fatalf("DefaultTarget = %q", configuration.DefaultTarget)
	}
	if strings.Join(configuration.DefaultLabels, ",") != "discovery,team-platform" {
		t.Fatalf("DefaultLabels = %#v", configuration.DefaultLabels)
	}

	if err := os.WriteFile(path, []byte(strings.Replace(content, "owner:", "unknown:\n      owner:", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("Load() accepted an unknown field")
	}
}

func TestLoadRejectsInvalidDefaultLabels(t *testing.T) {
	tests := []struct {
		name      string
		labels    string
		wantError string
	}{
		{name: "empty", labels: "  - '   '", wantError: "default_labels[0] must not be empty"},
		{name: "duplicate", labels: "  - discovery\n  - discovery", wantError: `default_labels contains duplicate label "discovery"`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "discovery.yaml")
			content := "default_labels:\n" + test.labels + `
targets:
  github-devex:
    provider: github
    github:
      owner: project-init
      repository: devex
`
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := Load(path)
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("Load() error = %v, want containing %q", err, test.wantError)
			}
		})
	}
}

func TestLoadRejectsUnknownDefaultTarget(t *testing.T) {
	path := filepath.Join(t.TempDir(), "discovery.yaml")
	content := `default_target: missing
targets:
  github-devex:
    provider: github
    github:
      owner: project-init
      repository: devex
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), `default_target "missing" is not defined`) {
		t.Fatalf("Load() error = %v", err)
	}
}

func TestResolveTarget(t *testing.T) {
	github := Target{Provider: "github"}
	jira := Target{Provider: "jira"}
	tests := []struct {
		name          string
		configuration File
		explicit      string
		wantName      string
		wantProvider  string
		wantError     string
	}{
		{
			name:          "explicit target overrides default",
			configuration: File{DefaultTarget: "github", Targets: map[string]Target{"github": github, "jira": jira}},
			explicit:      "jira",
			wantName:      "jira",
			wantProvider:  "jira",
		},
		{
			name:          "configured default",
			configuration: File{DefaultTarget: "github", Targets: map[string]Target{"github": github, "jira": jira}},
			wantName:      "github",
			wantProvider:  "github",
		},
		{
			name:          "sole target",
			configuration: File{Targets: map[string]Target{"jira": jira}},
			wantName:      "jira",
			wantProvider:  "jira",
		},
		{
			name:          "unknown explicit target",
			configuration: File{Targets: map[string]Target{"github": github, "jira": jira}},
			explicit:      "plane",
			wantError:     `target "plane" is not defined; available targets: github, jira`,
		},
		{
			name:          "ambiguous targets",
			configuration: File{Targets: map[string]Target{"jira": jira, "github": github}},
			wantError:     "multiple publication targets are configured (github, jira); pass --target or set default_target",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			name, target, err := test.configuration.ResolveTarget(test.explicit)
			if test.wantError != "" {
				if err == nil || err.Error() != test.wantError {
					t.Fatalf("ResolveTarget() error = %v, want %q", err, test.wantError)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if name != test.wantName || target.Provider != test.wantProvider {
				t.Fatalf("ResolveTarget() = %q, %#v", name, target)
			}
		})
	}
}

func TestWriteExampleCreatesLoadableConfigurationWithoutOverwriting(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".sre", "discovery.yaml")
	if err := WriteExample(path, false); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err != nil {
		t.Fatalf("generated configuration is invalid: %v", err)
	}
	if err := os.WriteFile(path, []byte("custom: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteExample(path, false); err == nil {
		t.Fatal("WriteExample() overwrote a different existing file")
	}
	if err := WriteExample(path, true); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err != nil {
		t.Fatalf("forced configuration is invalid: %v", err)
	}
}

func TestEmbeddedExampleMatchesContributorExample(t *testing.T) {
	embedded, err := examples.ReadFile("example.yaml")
	if err != nil {
		t.Fatal(err)
	}
	repositoryPath := filepath.Join("..", "..", "..", "cmd", "devex", "discovery", "example_config.yaml")
	repository, err := os.ReadFile(repositoryPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(embedded, repository) {
		t.Fatalf("embedded example differs from %s", repositoryPath)
	}
}
