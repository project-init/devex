// Package edit rewrites version text in place, so comments, formatting, and options around
// each version survive.
package edit

import (
	"cmp"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"

	"github.com/project-init/devex/internal/sre/dependencies/pins"
)

// Change replaces the text at Span in File.
type Change struct {
	// File is the slash-separated path relative to the repository root.
	File string
	// Line is the 1-based line holding Span, for error messages.
	Line int
	// Kind names the pin the change moves, when it moves a Go version pin.
	Kind pins.Kind
	// Span locates From within File.
	Span pins.Span
	// From is the text Span holds when planned.
	From string
	// To replaces From.
	To string
}

// Apply writes changes, replacing only each span. It confirms every span still holds its
// planned text before writing anything, so a stale plan fails with the repository untouched.
func Apply(root string, changes []Change) error {
	byFile := map[string][]Change{}
	for _, c := range changes {
		byFile[c.File] = append(byFile[c.File], c)
	}

	type write struct {
		path string
		data []byte
	}
	var writes []write
	for _, file := range slices.Sorted(maps.Keys(byFile)) {
		abs := filepath.Join(root, filepath.FromSlash(file))
		data, err := rewriteSpans(abs, byFile[file])
		if err != nil {
			return err
		}
		writes = append(writes, write{abs, data})
	}
	for _, w := range writes {
		// WriteFile keeps an existing file's permissions.
		if err := os.WriteFile(w.path, w.data, 0o644); err != nil {
			return err
		}
	}

	return nil
}

// rewriteSpans returns file's contents with each change applied, or an error when any span no
// longer holds its planned text. It writes nothing.
func rewriteSpans(file string, changes []Change) ([]byte, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}

	// Rewrite from the end so earlier offsets stay valid.
	slices.SortFunc(changes, func(a, b Change) int { return cmp.Compare(b.Span.Start, a.Span.Start) })
	for _, c := range changes {
		if c.Span.End > len(data) || string(data[c.Span.Start:c.Span.End]) != c.From {
			return nil, fmt.Errorf("%s:%d changed since planning; expected %q there", file, c.Line, c.From)
		}
		data = slices.Replace(data, c.Span.Start, c.Span.End, []byte(c.To)...)
	}

	return data, nil
}
