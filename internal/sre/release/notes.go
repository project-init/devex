package release

import (
	"regexp"
	"strings"
)

// conventionalTitle matches a conventional-commit title such as "feat(api)!: add search".
var conventionalTitle = regexp.MustCompile(`^(\w+)(\([^)]*\))?(!)?:\s`)

// changeKind groups release lines in the preview.
type changeKind string

const (
	kindFeature changeKind = "feature"
	kindFix     changeKind = "fix"
	kindOther   changeKind = "other"
)

// classify reads a title's conventional-commit type and whether it marks a breaking change.
func classify(title string) (changeKind, bool) {
	breaking := strings.Contains(title, "BREAKING CHANGE:") || strings.Contains(title, "BREAKING-CHANGE:")
	match := conventionalTitle.FindStringSubmatch(title)
	if match == nil {
		return kindOther, breaking
	}
	breaking = breaking || match[3] == "!"
	switch strings.ToLower(match[1]) {
	case "feat":
		return kindFeature, breaking
	case "fix":
		return kindFix, breaking
	default:
		return kindOther, breaking
	}
}

// suggestBump proposes a version bump from the release's titles: major for a breaking change,
// minor for a feature, and patch otherwise. When majors are off, a breaking change suggests
// minor and breakingBlocked reports it.
func suggestBump(lines []releaseLine, allowMajor bool) (suggestion bumpType, breakingBlocked bool) {
	suggestion = BumpPatch
	for _, line := range lines {
		switch {
		case line.Breaking && allowMajor:
			return BumpMajor, false
		case line.Breaking:
			breakingBlocked = true
			suggestion = BumpMinor
		case line.Kind == kindFeature:
			suggestion = BumpMinor
		}
	}
	return suggestion, breakingBlocked
}
