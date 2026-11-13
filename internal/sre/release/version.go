package release

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/manifoldco/promptui"
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
func versionAtTip(tip string) (version, string, error) {
	out, err := gitOutput("describe", "--tags", "--abbrev=0", "--first-parent", tip)
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

func selectBumpType(allowMajor bool) (bumpType, error) {

	items := []string{
		"patch (bug fixes)",
		"minor (new features, backward compatible)",
	}

	// Only add the major option if the config explicitly allows it
	if allowMajor {
		items = append(items, "major (breaking changes)")
	}

	prompt := promptui.Select{
		Label: "Select version bump type",
		Items: items,
	}

	idx, _, err := prompt.Run()
	if err != nil {
		return 0, err
	}

	switch idx {
	case 0:
		return BumpPatch, nil
	case 1:
		return BumpMinor, nil
	case 2:
		if allowMajor {
			return BumpMajor, nil
		}
		return 0, fmt.Errorf("invalid selection")
	default:
		return 0, fmt.Errorf("invalid selection")
	}
}
