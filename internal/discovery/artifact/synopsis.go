package artifact

import (
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/text"
)

// synopsisWordLimit holds a synopsis to about 60 seconds of speech.
const synopsisWordLimit = 150

// synopsisDiagramTypes are the Mermaid diagrams that show a change: a flow, a sequence, or a
// lifecycle. Other types describe code or data rather than a change.
var synopsisDiagramTypes = []string{"flowchart", "graph", "sequenceDiagram", "stateDiagram", "stateDiagram-v2"}

func checkSynopsis(directory string, synopsis string) error {
	if synopsis == "" {
		return nil
	}
	path, err := safeDocumentPath(directory, "discovery.synopsis", synopsis)
	if err != nil {
		return err
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read synopsis %s: %w", path, err)
	}
	if err := validateSynopsis(content); err != nil {
		return fmt.Errorf("synopsis %s: %w", path, err)
	}
	return nil
}

func validateSynopsis(content []byte) error {
	document := goldmark.New(goldmark.WithExtensions(extension.GFM)).Parser().Parse(text.NewReader(content))
	var problems []string
	title, ok := document.FirstChild().(*ast.Heading)
	if !ok || title.Level != 1 {
		problems = append(problems, "start with an H1 title")
	}
	// Goldmark splits one word into several text nodes around emphasis and characters such as
	// "_", so the walk joins each block's text and counts its words once. Fenced code, HTML, and
	// autolinks carry no text nodes. The callback never returns an error.
	var prose strings.Builder
	diagrams, extraHeadings := 0, false
	_ = ast.Walk(document, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		if node.Type() == ast.TypeBlock {
			prose.WriteByte(' ')
		}
		switch node := node.(type) {
		case *ast.Heading:
			extraHeadings = extraHeadings || node != title
			return ast.WalkSkipChildren, nil
		case *ast.FencedCodeBlock:
			if string(node.Language(content)) == "mermaid" {
				diagrams++
				if kind := diagramType(node.Lines(), content); !slices.Contains(synopsisDiagramTypes, kind) {
					problems = append(problems, fmt.Sprintf(
						"draw the diagram as one of %s, not %q",
						strings.Join(synopsisDiagramTypes, ", "),
						kind,
					))
				}
			}
		case *ast.Text:
			prose.Write(node.Segment.Value(content))
			if node.SoftLineBreak() || node.HardLineBreak() {
				prose.WriteByte(' ')
			}
		}
		return ast.WalkContinue, nil
	})
	if words := len(strings.Fields(prose.String())); words > synopsisWordLimit {
		problems = append(problems, fmt.Sprintf(
			"cut the prose from %d words to %d or fewer, about 60 seconds spoken",
			words,
			synopsisWordLimit,
		))
	}
	// Without a valid title, the H1 meant as the title would read as extra; the title problem
	// covers that case.
	if extraHeadings && ok && title.Level == 1 {
		problems = append(problems, "drop the headings after the title; a speaker never announces one")
	}
	if diagrams > 1 {
		problems = append(problems, fmt.Sprintf("draw one diagram, not %d", diagrams))
	}
	if len(problems) > 0 {
		return fmt.Errorf("invalid synopsis:\n- %s", strings.Join(problems, "\n- "))
	}
	return nil
}

func diagramType(lines *text.Segments, source []byte) string {
	inFrontMatter, inDirective := false, false
	for index := range lines.Len() {
		segment := lines.At(index)
		line := strings.TrimSpace(string(segment.Value(source)))
		switch {
		case inDirective:
			inDirective = !strings.Contains(line, "}%%")
		case strings.HasPrefix(line, "%%{"):
			inDirective = !strings.Contains(line, "}%%")
		case line == "---":
			inFrontMatter = !inFrontMatter
		case inFrontMatter, line == "", strings.HasPrefix(line, "%%"):
		default:
			return strings.TrimSuffix(strings.Fields(line)[0], ";")
		}
	}
	return ""
}
