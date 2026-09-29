package publish

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"slices"
	"time"

	"github.com/project-init/devex/internal/discovery/artifact"
	"github.com/project-init/devex/internal/discovery/config"
	"github.com/project-init/devex/internal/discovery/domain"
	"github.com/project-init/devex/internal/discovery/provider"
	"github.com/project-init/devex/internal/discovery/source"
)

// PlanOptions selects how a plan treats work already published. Sync reads the remote and plans
// updates and relationship removals; Force, with Sync, overwrites issues edited remotely.
type PlanOptions struct {
	Sync  bool
	Force bool
}

func CreatePlan(
	ctx context.Context,
	bundle *artifact.Bundle,
	targetName string,
	target config.Target,
	adapter provider.Adapter,
	options PlanOptions,
) (*provider.Plan, error) {
	if options.Force && !options.Sync {
		return nil, fmt.Errorf("--force requires --sync")
	}
	input := provider.PlanInput{
		WorkBreakdown: bundle.WorkBreakdown,
		DocumentURL:   source.DocumentURL(bundle.Directory, bundle.WorkBreakdown.Discovery.Document),
		TrackingURL:   artifact.TrackingURL(bundle.DiscoveryContent),
	}
	operations, warnings, err := adapter.Plan(ctx, input, target)
	if err != nil {
		return nil, err
	}
	// Sync and apply digest planned fields, so both must see them as the plan file stores them.
	if operations, err = canonical(operations); err != nil {
		return nil, err
	}
	seed := fmt.Sprintf("%s\x00%s\x00%s", bundle.Digest(), targetName, adapter.ID())
	if options.Sync {
		syncer, ok := adapter.(provider.Syncer)
		if !ok {
			return nil, fmt.Errorf("%s targets do not support sync", adapter.ID())
		}
		planned := operations
		operations, warnings, err = syncer.Sync(ctx, target, provider.SyncRequest{
			DiscoveryID: bundle.WorkBreakdown.Discovery.ID,
			Operations:  planned,
			Warnings:    warnings,
			Force:       options.Force,
		})
		if err != nil {
			return nil, fmt.Errorf("read published work: %w", err)
		}
		operations = followRenames(planned, operations)
		seed += "\x00sync"
	}
	id := sha256.Sum256([]byte(seed))
	plan := &provider.Plan{
		SchemaVersion: provider.SchemaVersion,
		ID:            "plan-" + hex.EncodeToString(id[:8]),
		Provider:      adapter.ID(),
		TargetName:    targetName,
		Target:        target,
		DiscoveryID:   bundle.WorkBreakdown.Discovery.ID,
		BundlePath:    bundle.Directory,
		SourceDigest:  bundle.Digest(),
		GeneratedAt:   time.Now().UTC(),
		Warnings:      warnings,
		Operations:    operations,
	}
	plan.PlanDigest, err = PlanDigest(plan)
	if err != nil {
		return nil, err
	}
	return plan, nil
}

func DefaultPlanPath(bundleDirectory string, targetName string) string {
	return filepath.Join(bundleDirectory, ".publish", targetName, "plan.yaml")
}

// followRenames points dependencies at the operations a sync renamed, such as a create it turned
// into a reuse. Matching each item's operation before and after the sync yields the renames.
func followRenames(before []provider.Operation, after []provider.Operation) []provider.Operation {
	current := make(map[domain.ItemID]string, len(after))
	for _, operation := range after {
		if operation.ItemID != "" {
			current[operation.ItemID] = operation.ID
		}
	}
	renamed := make(map[string]string, len(before))
	for _, operation := range before {
		if id, exists := current[operation.ItemID]; exists && id != operation.ID {
			renamed[operation.ID] = id
		}
	}
	for index, operation := range after {
		// Sync copies operations by value, so a fresh slice keeps before's dependencies intact.
		dependsOn := slices.Clone(operation.DependsOn)
		for dependency, id := range dependsOn {
			if renamedTo, exists := renamed[id]; exists {
				dependsOn[dependency] = renamedTo
			}
		}
		after[index].DependsOn = dependsOn
	}

	return after
}

// DefaultSyncPlanPath names each sync plan's directory for when it was made. A sync captures the
// remote at that moment, so two plans never share a receipt, even with equal content.
func DefaultSyncPlanPath(bundleDirectory string, targetName string, generatedAt time.Time) string {
	return filepath.Join(bundleDirectory, ".publish", targetName, "sync-"+generatedAt.UTC().Format("20060102T150405.000Z"), "plan.yaml")
}

func DefaultReceiptPath(planPath string) string {
	return filepath.Join(filepath.Dir(planPath), "receipt.yaml")
}

func ValidatePlan(plan *provider.Plan, bundle *artifact.Bundle) error {
	if plan.SourceDigest != bundle.Digest() {
		return fmt.Errorf("discovery bundle changed after the publication plan was generated; generate a new plan")
	}
	if err := plan.Target.Validate(); err != nil {
		return fmt.Errorf("invalid target embedded in plan: %w", err)
	}
	return nil
}
