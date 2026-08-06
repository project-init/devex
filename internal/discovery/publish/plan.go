package publish

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"time"

	"github.com/project-init/devex/internal/discovery/artifact"
	"github.com/project-init/devex/internal/discovery/config"
	"github.com/project-init/devex/internal/discovery/provider"
)

func CreatePlan(
	ctx context.Context,
	bundle *artifact.Bundle,
	targetName string,
	target config.Target,
	adapter provider.Adapter,
) (*provider.Plan, error) {
	operations, warnings, err := adapter.Plan(ctx, bundle.WorkBreakdown, bundle.DiscoveryContent, target)
	if err != nil {
		return nil, err
	}
	seed := sha256.Sum256([]byte(bundle.Digest() + "\x00" + targetName + "\x00" + adapter.ID()))
	plan := &provider.Plan{
		SchemaVersion: provider.SchemaVersion,
		ID:            "plan-" + hex.EncodeToString(seed[:8]),
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
