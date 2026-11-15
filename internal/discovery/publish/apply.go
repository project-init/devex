package publish

import (
	"context"
	"errors"
	"fmt"
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

// Apply runs the plan at planPath, recording each outcome in its receipt so a rerun resumes.
// It stops at the first failed operation.
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
		published, err = resolvePending(ctx, plan, adapter, receipt)
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
	report := func(index int, operation provider.Operation, result provider.OperationResult, resumed bool) {
		if hooks.Progress != nil {
			hooks.Progress(Progress{Index: index + 1, Total: len(plan.Operations), Operation: operation, Result: result, Resumed: resumed})
		}
	}
	for index, operation := range plan.Operations {
		previous, exists := receipt.Operations[operation.ID]
		if exists && settled(previous) {
			if operation.ItemID != "" {
				resolved[operation.ItemID] = *previous.Remote
			}
			report(index, operation, previous, true)
			continue
		}

		result := applyOperation(ctx, plan, adapter, operation, published, resolved)
		receipt.Operations[operation.ID] = result
		if result.Status == "failed" {
			receipt.Status = "partial"
		} else {
			receipt.Status = "applying"
		}
		if err := saveReceipt(receiptPath, receipt); err != nil {
			return nil, err
		}
		report(index, operation, result, false)
		if result.Status == "failed" {
			return receipt, fmt.Errorf("operation %s failed: %s", operation.ID, result.Error)
		}
	}

	receipt.Status = "complete"
	if err := saveReceipt(receiptPath, receipt); err != nil {
		return nil, err
	}
	return receipt, nil
}

// resolvePending asks the adapter which of the still-outstanding item operations already exist
// remotely. Operations a receipt has settled are skipped, and relationship operations are never
// offered because no remote carries their idempotency key. An empty set skips the call entirely,
// so a resume that only has links left costs nothing.
func resolvePending(
	ctx context.Context,
	plan *provider.Plan,
	adapter provider.Adapter,
	receipt *provider.Receipt,
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
	return adapter.Resolve(ctx, plan.Target, plan, pending)
}

// applyOperation runs one operation. A planned create reuses a matching remote issue instead of
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
			resolved[operation.ItemID] = remote
			return result
		case !found && (operation.Action == provider.ActionReuseIssue || operation.Action == provider.ActionUpdateIssue):
			result.Status = "failed"
			result.Error = "the issue this plan expected is gone; plan again"
			return result
		case found:
			resolved[operation.ItemID] = remote
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
	if operation.ItemID != "" {
		resolved[operation.ItemID] = executed
	}

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
		name := fmt.Sprintf("receipt-%s-%.12s.yaml", receipt.UpdatedAt.UTC().Format("20060102T150405Z"), digest)
		archived = filepath.Join(filepath.Dir(path), "archive", name)
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
