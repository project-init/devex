package publish

import "testing"

func TestPlanSynopsisRendersEachTrackerVariant(t *testing.T) {
	content := []byte("# Audit Logs\n\nAdmins cannot see who changed a role.\n\n```mermaid\nflowchart LR\n  A --> B\n```\n")
	const url = "https://github.com/project-init/devex/blob/main/audit-logs/2026-11-21/synopsis.md"

	got := planSynopsis(content, url)
	if want := "Admins cannot see who changed a role.\n\n```mermaid\nflowchart LR\n  A --> B\n```"; got.WithDiagram != want {
		t.Fatalf("WithDiagram = %q, want %q", got.WithDiagram, want)
	}
	if want := "Admins cannot see who changed a role.\n\nDiagram: " + url; got.DiagramLinked != want {
		t.Fatalf("DiagramLinked = %q, want %q", got.DiagramLinked, want)
	}
	// A bundle outside a GitHub checkout has no URL to link, so the diagram drops out.
	if want := "Admins cannot see who changed a role."; planSynopsis(content, "").DiagramLinked != want {
		t.Fatalf("DiagramLinked without a URL = %q, want %q", planSynopsis(content, "").DiagramLinked, want)
	}
}
