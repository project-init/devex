package publish

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/project-init/devex/internal/discovery/provider"
	"gopkg.in/yaml.v3"
)

func WriteYAMLAtomic(path string, value any) error {
	content, err := yaml.Marshal(value)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".discovery-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if _, err := temporary.Write(content); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Chmod(temporaryPath, 0o644); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}

func LoadPlan(path string) (*provider.Plan, error) {
	var plan provider.Plan
	if err := loadStrict(path, &plan); err != nil {
		return nil, err
	}
	if plan.SchemaVersion != provider.SchemaVersion {
		return nil, fmt.Errorf("unsupported plan schema version %q", plan.SchemaVersion)
	}
	expected, err := PlanDigest(&plan)
	if err != nil {
		return nil, err
	}
	if expected != plan.PlanDigest {
		return nil, fmt.Errorf("plan digest mismatch: the plan was modified after generation")
	}
	return &plan, nil
}

func LoadReceipt(path string) (*provider.Receipt, error) {
	var receipt provider.Receipt
	if err := loadStrict(path, &receipt); err != nil {
		return nil, err
	}
	if receipt.SchemaVersion != provider.SchemaVersion {
		return nil, fmt.Errorf("unsupported receipt schema version %q", receipt.SchemaVersion)
	}
	if receipt.PlanDigest == "" {
		return nil, fmt.Errorf("receipt is missing plan_digest")
	}
	if receipt.Operations == nil {
		receipt.Operations = make(map[string]provider.OperationResult)
	}
	return &receipt, nil
}

func PlanDigest(plan *provider.Plan) (string, error) {
	copy := *plan
	copy.PlanDigest = ""
	// The digest identifies the work, not the run that produced it. Hashing the timestamp gave
	// an unchanged bundle a new digest on every plan, and hashing the bundle's absolute path
	// gave it a different digest on every machine; both discarded a usable receipt. SourceDigest
	// already identifies the bundle's content, so its location adds nothing.
	copy.GeneratedAt = time.Time{}
	copy.BundlePath = ""
	copy.Operations = append([]provider.Operation(nil), plan.Operations...)
	for index := range copy.Operations {
		if copy.Operations[index].Fields == nil {
			copy.Operations[index].Fields = map[string]any{}
		}
	}
	content, err := json.Marshal(copy)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(content)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func loadStrict(path string, target any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	decoder.KnownFields(true)
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	return nil
}
