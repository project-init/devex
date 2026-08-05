package discovery

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadDefaultLabels(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.yaml")
	labels, err := loadDefaultLabels(missing)
	if err != nil {
		t.Fatal(err)
	}
	if labels != nil {
		t.Fatalf("missing config labels = %#v", labels)
	}

	path := filepath.Join(t.TempDir(), "discovery.yaml")
	content := `default_labels: [discovery, team-platform]
targets:
  github:
    provider: github
    github:
      owner: project-init
      repository: devex
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	labels, err = loadDefaultLabels(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(labels, ",") != "discovery,team-platform" {
		t.Fatalf("labels = %#v", labels)
	}
}
