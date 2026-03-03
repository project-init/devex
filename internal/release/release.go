package release

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

type Version struct {
	Major int
	Minor int
	Patch int
}

func (v Version) String() string {
	return fmt.Sprintf("v%d.%d.%d", v.Major, v.Minor, v.Patch)
}

type BumpType int

const (
	BumpMajor BumpType = iota
	BumpMinor
	BumpPatch
)

func (b BumpType) String() string {
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

func FetchLatestTag() (Version, error) {
	if err := runGit("fetch", "--tags"); err != nil {
		return Version{}, fmt.Errorf("failed to fetch tags: %w", err)
	}

	out, err := exec.Command("git", "describe", "--tags", "--abbrev=0").Output()
	if err != nil {
		return Version{}, nil // no tags yet, start at v0.0.0
	}

	return ParseVersion(strings.TrimSpace(string(out)))
}

func ParseVersion(tag string) (Version, error) {
	tag = strings.TrimPrefix(tag, "v")
	parts := strings.Split(tag, ".")
	if len(parts) != 3 {
		return Version{}, fmt.Errorf("invalid version format: %s (expected vMAJOR.MINOR.PATCH)", tag)
	}

	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return Version{}, fmt.Errorf("invalid major version: %s", parts[0])
	}

	minor, err := strconv.Atoi(parts[1])
	if err != nil {
		return Version{}, fmt.Errorf("invalid minor version: %s", parts[1])
	}

	patch, err := strconv.Atoi(parts[2])
	if err != nil {
		return Version{}, fmt.Errorf("invalid patch version: %s", parts[2])
	}

	return Version{Major: major, Minor: minor, Patch: patch}, nil
}

func Bump(v Version, bump BumpType) Version {
	switch bump {
	case BumpMajor:
		return Version{Major: v.Major + 1, Minor: 0, Patch: 0}
	case BumpMinor:
		return Version{Major: v.Major, Minor: v.Minor + 1, Patch: 0}
	case BumpPatch:
		return Version{Major: v.Major, Minor: v.Minor, Patch: v.Patch + 1}
	default:
		return v
	}
}

func CreateAndPushTag(tag string) error {
	if err := runGit("tag", "-a", tag, "-m", "Release "+tag); err != nil {
		return fmt.Errorf("failed to create tag: %w", err)
	}

	if err := runGit("push", "origin", tag); err != nil {
		return fmt.Errorf("failed to push tag: %w", err)
	}

	return nil
}

func runGit(args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run()
}
