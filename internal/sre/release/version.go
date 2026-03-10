package release

import (
	"fmt"
	"os/exec"
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

func fetchLatestTag() (version, error) {
	if err := runGit("fetch", "--tags"); err != nil {
		return version{}, fmt.Errorf("failed to fetch tags: %w", err)
	}

	out, err := exec.Command("git", "describe", "--tags", "--abbrev=0").Output()
	if err != nil {
		return version{}, nil // no tags yet, start at v0.0.0
	}

	return parseVersion(strings.TrimSpace(string(out)))
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

func selectBumpType() (bumpType, error) {
	prompt := promptui.Select{
		Label: "Select version bump type",
		Items: []string{
			"patch  (bug fixes)",
			"minor  (new features, backward compatible)",
			"major  (breaking changes)",
		},
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
		return BumpMajor, nil
	default:
		return 0, fmt.Errorf("invalid selection")
	}
}
