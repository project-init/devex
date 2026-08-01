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
	content := `targets:
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

	if err := os.WriteFile(path, []byte(strings.Replace(content, "owner:", "unknown:\n      owner:", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("Load() accepted an unknown field")
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
