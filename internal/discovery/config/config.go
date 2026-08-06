package config

import (
	"bytes"
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/project-init/devex/internal/discovery/domain"
	"gopkg.in/yaml.v3"
)

//go:embed example.yaml
var examples embed.FS

type File struct {
	DefaultTarget string            `yaml:"default_target,omitempty"`
	DefaultLabels []string          `yaml:"default_labels,omitempty"`
	Targets       map[string]Target `yaml:"targets"`
}

type Target struct {
	Provider string        `yaml:"provider" json:"provider"`
	Jira     *JiraTarget   `yaml:"jira,omitempty" json:"jira,omitempty"`
	GitHub   *GitHubTarget `yaml:"github,omitempty" json:"github,omitempty"`
}

type JiraTarget struct {
	BaseURL     string                     `yaml:"base_url" json:"base_url"`
	ProjectKey  string                     `yaml:"project_key" json:"project_key"`
	KindMapping map[domain.ItemKind]string `yaml:"kind_mapping" json:"kind_mapping"`
	// LinkType names the Jira issue link type used for depends_on edges. Defaults to Blocks.
	LinkType          string `yaml:"link_type,omitempty" json:"link_type,omitempty"`
	HierarchyFallback string `yaml:"hierarchy_fallback,omitempty" json:"hierarchy_fallback,omitempty"`
}

type GitHubTarget struct {
	Owner             string                     `yaml:"owner" json:"owner"`
	Repository        string                     `yaml:"repository" json:"repository"`
	APIURL            string                     `yaml:"api_url,omitempty" json:"api_url,omitempty"`
	KindLabels        map[domain.ItemKind]string `yaml:"kind_labels,omitempty" json:"kind_labels,omitempty"`
	HierarchyFallback string                     `yaml:"hierarchy_fallback,omitempty" json:"hierarchy_fallback,omitempty"`
}

func Load(path string) (*File, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read target configuration %s: %w", path, err)
	}
	var file File
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	decoder.KnownFields(true)
	if err := decoder.Decode(&file); err != nil {
		return nil, fmt.Errorf("decode target configuration %s: %w", path, err)
	}
	if len(file.Targets) == 0 {
		return nil, fmt.Errorf("target configuration must define at least one target")
	}
	for name, target := range file.Targets {
		if err := target.Validate(); err != nil {
			return nil, fmt.Errorf("target %q: %w", name, err)
		}
	}
	if file.DefaultTarget != "" {
		if _, exists := file.Targets[file.DefaultTarget]; !exists {
			return nil, fmt.Errorf("default_target %q is not defined in targets", file.DefaultTarget)
		}
	}
	seenLabels := make(map[string]struct{}, len(file.DefaultLabels))
	for index, label := range file.DefaultLabels {
		if strings.TrimSpace(label) == "" {
			return nil, fmt.Errorf("default_labels[%d] must not be empty", index)
		}
		if _, exists := seenLabels[label]; exists {
			return nil, fmt.Errorf("default_labels contains duplicate label %q", label)
		}
		seenLabels[label] = struct{}{}
	}
	return &file, nil
}

// ResolveTarget selects a publication target using an explicit name, the
// configured default, or the sole configured target, in that order.
func (f *File) ResolveTarget(explicit string) (string, Target, error) {
	names := f.targetNames()
	if explicit != "" {
		target, exists := f.Targets[explicit]
		if !exists {
			return "", Target{}, fmt.Errorf(
				"target %q is not defined; available targets: %s",
				explicit,
				strings.Join(names, ", "),
			)
		}
		return explicit, target, nil
	}
	if f.DefaultTarget != "" {
		target, exists := f.Targets[f.DefaultTarget]
		if !exists {
			return "", Target{}, fmt.Errorf("default_target %q is not defined in targets", f.DefaultTarget)
		}
		return f.DefaultTarget, target, nil
	}
	if len(names) == 1 {
		return names[0], f.Targets[names[0]], nil
	}
	return "", Target{}, fmt.Errorf(
		"multiple publication targets are configured (%s); pass --target or set default_target",
		strings.Join(names, ", "),
	)
}

func (f *File) targetNames() []string {
	names := make([]string, 0, len(f.Targets))
	for name := range f.Targets {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func WriteExample(path string, force bool) error {
	content, err := examples.ReadFile("example.yaml")
	if err != nil {
		return fmt.Errorf("read embedded discovery configuration: %w", err)
	}
	if existing, readErr := os.ReadFile(path); readErr == nil {
		if bytes.Equal(existing, content) {
			return nil
		}
		if !force {
			return fmt.Errorf("configuration %s already exists with different contents; use --force to replace it", path)
		}
	} else if !os.IsNotExist(readErr) {
		return fmt.Errorf("read configuration %s: %w", path, readErr)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create configuration directory for %s: %w", path, err)
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		return fmt.Errorf("write configuration %s: %w", path, err)
	}
	return nil
}

func (t Target) Validate() error {
	switch strings.ToLower(t.Provider) {
	case "jira":
		if t.Jira == nil || t.GitHub != nil {
			return fmt.Errorf("jira provider requires only a jira configuration")
		}
		if t.Jira.BaseURL == "" || t.Jira.ProjectKey == "" {
			return fmt.Errorf("jira.base_url and jira.project_key are required")
		}
		if !validFallback(t.Jira.HierarchyFallback) {
			return fmt.Errorf("jira.hierarchy_fallback must be error or flatten")
		}
	case "github":
		if t.GitHub == nil || t.Jira != nil {
			return fmt.Errorf("github provider requires only a github configuration")
		}
		if t.GitHub.Owner == "" || t.GitHub.Repository == "" {
			return fmt.Errorf("github.owner and github.repository are required")
		}
		if !validFallback(t.GitHub.HierarchyFallback) {
			return fmt.Errorf("github.hierarchy_fallback must be error or flatten")
		}
	default:
		return fmt.Errorf("provider must be jira or github")
	}
	return nil
}

func validFallback(value string) bool {
	return value == "" || value == "error" || value == "flatten"
}
