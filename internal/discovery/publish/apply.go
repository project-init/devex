package publish

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/project-init/devex/internal/discovery/artifact"
	"github.com/project-init/devex/internal/discovery/domain"
	"github.com/project-init/devex/internal/discovery/provider"
)

// Progress reports one operation's outcome during apply: its position among the plan's
// operations, and whether a previous run's receipt had already settled it.
type Progress struct {
	Index     int // from 1
	Total     int
	Operation provider.Operation
	Result    provider.OperationResult
	Resumed   bool
}

// ApplyHooks lets the caller watch an apply. Any hook may be nil.
type ApplyHooks struct {
	// Remote wraps each remote read that precedes writing, such as with a spinner.
	Remote func(ctx context.Context, title string, read func(context.Context) error) error
	// Progress receives each operation's outcome, in plan order.
	Progress func(Progress)
	// Archived receives where a receipt for another plan went.
	Archived func(path string)
}

func (h ApplyHooks) remote(ctx context.Context, title string, read func(context.Context) error) error {
	if h.Remote == nil {
		return read(ctx)
	}
	return h.Remote(ctx, title, read)
}

// defaultConcurrency runs one operation at a time, unless the adapter declares it is safe to run
// more.
const defaultConcurrency = 1

// Apply runs the plan at planPath, recording each outcome in its receipt so a rerun resumes. It
// runs operations whose dependencies have finished side by side, up to the adapter's limit, and
// writes the receipt from one goroutine. After a failed operation it lets the operations already
// running finish and starts no more.
func Apply(
	ctx context.Context,
	planPath string,
	adapter provider.Adapter,
	hooks ApplyHooks,
) (*provider.Receipt, error) {
	plan, err := LoadPlan(planPath)
	if err != nil {
		return nil, err
	}
	if adapter.ID() != plan.Provider {
		return nil, fmt.Errorf("plan provider %q does not match adapter %q", plan.Provider, adapter.ID())
	}
	bundle, err := artifact.Load(plan.BundlePath)
	if err != nil {
		return nil, err
	}
	if err := ValidatePlan(plan, bundle); err != nil {
		return nil, err
	}

	receiptPath := DefaultReceiptPath(planPath)
	receipt, archived, err := existingReceipt(receiptPath, plan)
	if err != nil {
		return nil, err
	}
	if archived != "" && hooks.Archived != nil {
		hooks.Archived(archived)
	}
	resolved := resolvedItems(receipt)
	receipt.Status = "applying"
	if err := saveReceipt(receiptPath, receipt); err != nil {
		return nil, err
	}

	// A failed search leaves the receipt on disk claiming it is mid-apply, so record the stop
	// and hand the receipt back rather than returning nil and losing the trail.
	var published map[string]provider.RemoteRef
	resolveErr := hooks.remote(ctx, "Checking for work already published", func(ctx context.Context) error {
		var err error
		published, err = resolvePending(ctx, plan, adapter, receipt, func() map[domain.ItemID]provider.RemoteRef {
			return recordedItems(plan, receiptPath)
		})
		return err
	})
	if resolveErr != nil {
		receipt.Status = "partial"
		if err := saveReceipt(receiptPath, receipt); err != nil {
			return nil, err
		}
		return receipt, fmt.Errorf("resolve already published work: %w", resolveErr)
	}

	if preflighter, ok := adapter.(provider.Preflighter); ok {
		// Only the creates left matter: one the receipt settled, or the remote already holds, is reused.
		pending := slices.DeleteFunc(slices.Clone(plan.Operations), func(operation provider.Operation) bool {
			_, reused := published[operation.IdempotencyKey]
			return operation.Action != provider.ActionCreateIssue || reused || !outstanding(receipt, operation)
		})
		err := hooks.remote(ctx, "Checking the plan against the target", func(ctx context.Context) error {
			return preflighter.Preflight(ctx, plan.Target, pending)
		})
		if err != nil {
			receipt.Status = "partial"
			return receipt, errors.Join(err, saveReceipt(receiptPath, receipt))
		}
	}
	completed := 0
	report := func(operation provider.Operation, result provider.OperationResult, resumed bool) {
		completed++
		if hooks.Progress != nil {
			hooks.Progress(Progress{Index: completed, Total: len(plan.Operations), Operation: operation, Result: result, Resumed: resumed})
		}
	}
	// unfinished holds the operations still to succeed. resolvedItems already loaded the
	// remote work of those a receipt settled.
	unfinished := make(map[string]bool, len(plan.Operations))
	var waiting []provider.Operation
	for _, operation := range plan.Operations {
		if previous, exists := receipt.Operations[operation.ID]; exists && settled(previous) {
			report(operation, previous, true)
			continue
		}
		unfinished[operation.ID] = true
		waiting = append(waiting, operation)
	}
	// An operation runs once everything it depends on has finished, so it can reference the
	// remote work those produced.
	ready := func(operation provider.Operation) bool {
		return !slices.ContainsFunc(operation.DependsOn, func(id string) bool { return unfinished[id] })
	}

	type outcome struct {
		operation provider.Operation
		result    provider.OperationResult
	}
	limit := defaultConcurrency
	if limited, ok := adapter.(provider.ConcurrencyLimiter); ok {
		limit = max(1, limited.MaxConcurrent())
	}
	// Buffered for every operation, so no worker blocks when a failure stops the loop early.
	outcomes := make(chan outcome, len(waiting))
	inFlight := 0
	var failure error
	for len(waiting) > 0 || inFlight > 0 {
		// After a failure, the work already started finishes, but nothing new starts.
		for index := 0; failure == nil && inFlight < limit && index < len(waiting); {
			operation := waiting[index]
			if !ready(operation) {
				index++
				continue
			}
			waiting = slices.Delete(waiting, index, index+1)
			view := maps.Clone(resolved) // a worker's own copy, holding every dependency's result
			inFlight++
			go func() { outcomes <- outcome{operation, applyOperation(ctx, plan, adapter, operation, published, view)} }()
		}
		if inFlight == 0 {
			break // stopped by a failure, or the rest wait on each other
		}
		done := <-outcomes
		inFlight--
		operation, result := done.operation, done.result
		receipt.Operations[operation.ID] = result
		if result.Status == "failed" {
			receipt.Status = "partial"
			failure = cmp.Or(failure, fmt.Errorf("operation %s failed: %s", operation.ID, result.Error))
		} else {
			delete(unfinished, operation.ID)
			if operation.ItemID != "" {
				resolved[operation.ItemID] = *result.Remote
			}
		}
		if err := saveReceipt(receiptPath, receipt); err != nil {
			receipt.Status = "partial"
			failure = cmp.Or(failure, err)
		}
		report(operation, result, false)
	}
	if failure == nil && len(waiting) > 0 {
		receipt.Status = "partial"
		failure = fmt.Errorf("%d operations depend on each other in a cycle, so none can run; plan again", len(waiting))
		_ = saveReceipt(receiptPath, receipt)
	}
	if failure != nil {
		return receipt, failure
	}

	receipt.Status = "complete"
	if err := saveReceipt(receiptPath, receipt); err != nil {
		return nil, err
	}
	return receipt, nil
}

// resolvePending finds which still-outstanding item operations already exist remotely. Operations
// a receipt has settled are skipped, and relationship operations are never offered because no
// remote carries their idempotency key; an empty set skips the call, so a resume that only has
// links left costs nothing. A search can lag behind a recent write, so resolvePending then asks a
// Confirmer to read directly each issue a receipt recorded that the search missed. It reads the
// receipts, through recorded, only when the search missed something.
func resolvePending(
	ctx context.Context,
	plan *provider.Plan,
	adapter provider.Adapter,
	receipt *provider.Receipt,
	recorded func() map[domain.ItemID]provider.RemoteRef,
) (map[string]provider.RemoteRef, error) {
	pending := make([]provider.Operation, 0, len(plan.Operations))
	for _, operation := range plan.Operations {
		if operation.ItemID != "" && outstanding(receipt, operation) {
			pending = append(pending, operation)
		}
	}
	if len(pending) == 0 {
		return nil, nil
	}
	published, err := adapter.Resolve(ctx, plan.Target, plan, pending)
	if err != nil {
		return nil, err
	}
	confirmer, ok := adapter.(provider.Confirmer)
	if !ok || len(published) == len(pending) {
		return published, nil
	}
	candidates := recordedCandidates(pending, published, recorded())
	if len(candidates) == 0 {
		return published, nil
	}
	confirmed, err := confirmer.Confirm(ctx, plan.Target, candidates)
	if err != nil {
		return nil, err
	}
	if published == nil { // Resolve may report nothing found as nil
		published = make(map[string]provider.RemoteRef, len(confirmed))
	}
	maps.Copy(published, confirmed)
	return published, nil
}

// recordedCandidates returns, keyed by idempotency key, the recorded issue of each pending
// operation Resolve did not find.
func recordedCandidates(pending []provider.Operation, published map[string]provider.RemoteRef, recorded map[domain.ItemID]provider.RemoteRef) map[string]provider.RemoteRef {
	candidates := make(map[string]provider.RemoteRef)
	for _, operation := range pending {
		if _, found := published[operation.IdempotencyKey]; found {
			continue
		}
		if remote, ok := recorded[operation.ItemID]; ok {
			candidates[operation.IdempotencyKey] = remote
		}
	}
	return candidates
}

// recordedItems collects the remote issue each item had in the receipts for plan's provider and
// target: every receipt under the bundle's .publish/<target>/, current or archived and from the
// main plan or a sync plan, and the receipt and archive beside receiptPath, the newest receipt
// winning. A receipt that cannot be read only loses its hint, since the search still runs.
func recordedItems(plan *provider.Plan, receiptPath string) map[domain.ItemID]provider.RemoteRef {
	var paths []string
	_ = filepath.WalkDir(filepath.Join(plan.BundlePath, ".publish", plan.TargetName), func(path string, entry fs.DirEntry, err error) error {
		if err == nil && !entry.IsDir() && isReceiptFile(path) {
			paths = append(paths, path)
		}
		return nil
	})
	// A plan written elsewhere keeps its receipt beside it; its directory may hold far more, such
	// as another bundle's, so only the receipt and its archive count.
	ownDirectory := filepath.Dir(receiptPath)
	archived, _ := filepath.Glob(filepath.Join(ownDirectory, archiveDirectory, archivePrefix+"*.yaml"))
	paths = append(append(paths, filepath.Join(ownDirectory, receiptFile)), archived...)

	var receipts []*provider.Receipt
	seen := make(map[string]bool, len(paths))
	for _, path := range paths {
		absolute, err := filepath.Abs(path)
		if err != nil || seen[absolute] {
			continue
		}
		seen[absolute] = true
		if receipt, err := LoadReceipt(absolute); err == nil && receipt.Provider == plan.Provider && receipt.TargetName == plan.TargetName {
			receipts = append(receipts, receipt)
		}
	}
	slices.SortStableFunc(receipts, func(a, b *provider.Receipt) int { return a.UpdatedAt.Compare(b.UpdatedAt) })
	recorded := make(map[domain.ItemID]provider.RemoteRef)
	for _, receipt := range receipts {
		maps.Copy(recorded, resolvedItems(receipt))
	}
	return recorded
}

// isReceiptFile reports whether path names a receipt: a plan's receipt, or one existingReceipt
// archived.
func isReceiptFile(path string) bool {
	name := filepath.Base(path)
	archived := filepath.Base(filepath.Dir(path)) == archiveDirectory && strings.HasPrefix(name, archivePrefix) && strings.HasSuffix(name, ".yaml")
	return name == receiptFile || archived
}

// applyOperation runs one operation. resolved is the caller's copy: the scheduler records each
// result in the shared map itself. A planned create reuses a matching remote issue instead of
// creating a second one. A reuse or update fails when the issue the plan saw is gone; an update
// then executes against that issue.
func applyOperation(
	ctx context.Context,
	plan *provider.Plan,
	adapter provider.Adapter,
	operation provider.Operation,
	published map[string]provider.RemoteRef,
	resolved map[domain.ItemID]provider.RemoteRef,
) provider.OperationResult {
	result := provider.OperationResult{ItemID: operation.ItemID}
	remote, found := published[operation.IdempotencyKey]
	if operation.ItemID != "" {
		switch {
		case found && operation.Action != provider.ActionUpdateIssue:
			result.Status = "reused"
			result.Remote = &remote
			return result
		case !found && (operation.Action == provider.ActionReuseIssue || operation.Action == provider.ActionUpdateIssue):
			result.Status = "failed"
			result.Error = "the issue this plan expected is gone; plan again"
			return result
		case found:
			resolved[operation.ItemID] = remote // the update's target, which Execute reads
		}
	}

	executed, err := adapter.Execute(ctx, plan.Target, operation, resolved)
	if err != nil {
		result.Status = "failed"
		result.Error = err.Error()
		return result
	}
	result.Status = executedStatus(operation.Action)
	result.Remote = &executed

	return result
}

func executedStatus(action string) string {
	switch action {
	case provider.ActionUpdateIssue:
		return "updated"
	case provider.ActionUnlinkIssues:
		return "unlinked"
	default:
		return "created"
	}
}

func outstanding(receipt *provider.Receipt, operation provider.Operation) bool {
	previous, exists := receipt.Operations[operation.ID]
	return !exists || !settled(previous)
}

// settled reports whether a receipt entry needs no further work on resume.
func settled(result provider.OperationResult) bool {
	switch result.Status {
	case "created", "reused", "updated", "unlinked":
		return result.Remote != nil
	default:
		return false
	}
}

// existingReceipt loads the receipt at path, or starts one. A receipt for another plan, such as
// the one a re-plan replaced, moves to archive/ first: apply still reuses the work it recorded,
// which it finds remotely. archived is where it went, or "".
func existingReceipt(path string, plan *provider.Plan) (receipt *provider.Receipt, archived string, err error) {
	receipt, err = LoadReceipt(path)
	switch {
	case err == nil && receipt.PlanDigest == plan.PlanDigest:
		return receipt, "", nil
	case err == nil:
		// The digest names the file, so only its hex survives: the receipt is a file on disk.
		digest := strings.Map(func(r rune) rune {
			if strings.ContainsRune("0123456789abcdef", r) {
				return r
			}
			return -1
		}, strings.TrimPrefix(receipt.PlanDigest, "sha256:"))
		name := fmt.Sprintf("%s%s-%.12s.yaml", archivePrefix, receipt.UpdatedAt.UTC().Format("20060102T150405Z"), digest)
		archived = filepath.Join(filepath.Dir(path), archiveDirectory, name)
		if err := os.MkdirAll(filepath.Dir(archived), 0o755); err != nil {
			return nil, "", err
		}
		if err := os.Rename(path, archived); err != nil {
			return nil, "", err
		}
	case !errors.Is(err, os.ErrNotExist):
		return nil, "", err
	}
	return &provider.Receipt{
		SchemaVersion: provider.SchemaVersion,
		PlanID:        plan.ID,
		Provider:      plan.Provider,
		TargetName:    plan.TargetName,
		PlanDigest:    plan.PlanDigest,
		Status:        "planned",
		Operations:    make(map[string]provider.OperationResult),
	}, archived, nil
}

func resolvedItems(receipt *provider.Receipt) map[domain.ItemID]provider.RemoteRef {
	resolved := make(map[domain.ItemID]provider.RemoteRef)
	for _, result := range receipt.Operations {
		if result.ItemID == "" {
			continue
		}
		if settled(result) {
			resolved[result.ItemID] = *result.Remote
		}
	}
	return resolved
}

func saveReceipt(path string, receipt *provider.Receipt) error {
	receipt.UpdatedAt = time.Now().UTC()
	return WriteYAMLAtomic(path, receipt)
}
