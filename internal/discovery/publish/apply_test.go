package publish

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/project-init/devex/internal/discovery/artifact"
	"github.com/project-init/devex/internal/discovery/config"
	"github.com/project-init/devex/internal/discovery/domain"
	"github.com/project-init/devex/internal/discovery/provider"
	"github.com/project-init/devex/internal/discovery/templates"
)

type fakeAdapter struct {
	executions map[string]int
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

func (f *fakeAdapter) Lookup(context.Context, config.Target, string) (*provider.RemoteRef, error) {
	return nil, nil
}

func (f *fakeAdapter) Execute(
	_ context.Context,
	_ config.Target,
	operation provider.Operation,
	_ map[domain.ItemID]provider.RemoteRef,
) (provider.RemoteRef, error) {
	f.executions[operation.ID]++
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

func mustBundleDigest(t *testing.T, directory string) string {
	t.Helper()
	bundle, err := artifact.Load(directory)
	if err != nil {
		t.Fatal(err)
	}
	return bundle.Digest()
}
