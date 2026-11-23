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
	if want := filepath.Join(base, "2026-11-21-audit-logs"); directory != want {
		t.Fatalf("directory = %q, want %q", directory, want)
	}
	bundle, err := artifact.Load(directory)
	if err != nil {
		t.Fatalf("generated bundle is invalid: %v", err)
	}
	if bundle.WorkBreakdown.Discovery.ID != "2026-11-21-audit-logs" {
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
	before := time.Now().Format(time.DateOnly) + "-audit-logs"
	directory, err := Generate(t.TempDir(), "Audit Logs")
	if err != nil {
		t.Fatal(err)
	}
	after := time.Now().Format(time.DateOnly) + "-audit-logs"
	// Accept either side of midnight so the test cannot flake on a date rollover.
	if folder := filepath.Base(directory); folder != before && folder != after {
		t.Fatalf("bundle folder = %q, want %q", folder, before)
	}
}

func TestGenerateKeepsADateThatBelongsToTheName(t *testing.T) {
	base := t.TempDir()
	// A dated folder that holds no bundle is not an earlier iteration.
	if err := os.Mkdir(filepath.Join(base, "2024-12-31-outage-review"), 0o755); err != nil {
		t.Fatal(err)
	}
	first, err := GenerateWithOptions(base, "2024-12-31-outage-review", GenerateOptions{Date: iterationDate})
	if err != nil {
		t.Fatal(err)
	}
	if got := filepath.Base(first); got != "2026-11-21-2024-12-31-outage-review" {
		t.Fatalf("bundle folder = %q, want the incident date kept", got)
	}
	// Iterating on that discovery drops only the iteration's date.
	second, err := GenerateWithOptions(base, filepath.Base(first), GenerateOptions{Date: iterationDate.AddDate(0, 2, 13)})
	if err != nil {
		t.Fatal(err)
	}
	if got := filepath.Base(second); got != "2027-02-03-2024-12-31-outage-review" {
		t.Fatalf("later iteration = %q, want one date dropped", got)
	}
}

// Bundles from before dated folders sit at <base>/<slug>; naming one keeps the caller's title.
func TestGenerateKeepsTheTitleBesideAnUndatedBundle(t *testing.T) {
	base := t.TempDir()
	if err := os.MkdirAll(filepath.Join(base, "api-gateway"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "api-gateway", "work-breakdown.yaml"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	directory, err := GenerateWithOptions(base, "API Gateway", GenerateOptions{Date: iterationDate})
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := artifact.Load(directory)
	if err != nil {
		t.Fatal(err)
	}
	if bundle.WorkBreakdown.Discovery.Title != "API Gateway" {
		t.Fatalf("discovery.title = %q, want the caller's casing", bundle.WorkBreakdown.Discovery.Title)
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
	// The skill passes the earlier folder's name as written, date and all, in any case.
	second, err := GenerateWithOptions(base, strings.ToUpper(filepath.Base(first)), GenerateOptions{Date: iterationDate.AddDate(0, 2, 13)})
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(filepath.Dir(first), "2027-02-03-audit-logs"); second != want {
		t.Fatalf("later iteration = %q, want %q beside %q", second, want, first)
	}
	bundle, err := artifact.Load(second)
	if err != nil {
		t.Fatal(err)
	}
	if bundle.WorkBreakdown.Discovery.Title != "Audit Logs" {
		t.Fatalf("discovery.title = %q, want the date dropped", bundle.WorkBreakdown.Discovery.Title)
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
