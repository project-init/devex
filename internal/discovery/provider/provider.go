package provider

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/project-init/devex/internal/discovery/config"
	"github.com/project-init/devex/internal/discovery/domain"
)

const SchemaVersion = "v1"

type Adapter interface {
	ID() string
	Plan(context.Context, *domain.WorkBreakdown, []byte, config.Target) ([]Operation, []string, error)
	// Resolve reports which of the given operations already exist remotely, keyed by
	// idempotency key. Apply calls it once with the operations a receipt has not already
	// accounted for, so an adapter can search for exactly the work that remains.
	Resolve(context.Context, config.Target, *Plan, []Operation) (map[string]RemoteRef, error)
	Execute(context.Context, config.Target, Operation, map[domain.ItemID]RemoteRef) (RemoteRef, error)
}

type Plan struct {
	SchemaVersion string        `yaml:"schema_version" json:"schema_version"`
	ID            string        `yaml:"id" json:"id"`
	Provider      string        `yaml:"provider" json:"provider"`
	TargetName    string        `yaml:"target" json:"target"`
	Target        config.Target `yaml:"target_config" json:"target_config"`
	DiscoveryID   string        `yaml:"discovery_id" json:"discovery_id"`
	BundlePath    string        `yaml:"bundle_path" json:"bundle_path"`
	SourceDigest  string        `yaml:"source_digest" json:"source_digest"`
	PlanDigest    string        `yaml:"plan_digest" json:"plan_digest"`
	GeneratedAt   time.Time     `yaml:"generated_at" json:"generated_at"`
	Warnings      []string      `yaml:"warnings,omitempty" json:"warnings,omitempty"`
	Operations    []Operation   `yaml:"operations" json:"operations"`
}

type Operation struct {
	ID             string         `yaml:"id" json:"id"`
	Action         string         `yaml:"action" json:"action"`
	ItemID         domain.ItemID  `yaml:"item_id" json:"item_id"`
	DependsOn      []string       `yaml:"depends_on,omitempty" json:"depends_on,omitempty"`
	IdempotencyKey string         `yaml:"idempotency_key" json:"idempotency_key"`
	Summary        string         `yaml:"summary" json:"summary"`
	Fields         map[string]any `yaml:"fields" json:"fields"`
}

type Receipt struct {
	SchemaVersion string                     `yaml:"schema_version" json:"schema_version"`
	PlanID        string                     `yaml:"plan_id" json:"plan_id"`
	Provider      string                     `yaml:"provider" json:"provider"`
	TargetName    string                     `yaml:"target" json:"target"`
	PlanDigest    string                     `yaml:"plan_digest" json:"plan_digest"`
	Status        string                     `yaml:"status" json:"status"`
	UpdatedAt     time.Time                  `yaml:"updated_at" json:"updated_at"`
	Operations    map[string]OperationResult `yaml:"operations" json:"operations"`
}

type OperationResult struct {
	Status string        `yaml:"status" json:"status"`
	ItemID domain.ItemID `yaml:"item_id" json:"item_id"`
	Remote *RemoteRef    `yaml:"remote,omitempty" json:"remote,omitempty"`
	Error  string        `yaml:"error,omitempty" json:"error,omitempty"`
}

type RemoteRef struct {
	ID   string `yaml:"id" json:"id"`
	Key  string `yaml:"key,omitempty" json:"key,omitempty"`
	URL  string `yaml:"url" json:"url"`
	Type string `yaml:"type" json:"type"`
}

func ResolveReferences(value string, resolved map[domain.ItemID]RemoteRef) (string, error) {
	for {
		start := strings.Index(value, "{{remote:")
		if start == -1 {
			return value, nil
		}
		endOffset := strings.Index(value[start:], "}}")
		if endOffset == -1 {
			return "", fmt.Errorf("unterminated remote reference")
		}
		end := start + endOffset + 2
		id := domain.ItemID(value[start+len("{{remote:") : start+endOffset])
		remote, exists := resolved[id]
		if !exists {
			return "", fmt.Errorf("remote reference for %q is unavailable", id)
		}
		value = value[:start] + remote.URL + value[end:]
	}
}
