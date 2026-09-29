package publish

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/project-init/devex/internal/discovery/artifact"
	"github.com/project-init/devex/internal/discovery/domain"
	"github.com/project-init/devex/internal/discovery/provider"
)

func Apply(
	ctx context.Context,
	planPath string,
	adapter provider.Adapter,
	onProgress func(provider.Operation, provider.OperationResult),
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
	receipt, err := existingReceipt(receiptPath, plan)
	if err != nil {
		return nil, err
	}
	resolved := resolvedItems(receipt)
	receipt.Status = "applying"
	if err := saveReceipt(receiptPath, receipt); err != nil {
		return nil, err
	}

	// A failed search leaves the receipt on disk claiming it is mid-apply, so record the stop
	// and hand the receipt back rather than returning nil and losing the trail.
	published, resolveErr := resolvePending(ctx, plan, adapter, receipt)
	if resolveErr != nil {
		receipt.Status = "partial"
		if err := saveReceipt(receiptPath, receipt); err != nil {
			return nil, err
		}
		return receipt, fmt.Errorf("resolve already published work: %w", resolveErr)
	}

	for _, operation := range plan.Operations {
		previous, exists := receipt.Operations[operation.ID]
		if exists && settled(previous) {
			if operation.ItemID != "" {
				resolved[operation.ItemID] = *previous.Remote
			}
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
		if onProgress != nil {
			onProgress(operation, result)
		}
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
		if operation.ItemID == "" {
			continue
		}
		previous, exists := receipt.Operations[operation.ID]
		if exists && settled(previous) {
			continue
		}
		pending = append(pending, operation)
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

// settled reports whether a receipt entry needs no further work on resume.
func settled(result provider.OperationResult) bool {
	switch result.Status {
	case "created", "reused", "updated", "unlinked":
		return result.Remote != nil
	default:
		return false
	}
}

func existingReceipt(path string, plan *provider.Plan) (*provider.Receipt, error) {
	receipt, err := LoadReceipt(path)
	if err == nil {
		if receipt.PlanDigest != plan.PlanDigest {
			return nil, fmt.Errorf(
				"existing receipt %s belongs to a different plan; delete it to apply this one, "+
					"which republishes nothing already recorded remotely",
				path,
			)
		}
		return receipt, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return &provider.Receipt{
		SchemaVersion: provider.SchemaVersion,
		PlanID:        plan.ID,
		Provider:      plan.Provider,
		TargetName:    plan.TargetName,
		PlanDigest:    plan.PlanDigest,
		Status:        "planned",
		Operations:    make(map[string]provider.OperationResult),
	}, nil
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
