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
			name: "task without a repository",
			mutate: func(workBreakdown *WorkBreakdown) {
				workBreakdown.Items[1].Kind = KindTask
			},
			wantError: `item "WI-001" needs a repository`,
		},
		{
			name: "feature without a repository",
			mutate: func(workBreakdown *WorkBreakdown) {
				workBreakdown.Items[1].Kind = KindFeature
			},
			wantError: `item "WI-001" needs a repository`,
		},
		{
			name: "defect without a repository",
			mutate: func(workBreakdown *WorkBreakdown) {
				workBreakdown.Items[1].Kind = KindDefect
			},
			wantError: `item "WI-001" needs a repository`,
		},
		{
			name: "task with a repository",
			mutate: func(workBreakdown *WorkBreakdown) {
				workBreakdown.Items[1].Kind = KindTask
				workBreakdown.Items[1].Repository = "acme/api"
			},
		},
		{
			// A parent spans its children's repositories, so only the children name one.
			name: "feature with children needs no repository",
			mutate: func(workBreakdown *WorkBreakdown) {
				workBreakdown.Items[1].Kind = KindFeature
				workBreakdown.Items = append(workBreakdown.Items, WorkItem{
					ID:          "WI-002",
					Kind:        KindTask,
					Parent:      "WI-001",
					Repository:  "group/subgroup/web",
					Title:       "Show audit events",
					Description: "Show audit events in the console.",
				})
			},
		},
		{
			// A research child changes no repository, so its parent still must name one.
			name: "feature with only research children",
			mutate: func(workBreakdown *WorkBreakdown) {
				workBreakdown.Items[1].Kind = KindFeature
				workBreakdown.Items = append(workBreakdown.Items, WorkItem{
					ID:          "WI-002",
					Kind:        KindResearch,
					Parent:      "WI-001",
					Title:       "Compare stores",
					Description: "Compare event stores.",
				})
			},
			wantError: `item "WI-001" needs a repository`,
		},
		{
			// The feature spans the tasks beneath its research item, so it needs no repository.
			name: "implementation beneath research",
			mutate: func(workBreakdown *WorkBreakdown) {
				workBreakdown.Items = append(workBreakdown.Items,
					WorkItem{ID: "FEAT-001", Kind: KindFeature, Parent: "INIT-001", Title: "Audit", Description: "Audit."},
					WorkItem{ID: "RES-001", Kind: KindResearch, Parent: "FEAT-001", Title: "Spike", Description: "Spike."},
					WorkItem{ID: "WI-002", Kind: KindTask, Parent: "RES-001", Repository: "acme/api", Title: "API", Description: "API."},
					WorkItem{ID: "WI-003", Kind: KindTask, Parent: "RES-001", Repository: "acme/web", Title: "Web", Description: "Web."},
				)
			},
		},
		{
			name: "parent names the repository of all its work",
			mutate: func(workBreakdown *WorkBreakdown) {
				workBreakdown.Items[0].Repository = "acme/api"
				workBreakdown.Items[1].Kind = KindTask
				workBreakdown.Items[1].Repository = "acme/api"
			},
		},
		{
			name: "parent names another repository than its work",
			mutate: func(workBreakdown *WorkBreakdown) {
				workBreakdown.Items[0].Repository = "acme/api"
				workBreakdown.Items[1].Kind = KindTask
				workBreakdown.Items[1].Repository = "acme/web"
			},
			wantError: `item "INIT-001" names repository "acme/api", but item "WI-001" beneath it changes "acme/web"`,
		},
		{
			name: "ancestor names another repository than its work",
			mutate: func(workBreakdown *WorkBreakdown) {
				workBreakdown.Items[0].Repository = "acme/api"
				workBreakdown.Items = append(workBreakdown.Items, WorkItem{
					ID:          "WI-002",
					Kind:        KindTask,
					Parent:      "WI-001",
					Repository:  "acme/web",
					Title:       "Show audit events",
					Description: "Show audit events.",
				})
			},
			wantError: `item "INIT-001" names repository "acme/api", but item "WI-002" beneath it changes "acme/web"`,
		},
		{
			name: "parent names its work's repository in another case",
			mutate: func(workBreakdown *WorkBreakdown) {
				workBreakdown.Items[0].Repository = "Acme/API"
				workBreakdown.Items[1].Kind = KindTask
				workBreakdown.Items[1].Repository = "acme/api"
			},
		},
		{
			// The cycle error must not hide that the task names no repository.
			name: "task in a parent cycle",
			mutate: func(workBreakdown *WorkBreakdown) {
				workBreakdown.Items[1].Kind = KindTask
				workBreakdown.Items = append(workBreakdown.Items, WorkItem{
					ID: "WI-002", Kind: KindResearch, Parent: "WI-001", Title: "Loop", Description: "Loop.",
				})
				workBreakdown.Items[1].Parent = "WI-002"
			},
			wantError: `item "WI-001" needs a repository`,
		},
		{
			name: "repository segment of dots",
			mutate: func(workBreakdown *WorkBreakdown) {
				workBreakdown.Items[1].Repository = "acme/.."
			},
			wantError: "write a path such as acme/api",
		},
		{
			name: "repository without an owner",
			mutate: func(workBreakdown *WorkBreakdown) {
				workBreakdown.Items[1].Repository = "api"
			},
			wantError: "write a path such as acme/api",
		},
		{
			name: "repository is not a path",
			mutate: func(workBreakdown *WorkBreakdown) {
				workBreakdown.Items[1].Repository = "acme/api [docs](https://example.com)"
			},
			wantError: "write a path such as acme/api",
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
