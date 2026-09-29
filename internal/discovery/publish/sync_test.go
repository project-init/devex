package publish

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/project-init/devex/internal/discovery/artifact"
	"github.com/project-init/devex/internal/discovery/config"
	"github.com/project-init/devex/internal/discovery/domain"
	"github.com/project-init/devex/internal/discovery/provider"
	"github.com/project-init/devex/internal/discovery/templates"
)

// writeSyncPlan freezes operations into a plan against a fresh bundle and returns its path.
func writeSyncPlan(t *testing.T, operations []provider.Operation) string {
	t.Helper()
	bundleDirectory, err := templates.Generate(t.TempDir(), "Audit Logs")
	if err != nil {
		t.Fatal(err)
	}
	planPath := filepath.Join(bundleDirectory, ".publish", "fake", "plan.yaml")
	plan := &provider.Plan{
		SchemaVersion: provider.SchemaVersion,
		ID:            "plan-sync",
		Provider:      "fake",
		TargetName:    "fake",
		Target: config.Target{
			Provider: "github",
			GitHub:   &config.GitHubTarget{Owner: "project-init", Repository: "devex"},
		},
		BundlePath:   bundleDirectory,
		SourceDigest: mustBundleDigest(t, bundleDirectory),
		Operations:   operations,
	}
	plan.PlanDigest, err = PlanDigest(plan)
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteYAMLAtomic(planPath, plan); err != nil {
		t.Fatal(err)
	}

	return planPath
}

func TestApplyUpdatesPublishedIssuesAndUnlinks(t *testing.T) {
	planPath := writeSyncPlan(t, []provider.Operation{
		{ID: "reuse-INIT-001", Action: provider.ActionReuseIssue, ItemID: "INIT-001", IdempotencyKey: "one"},
		{ID: "update-WI-001", Action: provider.ActionUpdateIssue, ItemID: "WI-001", IdempotencyKey: "two"},
		{ID: "unlink-WI-002/WI-001", Action: provider.ActionUnlinkIssues, IdempotencyKey: "three"},
	})
	adapter := &fakeAdapter{
		executions: make(map[string]int),
		resolvedAt: make(map[string][]domain.ItemID),
		published: map[string]provider.RemoteRef{
			"one": {ID: "1", Key: "DEVEX-1"},
			"two": {ID: "2", Key: "DEVEX-2"},
		},
	}

	receipt, err := Apply(context.Background(), planPath, adapter, nil)
	if err != nil {
		t.Fatal(err)
	}

	statuses := map[string]string{}
	for id, result := range receipt.Operations {
		statuses[id] = result.Status
	}
	want := map[string]string{"reuse-INIT-001": "reused", "update-WI-001": "updated", "unlink-WI-002/WI-001": "unlinked"}
	for id, status := range want {
		if statuses[id] != status {
			t.Fatalf("statuses = %v, want %v", statuses, want)
		}
	}
	if adapter.executions["reuse-INIT-001"] != 0 {
		t.Fatal("a reuse must not execute")
	}
	// The update executes against its own published issue.
	if got := adapter.resolvedAt["update-WI-001"]; len(got) != 2 || got[1] != "WI-001" {
		t.Fatalf("update saw resolved items %v, want INIT-001 and WI-001", got)
	}
	if remote := receipt.Operations["update-WI-001"].Remote; remote == nil || remote.ID != "2" {
		t.Fatalf("update remote = %#v, want the published issue's ID", remote)
	}
}

func TestApplyFailsWhenAPlannedIssueIsGone(t *testing.T) {
	for _, action := range []string{provider.ActionReuseIssue, provider.ActionUpdateIssue} {
		planPath := writeSyncPlan(t, []provider.Operation{
			{ID: action + "-WI-001", Action: action, ItemID: "WI-001", IdempotencyKey: "two"},
		})
		adapter := &fakeAdapter{executions: make(map[string]int)}

		_, err := Apply(context.Background(), planPath, adapter, nil)
		if err == nil || !strings.Contains(err.Error(), "plan again") {
			t.Fatalf("%s: err = %v, want a gone-issue failure", action, err)
		}
		if adapter.executions[action+"-WI-001"] != 0 {
			t.Fatalf("%s must not execute without its issue", action)
		}
	}
}

func TestApplyResumeSkipsSettledUpdatesAndUnlinks(t *testing.T) {
	planPath := writeSyncPlan(t, []provider.Operation{
		{ID: "update-WI-001", Action: provider.ActionUpdateIssue, ItemID: "WI-001", IdempotencyKey: "two"},
		{ID: "unlink-WI-002/WI-001", Action: provider.ActionUnlinkIssues, IdempotencyKey: "three"},
		{ID: "create-WI-003", Action: provider.ActionCreateIssue, ItemID: "WI-003", IdempotencyKey: "four"},
	})
	adapter := &fakeAdapter{
		executions: make(map[string]int),
		failOnce:   "create-WI-003",
		published:  map[string]provider.RemoteRef{"two": {ID: "2", Key: "DEVEX-2"}},
	}
	if _, err := Apply(context.Background(), planPath, adapter, nil); err == nil {
		t.Fatal("first Apply() succeeded, want temporary failure")
	}

	if _, err := Apply(context.Background(), planPath, adapter, nil); err != nil {
		t.Fatal(err)
	}

	if adapter.executions["update-WI-001"] != 1 || adapter.executions["unlink-WI-002/WI-001"] != 1 {
		t.Fatalf("executions = %v, want settled work run once", adapter.executions)
	}
}

type syncingAdapter struct {
	fakeAdapter
	request provider.SyncRequest
}

func (s *syncingAdapter) Sync(
	_ context.Context,
	_ config.Target,
	request provider.SyncRequest,
) ([]provider.Operation, []string, error) {
	s.request = request
	return []provider.Operation{{ID: "reuse-INIT-001", Action: provider.ActionReuseIssue}}, []string{"synced"}, nil
}

func TestCreatePlanSyncsThroughTheAdapter(t *testing.T) {
	bundle := mustLoadBundle(t)
	adapter := &syncingAdapter{}

	plan, err := CreatePlan(context.Background(), bundle, "fake", config.Target{}, adapter, PlanOptions{Sync: true, Force: true})
	if err != nil {
		t.Fatal(err)
	}

	if len(plan.Operations) != 1 || plan.Operations[0].Action != provider.ActionReuseIssue {
		t.Fatalf("operations = %#v", plan.Operations)
	}
	if !adapter.request.Force || adapter.request.DiscoveryID != bundle.WorkBreakdown.Discovery.ID {
		t.Fatalf("request = %#v", adapter.request)
	}
	if len(plan.Warnings) != 1 || plan.Warnings[0] != "synced" {
		t.Fatalf("warnings = %v", plan.Warnings)
	}
}

func TestCreatePlanRejectsUnsupportedSync(t *testing.T) {
	bundle := mustLoadBundle(t)

	if _, err := CreatePlan(context.Background(), bundle, "fake", config.Target{}, &fakeAdapter{}, PlanOptions{Sync: true}); err == nil ||
		!strings.Contains(err.Error(), "do not support sync") {
		t.Fatalf("err = %v, want unsupported sync", err)
	}
	if _, err := CreatePlan(context.Background(), bundle, "fake", config.Target{}, &fakeAdapter{}, PlanOptions{Force: true}); err == nil {
		t.Fatal("force without sync must fail")
	}
}

func mustLoadBundle(t *testing.T) *artifact.Bundle {
	t.Helper()
	directory, err := templates.Generate(t.TempDir(), "Audit Logs")
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := artifact.Load(directory)
	if err != nil {
		t.Fatal(err)
	}

	return bundle
}

type listAdapter struct {
	fakeAdapter
	operations []provider.Operation
}

func (l *listAdapter) Plan(context.Context, provider.PlanInput, config.Target) ([]provider.Operation, []string, error) {
	return l.operations, nil, nil
}

// An item without acceptance criteria plans a nil list, which a plan file reads back as an empty
// one. The plan digest and field digests must survive that round trip, or apply rejects the plan
// and every later sync reports a change that never happened.
func TestPlanDigestsSurviveThePlanFile(t *testing.T) {
	var noCriteria []string
	operation := provider.Operation{ID: "create-WI-001", Action: provider.ActionCreateIssue, ItemID: "WI-001", Fields: map[string]any{
		"title": "First", "acceptance_criteria": noCriteria, "labels": []string{"a"},
	}}
	// A target without kind_mapping carries a nil map, which the plan file reads back as {}.
	target := config.Target{Provider: "jira", Jira: &config.JiraTarget{BaseURL: "https://jira.test", ProjectKey: "DEVEX"}}
	bundle := mustLoadBundle(t)
	plan, err := CreatePlan(context.Background(), bundle, "fake", target, &listAdapter{operations: []provider.Operation{operation}}, PlanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "plan.yaml")
	if err := WriteYAMLAtomic(path, plan); err != nil {
		t.Fatal(err)
	}

	loaded, err := LoadPlan(path)
	if err != nil {
		t.Fatalf("LoadPlan() = %v, want the digest to survive the plan file", err)
	}

	names := []string{"title", "acceptance_criteria", "labels"}
	if before, after := provider.FieldDigests(plan.Operations[0], names...), provider.FieldDigests(loaded.Operations[0], names...); before["acceptance_criteria"] != after["acceptance_criteria"] {
		t.Fatalf("field digests changed across the plan file: %v -> %v", before, after)
	}
}

func TestFollowRenamesPointsDependenciesAtSyncedOperations(t *testing.T) {
	before := []provider.Operation{
		{ID: "create-WI-001", ItemID: "WI-001"},
		{ID: "create-WI-002", ItemID: "WI-002", DependsOn: []string{"create-WI-001"}},
		{ID: "link-WI-001/WI-002", DependsOn: []string{"create-WI-001", "create-WI-002"}},
	}
	operations := followRenames(before, []provider.Operation{
		{ID: "reuse-WI-001", ItemID: "WI-001"},
		{ID: "create-WI-002", ItemID: "WI-002", DependsOn: []string{"create-WI-001"}},
		{ID: "link-WI-001/WI-002", DependsOn: []string{"create-WI-001", "create-WI-002"}},
	})

	if got := strings.Join(operations[2].DependsOn, ","); got != "reuse-WI-001,create-WI-002" || operations[1].DependsOn[0] != "reuse-WI-001" {
		t.Fatalf("depends_on = %v and %v", operations[1].DependsOn, operations[2].DependsOn)
	}
}
