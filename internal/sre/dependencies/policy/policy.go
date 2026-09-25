// Package policy decides how far an upgrade may move a version.
package policy

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"golang.org/x/mod/semver"
)

// Policy bounds the versions an upgrade may choose.
type Policy string

const (
	// Latest allows any newer release, across majors.
	Latest Policy = "latest"
	// Minor allows newer releases within the current major.
	Minor Policy = "minor"
	// Patch allows newer releases within the current minor.
	Patch Policy = "patch"
	// Pin allows nothing: the version never moves.
	Pin Policy = "pin"
)

// Parse returns the policy s names. An empty s is Latest.
func Parse(s string) (Policy, error) {
	switch p := Policy(s); p {
	case "":
		return Latest, nil
	case Latest, Minor, Patch, Pin:
		return p, nil
	default:
		return "", fmt.Errorf("unknown policy %q; valid policies are latest, minor, patch, and pin", s)
	}
}

// ParseAll parses a config section's policies, keyed by the entry each governs. Errors name
// the first bad key in sorted order, so every run reports the same one.
func ParseAll(section string, raw map[string]string) (map[string]Policy, error) {
	policies := make(map[string]Policy, len(raw))
	for _, key := range slices.Sorted(maps.Keys(raw)) {
		p, err := Parse(raw[key])
		if err != nil {
			return nil, fmt.Errorf("%s.%s: %w", section, key, err)
		}
		policies[key] = p
	}

	return policies, nil
}

// Unknown returns the sorted policy keys that seen lacks, which name nothing the upgrade found.
func Unknown(policies map[string]Policy, seen map[string]bool) []string {
	var unknown []string
	for key := range policies {
		if !seen[key] {
			unknown = append(unknown, key)
		}
	}
	slices.Sort(unknown)

	return unknown
}

// Fixed returns how many leading version components the policy holds: 0 for Latest, 1 for
// Minor, and 2 for Patch.
func (p Policy) Fixed() int {
	switch p {
	case Minor:
		return 1
	case Patch:
		return 2
	default:
		return 0
	}
}

// Pick returns the newest release among candidates that the policy allows and that is newer
// than current, or "" when none is. Versions compare as semver with or without a leading v;
// prereleases and anything else that is not a release version never qualify.
func (p Policy) Pick(current string, candidates []string) string {
	cur, ok := canonical(current)
	if !ok || p == Pin {
		return ""
	}

	var best, bestCanon string
	for _, c := range candidates {
		canon, ok := canonical(c)
		if !ok || semver.Compare(canon, cur) <= 0 || !p.allows(cur, canon) {
			continue
		}
		if best == "" || semver.Compare(canon, bestCanon) > 0 {
			best, bestCanon = c, canon
		}
	}

	return best
}

// Newer reports whether next is a newer release than current.
func Newer(current, next string) bool {
	cur, ok := canonical(current)
	if !ok {
		return false
	}
	n, ok := canonical(next)

	return ok && semver.Compare(n, cur) > 0
}

func (p Policy) allows(current, candidate string) bool {
	switch p {
	case Minor:
		return semver.Major(candidate) == semver.Major(current)
	case Patch:
		return semver.MajorMinor(candidate) == semver.MajorMinor(current)
	default:
		return true
	}
}

// Components returns the number of dot-separated numbers in v, such as 3 for "v1.2.3", or 0
// when v is not a release version.
func Components(v string) int {
	if _, ok := canonical(v); !ok {
		return 0
	}

	return strings.Count(strings.TrimPrefix(v, "v"), ".") + 1
}

// Prefix returns the first n components of v, keeping any leading v, such as "v1.2" for
// ("v1.2.3", 2).
func Prefix(v string, n int) string {
	parts := strings.SplitN(v, ".", n+1)

	return strings.Join(parts[:min(n, len(parts))], ".")
}

// Format writes v in the style of like: the same number of components and the same leading v,
// so "1.73.0" becomes "v1.73.0" beside "v1.72.0", and "26.10.0" becomes "26.10" beside "26.8".
func Format(like, v string) string {
	v = Prefix(strings.TrimPrefix(v, "v"), Components(like))
	if strings.HasPrefix(like, "v") {
		return "v" + v
	}

	return v
}

// canonical returns v as a semver string with a leading v, or false when v is not a plain
// release version.
func canonical(v string) (string, bool) {
	v = "v" + strings.TrimPrefix(v, "v")
	if !semver.IsValid(v) || semver.Prerelease(v) != "" || semver.Build(v) != "" {
		return "", false
	}

	return v, true
}
