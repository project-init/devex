package gosync

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/project-init/devex/internal/sre/dependencies/edit"
	"github.com/project-init/devex/internal/sre/dependencies/goversion"
	"github.com/project-init/devex/internal/sre/dependencies/pins"
	"github.com/project-init/devex/internal/sre/dependencies/registry"
)

// Resolver lists published Go releases.
type Resolver interface {
	Releases(ctx context.Context) ([]goversion.Version, error)
}

// Registry resolves container image tags to digests.
type Registry interface {
	Digest(ctx context.Context, image, tag string) (string, error)
}

// Change replaces the text at Span in File.
type Change = edit.Change

// Plan is every change a Go upgrade makes, computed and verified before devex writes a file.
type Plan struct {
	pins.Result
	Current goversion.Version
	Target  goversion.Version
	Changes []Change
}

// Planner computes a Plan.
type Planner struct {
	Settings Settings
	Resolver Resolver
	Registry Registry
	// Override forces the target version and waives drift between existing pins.
	Override *goversion.Version
}

// Plan discovers every pin, resolves the target version, and verifies each rewritten image
// tag. It writes nothing, so any error leaves the repository untouched.
func (p Planner) Plan(ctx context.Context) (Plan, error) {
	found, err := pins.Discover(p.Settings.Discovery)
	if err != nil {
		return Plan{}, err
	}
	if len(found.Modules) == 0 {
		return Plan{}, errors.New("found no go.mod; run from the root of a Go repository")
	}
	found.Warnings = append(found.Warnings, pins.ShadowWarnings(p.Settings.Discovery.Root, found)...)
	plan := Plan{Result: found}

	current, err := Current(found.Pins)
	if err != nil && p.Override == nil {
		return Plan{}, fmt.Errorf("%w\nsettle it by hand, or pass --go-version to choose one version for every pin", err)
	}
	plan.Current = current

	releases, err := p.Resolver.Releases(ctx)
	if err != nil {
		return Plan{}, err
	}
	switch {
	case p.Override != nil:
		if !goversion.Released(releases, *p.Override) {
			return Plan{}, fmt.Errorf("--go-version %s is not a published Go release", p.Override)
		}
		plan.Target = *p.Override
	case p.Settings.Target == TargetLatest:
		plan.Target, err = goversion.Latest(releases, current)
	default:
		plan.Target, err = goversion.LatestPatch(releases, current)
	}
	if err != nil {
		return Plan{}, err
	}

	if err := problemsErr(fmt.Sprintf("a go directive requires a newer Go than target %s; devex never lowers a floor", plan.Target), directiveProblems(found.Pins, plan.Target, DirectiveNone)); err != nil {
		return Plan{}, err
	}

	// planned holds every pin at the version the plan gives it, and must pass the same check
	// the upgrade runs after writing. A go.work floor rises with the modules it uses.
	planned := found
	planned.Pins = slices.Clone(found.Pins)
	for i, pin := range found.Pins {
		planned.Pins[i].Version = p.rewrite(pin, plan.Target)
	}
	for _, r := range workspaceRaises(planned) {
		planned.Pins[r.index].Version = r.module.Version
	}
	if _, err := Check(planned, p.Settings.Directive); err != nil {
		return Plan{}, fmt.Errorf("the planned versions fail check; devex wrote nothing\n%w", err)
	}

	var tagProblems []Problem
	verified := map[string]verification{}
	for i, pin := range found.Pins {
		to := planned.Pins[i].Version
		changed := to != pin.Version
		if changed {
			plan.Changes = append(plan.Changes, Change{File: pin.File, Line: pin.Line, Kind: pin.Kind, Span: pin.Span, From: pin.Version.String(), To: to.String()})
		}
		for _, img := range pin.Images {
			// A tag whose text stays put, such as golang:1.26, can still move to a new digest.
			if !changed && img.Digest == "" {
				continue
			}
			change, warning, problem := p.verifyImage(ctx, verified, pin, img, to)
			switch {
			case problem != "" && changed:
				tagProblems = append(tagProblems, Problem{Pin: pin, Reason: problem})
			case problem != "":
				// The old digest still matches an unchanged tag, so a failed refresh is safe.
				warning = fmt.Sprintf("%s: %s; left its digest as is", pin.Location(), problem)
			}
			if warning != "" {
				plan.Warnings = append(plan.Warnings, warning)
			}
			if change != nil {
				plan.Changes = append(plan.Changes, *change)
			}
		}
	}
	if err := problemsErr("image tags failed verification; devex wrote nothing", tagProblems); err != nil {
		return Plan{}, err
	}
	slices.SortStableFunc(plan.Changes, func(a, b Change) int {
		return cmp.Or(strings.Compare(a.File, b.File), cmp.Compare(a.Span.Start, b.Span.Start))
	})
	if len(plan.Changes) > 0 {
		plan.Warnings = append(plan.Warnings, lockWarnings(found.Files)...)
	}

	return plan, nil
}

// lockWarnings warns about each mise lock file in files: a lock pins exact tool versions,
// and devex leaves it on the old Go.
func lockWarnings(files []string) []string {
	var warnings []string
	for _, file := range files {
		if pins.IsMiseLock(file) {
			warnings = append(warnings, fmt.Sprintf("%s pins exact tool versions; run mise lock after the upgrade", file))
		}
	}

	return warnings
}

// rewrite returns the version pin should hold at target.
func (p Planner) rewrite(pin pins.Pin, target goversion.Version) goversion.Version {
	switch {
	case pin.IsToolchain():
		return target.WithPrecision(pin.Version.Precision)
	case p.Settings.Directive == DirectiveExact:
		return target
	case p.Settings.Directive == DirectiveMinor && !pin.Version.SameMinor(target):
		return target.MinorFloor()
	default:
		return pin.Version
	}
}

type verification struct {
	digest string
	err    error
}

// verifyImage confirms img's rewritten tag exists and, for a digest pin, returns the change
// that moves the digest with it. verified caches each reference, since many Dockerfiles
// often share one image.
func (p Planner) verifyImage(ctx context.Context, verified map[string]verification, pin pins.Pin, img pins.Image, to goversion.Version) (change *Change, warning, problem string) {
	ref := img.Ref(to)
	if img.Unresolved {
		if img.Digest != "" {
			return nil, "", fmt.Sprintf("%s takes its registry from a build arg, so devex cannot refresh its digest pin", ref)
		}

		return nil, fmt.Sprintf("%s: %s takes its registry from a build arg; devex wrote it unverified", pin.Location(), ref), ""
	}
	v, ok := verified[ref]
	if !ok {
		v.digest, v.err = p.Registry.Digest(ctx, img.Repository, img.Tag(to))
		verified[ref] = v
	}
	switch err := v.err; {
	case errors.Is(err, registry.ErrNotFound):
		return nil, "", fmt.Sprintf("%s is not published", ref)
	case errors.Is(err, registry.ErrAuthRequired) && img.Digest != "":
		return nil, "", fmt.Sprintf("%s needs credentials, so devex cannot refresh its digest pin", ref)
	case errors.Is(err, registry.ErrAuthRequired):
		return nil, fmt.Sprintf("%s: %s needs registry credentials; devex wrote it unverified", pin.Location(), ref), ""
	case err != nil:
		return nil, "", fmt.Sprintf("%s: %v", ref, err)
	}
	if img.Digest == "" || img.Digest == v.digest {
		return nil, "", ""
	}

	return &Change{File: pin.File, Line: img.DigestLine, Kind: pin.Kind, Span: img.DigestSpan, From: img.Digest, To: v.digest}, "", ""
}
