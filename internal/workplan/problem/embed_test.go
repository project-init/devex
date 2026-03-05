package problem

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateProblemTemplate_CreatesFile(t *testing.T) {
	// t.TempDir() creates a unique temporary directory under the OS temp location
	// (e.g. /var/folders/... on macOS) and automatically cleans it up when the test finishes.
	tmpDir := t.TempDir()
	outputPath := filepath.Join(tmpDir, "problem.md")

	err := GenerateProblemTemplate(outputPath)
	if err != nil {
		t.Fatalf("GenerateProblemTemplate returned error: %v", err)
	}

	content, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("failed to read generated file: %v", err)
	}

	if len(content) == 0 {
		t.Fatal("generated problem.md is empty")
	}
}

func TestGenerateProblemTemplate_ContainsExpectedContent(t *testing.T) {
	tmpDir := t.TempDir()
	outputPath := filepath.Join(tmpDir, "problem.md")

	err := GenerateProblemTemplate(outputPath)
	if err != nil {
		t.Fatalf("GenerateProblemTemplate returned error: %v", err)
	}

	content, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("failed to read generated file: %v", err)
	}

	problemStr := string(content)

	expectedContent := []string{
		"# [Problem Name]",
		"## Who is Impacted?",
		"## Useful Context",
		"## Suggested Solution",
		"## Knowns",
		"## Assumptions",
		"## Unknowns",
	}

	for _, expected := range expectedContent {
		if !strings.Contains(problemStr, expected) {
			t.Errorf("problem.md missing expected content %q", expected)
		}
	}
}

func TestGenerateProblemTemplate_CreatesIntermediateDirectories(t *testing.T) {
	tmpDir := t.TempDir()
	outputPath := filepath.Join(tmpDir, "nested", "dirs", "problem.md")

	err := GenerateProblemTemplate(outputPath)
	if err != nil {
		t.Fatalf("GenerateProblemTemplate returned error: %v", err)
	}

	if _, err := os.Stat(outputPath); os.IsNotExist(err) {
		t.Fatal("expected file to exist at nested path")
	}
}
