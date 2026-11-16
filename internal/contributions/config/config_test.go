package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/project-init/devex/internal/strictyaml"
)

func TestNewConfigFromYamlRejectsAMisspelledKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "contributions.yaml")
	if err := os.WriteFile(path, []byte("numLookBakDays: 30\norganization: octo-org\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := NewConfigFromYaml(path); err == nil || !strings.Contains(err.Error(), "numLookBakDays") {
		t.Errorf("NewConfigFromYaml() error = %v, want the misspelled key named", err)
	}
}

func TestNewConfigFromYamlRejectsAnEmptyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "contributions.yaml")
	if err := os.WriteFile(path, []byte("# fill in organization\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := NewConfigFromYaml(path); !errors.Is(err, strictyaml.ErrEmpty) {
		t.Errorf("NewConfigFromYaml(empty) error = %v, want ErrEmpty", err)
	}
}
