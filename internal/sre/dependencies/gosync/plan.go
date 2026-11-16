package gosync

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/project-init/devex/internal/sre/dependencies/edit"
	"github.com/project-init/devex/internal/sre/dependencies/goversion"
	"github.com/project-init/devex/internal/sre/dependencies/lookup"
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

	if plan.Target, err = p.target(ctx, current); err != nil {
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
	verified := p.resolveDigests(ctx, found.Pins, planned.Pins)
	for i, pin := range found.Pins {
		to := planned.Pins[i].Version
		changed := to != pin.Version
		if changed {
			plan.Changes = append(plan.Changes, Change{File: pin.File, Line: pin.Line, Kind: pin.Kind, Span: pin.Span, From: pin.Version.String(), To: to.String()})
		}
		for _, img := range pin.Images {
			if !verifies(img, changed) {
				continue
			}
			change, warning, problem := p.verifyImage(verified, pin, img, to)
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

// supportedResolver is a Resolver that can also list only the supported minor lines.
type supportedResolver interface {
	Supported(ctx context.Context) ([]goversion.Version, error)
}

// target picks the version the plan moves Go to. When the resolver offers the supported lines
// alone, a feed a hundredth the size of the full one, it picks from those first, and reads the
// full feed only when they hold no answer, as for an older line, or cannot be read.
func (p Planner) target(ctx context.Context, current goversion.Version) (goversion.Version, error) {
	if s, ok := p.Resolver.(supportedResolver); ok {
		// A failed read of the small feed falls through to the full one, which answers everything.
		if supported, err := s.Supported(ctx); err == nil {
			if target, err := p.pick(supported, current); err == nil {
				return target, nil
			}
		}
	}
	releases, err := p.Resolver.Releases(ctx)
	if err != nil {
		return goversion.Version{}, err
	}
	return p.pick(releases, current)
}

// pick chooses the target from releases: the override, the newest release, or the newest patch
// on current's line.
func (p Planner) pick(releases []goversion.Version, current goversion.Version) (goversion.Version, error) {
	switch {
	case p.Override != nil:
		if !goversion.Released(releases, *p.Override) {
			return goversion.Version{}, fmt.Errorf("--go-version %s is not a published Go release", p.Override)
		}
		return *p.Override, nil
	case p.Settings.Target == TargetLatest:
		return goversion.Latest(releases, current)
	default:
		return goversion.LatestPatch(releases, current)
	}
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

// verifies reports whether the plan checks img: a rewritten tag, or a digest pin, since a tag
// whose text stays put, such as golang:1.26, can still move to a new digest.
func verifies(img pins.Image, changed bool) bool {
	return changed || img.Digest != ""
}

type imageRef struct {
	repository string
	tag        string
}

// resolveDigests looks up, concurrently, every image tag the plan verifies: each rewritten tag,
// and each digest-pinned tag, whose digest can move even when its text stays put. Many
// Dockerfiles share one image, so each reference resolves once.
func (p Planner) resolveDigests(ctx context.Context, from, to []pins.Pin) map[string]lookup.Result[string] {
	refs := map[string]imageRef{}
	for i, pin := range from {
		changed := to[i].Version != pin.Version
		for _, img := range pin.Images {
			if img.Unresolved || !verifies(img, changed) {
				continue
			}
			refs[img.Ref(to[i].Version)] = imageRef{repository: img.Repository, tag: img.Tag(to[i].Version)}
		}
	}
	return lookup.All(ctx, slices.Sorted(maps.Keys(refs)), func(ctx context.Context, ref string) (string, error) {
		return p.Registry.Digest(ctx, refs[ref].repository, refs[ref].tag)
	})
}

// verifyImage confirms img's rewritten tag exists and, for a digest pin, returns the change
// that moves the digest with it, from the lookups resolveDigests made.
func (p Planner) verifyImage(verified map[string]lookup.Result[string], pin pins.Pin, img pins.Image, to goversion.Version) (change *Change, warning, problem string) {
	ref := img.Ref(to)
	if img.Unresolved {
		if img.Digest != "" {
			return nil, "", fmt.Sprintf("%s takes its registry from a build arg, so devex cannot refresh its digest pin", ref)
		}

		return nil, fmt.Sprintf("%s: %s takes its registry from a build arg; devex wrote it unverified", pin.Location(), ref), ""
	}
	v := verified[ref]
	switch err := v.Err; {
	case errors.Is(err, registry.ErrNotFound):
		return nil, "", fmt.Sprintf("%s is not published", ref)
	case errors.Is(err, registry.ErrAuthRequired) && img.Digest != "":
		return nil, "", fmt.Sprintf("%s needs credentials, so devex cannot refresh its digest pin", ref)
	case errors.Is(err, registry.ErrAuthRequired):
		return nil, fmt.Sprintf("%s: %s needs registry credentials; devex wrote it unverified", pin.Location(), ref), ""
	case err != nil:
		return nil, "", fmt.Sprintf("%s: %v", ref, err)
	}
	if img.Digest == "" || img.Digest == v.Value {
		return nil, "", ""
	}

	return &Change{File: pin.File, Line: img.DigestLine, Kind: pin.Kind, Span: img.DigestSpan, From: img.Digest, To: v.Value}, "", ""
}
