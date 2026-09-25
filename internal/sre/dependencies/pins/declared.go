package pins

import (
	"fmt"
	"regexp"
)

// Declared is a version location the project describes in config: the files it may appear
// in, and a pattern whose "version" group captures it.
type Declared struct {
	files   []Glob
	pattern *regexp.Regexp
	version int
}

// CompileDeclared validates and compiles a declared pin.
func CompileDeclared(files []string, pattern string) (Declared, error) {
	if len(files) == 0 {
		return Declared{}, fmt.Errorf("pin %q lists no files", pattern)
	}
	globs, err := compileGlobs(files)
	if err != nil {
		return Declared{}, err
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return Declared{}, fmt.Errorf("pin pattern %q: %w", pattern, err)
	}
	group := re.SubexpIndex("version")
	if group < 0 {
		return Declared{}, fmt.Errorf("pin pattern %q has no (?P<version>…) group", pattern)
	}

	return Declared{files: globs, pattern: re, version: group}, nil
}

func findDeclared(file string, data []byte, d Declared) ([]Pin, []string) {
	var pins []Pin
	var warnings []string
	offsets := LineOffsets(data)
	for _, m := range d.pattern.FindAllSubmatchIndex(data, -1) {
		start, end := m[2*d.version], m[2*d.version+1]
		if start < 0 {
			continue
		}
		raw := string(data[start:end])
		line := lineAt(offsets, start)
		v, span, ok := parseVersionAt(raw, start)
		if !ok {
			warnings = append(warnings, fmt.Sprintf("%s:%d: declared pin captured %q, which is not a release version", file, line, raw))

			continue
		}
		pins = append(pins, Pin{File: file, Line: line, Kind: KindDeclared, Version: v, Span: span})
	}

	return pins, warnings
}
