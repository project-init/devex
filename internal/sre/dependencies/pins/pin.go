// Package pins finds every place a repository states its Go version.
package pins

import (
	"fmt"
	"iter"
	"regexp"
	"slices"
	"strings"

	"github.com/project-init/devex/internal/sre/dependencies/goversion"
)

type Kind string

const (
	KindMise          Kind = "mise"
	KindGoVersion     Kind = ".go-version"
	KindToolVersions  Kind = ".tool-versions"
	KindSetupGo       Kind = "setup-go"
	KindGoDirective   Kind = "go.mod go"
	KindGoToolchain   Kind = "go.mod toolchain"
	KindWorkGo        Kind = "go.work go"
	KindWorkToolchain Kind = "go.work toolchain"
	KindDockerfile    Kind = "dockerfile"
	KindDeclared      Kind = "declared"
)

// Span is a byte range within a file.
type Span struct {
	Start int
	End   int
}

// Pin is one place a repository states its Go version.
type Pin struct {
	// File is the slash-separated path relative to the repository root.
	File string
	// Line is the 1-based line holding the version.
	Line int
	// Kind names the finder that produced the pin.
	Kind Kind
	// Version is the pinned version at the precision written.
	Version goversion.Version
	// Span locates the version text within File.
	Span Span
	// Images lists the container images whose tags embed this version. Dockerfile pins only.
	Images []Image
	// Arg names the Dockerfile ARG that supplies the version, when one does.
	Arg string
}

// Floor reports whether pins of this kind name a language floor, as the go directives in
// go.mod and go.work do, rather than a toolchain.
func (k Kind) Floor() bool {
	return k == KindGoDirective || k == KindWorkGo
}

// IsToolchain reports whether the pin names a toolchain rather than a language floor.
func (p Pin) IsToolchain() bool {
	return !p.Kind.Floor()
}

// Location formats the pin's position as file:line.
func (p Pin) Location() string {
	return fmt.Sprintf("%s:%d", p.File, p.Line)
}

// Image is a container image reference whose tag embeds a pinned version.
type Image struct {
	// Repository is the image name with ARG defaults substituted, such as "golang" or
	// "public.ecr.aws/docker/library/golang".
	Repository string
	// Unresolved reports that a build arg with no default names the registry, so devex cannot
	// query it.
	Unresolved bool
	// TagPrefix precedes the version in the tag.
	TagPrefix string
	// TagSuffix follows the version in the tag, such as "-alpine3.24".
	TagSuffix string
	// Digest is the pinned digest, such as "sha256:…", or empty when the reference is unpinned.
	Digest string
	// DigestSpan locates Digest within the pin's file.
	DigestSpan Span
	// DigestLine is the 1-based line holding Digest, which for an ARG-fed pin differs from the
	// pin's own line.
	DigestLine int
}

// Tag returns the image tag with v substituted for the pinned version.
func (i Image) Tag(v goversion.Version) string {
	return i.TagPrefix + v.String() + i.TagSuffix
}

// Ref returns the full reference with v substituted, excluding any digest.
func (i Image) Ref(v goversion.Version) string {
	return i.Repository + ":" + i.Tag(v)
}

var leadingVersion = regexp.MustCompile(`^(\d+\.\d+(?:\.\d+)?)(?:$|-)`)

// splitLeadingVersion splits "1.26.6-alpine3.24" into "1.26.6" and "-alpine3.24". It reports
// false for tags without a leading release version, such as "latest" or "1.27rc1".
func splitLeadingVersion(s string) (version, suffix string, ok bool) {
	m := leadingVersion.FindStringSubmatchIndex(s)
	if m == nil {
		return "", "", false
	}

	return s[m[2]:m[3]], s[m[3]:], true
}

// parseVersionAt parses raw, which begins at byte offset start, and returns the span of its
// version digits alone, so a rewrite keeps any "go" prefix or surrounding whitespace.
func parseVersionAt(raw string, start int) (goversion.Version, Span, bool) {
	v, err := goversion.Parse(raw)
	if err != nil {
		return goversion.Version{}, Span{}, false
	}
	i := strings.Index(raw, v.String())

	return v, Span{Start: start + i, End: start + i + len(v.String())}, true
}

// textLine is one line of a file: its 1-based number, starting byte offset, and text,
// including any newline.
type textLine struct {
	no, start int
	text      string
}

// numberedLines yields each line of data with its number and offset.
func numberedLines(data []byte) iter.Seq[textLine] {
	return func(yield func(textLine) bool) {
		start, no := 0, 0
		for text := range strings.Lines(string(data)) {
			no++
			if !yield(textLine{no: no, start: start, text: text}) {
				return
			}
			start += len(text)
		}
	}
}

// lineAt returns the 1-based line holding offset, given lineOffsets' result.
func lineAt(offsets []int, offset int) int {
	i, _ := slices.BinarySearch(offsets, offset+1)

	return i - 1
}

// lineOffsets returns the byte offset at which each line begins, indexed from 1.
func lineOffsets(data []byte) []int {
	offsets := []int{0, 0}
	for i, b := range data {
		if b == '\n' {
			offsets = append(offsets, i+1)
		}
	}

	return offsets
}
