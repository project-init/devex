// Package strictyaml decodes YAML configuration that must hold exactly one document with no
// unknown keys, so a misspelled key or a stray document fails instead of being ignored.
package strictyaml

import (
	"bytes"
	"errors"
	"io"
	"slices"

	"gopkg.in/yaml.v3"
)

// ErrEmpty reports data with no YAML document holding content, such as an empty file, one of
// only comments, or a bare separator. A loader that accepts an empty file ignores it.
var ErrEmpty = errors.New("the file holds no YAML document")

// Decode decodes the one document in data with content into out, failing on a key out has no
// field for or on a second document with content. It skips documents that hold nothing, such as a
// bare separator or a comment-only header, and returns ErrEmpty when no other document remains.
func Decode(data []byte, out any) error {
	index, _, err := contentDocument(data)
	if err != nil {
		return err
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	for range index {
		var skipped yaml.Node
		if err := decoder.Decode(&skipped); err != nil {
			return err
		}
	}
	return decoder.Decode(out)
}

// HasTopLevelKey reports whether the document Decode would pick has one of keys at its top level.
// Data that is empty, malformed, or holds more than one document with content has none.
func HasTopLevelKey(data []byte, keys ...string) bool {
	_, document, err := contentDocument(data)
	if err != nil || document.Content[0].Kind != yaml.MappingNode {
		return false
	}
	mapping := document.Content[0].Content
	for index := 0; index+1 < len(mapping); index += 2 {
		if slices.Contains(keys, mapping[index].Value) {
			return true
		}
	}
	return false
}

// contentDocument returns the position and node of the only document in data with content.
func contentDocument(data []byte) (int, *yaml.Node, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	index := -1
	var found *yaml.Node
	for position := 0; ; position++ {
		var document yaml.Node
		switch err := decoder.Decode(&document); {
		case errors.Is(err, io.EOF) && index < 0:
			return 0, nil, ErrEmpty
		case errors.Is(err, io.EOF):
			return index, found, nil
		case err != nil:
			return 0, nil, err
		case empty(&document):
		case index >= 0:
			return 0, nil, errors.New("more than one YAML document; keep one per file")
		default:
			index, found = position, &document
		}
	}
}

// empty reports whether document holds nothing but a null, as a bare separator or a document of
// comments decodes.
func empty(document *yaml.Node) bool {
	return len(document.Content) == 1 && document.Content[0].Kind == yaml.ScalarNode && document.Content[0].Tag == "!!null"
}
