package domain

import (
	"strings"
	"testing"
)

func TestWorkBreakdownValidate(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(*WorkBreakdown)
		wantError string
	}{
		{name: "valid"},
		{
			name: "unknown parent",
			mutate: func(workBreakdown *WorkBreakdown) {
				workBreakdown.Items[1].Parent = "MISSING"
			},
			wantError: "unknown parent",
		},
		{
			name: "cycle",
			mutate: func(workBreakdown *WorkBreakdown) {
				workBreakdown.Items[0].Kind = KindFeature
				workBreakdown.Items[0].Parent = "WI-001"
			},
			wantError: "cycle",
		},
		{
			name: "duplicate",
			mutate: func(workBreakdown *WorkBreakdown) {
				workBreakdown.Items[1].ID = "INIT-001"
			},
			wantError: "duplicate",
		},
		{
			// Hierarchy already orders this work, and providers publish dependencies as
			// blocking links, so the edge would make a parent block its own child.
			name: "depends on parent",
			mutate: func(workBreakdown *WorkBreakdown) {
				workBreakdown.Items[1].DependsOn = []ItemID{workBreakdown.Items[1].Parent}
			},
			wantError: "ancestor",
		},
		{
			// The discovery ID is published as a Jira label, and Jira rejects whitespace in
			// labels only after every issue has been created.
			name: "discovery id is not label safe",
			mutate: func(workBreakdown *WorkBreakdown) {
				workBreakdown.Discovery.ID = "Audit Logs"
			},
			wantError: "discovery.id",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			workBreakdown := validWorkBreakdown()
			if test.mutate != nil {
				test.mutate(workBreakdown)
			}
			err := workBreakdown.Validate()
			if test.wantError == "" && err != nil {
				t.Fatalf("Validate() error = %v", err)
			}
			if test.wantError != "" && (err == nil || !strings.Contains(err.Error(), test.wantError)) {
				t.Fatalf("Validate() error = %v, want substring %q", err, test.wantError)
			}
		})
	}
}

func TestOrderedItemsPlacesReferencesFirst(t *testing.T) {
	workBreakdown := validWorkBreakdown()
	workBreakdown.Items[0], workBreakdown.Items[1] = workBreakdown.Items[1], workBreakdown.Items[0]
	ordered, err := workBreakdown.OrderedItems()
	if err != nil {
		t.Fatal(err)
	}
	if ordered[0].ID != "INIT-001" || ordered[1].ID != "WI-001" {
		t.Fatalf("OrderedItems() = %#v", ordered)
	}
}

func validWorkBreakdown() *WorkBreakdown {
	return &WorkBreakdown{
		SchemaVersion: SchemaVersion,
		Discovery: DiscoveryRef{
			ID:       "audit-logs",
			Title:    "Audit logs",
			Document: "discovery.md",
		},
		Items: []WorkItem{
			{
				ID:                 "INIT-001",
				Kind:               KindInitiative,
				Title:              "Audit logs",
				Description:        "Deliver audit logs.",
				AcceptanceCriteria: []string{"Logs are searchable."},
			},
			{
				ID:          "WI-001",
				Kind:        KindResearch,
				Parent:      "INIT-001",
				Title:       "Validate schema",
				Description: "Validate the event schema.",
			},
		},
	}
}
