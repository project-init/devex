package config

import (
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
