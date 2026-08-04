package templates

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/project-init/devex/internal/discovery/artifact"
)

func TestGenerateCreatesValidBundle(t *testing.T) {
	directory, err := Generate(t.TempDir(), "Audit Logs")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(directory) != "audit-logs" {
		t.Fatalf("directory = %q", directory)
	}
	if _, err := artifact.Load(directory); err != nil {
		t.Fatalf("generated bundle is invalid: %v", err)
	}
	gitignore, err := os.ReadFile(filepath.Join(directory, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(gitignore), ".publish/") {
		t.Fatalf(".gitignore = %q", gitignore)
	}
	workBreakdown, err := os.ReadFile(filepath.Join(directory, "work-breakdown.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{
		"id:",
		"kind:",
		"parent:",
		"title:",
		"description:",
		"acceptance_criteria:",
		"depends_on:",
		"labels:",
		"estimate:",
		"value:",
		"unit:",
	} {
		if !strings.Contains(string(workBreakdown), field) {
			t.Errorf("work-breakdown.yaml does not expose %s", field)
		}
	}
}

func TestGenerateDoesNotOverwrite(t *testing.T) {
	base := t.TempDir()
	if _, err := Generate(base, "Audit Logs"); err != nil {
		t.Fatal(err)
	}
	if _, err := Generate(base, "Audit Logs"); err == nil {
		t.Fatal("second Generate() succeeded, want overwrite protection")
	}
}
