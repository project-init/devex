package release

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/project-init/devex/internal/ui"
)

type version struct {
	Major int
	Minor int
	Patch int
}

func (v version) String() string {
	return fmt.Sprintf("v%d.%d.%d", v.Major, v.Minor, v.Patch)
}

type bumpType int

const (
	BumpMajor bumpType = iota
	BumpMinor
	BumpPatch
)

func (b bumpType) String() string {
	switch b {
	case BumpMajor:
		return "major"
	case BumpMinor:
		return "minor"
	case BumpPatch:
		return "patch"
	default:
		return "unknown"
	}
}

// versionAtTip returns the semver of the latest tag on tip's first-parent history.
// baseline is that tag name. With no such tag, the version is v0.0.0 and baseline is empty.
func versionAtTip(ctx context.Context, tip string) (version, string, error) {
	out, err := gitOutput(ctx, "describe", "--tags", "--abbrev=0", "--first-parent", tip)
	if err != nil {
		if describeFoundNoTag(err) {
			return version{}, "", nil
		}
		return version{}, "", fmt.Errorf("failed to describe %s: %w", tip, err)
	}
	tag := strings.TrimSpace(out)
	v, err := parseVersion(tag)
	if err != nil {
		return version{}, "", err
	}
	return v, tag, nil
}

// describeFoundNoTag reports the two messages git uses when no tag is reachable.
// Any other describe failure, such as a bad revision, must surface git's stderr.
func describeFoundNoTag(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "No names found, cannot describe anything") ||
		strings.Contains(msg, "No tags can describe")
}

func parseVersion(tag string) (version, error) {
	tag = strings.TrimPrefix(tag, "v")
	parts := strings.Split(tag, ".")
	if len(parts) != 3 {
		return version{}, fmt.Errorf("invalid version format: %s (expected vMAJOR.MINOR.PATCH)", tag)
	}

	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return version{}, fmt.Errorf("invalid major version: %s", parts[0])
	}

	minor, err := strconv.Atoi(parts[1])
	if err != nil {
		return version{}, fmt.Errorf("invalid minor version: %s", parts[1])
	}

	patch, err := strconv.Atoi(parts[2])
	if err != nil {
		return version{}, fmt.Errorf("invalid patch version: %s", parts[2])
	}

	return version{Major: major, Minor: minor, Patch: patch}, nil
}

func bump(v version, bump bumpType) version {
	switch bump {
	case BumpMajor:
		return version{Major: v.Major + 1, Minor: 0, Patch: 0}
	case BumpMinor:
		return version{Major: v.Major, Minor: v.Minor + 1, Patch: 0}
	case BumpPatch:
		return version{Major: v.Major, Minor: v.Minor, Patch: v.Patch + 1}
	default:
		return v
	}
}

// parseBumpType parses a --bump value. major needs allowMajorVersionBump in the config.
func parseBumpType(value string, allowMajor bool) (bumpType, error) {
	switch value {
	case "patch":
		return BumpPatch, nil
	case "minor":
		return BumpMinor, nil
	case "major":
		if !allowMajor {
			return 0, fmt.Errorf("--bump major needs release.allowMajorVersionBump: true in the sre config")
		}
		return BumpMajor, nil
	default:
		return 0, fmt.Errorf("--bump must be patch, minor, or major, got %q", value)
	}
}

// selectBumpType prompts for a version bump, preselecting and marking suggested. The major
// option appears only when the config allows it. The command validates --bump itself, before
// any network work.
func selectBumpType(ctx context.Context, u *ui.UI, allowMajor bool, suggested bumpType) (bumpType, error) {
	options := []ui.Option{
		{Label: "patch (bug fixes, performance optimizations, code refactors)", Value: "patch"},
		{Label: "minor (new features, backward compatible)", Value: "minor"},
	}
	if allowMajor {
		options = append(options, ui.Option{Label: "major (breaking changes)", Value: "major"})
	}
	for index := range options {
		options[index].Suggested = options[index].Value == suggested.String()
	}
	choice, err := u.Select(ctx, "Select version bump type", options, suggested.String(), bumpFlag)
	if err != nil {
		return 0, err
	}
	return parseBumpType(choice, allowMajor)
}
