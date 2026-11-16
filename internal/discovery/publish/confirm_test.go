package publish

import (
	"context"
	"maps"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/project-init/devex/internal/discovery/config"
	"github.com/project-init/devex/internal/discovery/domain"
	"github.com/project-init/devex/internal/discovery/provider"
)

// confirmAdapter's search sees nothing yet, as Jira's can right after a create; reading by key
// finds the issues in exists.
type confirmAdapter struct {
	*fakeAdapter
	exists     map[string]bool
	candidates []string
}

func (c *confirmAdapter) Confirm(_ context.Context, _ config.Target, candidates map[string]provider.RemoteRef) (map[string]provider.RemoteRef, error) {
	c.candidates = slices.Sorted(maps.Keys(candidates))
	confirmed := make(map[string]provider.RemoteRef)
	for key, remote := range candidates {
		if c.exists[key] {
			confirmed[key] = remote
		}
	}
	return confirmed, nil
}

// applyThenReplan applies a plan with INIT-001, then re-plans it with WI-001 added and applies
// again. It returns how many times INIT-001's create ran.
func applyThenReplan(t *testing.T, adapter provider.Adapter, fake *fakeAdapter) int {
	t.Helper()
	bundle := newBundle(t)
	first := provider.Operation{ID: "create-INIT-001", Action: provider.ActionCreateIssue, ItemID: "INIT-001", IdempotencyKey: "one"}
	planPath := writePlan(t, bundle, first)
	if _, err := Apply(context.Background(), planPath, adapter, ApplyHooks{}); err != nil {
		t.Fatal(err)
	}
	writePlan(t, bundle, first, provider.Operation{ID: "create-WI-001", Action: provider.ActionCreateIssue, ItemID: "WI-001", IdempotencyKey: "two"})
	if _, err := Apply(context.Background(), planPath, adapter, ApplyHooks{}); err != nil {
		t.Fatal(err)
	}
	return fake.executions["create-INIT-001"]
}

func TestApplyConfirmsWhatTheReplacedPlanPublishedBeforeTheSearchCanSeeIt(t *testing.T) {
	fake := &fakeAdapter{executions: map[string]int{}}
	adapter := &confirmAdapter{fakeAdapter: fake, exists: map[string]bool{"one": true}}
	if creates := applyThenReplan(t, adapter, fake); creates != 1 {
		t.Errorf("INIT-001 created %d times, want once and reused after the re-plan", creates)
	}
	if slices.Compare(adapter.candidates, []string{"one"}) != 0 {
		t.Errorf("confirmed %q, want only INIT-001, the item the archived receipt recorded", adapter.candidates)
	}
}

func TestApplyCreatesAgainWhenTheRecordedIssueIsGone(t *testing.T) {
	fake := &fakeAdapter{executions: map[string]int{}}
	adapter := &confirmAdapter{fakeAdapter: fake, exists: map[string]bool{}}
	if creates := applyThenReplan(t, adapter, fake); creates != 2 {
		t.Errorf("INIT-001 created %d times, want it created again once deleted", creates)
	}
}

func TestRecordedItemsReadEveryReceiptForThisTargetNewestLast(t *testing.T) {
	bundle := t.TempDir()
	publish := filepath.Join(bundle, ".publish", "work")
	write := func(path, providerID, target string, at time.Time, item, key string) {
		receipt := &provider.Receipt{
			SchemaVersion: provider.SchemaVersion, PlanDigest: "sha256:" + path, Provider: providerID, TargetName: target, UpdatedAt: at,
			Operations: map[string]provider.OperationResult{"create-" + item: {ItemID: domain.ItemID(item), Status: "created", Remote: &provider.RemoteRef{Key: key}}},
		}
		if err := WriteYAMLAtomic(filepath.Join(publish, path), receipt); err != nil {
			t.Fatal(err)
		}
	}
	day := func(n int) time.Time { return time.Date(2026, 11, n, 0, 0, 0, 0, time.UTC) }
	// Names sort against time here, so only the receipts' own times give the right order.
	write("archive/receipt-20261101T000000Z-000000000000.yaml", "jira", "work", day(3), "WI-001", "ED-9")
	write("archive/receipt-20261101T000000Z-ffffffffffff.yaml", "jira", "work", day(1), "WI-001", "ED-1")
	write("sync-20261102T000000.000Z/receipt.yaml", "jira", "work", day(2), "WI-002", "ED-2") // a sync plan's
	write("archive/receipt-20261104T000000Z-dddddddddddd.yaml", "github", "work", day(4), "WI-003", "7")
	write("archive/receipt-20261105T000000Z-eeeeeeeeeeee.yaml", "jira", "personal", day(5), "WI-004", "ME-1")
	plan := &provider.Plan{Provider: "jira", TargetName: "work", BundlePath: bundle}
	// Seen from a sync plan, whose own directory holds none of the main plan's archives.
	recorded := recordedItems(plan, filepath.Join(publish, "sync-20261106T000000.000Z", "receipt.yaml"))
	if len(recorded) != 2 || recorded["WI-001"].Key != "ED-9" || recorded["WI-002"].Key != "ED-2" {
		t.Errorf("recorded = %v, want WI-001 from the newest receipt and WI-002 from the sync plan, nothing from other targets", recorded)
	}
}

func TestRecordedCandidatesSkipIssuesTheSearchFound(t *testing.T) {
	pending := []provider.Operation{
		{ItemID: "WI-001", IdempotencyKey: "audit/WI-001"},
		{ItemID: "WI-002", IdempotencyKey: "audit/WI-002"},
	}
	recorded := map[domain.ItemID]provider.RemoteRef{"WI-001": {Key: "ED-1"}, "WI-002": {Key: "ED-2"}}
	found := map[string]provider.RemoteRef{"audit/WI-001": {Key: "ED-1"}}
	if got := recordedCandidates(pending, found, recorded); len(got) != 1 || got["audit/WI-002"].Key != "ED-2" {
		t.Errorf("candidates = %v, want only WI-002, which the search missed", got)
	}
}

func TestApplyConfirmsAcrossAReplanWhoseApplyRecordedNothing(t *testing.T) {
	fake := &fakeAdapter{executions: map[string]int{}}
	adapter := &confirmAdapter{fakeAdapter: fake, exists: map[string]bool{"one": true}}
	bundle := newBundle(t)
	first := provider.Operation{ID: "create-INIT-001", Action: provider.ActionCreateIssue, ItemID: "INIT-001", IdempotencyKey: "one"}
	planPath := writePlan(t, bundle, first)
	if _, err := Apply(context.Background(), planPath, adapter, ApplyHooks{}); err != nil {
		t.Fatal(err)
	}
	// The second plan's apply stops at its preflight, so its receipt records nothing.
	second := provider.Operation{ID: "create-WI-001", Action: provider.ActionCreateIssue, ItemID: "WI-001", IdempotencyKey: "two"}
	writePlan(t, bundle, first, second)
	if _, err := Apply(context.Background(), planPath, preflightAdapter{fake}, ApplyHooks{}); err == nil {
		t.Fatal("Apply() with a failing preflight succeeded")
	}
	writePlan(t, bundle, first, second, provider.Operation{ID: "create-WI-002", Action: provider.ActionCreateIssue, ItemID: "WI-002", IdempotencyKey: "three"})
	if _, err := Apply(context.Background(), planPath, adapter, ApplyHooks{}); err != nil {
		t.Fatal(err)
	}
	if fake.executions["create-INIT-001"] != 1 {
		t.Errorf("INIT-001 created %d times, want the first plan's archive to keep it reused", fake.executions["create-INIT-001"])
	}
}

func TestResolvePendingReadsNoReceiptsWhenTheSearchFoundEverything(t *testing.T) {
	plan := &provider.Plan{Operations: []provider.Operation{{ID: "create-WI-001", ItemID: "WI-001", IdempotencyKey: "one"}}}
	fake := &fakeAdapter{published: map[string]provider.RemoteRef{"one": {Key: "ED-1"}}}
	adapter := &confirmAdapter{fakeAdapter: fake}
	recorded := func() map[domain.ItemID]provider.RemoteRef {
		t.Error("read the receipts though the search found every issue")
		return nil
	}
	if _, err := resolvePending(context.Background(), plan, adapter, &provider.Receipt{}, recorded); err != nil {
		t.Fatal(err)
	}
}

func TestRecordedItemsIgnoreOtherBundlesBesideAPlanWrittenElsewhere(t *testing.T) {
	root := t.TempDir()
	other := &provider.Receipt{
		SchemaVersion: provider.SchemaVersion, PlanDigest: "sha256:other", Provider: "jira", TargetName: "work", UpdatedAt: time.Now(),
		Operations: map[string]provider.OperationResult{"create-WI-001": {ItemID: "WI-001", Status: "created", Remote: &provider.RemoteRef{Key: "OTHER-1"}}},
	}
	// Another discovery's receipts sit under the directory the plan was written to.
	if err := WriteYAMLAtomic(filepath.Join(root, "other-discovery", ".publish", "work", "receipt.yaml"), other); err != nil {
		t.Fatal(err)
	}
	plan := &provider.Plan{Provider: "jira", TargetName: "work", BundlePath: filepath.Join(root, "this-discovery")}
	if recorded := recordedItems(plan, filepath.Join(root, "receipt.yaml")); len(recorded) != 0 {
		t.Errorf("recorded = %v, want nothing from another discovery", recorded)
	}
}

func TestRecordedItemsReadTheArchiveBesideAPlanWrittenElsewhere(t *testing.T) {
	elsewhere := t.TempDir()
	archived := &provider.Receipt{
		SchemaVersion: provider.SchemaVersion, PlanDigest: "sha256:earlier", Provider: "jira", TargetName: "work", UpdatedAt: time.Now(),
		Operations: map[string]provider.OperationResult{"create-WI-001": {ItemID: "WI-001", Status: "created", Remote: &provider.RemoteRef{Key: "ED-1"}}},
	}
	if err := WriteYAMLAtomic(filepath.Join(elsewhere, archiveDirectory, archivePrefix+"20261101T000000Z-aaaaaaaaaaaa.yaml"), archived); err != nil {
		t.Fatal(err)
	}
	// A backup named like an archive, outside archive/, is not one.
	backup := *archived
	backup.UpdatedAt = time.Now().Add(time.Hour)
	backup.Operations = map[string]provider.OperationResult{"create-WI-001": {ItemID: "WI-001", Status: "created", Remote: &provider.RemoteRef{Key: "STALE-1"}}}
	bundle := t.TempDir()
	if err := WriteYAMLAtomic(filepath.Join(bundle, ".publish", "work", archivePrefix+"backup.yaml"), &backup); err != nil {
		t.Fatal(err)
	}
	plan := &provider.Plan{Provider: "jira", TargetName: "work", BundlePath: bundle}
	if recorded := recordedItems(plan, filepath.Join(elsewhere, receiptFile)); recorded["WI-001"].Key != "ED-1" {
		t.Errorf("recorded = %v, want ED-1 from the archive beside the plan, not the backup", recorded)
	}
}
