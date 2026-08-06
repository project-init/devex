package publish

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"testing"

	"github.com/project-init/devex/internal/discovery/artifact"
	"github.com/project-init/devex/internal/discovery/config"
	"github.com/project-init/devex/internal/discovery/domain"
	"github.com/project-init/devex/internal/discovery/provider"
	"github.com/project-init/devex/internal/discovery/templates"
)

type fakeAdapter struct {
	executions map[string]int
	resolvedAt map[string][]domain.ItemID
	lookups    []string
	failOnce   string
}

func (f *fakeAdapter) ID() string { return "fake" }

func (f *fakeAdapter) Plan(
	context.Context,
	*domain.WorkBreakdown,
	[]byte,
	config.Target,
) ([]provider.Operation, []string, error) {
	return nil, nil, nil
}

func (f *fakeAdapter) Lookup(
	_ context.Context,
	_ config.Target,
	idempotencyKey string,
) (*provider.RemoteRef, error) {
	f.lookups = append(f.lookups, idempotencyKey)
	return nil, nil
}

func (f *fakeAdapter) Execute(
	_ context.Context,
	_ config.Target,
	operation provider.Operation,
	resolved map[domain.ItemID]provider.RemoteRef,
) (provider.RemoteRef, error) {
	f.executions[operation.ID]++
	if f.resolvedAt != nil {
		items := make([]domain.ItemID, 0, len(resolved))
		for itemID := range resolved {
			items = append(items, itemID)
		}
		sort.Slice(items, func(i int, j int) bool { return items[i] < items[j] })
		f.resolvedAt[operation.ID] = items
	}
	if operation.ID == f.failOnce && f.executions[operation.ID] == 1 {
		return provider.RemoteRef{}, fmt.Errorf("temporary failure")
	}
	return provider.RemoteRef{ID: operation.ID, URL: "https://example.test/" + operation.ID}, nil
}

func TestApplyResumesFromFileReceipt(t *testing.T) {
	bundleDirectory, err := templates.Generate(t.TempDir(), "Audit Logs")
	if err != nil {
		t.Fatal(err)
	}
	planPath := filepath.Join(bundleDirectory, ".publish", "fake", "plan.yaml")
	plan := &provider.Plan{
		SchemaVersion: provider.SchemaVersion,
		ID:            "plan-test",
		Provider:      "fake",
		TargetName:    "fake",
		Target: config.Target{
			Provider: "github",
			GitHub: &config.GitHubTarget{
				Owner:      "project-init",
				Repository: "devex",
			},
		},
		BundlePath:   bundleDirectory,
		SourceDigest: mustBundleDigest(t, bundleDirectory),
		Operations: []provider.Operation{
			{ID: "create-INIT-001", ItemID: "INIT-001", IdempotencyKey: "one"},
			{ID: "create-WI-001", ItemID: "WI-001", IdempotencyKey: "two"},
		},
	}
	plan.PlanDigest, err = PlanDigest(plan)
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteYAMLAtomic(planPath, plan); err != nil {
		t.Fatal(err)
	}

	adapter := &fakeAdapter{executions: make(map[string]int), failOnce: "create-WI-001"}
	if _, err := Apply(context.Background(), planPath, adapter, nil); err == nil {
		t.Fatal("first Apply() succeeded, want temporary failure")
	}
	receipt, err := Apply(context.Background(), planPath, adapter, nil)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Status != "complete" {
		t.Fatalf("receipt status = %q", receipt.Status)
	}
	if adapter.executions["create-INIT-001"] != 1 {
		t.Fatalf("completed operation executed %d times", adapter.executions["create-INIT-001"])
	}
	if adapter.executions["create-WI-001"] != 2 {
		t.Fatalf("failed operation executed %d times", adapter.executions["create-WI-001"])
	}
}

// Relationship operations such as Jira issue links publish an edge rather than a work item, so
// they must not claim an entry in the resolved-item map that later operations read.
func TestApplyKeepsRelationshipOperationsOutOfResolution(t *testing.T) {
	bundleDirectory, err := templates.Generate(t.TempDir(), "Audit Logs")
	if err != nil {
		t.Fatal(err)
	}
	planPath := filepath.Join(bundleDirectory, ".publish", "fake", "plan.yaml")
	plan := &provider.Plan{
		SchemaVersion: provider.SchemaVersion,
		ID:            "plan-test",
		Provider:      "fake",
		TargetName:    "fake",
		Target: config.Target{
			Provider: "github",
			GitHub: &config.GitHubTarget{
				Owner:      "project-init",
				Repository: "devex",
			},
		},
		BundlePath:   bundleDirectory,
		SourceDigest: mustBundleDigest(t, bundleDirectory),
		Operations: []provider.Operation{
			{ID: "create-WI-001", ItemID: "WI-001", IdempotencyKey: "one"},
			{ID: "link-WI-001-WI-002", IdempotencyKey: "link"},
			{ID: "create-WI-002", ItemID: "WI-002", IdempotencyKey: "two"},
		},
	}
	plan.PlanDigest, err = PlanDigest(plan)
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteYAMLAtomic(planPath, plan); err != nil {
		t.Fatal(err)
	}

	adapter := &fakeAdapter{
		executions: make(map[string]int),
		resolvedAt: make(map[string][]domain.ItemID),
	}
	if _, err := Apply(context.Background(), planPath, adapter, nil); err != nil {
		t.Fatal(err)
	}

	resolved := adapter.resolvedAt["create-WI-002"]
	if len(resolved) != 1 || resolved[0] != "WI-001" {
		t.Fatalf("resolved items = %#v, want only WI-001", resolved)
	}

	// No remote carries a relationship operation's key, so looking it up could only ever miss.
	for _, idempotencyKey := range adapter.lookups {
		if idempotencyKey == "link" {
			t.Fatalf("lookups = %#v, want no lookup for the link operation", adapter.lookups)
		}
	}

	// A resumed run reads the receipt instead of the live map, so it must skip the link too.
	second := &fakeAdapter{executions: make(map[string]int)}
	if _, err := Apply(context.Background(), planPath, second, nil); err != nil {
		t.Fatal(err)
	}
	if len(second.executions) != 0 {
		t.Fatalf("resumed executions = %#v, want none", second.executions)
	}
}

func mustBundleDigest(t *testing.T, directory string) string {
	t.Helper()
	bundle, err := artifact.Load(directory)
	if err != nil {
		t.Fatal(err)
	}
	return bundle.Digest()
}
