// Package gosync keeps every Go version pin in a repository on one version.
package gosync

import (
	"fmt"
	"slices"

	"github.com/project-init/devex/internal/sre/config"
	"github.com/project-init/devex/internal/sre/dependencies/pins"
)

const (
	// TargetPatch upgrades to the latest patch of the current minor line.
	TargetPatch = "patch"
	// TargetLatest upgrades to the latest release.
	TargetLatest = "latest"

	// DirectiveNone leaves the go directives in go.mod and go.work alone, requiring only that
	// they stay at or below the toolchain.
	DirectiveNone = "none"
	// DirectiveMinor holds the go directives on the toolchain's minor line.
	DirectiveMinor = "minor"
	// DirectiveExact sets the go directives to the toolchain version.
	DirectiveExact = "exact"
)

// Settings is validated Go sync configuration with defaults applied.
type Settings struct {
	Target    string
	Directive string
	Discovery pins.Options
}

// NewSettings validates cfg and applies defaults for a repository rooted at root.
func NewSettings(root string, cfg config.GoDependenciesConfiguration) (Settings, error) {
	s := Settings{
		Target:    cfg.Target,
		Directive: cfg.Directive,
		Discovery: pins.Options{Root: root, Images: cfg.Images, Exclude: cfg.Exclude},
	}
	if s.Target == "" {
		s.Target = TargetPatch
	}
	if s.Directive == "" {
		s.Directive = DirectiveNone
	}
	if !slices.Contains([]string{TargetPatch, TargetLatest}, s.Target) {
		return Settings{}, fmt.Errorf("dependencies.go.target %q must be %s or %s", s.Target, TargetPatch, TargetLatest)
	}
	if !slices.Contains([]string{DirectiveNone, DirectiveMinor, DirectiveExact}, s.Directive) {
		return Settings{}, fmt.Errorf("dependencies.go.directive %q must be %s, %s, or %s", s.Directive, DirectiveNone, DirectiveMinor, DirectiveExact)
	}
	for i, p := range cfg.Pins {
		d, err := pins.CompileDeclared(p.Files, p.Pattern)
		if err != nil {
			return Settings{}, fmt.Errorf("dependencies.go.pins[%d]: %w", i, err)
		}
		s.Discovery.Declared = append(s.Discovery.Declared, d)
	}

	return s, nil
}
