package pins

import (
	"bytes"
	"fmt"
	"path"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

func isWorkflow(p string) bool {
	ext := path.Ext(p)
	if ext != ".yml" && ext != ".yaml" {
		return false
	}
	if path.Dir(p) == ".github/workflows" {
		return true
	}
	base := strings.TrimSuffix(path.Base(p), ext)

	return strings.HasPrefix(p, ".github/actions/") && base == "action"
}

// findSetupGo reads literal go-version inputs to actions/setup-go steps. An expression such as
// ${{ matrix.go }} produces a warning, and go-version-file inputs are left alone.
func findSetupGo(file string, data []byte) ([]Pin, []string) {
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, []string{fmt.Sprintf("%s: parse YAML: %v", file, err)}
	}

	var pins []Pin
	var warnings []string
	offsets := lineOffsets(data)
	walkMappings(&root, func(step *yaml.Node) {
		uses := mappingValue(step, "uses")
		if uses == nil || !strings.HasPrefix(uses.Value, "actions/setup-go@") {
			return
		}
		version := mappingValue(mappingValue(step, "with"), "go-version")
		if version == nil || version.Kind != yaml.ScalarNode {
			return
		}
		if strings.Contains(version.Value, "${{") {
			warnings = append(warnings, fmt.Sprintf("%s:%d: go-version %q comes from an expression; declare a pin where its value is set", file, version.Line, version.Value))

			return
		}

		if version.Style&(yaml.LiteralStyle|yaml.FoldedStyle) != 0 {
			warnings = append(warnings, fmt.Sprintf("%s:%d: go-version uses a block scalar; write it on one line for devex to manage it", file, version.Line))

			return
		}
		// The column points at any anchor, tag, or quote before the value, so find the value
		// itself on the rest of the line.
		start := byteColumn(data, offsets[version.Line], version.Column)
		end := len(data)
		if version.Line+1 < len(offsets) {
			end = offsets[version.Line+1]
		}
		at := strings.Index(string(data[start:end]), version.Value)
		if at < 0 {
			warnings = append(warnings, fmt.Sprintf("%s:%d: cannot locate go-version %q; left unmanaged", file, version.Line, version.Value))

			return
		}
		start += at
		v, span, ok := parseVersionAt(strings.TrimSuffix(version.Value, ".x"), start)
		if !ok {
			warnings = append(warnings, fmt.Sprintf("%s:%d: go-version %q is not a release version; left unmanaged", file, version.Line, version.Value))

			return
		}
		pins = append(pins, Pin{File: file, Line: version.Line, Kind: KindSetupGo, Version: v, Span: span})
	})

	return pins, warnings
}

// byteColumn converts yaml's 1-based character column on the line starting at lineStart into
// a byte offset, since text before the value may hold multibyte characters.
func byteColumn(data []byte, lineStart, column int) int {
	line, _, _ := bytes.Cut(data[lineStart:], []byte("\n"))
	offset := 0
	for range column - 1 {
		if offset >= len(line) {
			break
		}
		_, size := utf8.DecodeRune(line[offset:])
		offset += size
	}

	return lineStart + offset
}

func walkMappings(n *yaml.Node, visit func(*yaml.Node)) {
	if n == nil {
		return
	}
	if n.Kind == yaml.MappingNode {
		visit(n)
	}
	for _, c := range n.Content {
		walkMappings(c, visit)
	}
}

func mappingValue(n *yaml.Node, key string) *yaml.Node {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}

	return nil
}
