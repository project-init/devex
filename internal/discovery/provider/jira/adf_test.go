package jira

import (
	"encoding/json"
	"testing"
)

// Descriptions are authored as Markdown and render as Markdown on GitHub, so the Jira issue for
// the same item has to carry the same structure rather than a flattened transcript of it.
func TestAdfRendersMarkdownStructure(t *testing.T) {
	tests := []struct {
		name     string
		markdown string
		want     string
	}{
		{
			name:     "wrapped paragraph",
			markdown: "One sentence\nwrapped by the author.",
			want:     `[{"content":[{"text":"One sentence wrapped by the author.","type":"text"}],"type":"paragraph"}]`,
		},
		{
			name:     "bullet list",
			markdown: "- First\n- Second",
			want: `[{"content":[` +
				`{"content":[{"content":[{"text":"First","type":"text"}],"type":"paragraph"}],"type":"listItem"},` +
				`{"content":[{"content":[{"text":"Second","type":"text"}],"type":"paragraph"}],"type":"listItem"}` +
				`],"type":"bulletList"}]`,
		},
		{
			name:     "ordered list keeps its start",
			markdown: "3. Third\n4. Fourth",
			want: `[{"attrs":{"order":3},"content":[` +
				`{"content":[{"content":[{"text":"Third","type":"text"}],"type":"paragraph"}],"type":"listItem"},` +
				`{"content":[{"content":[{"text":"Fourth","type":"text"}],"type":"paragraph"}],"type":"listItem"}` +
				`],"type":"orderedList"}]`,
		},
		{
			// An author heading sits below the summary Jira renders above the description.
			name:     "heading drops two levels",
			markdown: "# Top",
			want:     `[{"attrs":{"level":3},"content":[{"text":"Top","type":"text"}],"type":"heading"}]`,
		},
		{
			name:     "inline code",
			markdown: "Run `mise test` now.",
			want: `[{"content":[` +
				`{"text":"Run ","type":"text"},` +
				`{"marks":[{"type":"code"}],"text":"mise test","type":"text"},` +
				`{"text":" now.","type":"text"}` +
				`],"type":"paragraph"}]`,
		},
		{
			name:     "emphasis",
			markdown: "**bold** and *italic*",
			want: `[{"content":[` +
				`{"marks":[{"type":"strong"}],"text":"bold","type":"text"},` +
				`{"text":" and ","type":"text"},` +
				`{"marks":[{"type":"em"}],"text":"italic","type":"text"}` +
				`],"type":"paragraph"}]`,
		},
		{
			name:     "link",
			markdown: "See [the doc](https://example.test/doc).",
			want: `[{"content":[` +
				`{"text":"See ","type":"text"},` +
				`{"marks":[{"attrs":{"href":"https://example.test/doc"},"type":"link"}],"text":"the doc","type":"text"},` +
				`{"text":".","type":"text"}` +
				`],"type":"paragraph"}]`,
		},
		{
			name:     "fenced code keeps its language",
			markdown: "```go\nfmt.Println()\n```",
			want: `[{"attrs":{"language":"go"},` +
				`"content":[{"text":"fmt.Println()\n","type":"text"}],"type":"codeBlock"}]`,
		},
		{
			// Marks nest, so a link inside bold carries both.
			name:     "nested marks",
			markdown: "**[bold link](https://example.test)**",
			want: `[{"content":[{"marks":[{"type":"strong"},` +
				`{"attrs":{"href":"https://example.test"},"type":"link"}],` +
				`"text":"bold link","type":"text"}],"type":"paragraph"}]`,
		},
		{
			// ADF cannot express raw HTML, and leaking the markup as text is worse than dropping it.
			name:     "raw html is dropped",
			markdown: "<div>hidden</div>",
			want:     `[]`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			encoded, err := json.Marshal((&adfRenderer{}).blocks(test.markdown))
			if err != nil {
				t.Fatal(err)
			}
			if string(encoded) != test.want {
				t.Fatalf("blocks(%q) =\n%s\nwant\n%s", test.markdown, encoded, test.want)
			}
		})
	}
}

// Jira rejects a document with no content, so an item that says nothing still needs a body.
func TestAdfDescriptionIsNeverEmpty(t *testing.T) {
	document := adfDescription("", nil, "")

	content := document["content"].([]map[string]any)
	if len(content) != 1 || content[0]["type"] != "paragraph" {
		t.Fatalf("content = %#v, want one empty paragraph", content)
	}
}

// Acceptance criteria are Markdown too, so a criterion naming a file renders it as code.
func TestAdfBulletListRendersMarkdown(t *testing.T) {
	list := (&adfRenderer{}).bulletList([]string{"`mise.toml` no longer declares prettier."})

	item := list["content"].([]map[string]any)[0]
	paragraph := item["content"].([]map[string]any)[0]
	first := paragraph["content"].([]map[string]any)[0]
	marks, ok := first["marks"].([]map[string]any)
	if !ok || marks[0]["type"] != "code" {
		t.Fatalf("first node = %#v, want a code mark", first)
	}
}

// Jira validates the whole document, so one node ADF rejects fails the entire issue. Each case
// here produced an invalid document before it was handled.
func TestAdfStaysWithinTheSchema(t *testing.T) {
	tests := []struct {
		name     string
		markdown string
		want     string
	}{
		{
			// codeBlock.content must be an array; a nil slice marshals as null.
			name:     "empty fence omits content",
			markdown: "```go\n```",
			want:     `[{"attrs":{"language":"go"},"type":"codeBlock"}]`,
		},
		{
			// listItem.content requires at least one block.
			name:     "empty bullet keeps a paragraph",
			markdown: "-",
			want:     `[{"content":[{"content":[{"content":[],"type":"paragraph"}],"type":"listItem"}],"type":"bulletList"}]`,
		},
		{
			// A blockquote may hold only paragraphs, lists, and code, so a heading is demoted.
			name:     "quoted heading becomes bold text",
			markdown: "> # Quoted",
			want: `[{"content":[{"content":[{"marks":[{"type":"strong"}],"text":"Quoted","type":"text"}],` +
				`"type":"paragraph"}],"type":"blockquote"}]`,
		},
		{
			name:     "nested quote is hoisted",
			markdown: "> > deep",
			want:     `[{"content":[{"content":[{"text":"deep","type":"text"}],"type":"paragraph"}],"type":"blockquote"}]`,
		},
		{
			// blockquote.content requires at least one node, so an empty quote is dropped.
			name:     "empty quote is dropped",
			markdown: ">",
			want:     `[]`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			encoded, err := json.Marshal((&adfRenderer{}).blocks(test.markdown))
			if err != nil {
				t.Fatal(err)
			}
			if string(encoded) != test.want {
				t.Fatalf("blocks(%q) =\n%s\nwant\n%s", test.markdown, encoded, test.want)
			}
		})
	}
}

// Markdown defers escapes and character references to rendering, so reading the source verbatim
// shows the spelling rather than the text. GitHub renders the resolved form.
func TestAdfResolvesEscapesAndReferences(t *testing.T) {
	tests := []struct {
		name     string
		markdown string
		want     string
	}{
		{name: "backslash escape", markdown: `Cost is 50\_000 units`, want: "Cost is 50_000 units"},
		{name: "escaped percent", markdown: `a \* b and 100\%`, want: "a * b and 100%"},
		{name: "named entity", markdown: "AT&amp;T and &copy; 2026", want: "AT&T and © 2026"},
		{name: "numeric reference", markdown: "&#8212; em dash", want: "— em dash"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			blocks := (&adfRenderer{}).blocks(test.markdown)
			got := blocks[0]["content"].([]map[string]any)[0]["text"]
			if got != test.want {
				t.Fatalf("text = %q, want %q", got, test.want)
			}
		})
	}
}

// Code is verbatim, so an escape inside it is content rather than syntax.
func TestAdfKeepsCodeVerbatim(t *testing.T) {
	blocks := (&adfRenderer{}).blocks("`code with \\* backslash`")

	got := blocks[0]["content"].([]map[string]any)[0]["text"]
	if got != `code with \* backslash` {
		t.Fatalf("code text = %q, want the backslash kept", got)
	}
}

// The same description is published to GitHub, which renders GFM, so the two must agree.
func TestAdfRendersGitHubFlavouredMarkdown(t *testing.T) {
	tests := []struct {
		name     string
		markdown string
		want     string
	}{
		{
			name:     "strikethrough",
			markdown: "~~gone~~",
			want:     `[{"content":[{"marks":[{"type":"strike"}],"text":"gone","type":"text"}],"type":"paragraph"}]`,
		},
		{
			name:     "bare url is linked",
			markdown: "See https://example.test/doc here.",
			want: `[{"content":[{"text":"See ","type":"text"},` +
				`{"marks":[{"attrs":{"href":"https://example.test/doc"},"type":"link"}],` +
				`"text":"https://example.test/doc","type":"text"},` +
				`{"text":" here.","type":"text"}],"type":"paragraph"}]`,
		},
		{
			name:     "table",
			markdown: "| a | b |\n| --- | --- |\n| 1 | 2 |",
			want: `[{"attrs":{"isNumberColumnEnabled":false,"layout":"default"},"content":[` +
				`{"content":[` +
				`{"attrs":{},"content":[{"content":[{"text":"a","type":"text"}],"type":"paragraph"}],"type":"tableHeader"},` +
				`{"attrs":{},"content":[{"content":[{"text":"b","type":"text"}],"type":"paragraph"}],"type":"tableHeader"}` +
				`],"type":"tableRow"},` +
				`{"content":[` +
				`{"attrs":{},"content":[{"content":[{"text":"1","type":"text"}],"type":"paragraph"}],"type":"tableCell"},` +
				`{"attrs":{},"content":[{"content":[{"text":"2","type":"text"}],"type":"paragraph"}],"type":"tableCell"}` +
				`],"type":"tableRow"}],"type":"table"}]`,
		},
		{
			name:     "task list",
			markdown: "- [ ] todo\n- [x] done",
			want: `[{"attrs":{"localId":"tasks-3"},"content":[` +
				`{"attrs":{"localId":"task-1","state":"TODO"},"content":[{"text":"todo","type":"text"}],"type":"taskItem"},` +
				`{"attrs":{"localId":"task-2","state":"DONE"},"content":[{"text":"done","type":"text"}],"type":"taskItem"}` +
				`],"type":"taskList"}]`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			encoded, err := json.Marshal((&adfRenderer{}).blocks(test.markdown))
			if err != nil {
				t.Fatal(err)
			}
			if string(encoded) != test.want {
				t.Fatalf("blocks(%q) =\n%s\nwant\n%s", test.markdown, encoded, test.want)
			}
		})
	}
}

// An email autolink needs its scheme, or the href resolves against the Jira instance.
func TestAdfGivesEmailLinksTheirScheme(t *testing.T) {
	blocks := (&adfRenderer{}).blocks("Ask <person@example.test> about it.")

	linked := blocks[0]["content"].([]map[string]any)[1]
	marks := linked["marks"].([]map[string]any)
	if href := marks[0]["attrs"].(map[string]any)["href"]; href != "mailto:person@example.test" {
		t.Fatalf("href = %q, want a mailto scheme", href)
	}
	if linked["text"] != "person@example.test" {
		t.Fatalf("text = %q, want the address", linked["text"])
	}
}
