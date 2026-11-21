package artifact

import (
	"fmt"
	"net/url"
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

func loadSynopsis(directory string, synopsis string) ([]byte, error) {
	if synopsis == "" {
		return nil, nil
	}
	path, err := safeDocumentPath(directory, "discovery.synopsis", synopsis)
	if err != nil {
		return nil, err
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read synopsis %s: %w", path, err)
	}
	if err := validateSynopsis(content); err != nil {
		return nil, fmt.Errorf("synopsis %s: %w", path, err)
	}
	return content, nil
}

func parseSynopsis(content []byte) ast.Node {
	return goldmark.New(goldmark.WithExtensions(extension.GFM)).Parser().Parse(text.NewReader(content))
}

// IssueSynopsis renders a synopsis that passed validation, which admits only paragraphs, the
// title, one Mermaid diagram, and comments, for the top of an initiative's issue. It drops the
// title, which the issue shows, and any paragraph that only links a file beside the synopsis,
// such as the closing link to the discovery: no issue can open a relative link, and the issue's
// footer links the discovery. diagram renders the Mermaid source of the diagram for the tracker,
// or returns "" to leave it out.
func IssueSynopsis(content []byte, diagram func(mermaid string) string) string {
	var blocks []string
	for node := parseSynopsis(content).FirstChild(); node != nil; node = node.NextSibling() {
		switch node := node.(type) {
		case *ast.Paragraph:
			// GitHub issues render each newline as a break, so a paragraph wrapped in the repository
			// joins into one line.
			if !onlyRelativeLink(node) {
				blocks = append(blocks, lineText(node.Lines(), content, " "))
			}
		case *ast.FencedCodeBlock:
			if rendered := diagram(lineText(node.Lines(), content, "\n")); rendered != "" {
				blocks = append(blocks, rendered)
			}
		}
	}
	return strings.Join(blocks, "\n\n")
}

func onlyRelativeLink(paragraph *ast.Paragraph) bool {
	link, ok := paragraph.FirstChild().(*ast.Link)
	return ok && link.NextSibling() == nil && !portable(link.Destination)
}

// portable reports whether a link opens from an issue. An issue resolves a relative path, a root
// path, or an anchor against itself, and trackers strip schemes such as data: and file:, so only a
// web or mail address survives the move.
func portable(destination []byte) bool {
	parsed, err := url.Parse(string(destination))
	if err != nil {
		return false
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https", "mailto":
		return true
	case "":
		return parsed.Host != ""
	}
	return false
}

func lineText(lines *text.Segments, source []byte, separator string) string {
	parts := make([]string, lines.Len())
	for index := range parts {
		segment := lines.At(index)
		parts[index] = strings.TrimRight(string(segment.Value(source)), "\r\n")
	}
	return strings.Join(parts, separator)
}

func validateSynopsis(content []byte) error {
	document := parseSynopsis(content)
	var problems []string
	title, ok := document.FirstChild().(*ast.Heading)
	if !ok || title.Level != 1 {
		problems = append(problems, "start with an H1 title")
	}
	// Issues publish the synopsis block by block, so it holds only blocks they know how to carry.
	for node := document.FirstChild(); node != nil; node = node.NextSibling() {
		if !synopsisBlock(node, content) {
			problems = append(problems, "write paragraphs; drop lists, quotes, tables, rules, link definitions, HTML, and code other than the diagram")
			break
		}
	}
	// Goldmark splits one word into several text nodes around emphasis and characters such as
	// "_", so the walk joins each block's text and counts its words once. Fenced code, HTML, and
	// autolinks carry no text nodes. The callback never returns an error.
	var prose strings.Builder
	diagrams, extraHeadings, relativeLinks, repositoryImages, inlineHTML := 0, false, false, false, false
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
		case *ast.Link:
			if paragraph, ok := node.Parent().(*ast.Paragraph); !portable(node.Destination) && (!ok || !onlyRelativeLink(paragraph)) {
				relativeLinks = true
			}
		case *ast.Image:
			repositoryImages = repositoryImages || !portable(node.Destination)
		case *ast.RawHTML:
			raw := strings.TrimSpace(string(node.Segments.Value(content)))
			inlineHTML = inlineHTML || !strings.HasPrefix(raw, "<!--")
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
	if relativeLinks {
		problems = append(problems, "link a file only in a paragraph of its own, as the closing link does; an issue cannot open it")
	}
	if repositoryImages {
		problems = append(problems, "embed images only from the web; an issue cannot load one from the repository")
	}
	if inlineHTML {
		problems = append(problems, "drop the inline HTML; write the sentence in Markdown")
	}
	if diagrams > 1 {
		problems = append(problems, fmt.Sprintf("draw one diagram, not %d", diagrams))
	}
	if len(problems) > 0 {
		return fmt.Errorf("invalid synopsis:\n- %s", strings.Join(problems, "\n- "))
	}
	return nil
}

func synopsisBlock(node ast.Node, source []byte) bool {
	switch node := node.(type) {
	case *ast.Paragraph, *ast.Heading:
		return true
	case *ast.FencedCodeBlock:
		return string(node.Language(source)) == "mermaid"
	case *ast.HTMLBlock:
		// A comment, which no reader sees, unless text follows it on its closing line.
		return node.HTMLBlockType == ast.HTMLBlockType2 &&
			strings.HasSuffix(strings.TrimSpace(lineText(node.Lines(), source, "\n")), "-->")
	}
	return false
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
