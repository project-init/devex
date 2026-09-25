package pins

import (
	"bytes"
	"fmt"
	"path"
	"regexp"
	"strings"

	"github.com/moby/buildkit/frontend/dockerfile/parser"
	"github.com/project-init/devex/internal/sre/dependencies/goversion"
)

// varRef matches ${VAR}, ${VAR:-default}, and $VAR in a Dockerfile image reference.
var varRef = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(?::-([^}]*))?\}|\$([A-Za-z_][A-Za-z0-9_]*)`)

// sourceExtensions lists the extensions of source and data files that devex never reads as
// Dockerfiles.
var sourceExtensions = map[string]bool{
	".go": true, ".sh": true, ".bash": true, ".py": true, ".js": true, ".jsx": true, ".mjs": true,
	".cjs": true, ".ts": true, ".tsx": true, ".rb": true, ".rs": true, ".java": true, ".kt": true,
	".json": true, ".yaml": true, ".yml": true, ".toml": true, ".tf": true, ".hcl": true,
	".nix": true, ".bzl": true, ".dockerignore": true,
}

func isDockerfile(p string) bool {
	if isProse(p) {
		return false
	}
	lower := strings.ToLower(path.Base(p))
	// Source files named after Dockerfiles, such as dockerfile.go, are not Dockerfiles.
	if sourceExtensions[path.Ext(lower)] {
		return false
	}
	for _, name := range []string{"dockerfile", "containerfile"} {
		if lower == name || strings.HasPrefix(lower, name+".") || strings.HasSuffix(lower, "."+name) {
			return true
		}
	}

	return false
}

// dockerArg is an ARG declared before the first FROM, the only ARGs a FROM line can see.
type dockerArg struct {
	value string
	span  Span
}

type dockerfileFinder struct {
	file     string
	data     []byte
	offsets  []int
	images   []string
	args     map[string]dockerArg
	pins     []Pin
	pinAt    map[int]int
	warnings []string
	// stages holds the lowercase names of earlier build stages, which a FROM may name.
	stages map[string]bool
}

// findDockerfile finds golang image tags in FROM lines, including tags supplied by ARGs.
// When an ARG supplies the version, the pin sits on the ARG and lists every FROM it feeds.
func findDockerfile(file string, data []byte, images []string) ([]Pin, []string) {
	res, err := parser.Parse(bytes.NewReader(data))
	if err != nil {
		return nil, []string{fmt.Sprintf("%s: parse Dockerfile: %v", file, err)}
	}

	f := &dockerfileFinder{
		file:    file,
		data:    data,
		offsets: LineOffsets(data),
		images:  images,
		args:    map[string]dockerArg{},
		pinAt:   map[int]int{},
		stages:  map[string]bool{},
	}
	seenFrom := false
	for _, n := range res.AST.Children {
		switch strings.ToLower(n.Value) {
		case "arg":
			if !seenFrom {
				f.recordArgs(n)
			}
		case "from":
			seenFrom = true
			if n.Next != nil {
				f.visitFrom(n.Next.Value, n.StartLine, n.EndLine)
				if as := n.Next.Next; as != nil && strings.EqualFold(as.Value, "as") && as.Next != nil {
					f.stages[strings.ToLower(as.Next.Value)] = true
				}
			}
		}
	}

	return f.pins, f.warnings
}

func (f *dockerfileFinder) recordArgs(n *parser.Node) {
	text := f.source(n.StartLine, n.EndLine)
	base := f.offsets[n.StartLine]
	for a := n.Next; a != nil; a = a.Next {
		name, value, hasDefault := strings.Cut(a.Value, "=")
		var arg dockerArg
		if hasDefault {
			value = strings.Trim(value, `"'`)
			loc := regexp.MustCompile(`(?:^|\s)` + regexp.QuoteMeta(name) + `=`).FindStringIndex(text)
			if loc == nil {
				continue
			}
			valueStart := loc[1]
			if valueStart < len(text) && (text[valueStart] == '"' || text[valueStart] == '\'') {
				valueStart++
			}
			arg.value = value
			arg.span = Span{Start: base + valueStart, End: base + valueStart + len(value)}
		}
		f.args[name] = arg
	}
}

func (f *dockerfileFinder) visitFrom(ref string, startLine, endLine int) {
	if f.stages[strings.ToLower(ref)] {
		return
	}
	repo, tag, digest := splitImageRef(ref)
	resolved, ok := f.expand(repo)
	if tag == "" {
		// An ARG can carry the whole reference, tag included, such as ${GO_IMAGE}.
		whole, _, _ := splitImageRef(resolved)
		switch {
		case !matchImage(f.images, whole):
		case resolved != repo:
			f.warn(startLine, "image reference %q comes from a build arg; declare a pin for it", ref)
		default:
			f.warn(startLine, "image %q has no tag; left unmanaged", ref)
		}

		return
	}
	if !matchImage(f.images, resolved) {
		return
	}

	// The digest pins the image outright, so moving the tag alone would change nothing.
	if varRef.MatchString(digest) {
		f.warn(startLine, "digest %q comes from a build arg; declare a pin for it", digest)

		return
	}

	text := f.source(startLine, endLine)
	base := f.offsets[startLine]
	refIdx := strings.Index(text, ref)
	if refIdx < 0 {
		f.warn(startLine, "cannot locate image reference %q; left unmanaged", ref)

		return
	}
	tagStart := base + refIdx + len(repo) + 1
	img := Image{Repository: resolved, Unresolved: !ok}
	if digest != "" {
		digestStart := tagStart + len(tag) + 1
		img.Digest = digest
		img.DigestSpan = Span{Start: digestStart, End: digestStart + len(digest)}
		img.DigestLine = lineAt(f.offsets, digestStart)
	}

	refs := varRef.FindAllStringSubmatchIndex(tag, -1)
	switch len(refs) {
	case 0:
		f.literalTag(tag, tagStart, img)
	case 1:
		f.argTag(tag, tagStart, refs[0], img)
	default:
		f.warn(startLine, "tag %q combines several variables; declare a pin for it", tag)
	}
}

// literalTag handles FROM golang:1.26.6-alpine3.24.
func (f *dockerfileFinder) literalTag(tag string, tagStart int, img Image) {
	raw, suffix, ok := splitLeadingVersion(tag)
	v, span, parsed := parseVersionAt(raw, tagStart)
	if !ok || !parsed {
		f.warn(lineAt(f.offsets, tagStart), "image tag %q has no release version; left unmanaged", tag)

		return
	}
	img.TagSuffix = suffix
	f.addPin(span, v, img, "")
}

// argTag handles FROM golang:${GO_IMAGE_TAG} and FROM golang:${GO_VERSION:-1.26.6}-alpine.
func (f *dockerfileFinder) argTag(tag string, tagStart int, ref []int, img Image) {
	name := refName(tag, ref)
	img.TagPrefix = tag[:ref[0]]
	fromSuffix := tag[ref[1]:]
	line := lineAt(f.offsets, tagStart)

	arg, declared := f.args[name]
	if declared && arg.value != "" {
		raw, argSuffix, ok := splitLeadingVersion(arg.value)
		v, span, parsed := parseVersionAt(raw, arg.span.Start)
		if !ok || !parsed {
			f.warn(lineAt(f.offsets, arg.span.Start), "ARG %s=%q has no release version; left unmanaged", name, arg.value)

			return
		}
		img.TagSuffix = argSuffix + fromSuffix
		f.addPin(span, v, img, name)

		return
	}

	// With no ARG default, or an empty one, ${VAR:-default} falls back to the default in the
	// FROM line, as Docker does.
	if ref[4] >= 0 {
		def := tag[ref[4]:ref[5]]
		raw, defSuffix, ok := splitLeadingVersion(def)
		v, span, parsed := parseVersionAt(raw, tagStart+ref[4])
		if !ok || !parsed {
			f.warn(line, "default %q for %s has no release version; left unmanaged", def, name)

			return
		}
		img.TagSuffix = defSuffix + fromSuffix
		f.addPin(span, v, img, "")

		return
	}

	if declared {
		f.warn(line, "ARG %s has no default, so the version arrives as a --build-arg; declare a pin where the build sets it", name)
	} else {
		f.warn(line, "%s is not an ARG declared before the first FROM; left unmanaged", name)
	}
}

// addPin records a pin, or adds img to the existing pin when one ARG feeds several FROMs.
func (f *dockerfileFinder) addPin(span Span, v goversion.Version, img Image, arg string) {
	if i, ok := f.pinAt[span.Start]; ok {
		f.pins[i].Images = append(f.pins[i].Images, img)

		return
	}
	f.pinAt[span.Start] = len(f.pins)
	f.pins = append(f.pins, Pin{
		File:    f.file,
		Line:    lineAt(f.offsets, span.Start),
		Kind:    KindDockerfile,
		Version: v,
		Span:    span,
		Images:  []Image{img},
		Arg:     arg,
	})
}

func (f *dockerfileFinder) source(startLine, endLine int) string {
	end := len(f.data)
	if endLine+1 < len(f.offsets) {
		end = f.offsets[endLine+1]
	}

	return string(f.data[f.offsets[startLine]:end])
}

func (f *dockerfileFinder) warn(line int, format string, args ...any) {
	f.warnings = append(f.warnings, fmt.Sprintf("%s:%d: ", f.file, line)+fmt.Sprintf(format, args...))
}

// expand substitutes ARG defaults into s. It reports false when a reference has no value,
// leaving that reference in place.
func (f *dockerfileFinder) expand(s string) (string, bool) {
	resolved := true
	out := varRef.ReplaceAllStringFunc(s, func(ref string) string {
		m := varRef.FindStringSubmatchIndex(ref)
		if v := f.args[refName(ref, m)].value; v != "" {
			return v
		}
		if m[4] >= 0 && m[5] > m[4] {
			return ref[m[4]:m[5]]
		}
		resolved = false

		return ref
	})

	return out, resolved
}

// refName returns the variable a varRef match names, in either its ${VAR} or $VAR form.
func refName(s string, m []int) string {
	if m[2] >= 0 {
		return s[m[2]:m[3]]
	}

	return s[m[6]:m[7]]
}

// matchImage reports whether repo matches an images entry: by its last path segment, or by
// the whole repository when the entry contains a slash.
func matchImage(images []string, repo string) bool {
	name := path.Base(repo)
	for _, pattern := range images {
		target := name
		if strings.Contains(pattern, "/") {
			target = repo
		}
		if ok, _ := path.Match(pattern, target); ok {
			return true
		}
	}

	return false
}

// splitImageRef splits "registry/repo:tag@sha256:…" into its repository, tag, and digest.
// Colons inside ${VAR:-default} do not separate the tag.
func splitImageRef(ref string) (repo, tag, digest string) {
	ref, digest, _ = strings.Cut(ref, "@")
	masked := varRef.ReplaceAllStringFunc(ref, func(m string) string { return strings.Repeat("_", len(m)) })
	repo = ref
	name := strings.LastIndex(masked, "/") + 1
	if i := strings.Index(masked[name:], ":"); i >= 0 {
		repo, tag = ref[:name+i], ref[name+i+1:]
	}

	return repo, tag, digest
}
