package publish

import "testing"

func TestPlanSynopsisRendersEachTrackerVariant(t *testing.T) {
	// The layout the template produces: what, how, the diagram, why, and the closing link.
	content := []byte("# Audit Logs\n\nWe propose audit logs.\n\nHere's how audit logs work: events stream to a store.\n\n" +
		"```mermaid\nflowchart LR\n  A --> B\n```\n\nWe chose audit logs because admins wait a week.\n\n" +
		"[Read the full discovery](discovery.md)\n")
	const url = "https://github.com/project-init/devex/blob/main/docs/discoveries/2026-11-21-audit-logs/synopsis.md"
	const what, how, why = "We propose audit logs.", "Here's how audit logs work: events stream to a store.",
		"We chose audit logs because admins wait a week."

	got := planSynopsis(content, url)
	if want := what + "\n\n" + how + "\n\n```mermaid\nflowchart LR\n  A --> B\n```\n\n" + why; got.WithDiagram != want {
		t.Fatalf("WithDiagram = %q, want %q", got.WithDiagram, want)
	}
	if want := what + "\n\n" + how + "\n\nDiagram: " + url + "\n\n" + why; got.DiagramLinked != want {
		t.Fatalf("DiagramLinked = %q, want %q", got.DiagramLinked, want)
	}
	// A bundle outside a GitHub checkout has no URL to link, so the diagram drops out.
	if want := what + "\n\n" + how + "\n\n" + why; planSynopsis(content, "").DiagramLinked != want {
		t.Fatalf("DiagramLinked without a URL = %q, want %q", planSynopsis(content, "").DiagramLinked, want)
	}
}
