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
	onProgress func(provider.OperationResult),
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

	for _, operation := range plan.Operations {
		previous, exists := receipt.Operations[operation.ID]
		if exists && (previous.Status == "created" || previous.Status == "reused") && previous.Remote != nil {
			resolved[operation.ItemID] = *previous.Remote
			continue
		}

		remote, lookupErr := adapter.Lookup(ctx, plan.Target, operation.IdempotencyKey)
		result := provider.OperationResult{ItemID: operation.ItemID}
		if lookupErr != nil {
			result.Status = "failed"
			result.Error = lookupErr.Error()
		} else if remote != nil {
			result.Status = "reused"
			result.Remote = remote
			resolved[operation.ItemID] = *remote
		} else {
			created, executeErr := adapter.Execute(ctx, plan.Target, operation, resolved)
			if executeErr != nil {
				result.Status = "failed"
				result.Error = executeErr.Error()
			} else {
				result.Status = "created"
				result.Remote = &created
				resolved[operation.ItemID] = created
			}
		}
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
			onProgress(result)
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

func existingReceipt(path string, plan *provider.Plan) (*provider.Receipt, error) {
	receipt, err := LoadReceipt(path)
	if err == nil {
		if receipt.PlanDigest != plan.PlanDigest {
			return nil, fmt.Errorf("existing receipt belongs to a different plan")
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
		if result.Remote != nil && (result.Status == "created" || result.Status == "reused") {
			resolved[result.ItemID] = *result.Remote
		}
	}
	return resolved
}

func saveReceipt(path string, receipt *provider.Receipt) error {
	receipt.UpdatedAt = time.Now().UTC()
	return WriteYAMLAtomic(path, receipt)
}
