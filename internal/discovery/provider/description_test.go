package provider

import (
	"testing"

	"github.com/project-init/devex/internal/discovery/domain"
)

func TestDescription(t *testing.T) {
	initiative := domain.WorkItem{Kind: domain.KindInitiative, Description: "Deliver audit logs."}
	initiativeInAPI := initiative
	initiativeInAPI.Repository = "acme/api"
	task := domain.WorkItem{Kind: domain.KindTask, Repository: "acme/api", Description: "Add the endpoint."}
	tests := []struct {
		name     string
		item     domain.WorkItem
		synopsis string
		want     string
	}{
		// A bundle without a synopsis must plan the description it always has, or sync would
		// rewrite every published initiative.
		{name: "initiative without a synopsis", item: initiative, want: "Deliver audit logs."},
		{name: "initiative with a synopsis", item: initiative, synopsis: "Synopsis.", want: "Synopsis.\n\nDeliver audit logs."},
		{name: "task with a repository", item: task, synopsis: "Synopsis.", want: "Repository: `acme/api`\n\nAdd the endpoint."},
		// Readers land on the initiative, so the synopsis stays first.
		{name: "initiative with a repository", item: initiativeInAPI, synopsis: "Synopsis.", want: "Synopsis.\n\nRepository: `acme/api`\n\nDeliver audit logs."},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := Description(test.item, test.synopsis); got != test.want {
				t.Fatalf("Description() = %q, want %q", got, test.want)
			}
		})
	}
}
