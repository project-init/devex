package templates

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/project-init/devex/internal/discovery/artifact"
)

var iterationDate = time.Date(2026, time.November, 21, 15, 4, 5, 0, time.Local)

func TestGenerateCreatesValidBundle(t *testing.T) {
	base := t.TempDir()
	directory, err := GenerateWithOptions(base, "Audit Logs", GenerateOptions{Date: iterationDate})
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(base, "audit-logs", "2026-11-21"); directory != want {
		t.Fatalf("directory = %q, want %q", directory, want)
	}
	bundle, err := artifact.Load(directory)
	if err != nil {
		t.Fatalf("generated bundle is invalid: %v", err)
	}
	if bundle.WorkBreakdown.Discovery.ID != "audit-logs-2026-11-21" {
		t.Fatalf("discovery.id = %q", bundle.WorkBreakdown.Discovery.ID)
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

func TestGenerateDatesBundleToday(t *testing.T) {
	before := time.Now().Format(time.DateOnly)
	directory, err := Generate(t.TempDir(), "Audit Logs")
	if err != nil {
		t.Fatal(err)
	}
	// Accept either side of midnight so the test cannot flake on a date rollover.
	if iteration := filepath.Base(directory); iteration != before && iteration != time.Now().Format(time.DateOnly) {
		t.Fatalf("iteration folder = %q, want today (%s)", iteration, before)
	}
}

func TestGenerateDoesNotOverwrite(t *testing.T) {
	base := t.TempDir()
	options := GenerateOptions{Date: iterationDate}
	if _, err := GenerateWithOptions(base, "Audit Logs", options); err != nil {
		t.Fatal(err)
	}
	_, err := GenerateWithOptions(base, "Audit Logs", options)
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("second GenerateWithOptions() error = %v, want overwrite protection", err)
	}
}

func TestGenerateStartsLaterIterationBesideEarlierOne(t *testing.T) {
	base := t.TempDir()
	first, err := GenerateWithOptions(base, "Audit Logs", GenerateOptions{Date: iterationDate})
	if err != nil {
		t.Fatal(err)
	}
	second, err := GenerateWithOptions(base, "audit-logs", GenerateOptions{Date: iterationDate.AddDate(0, 2, 13)})
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(filepath.Dir(first), "2027-02-03"); second != want {
		t.Fatalf("later iteration = %q, want %q beside %q", second, want, first)
	}
}

func TestGenerateAppliesDefaultLabelsToEveryWorkItem(t *testing.T) {
	directory, err := GenerateWithOptions(t.TempDir(), "Audit Logs", GenerateOptions{
		DefaultLabels: []string{"discovery", "team-platform"},
	})
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := artifact.Load(directory)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range bundle.WorkBreakdown.Items {
		if strings.Join(item.Labels, ",") != "discovery,team-platform" {
			t.Errorf("item %s labels = %#v", item.ID, item.Labels)
		}
	}
}
