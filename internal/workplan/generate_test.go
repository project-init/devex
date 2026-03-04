package workplan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateFiles_CreatesWorkplanAndProblemFiles(t *testing.T) {
	tmpDir := t.TempDir()

	err := GenerateFiles(tmpDir, "test_feature")
	if err != nil {
		t.Fatalf("GenerateFiles returned error: %v", err)
	}

	// Find the generated directory (dated name)
	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		t.Fatalf("failed to read temp dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 directory, got %d", len(entries))
	}

	generatedDir := entries[0].Name()
	if !strings.HasSuffix(generatedDir, "_test_feature") {
		t.Errorf("expected directory name to end with '_test_feature', got %q", generatedDir)
	}

	// Verify problem.md exists and has content
	problemPath := filepath.Join(tmpDir, generatedDir, "problem.md")
	problemContent, err := os.ReadFile(problemPath)
	if err != nil {
		t.Fatalf("failed to read problem.md: %v", err)
	}
	if len(problemContent) == 0 {
		t.Error("problem.md is empty")
	}
	if !strings.Contains(string(problemContent), "Problem Name") {
		t.Error("problem.md does not contain expected template content")
	}

	// Verify workplan.yaml exists and has content
	workplanPath := filepath.Join(tmpDir, generatedDir, "workplan.yaml")
	workplanContent, err := os.ReadFile(workplanPath)
	if err != nil {
		t.Fatalf("failed to read workplan.yaml: %v", err)
	}
	if len(workplanContent) == 0 {
		t.Error("workplan.yaml is empty")
	}
	if !strings.Contains(string(workplanContent), "epics:") {
		t.Error("workplan.yaml does not contain expected 'epics:' section")
	}
	if !strings.Contains(string(workplanContent), "project: INIT") {
		t.Error("workplan.yaml does not contain expected 'project: INIT' field")
	}
}

func TestGenerateFiles_WorkplanContainsRequiredStructure(t *testing.T) {
	tmpDir := t.TempDir()

	err := GenerateFiles(tmpDir, "structured_plan")
	if err != nil {
		t.Fatalf("GenerateFiles returned error: %v", err)
	}

	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		t.Fatalf("failed to read temp dir: %v", err)
	}

	generatedDir := entries[0].Name()
	workplanPath := filepath.Join(tmpDir, generatedDir, "workplan.yaml")
	content, err := os.ReadFile(workplanPath)
	if err != nil {
		t.Fatalf("failed to read workplan.yaml: %v", err)
	}

	workplanStr := string(content)
	requiredFields := []string{"jiraIssue:", "project:", "epics:", "summary:", "description:", "tasks:"}
	for _, field := range requiredFields {
		if !strings.Contains(workplanStr, field) {
			t.Errorf("workplan.yaml missing required field %q", field)
		}
	}
}

func TestGenerateFiles_ProblemContainsRequiredSections(t *testing.T) {
	tmpDir := t.TempDir()

	err := GenerateFiles(tmpDir, "problem_sections")
	if err != nil {
		t.Fatalf("GenerateFiles returned error: %v", err)
	}

	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		t.Fatalf("failed to read temp dir: %v", err)
	}

	generatedDir := entries[0].Name()
	problemPath := filepath.Join(tmpDir, generatedDir, "problem.md")
	content, err := os.ReadFile(problemPath)
	if err != nil {
		t.Fatalf("failed to read problem.md: %v", err)
	}

	problemStr := string(content)
	requiredSections := []string{
		"## Who is Impacted?",
		"## Useful Context",
		"## Suggested Solution",
		"## Knowns",
		"## Assumptions",
		"## Unknowns",
	}
	for _, section := range requiredSections {
		if !strings.Contains(problemStr, section) {
			t.Errorf("problem.md missing required section %q", section)
		}
	}
}

func TestDatedWorkplanName_Format(t *testing.T) {
	name := datedWorkplanName("my_feature")
	if !strings.HasSuffix(name, "_my_feature") {
		t.Errorf("expected name to end with '_my_feature', got %q", name)
	}

	// Should contain underscores separating year_month_day_name
	parts := strings.SplitN(name, "_", 4)
	if len(parts) < 4 {
		t.Errorf("expected at least 4 parts separated by underscores, got %d in %q", len(parts), name)
	}
}
