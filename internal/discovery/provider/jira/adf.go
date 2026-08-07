package jira

import (
	"reflect"
	"strconv"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	extensionast "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// markdown parses GitHub Flavored Markdown. The same description is published to GitHub, which
// renders GFM, so parsing anything narrower would make the two issues disagree.
var markdown = goldmark.New(goldmark.WithExtensions(extension.GFM))

// blockquoteContent lists the node types ADF accepts inside a blockquote. Anything else has to be
// demoted or hoisted, because Jira rejects the whole document rather than the offending node.
var blockquoteContent = map[string]bool{
	"paragraph":   true,
	"bulletList":  true,
	"orderedList": true,
	"codeBlock":   true,
}

// adfDescription renders a work item as an Atlassian Document Format document. ADF has no
// Markdown fallback: a heading is a heading node and a bullet is a list node, so the same prose
// that renders on GitHub has to be translated rather than passed through.
func adfDescription(description string, acceptanceCriteria []string, documentURL string) map[string]any {
	renderer := &adfRenderer{}
	content := renderer.blocks(description)
	if len(acceptanceCriteria) > 0 {
		content = append(content, adfHeading("Acceptance criteria"), renderer.bulletList(acceptanceCriteria))
	}
	if documentURL != "" {
		content = append(content, adfDocumentLink(documentURL))
	}
	// ADF rejects an empty document, and an item with nothing to say still needs a description.
	if len(content) == 0 {
		content = append(content, adfParagraph(nil))
	}

	return map[string]any{"type": "doc", "version": 1, "content": content}
}

// adfRenderer converts Markdown to ADF. It carries the source being read and a counter for the
// local IDs ADF task items require.
type adfRenderer struct {
	source []byte
	tasks  int
}

func (r *adfRenderer) blocks(source string) []map[string]any {
	previous := r.source
	r.source = []byte(source)
	defer func() { r.source = previous }()

	return r.children(markdown.Parser().Parse(text.NewReader(r.source)))
}

func (r *adfRenderer) children(parent ast.Node) []map[string]any {
	blocks := make([]map[string]any, 0, parent.ChildCount())
	for child := parent.FirstChild(); child != nil; child = child.NextSibling() {
		if block := r.block(child); block != nil {
			blocks = append(blocks, block)
		}
	}

	return blocks
}

// block maps one Markdown block to its ADF equivalent. Anything ADF cannot express, such as raw
// HTML, yields nil and is dropped rather than leaked as literal markup.
func (r *adfRenderer) block(node ast.Node) map[string]any {
	switch typed := node.(type) {
	case *ast.Paragraph, *ast.TextBlock:
		return adfParagraph(r.inline(node, nil))

	case *ast.Heading:
		// Jira renders the issue summary above the description, so author headings sit below it.
		// Levels deeper than ADF allows collapse onto the deepest it has.
		return adfHeadingNode(min(typed.Level+2, 6), r.inline(node, nil))

	case *ast.List:
		return r.list(typed)

	case *ast.ListItem:
		// ADF requires a list item to hold at least one block, which an empty bullet lacks.
		return map[string]any{"type": "listItem", "content": r.blocksOrEmptyParagraph(node)}

	case *ast.Blockquote:
		return r.blockquote(node)

	case *ast.ThematicBreak:
		return map[string]any{"type": "rule"}

	case *ast.FencedCodeBlock:
		return codeBlock(r.codeLines(node), string(typed.Language(r.source)))

	case *ast.CodeBlock:
		return codeBlock(r.codeLines(node), "")

	case *extensionast.Table:
		return r.table(node)

	default:
		return nil
	}
}

func (r *adfRenderer) list(node *ast.List) map[string]any {
	// A GFM task list is a list whose items open with a checkbox, and ADF models it separately.
	if itemCheckbox(node.FirstChild()) != nil {
		return r.taskList(node)
	}
	if node.IsOrdered() {
		return map[string]any{
			"type":    "orderedList",
			"attrs":   map[string]any{"order": node.Start},
			"content": r.children(node),
		}
	}

	return map[string]any{"type": "bulletList", "content": r.children(node)}
}

func (r *adfRenderer) taskList(node *ast.List) map[string]any {
	items := make([]map[string]any, 0, node.ChildCount())
	for child := node.FirstChild(); child != nil; child = child.NextSibling() {
		state := "TODO"
		if checkbox := itemCheckbox(child); checkbox != nil && checkbox.IsChecked {
			state = "DONE"
		}
		r.tasks++
		items = append(items, map[string]any{
			"type": "taskItem",
			"attrs": map[string]any{
				"localId": "task-" + strconv.Itoa(r.tasks),
				"state":   state,
			},
			"content": r.inline(firstBlock(child), nil),
		})
	}
	r.tasks++

	return map[string]any{
		"type":    "taskList",
		"attrs":   map[string]any{"localId": "tasks-" + strconv.Itoa(r.tasks)},
		"content": items,
	}
}

// blockquote keeps only the children ADF allows inside one. A nested quote is hoisted, a heading
// is demoted to bold text, and anything else is dropped; Jira rejects the document otherwise.
func (r *adfRenderer) blockquote(node ast.Node) map[string]any {
	allowed := make([]map[string]any, 0, node.ChildCount())
	for _, block := range r.children(node) {
		switch {
		case blockquoteContent[block["type"].(string)]:
			allowed = append(allowed, block)

		case block["type"] == "blockquote":
			nested, _ := block["content"].([]map[string]any)
			allowed = append(allowed, nested...)

		case block["type"] == "heading":
			content, _ := block["content"].([]map[string]any)
			allowed = append(allowed, adfParagraph(markAll(content, "strong")))
		}
	}
	// An empty blockquote is invalid, and a quote with nothing left to say is not worth keeping.
	if len(allowed) == 0 {
		return nil
	}

	return map[string]any{"type": "blockquote", "content": allowed}
}

func (r *adfRenderer) table(node ast.Node) map[string]any {
	rows := make([]map[string]any, 0, node.ChildCount())
	for row := node.FirstChild(); row != nil; row = row.NextSibling() {
		cellType := "tableCell"
		if _, isHeader := row.(*extensionast.TableHeader); isHeader {
			cellType = "tableHeader"
		}
		cells := make([]map[string]any, 0, row.ChildCount())
		for cell := row.FirstChild(); cell != nil; cell = cell.NextSibling() {
			cells = append(cells, map[string]any{
				"type":    cellType,
				"attrs":   map[string]any{},
				"content": []map[string]any{adfParagraph(r.inline(cell, nil))},
			})
		}
		rows = append(rows, map[string]any{"type": "tableRow", "content": cells})
	}
	if len(rows) == 0 {
		return nil
	}

	return map[string]any{
		"type":    "table",
		"attrs":   map[string]any{"isNumberColumnEnabled": false, "layout": "default"},
		"content": rows,
	}
}

// inline flattens a block's inline children into ADF text nodes, carrying the marks collected
// from the enclosing emphasis, code, and link nodes.
func (r *adfRenderer) inline(parent ast.Node, marks []map[string]any) []map[string]any {
	if parent == nil {
		return nil
	}
	nodes := make([]map[string]any, 0, parent.ChildCount())
	for child := parent.FirstChild(); child != nil; child = child.NextSibling() {
		switch typed := child.(type) {
		case *ast.Text:
			nodes = appendInline(nodes, adfText(unescape(typed.Segment.Value(r.source)), marks))
			if typed.HardLineBreak() {
				nodes = append(nodes, map[string]any{"type": "hardBreak"})
			} else if typed.SoftLineBreak() {
				// A wrapped line is not a break; ADF has no concept of source wrapping.
				nodes = appendInline(nodes, adfText(" ", marks))
			}

		case *ast.String:
			nodes = appendInline(nodes, adfText(unescape(typed.Value), marks))

		case *ast.CodeSpan:
			// Code is written verbatim, so its escapes are content rather than syntax.
			nodes = appendInline(nodes, adfText(string(r.rawText(typed)), withMark(marks, "code")))

		case *ast.Emphasis:
			name := "em"
			if typed.Level >= 2 {
				name = "strong"
			}
			nodes = appendInline(nodes, r.inline(child, withMark(marks, name)))

		case *extensionast.Strikethrough:
			nodes = appendInline(nodes, r.inline(child, withMark(marks, "strike")))

		case *ast.Link:
			nodes = appendInline(nodes, r.inline(child, withLinkMark(marks, destination(typed.Destination))))

		case *ast.AutoLink:
			href := destination(typed.URL(r.source))
			label := string(typed.Label(r.source))
			if typed.AutoLinkType == ast.AutoLinkEmail {
				href = "mailto:" + href
			}
			nodes = appendInline(nodes, adfText(label, withLinkMark(marks, href)))

		case *ast.Image:
			// ADF images reference uploaded attachments, which a description cannot create, so
			// the image degrades to a link to its source.
			nodes = appendInline(nodes, r.inline(child, withLinkMark(marks, destination(typed.Destination))))

		case *extensionast.TaskCheckBox:
			// The checkbox is state on the task item, not text inside it.

		default:
			nodes = appendInline(nodes, r.inline(child, marks))
		}
	}

	return nodes
}

func (r *adfRenderer) blocksOrEmptyParagraph(node ast.Node) []map[string]any {
	if blocks := r.children(node); len(blocks) > 0 {
		return blocks
	}

	return []map[string]any{adfParagraph(nil)}
}

// bulletList renders each entry as Markdown, so a criterion may carry inline code or a link.
func (r *adfRenderer) bulletList(items []string) map[string]any {
	listItems := make([]map[string]any, 0, len(items))
	for _, item := range items {
		content := r.blocks(item)
		if len(content) == 0 {
			content = []map[string]any{adfParagraph(nil)}
		}
		listItems = append(listItems, map[string]any{"type": "listItem", "content": content})
	}

	return map[string]any{"type": "bulletList", "content": listItems}
}

// codeLines joins the raw lines of a code block, which goldmark exposes as source segments
// rather than as child text nodes.
func (r *adfRenderer) codeLines(node ast.Node) string {
	var joined []byte
	lines := node.Lines()
	for index := range lines.Len() {
		line := lines.At(index)
		joined = append(joined, line.Value(r.source)...)
	}

	return string(joined)
}

func (r *adfRenderer) rawText(node ast.Node) []byte {
	var joined []byte
	for child := node.FirstChild(); child != nil; child = child.NextSibling() {
		if segment, ok := child.(*ast.Text); ok {
			joined = append(joined, segment.Segment.Value(r.source)...)
		}
	}

	return joined
}

// codeBlock omits its content when the fence is empty, because ADF rejects a null content array.
func codeBlock(code string, language string) map[string]any {
	block := map[string]any{"type": "codeBlock"}
	if content := adfText(code, nil); content != nil {
		block["content"] = content
	}
	if language != "" {
		block["attrs"] = map[string]any{"language": language}
	}

	return block
}

// appendInline merges runs of text that carry the same marks. Goldmark splits a paragraph at
// every wrapped line, so without this one sentence arrives as a stack of nodes. Jira renders
// either form identically; this only keeps the document readable.
func appendInline(nodes []map[string]any, added []map[string]any) []map[string]any {
	for _, node := range added {
		last := len(nodes) - 1
		if len(nodes) > 0 && nodes[last]["type"] == "text" && node["type"] == "text" &&
			reflect.DeepEqual(nodes[last]["marks"], node["marks"]) {
			nodes[last]["text"] = nodes[last]["text"].(string) + node["text"].(string)

			continue
		}
		nodes = append(nodes, node)
	}

	return nodes
}

// adfText builds a text node, or nothing at all: ADF rejects a text node with an empty value.
func adfText(value string, marks []map[string]any) []map[string]any {
	if value == "" {
		return nil
	}
	node := map[string]any{"type": "text", "text": value}
	if len(marks) > 0 {
		node["marks"] = marks
	}

	return []map[string]any{node}
}

// withMark appends to a copy, because sibling nodes share the slice their parent passed down.
func withMark(marks []map[string]any, name string) []map[string]any {
	return append(append([]map[string]any{}, marks...), map[string]any{"type": name})
}

func withLinkMark(marks []map[string]any, href string) []map[string]any {
	return append(append([]map[string]any{}, marks...),
		map[string]any{"type": "link", "attrs": map[string]any{"href": href}})
}

func markAll(nodes []map[string]any, name string) []map[string]any {
	for _, node := range nodes {
		existing, _ := node["marks"].([]map[string]any)
		node["marks"] = withMark(existing, name)
	}

	return nodes
}

func adfParagraph(content []map[string]any) map[string]any {
	if content == nil {
		content = []map[string]any{}
	}

	return map[string]any{"type": "paragraph", "content": content}
}

// adfHeading builds the one heading depth the tool generates. Level 3 sits below the issue
// summary Jira renders above it.
func adfHeading(text string) map[string]any {
	return adfHeadingNode(3, adfText(text, nil))
}

func adfHeadingNode(level int, content []map[string]any) map[string]any {
	if content == nil {
		content = []map[string]any{}
	}

	return map[string]any{
		"type":    "heading",
		"attrs":   map[string]any{"level": level},
		"content": content,
	}
}

// adfDocumentLink footers the issue with the discovery document. The URL is a link mark rather
// than bare text, because a path a reader cannot click is worth less than no footer at all.
func adfDocumentLink(documentURL string) map[string]any {
	content := adfText("Discovery: ", nil)
	content = append(content, adfText(documentURL, withLinkMark(nil, documentURL))...)

	return adfParagraph(content)
}

// unescape resolves the backslash escapes and character references Markdown defers to rendering,
// so Jira shows what GitHub shows rather than the source spelling.
func unescape(value []byte) string {
	resolved := util.ResolveEntityNames(util.ResolveNumericReferences(util.UnescapePunctuations(value)))

	return string(resolved)
}

func destination(value []byte) string {
	return string(util.URLEscape(util.UnescapePunctuations(value), true))
}

// itemCheckbox reports the checkbox opening a list item, which GFM parses as the first inline of
// the item's first block. A list is a task list when its first item carries one.
func itemCheckbox(item ast.Node) *extensionast.TaskCheckBox {
	block := firstBlock(item)
	if block == nil {
		return nil
	}
	checkbox, _ := block.FirstChild().(*extensionast.TaskCheckBox)

	return checkbox
}

func firstBlock(node ast.Node) ast.Node {
	if node == nil {
		return nil
	}

	return node.FirstChild()
}
