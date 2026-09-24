// Package goversion parses Go release versions and resolves upgrade targets from go.dev.
package goversion

import (
	"cmp"
	"fmt"
	"go/version"
	"regexp"
	"strconv"
	"strings"
)

// versionPattern rejects leading zeros, so every parsed version formats back to its source text.
var versionPattern = regexp.MustCompile(`^(?:go)?(0|[1-9]\d*)\.(0|[1-9]\d*)(?:\.(0|[1-9]\d*))?$`)

// Version is a Go release version that remembers how many components it was written with,
// so "1.26" and "1.26.6" round-trip unchanged.
type Version struct {
	Major     int
	Minor     int
	Patch     int
	Precision int
}

// Parse reads "1.26", "1.26.6", or "go1.26.6". It rejects prereleases such as "1.27rc1" and
// bare majors such as "1", which name no single release line.
func Parse(s string) (Version, error) {
	m := versionPattern.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return Version{}, fmt.Errorf("%q is not a Go release version", s)
	}

	v := Version{Precision: 2}
	v.Major, _ = strconv.Atoi(m[1])
	v.Minor, _ = strconv.Atoi(m[2])
	if m[3] != "" {
		v.Patch, _ = strconv.Atoi(m[3])
		v.Precision = 3
	}

	return v, nil
}

// String formats the version at its own precision, without a "go" prefix.
func (v Version) String() string {
	if v.Precision < 3 {
		return fmt.Sprintf("%d.%d", v.Major, v.Minor)
	}

	return v.Full()
}

// Full formats the version with all three components, as GOTOOLCHAIN requires.
func (v Version) Full() string {
	return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
}

// WithPrecision returns v formatted to p components, dropping the patch when p is 2.
func (v Version) WithPrecision(p int) Version {
	v.Precision = p
	if p < 3 {
		v.Patch = 0
	}

	return v
}

// MinorFloor returns the first release of v's minor line, such as 1.27.0 for 1.27.1.
func (v Version) MinorFloor() Version {
	return Version{Major: v.Major, Minor: v.Minor, Precision: 3}
}

// SameMinor reports whether v and o belong to the same minor line.
func (v Version) SameMinor(o Version) bool {
	return v.Major == o.Major && v.Minor == o.Minor
}

// Compare orders versions numerically, treating a missing patch as zero.
func (v Version) Compare(o Version) int {
	return cmp.Or(cmp.Compare(v.Major, o.Major), cmp.Compare(v.Minor, o.Minor), cmp.Compare(v.Patch, o.Patch))
}

// Precedes reports whether v sorts before o in the go command's order for go directives,
// where a two-component version precedes every patch of its minor: 1.22 < 1.22.0 < 1.22.1.
func (v Version) Precedes(o Version) bool {
	return version.Compare("go"+v.String(), "go"+o.String()) < 0
}

// AtMost reports whether v ≤ o, comparing only the components both versions state.
// A go directive of 1.26.6 is therefore at most a toolchain pin of "1.26".
func (v Version) AtMost(o Version) bool {
	return v.compareShared(o) <= 0
}

// Agrees reports whether v and c name the same release at the precision both state:
// "1.26" agrees with 1.26.6, while "1.26.5" does not.
func (v Version) Agrees(c Version) bool {
	return v.compareShared(c) == 0
}

// compareShared compares only the components both versions state.
func (v Version) compareShared(o Version) int {
	p := min(v.Precision, o.Precision)

	return v.WithPrecision(p).Compare(o.WithPrecision(p))
}
