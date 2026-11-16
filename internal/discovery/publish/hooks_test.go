package publish

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/project-init/devex/internal/discovery/config"
	"github.com/project-init/devex/internal/discovery/provider"
	"github.com/project-init/devex/internal/discovery/templates"
)

// writePlan writes a plan of operations for a fresh bundle and returns its path.
func writePlan(t *testing.T, bundleDirectory string, operations ...provider.Operation) string {
	t.Helper()
	planPath := filepath.Join(bundleDirectory, ".publish", "fake", "plan.yaml")
	plan := &provider.Plan{
		SchemaVersion: provider.SchemaVersion, ID: "plan-test", Provider: "fake", TargetName: "fake",
		Target:       config.Target{Provider: "github", GitHub: &config.GitHubTarget{Owner: "project-init", Repository: "devex"}},
		BundlePath:   bundleDirectory,
		SourceDigest: mustBundleDigest(t, bundleDirectory),
		Operations:   operations,
	}
	var err error
	if plan.PlanDigest, err = PlanDigest(plan); err != nil {
		t.Fatal(err)
	}
	if err := WriteYAMLAtomic(planPath, plan); err != nil {
		t.Fatal(err)
	}
	return planPath
}

func newBundle(t *testing.T) string {
	t.Helper()
	directory, err := templates.Generate(t.TempDir(), "Audit Logs")
	if err != nil {
		t.Fatal(err)
	}
	return directory
}

func TestApplyReportsEachOperationAndWhatAResumeSkipped(t *testing.T) {
	planPath := writePlan(t, newBundle(t),
		provider.Operation{ID: "create-INIT-001", ItemID: "INIT-001", IdempotencyKey: "one"},
		provider.Operation{ID: "create-WI-001", ItemID: "WI-001", IdempotencyKey: "two"},
	)
	adapter := &fakeAdapter{executions: map[string]int{}, failOnce: "create-WI-001"}
	var seen []Progress
	hooks := ApplyHooks{Progress: func(p Progress) { seen = append(seen, p) }}
	if _, err := Apply(context.Background(), planPath, adapter, hooks); err == nil {
		t.Fatal("first Apply() succeeded, want the planned failure")
	}
	if len(seen) != 2 || seen[0].Result.Status != "created" || seen[1].Result.Status != "failed" || seen[1].Index != 2 || seen[1].Total != 2 {
		t.Fatalf("first run progress = %+v, want [1/2] created then [2/2] failed", seen)
	}
	seen = nil
	if _, err := Apply(context.Background(), planPath, adapter, hooks); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 2 || !seen[0].Resumed || seen[1].Resumed || seen[1].Result.Status != "created" {
		t.Errorf("second run progress = %+v, want the first resumed and the second created", seen)
	}
}

// preflightAdapter refuses every plan before apply writes anything.
type preflightAdapter struct{ *fakeAdapter }

func (preflightAdapter) Preflight(context.Context, config.Target, []provider.Operation) error {
	return errors.New("Jira project DEVEX has no issue type Story")
}

func TestApplyStopsAtAFailedPreflightBeforeWriting(t *testing.T) {
	planPath := writePlan(t, newBundle(t), provider.Operation{ID: "create-INIT-001", ItemID: "INIT-001", IdempotencyKey: "one"})
	adapter := preflightAdapter{&fakeAdapter{executions: map[string]int{}}}
	_, err := Apply(context.Background(), planPath, adapter, ApplyHooks{})
	if err == nil || len(adapter.executions) != 0 {
		t.Fatalf("Apply() = %v after %d executions, want the preflight failure and none", err, len(adapter.executions))
	}
	receipt, loadErr := LoadReceipt(DefaultReceiptPath(planPath))
	if loadErr != nil || len(receipt.Operations) != 0 || receipt.Status != "partial" {
		t.Errorf("receipt = %+v, %v; want a partial receipt recording no operation", receipt, loadErr)
	}
}

func TestReplanningArchivesTheReceiptSoApplySucceeds(t *testing.T) {
	bundle := newBundle(t)
	planPath := writePlan(t, bundle, provider.Operation{ID: "create-INIT-001", ItemID: "INIT-001", IdempotencyKey: "one"})
	adapter := &fakeAdapter{executions: map[string]int{}}
	var archived []string
	hooks := ApplyHooks{Archived: func(path string) { archived = append(archived, path) }}
	if _, err := Apply(context.Background(), planPath, adapter, hooks); err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(context.Background(), planPath, adapter, hooks); err != nil || len(archived) != 0 {
		t.Fatalf("re-apply of the same plan = %v, archived %q; want its own receipt kept", err, archived)
	}

	// The bundle gains an item, so the rewritten plan has another digest.
	writePlan(t, bundle,
		provider.Operation{ID: "create-INIT-001", ItemID: "INIT-001", IdempotencyKey: "one"},
		provider.Operation{ID: "create-WI-001", ItemID: "WI-001", IdempotencyKey: "two"},
	)
	if _, err := Apply(context.Background(), planPath, adapter, hooks); err != nil {
		t.Fatalf("Apply(replanned) = %v, want success without deleting files by hand", err)
	}
	if len(archived) != 1 {
		t.Fatalf("archived = %q, want the replaced plan's receipt", archived)
	}
	if _, err := os.Stat(archived[0]); err != nil {
		t.Errorf("archived receipt: %v", err)
	}
}

func TestApplyRefusesAReceiptItCannotRead(t *testing.T) {
	planPath := writePlan(t, newBundle(t), provider.Operation{ID: "create-INIT-001", ItemID: "INIT-001", IdempotencyKey: "one"})
	if err := os.MkdirAll(filepath.Dir(planPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(DefaultReceiptPath(planPath), []byte("status: [unterminated\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	adapter := &fakeAdapter{executions: map[string]int{}}
	if _, err := Apply(context.Background(), planPath, adapter, ApplyHooks{}); err == nil || len(adapter.executions) != 0 {
		t.Errorf("Apply() = %v after %d executions, want a refusal that keeps the receipt's record", err, len(adapter.executions))
	}
}

// strictPreflight fails on any operation it is asked about, as a project missing every type would.
type strictPreflight struct{ *fakeAdapter }

func (strictPreflight) Preflight(_ context.Context, _ config.Target, operations []provider.Operation) error {
	if len(operations) > 0 {
		return errors.New("the Jira project has no issue type Story")
	}
	return nil
}

func TestPreflightChecksOnlyTheWorkLeft(t *testing.T) {
	planPath := writePlan(t, newBundle(t), provider.Operation{ID: "create-INIT-001", Action: provider.ActionCreateIssue, ItemID: "INIT-001", IdempotencyKey: "one"})
	if _, err := Apply(context.Background(), planPath, &fakeAdapter{executions: map[string]int{}}, ApplyHooks{}); err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(context.Background(), planPath, strictPreflight{&fakeAdapter{executions: map[string]int{}}}, ApplyHooks{}); err != nil {
		t.Errorf("Apply(resumed, nothing left) = %v, want the preflight to see no work", err)
	}
}

func TestPreflightSkipsCreatesTheRemoteAlreadyHolds(t *testing.T) {
	// The receipt was archived, but the remote holds the issue, so apply reuses it and creates nothing.
	planPath := writePlan(t, newBundle(t), provider.Operation{ID: "create-INIT-001", Action: provider.ActionCreateIssue, ItemID: "INIT-001", IdempotencyKey: "one"})
	adapter := strictPreflight{&fakeAdapter{executions: map[string]int{}, published: map[string]provider.RemoteRef{"one": {ID: "1", URL: "https://example.test/1"}}}}
	if _, err := Apply(context.Background(), planPath, adapter, ApplyHooks{}); err != nil {
		t.Errorf("Apply() = %v, want the published create left out of the preflight", err)
	}
}

func TestPreflightSeesOnlyCreates(t *testing.T) {
	planPath := writePlan(t, newBundle(t), provider.Operation{ID: "link-INIT-001/WI-001", Action: provider.ActionLinkIssues, IdempotencyKey: "link"})
	if _, err := Apply(context.Background(), planPath, strictPreflight{&fakeAdapter{executions: map[string]int{}}}, ApplyHooks{}); err != nil {
		t.Errorf("Apply(links only) = %v, want the preflight to see no creates", err)
	}
}

func TestArchiveNameStaysInTheArchive(t *testing.T) {
	bundle := newBundle(t)
	planPath := writePlan(t, bundle, provider.Operation{ID: "create-INIT-001", ItemID: "INIT-001", IdempotencyKey: "one"})
	receipt := []byte("schema_version: v1\nplan_digest: /../../../../escaped\nstatus: complete\noperations: {}\n")
	if err := os.WriteFile(DefaultReceiptPath(planPath), receipt, 0o644); err != nil {
		t.Fatal(err)
	}
	var archived string
	if _, err := Apply(context.Background(), planPath, &fakeAdapter{executions: map[string]int{}}, ApplyHooks{Archived: func(p string) { archived = p }}); err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(archived) != filepath.Join(filepath.Dir(planPath), "archive") {
		t.Errorf("archived to %s, want inside the archive directory", archived)
	}
}
