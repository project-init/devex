package artifact

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const flowchart = "```mermaid\nflowchart LR\n  A[Service] -->|writes| B[(Audit store)]\n```\n"

func words(count int) string {
	return strings.TrimSpace(strings.Repeat("word ", count))
}

func TestValidateSynopsis(t *testing.T) {
	tests := []struct {
		name    string
		content string
		wantErr string
	}{
		{
			name: "counts only the prose a speaker reads",
			content: "# Audit logs with a long title nobody reads aloud\n\n" + words(146) + "\n\n" + flowchart +
				"\n<!-- a comment the reader never sees -->\n\n[Read the full discovery](discovery.md)\n",
		},
		{
			name:    "rejects prose past a minute of speech",
			content: "# Audit logs\n\n" + words(151) + "\n",
			wantErr: "cut the prose from 151 words to 150 or fewer",
		},
		{
			name:    "counts a word split by markup once",
			content: "# Audit logs\n\n" + words(146) + " snake_case **re**written [link](https://u.test)s\n",
		},
		{
			name:    "keeps words apart across line and paragraph breaks",
			content: "# Audit logs\n\n" + words(50) + "\n" + words(50) + "\n\n" + words(51) + "\n",
			wantErr: "cut the prose from 151 words",
		},
		{
			name:    "rejects headings after the title",
			content: "# Audit logs\n\n" + words(10) + "\n\n# Hidden prose\n\n## More hidden prose\n",
			wantErr: "drop the headings after the title",
		},
		{
			name:    "rejects a list",
			content: "# Audit logs\n\n" + words(10) + "\n\n- first\n- second\n",
			wantErr: "write paragraphs",
		},
		{
			name:    "rejects code that is not the diagram",
			content: "# Audit logs\n\n" + words(10) + "\n\n```go\nfunc main() {}\n```\n",
			wantErr: "write paragraphs",
		},
		{
			name:    "rejects HTML a reader would see",
			content: "# Audit logs\n\n" + words(10) + "\n\n<div>hidden from the count</div>\n",
			wantErr: "write paragraphs",
		},
		{
			name:    "rejects a file link inside prose",
			content: "# Audit logs\n\nSee [the appendix](appendix.md) for detail.\n",
			wantErr: "link a file only in a paragraph of its own",
		},
		{
			name:    "rejects a file link that opens a sentence",
			content: "# Audit logs\n\n[The appendix](appendix.md) lists every event.\n",
			wantErr: "link a file only in a paragraph of its own",
		},
		{
			name:    "rejects an anchor link joined to prose",
			content: "# Audit logs\n\nProse here.\n[Read the full discovery](#top)\n",
			wantErr: "link a file only in a paragraph of its own",
		},
		{
			name:    "rejects an image from the repository",
			content: "# Audit logs\n\n![chart](chart.png)\n",
			wantErr: "embed images only from the web",
		},
		{
			name:    "rejects an image a tracker strips",
			content: "# Audit logs\n\n![chart](data:image/png;base64,AAAA) and [notes](notes:v2.md)\n",
			wantErr: "embed images only from the web",
		},
		{
			name:    "accepts an image from the web",
			content: "# Audit logs\n\n![chart](https://example.com/chart.png)\n",
		},
		{
			name:    "rejects text after a comment on its line",
			content: "# Audit logs\n\n" + words(10) + "\n\n<!-- c --> <b>Visible words</b>\n",
			wantErr: "write paragraphs",
		},
		{
			name:    "rejects a file link wrapped in emphasis",
			content: "# Audit logs\n\nSee *[the appendix](appendix.md)* now.\n",
			wantErr: "link a file only in a paragraph of its own",
		},
		{
			name:    "rejects inline HTML",
			content: "# Audit logs\n\nSee <a href=\"appendix.md\">the appendix</a> now.\n",
			wantErr: "drop the inline HTML",
		},
		{
			name:    "accepts an inline comment",
			content: "# Audit logs\n\nAdmins wait a week. <!-- source: survey -->\n",
		},
		{
			name: "accepts web links anywhere",
			content: "# Audit logs\n\nSee [the RFC](https://example.com/rfc), [the host](//example.com/x), or " +
				"[email us](mailto:team@example.com).\n\n[Read the full discovery](discovery.md)\n",
		},
		{
			name:    "requires an H1 title first",
			content: "## Audit logs\n\n" + words(10) + "\n",
			wantErr: "start with an H1 title",
		},
		{
			name:    "rejects a diagram type that shows no change",
			content: "# Audit logs\n\n" + words(10) + "\n\n```mermaid\npie title Events\n  \"login\" : 3\n```\n",
			wantErr: `not "pie"`,
		},
		{
			name: "reads the diagram type past front matter and comments",
			content: "# Audit logs\n\n" + words(10) + "\n\n```mermaid\n---\ntitle: Ingest\n---\n%%{\n  init: {}\n}%%\n\n" +
				"%% new path\nsequenceDiagram\n  Admin->>Console: search\n```\n",
		},
		{
			name:    "rejects a second diagram",
			content: "# Audit logs\n\n" + words(10) + "\n\n" + flowchart + "\n" + flowchart,
			wantErr: "draw one diagram, not 2",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateSynopsis([]byte(test.content))
			if test.wantErr == "" {
				if err != nil {
					t.Fatalf("validateSynopsis() error = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("validateSynopsis() error = %v, want it to contain %q", err, test.wantErr)
			}
		})
	}
}

const discoveryDocument = `# Audit logs

## Goals
## Useful Context
## Proposed Approach
### Benefits
### Considerations
### Alternatives
## Phasing
## Ownership and Access
## Assumptions
## Unknowns
`

func write(t *testing.T, directory string, name string, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(directory, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadChecksTheNamedSynopsis(t *testing.T) {
	tests := []struct {
		name     string
		key      string
		synopsis string
		wantErr  string
	}{
		{name: "valid synopsis", key: "synopsis.md", synopsis: "# Audit logs\n\n" + words(20) + "\n"},
		{name: "no synopsis named"},
		{name: "missing file", key: "synopsis.md", wantErr: "read synopsis"},
		{name: "invalid file", key: "synopsis.md", synopsis: words(10) + "\n", wantErr: "start with an H1 title"},
		{name: "outside the bundle", key: "../synopsis.md", wantErr: "discovery.synopsis must stay inside the bundle"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			key := ""
			if test.key != "" {
				key = "  synopsis: " + test.key + "\n"
			}
			write(t, directory, WorkBreakdownFile, "schema_version: v1\ndiscovery:\n  id: 2026-11-21-audit-logs\n"+
				"  title: Audit logs\n  document: discovery.md\n"+key+"items:\n  - id: INIT-001\n"+
				"    kind: initiative\n    title: Deliver audit logs\n    description: Record security events.\n")
			write(t, directory, "discovery.md", discoveryDocument)
			if test.synopsis != "" {
				write(t, directory, "synopsis.md", test.synopsis)
			}
			_, err := Load(directory)
			if test.wantErr == "" {
				if err != nil {
					t.Fatalf("Load() error = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("Load() error = %v, want it to contain %q", err, test.wantErr)
			}
		})
	}
}

func TestValidateSynopsisReportsExtraHeadingsOnceAndOnlyAfterATitle(t *testing.T) {
	const message = "drop the headings after the title"
	err := validateSynopsis([]byte("# Audit logs\n\nword\n\n## One\n\n## Two\n"))
	if err == nil || strings.Count(err.Error(), message) != 1 {
		t.Fatalf("two extra headings: error = %v, want %q once", err, message)
	}
	err = validateSynopsis([]byte("<!-- note -->\n# Audit logs\n\nword\n"))
	if err == nil || strings.Contains(err.Error(), message) {
		t.Fatalf("misplaced title: error = %v, want only the title problem", err)
	}
}

func TestIssueSynopsis(t *testing.T) {
	content := []byte("# Audit logs\n\nAdmins cannot see who changed a role,\nso audits take a week.\n\n" +
		"[The RFC](https://example.com/rfc) proposes an audit log.\n\n" +
		"[The pilot notes](https://example.com/pilot)\n\n<!-- a comment -->\n\n```mermaid\nflowchart LR\n  A --> B\n```\n\n" +
		"[Read the full discovery](discovery.md)\n")
	prose := "Admins cannot see who changed a role, so audits take a week.\n\n" +
		"[The RFC](https://example.com/rfc) proposes an audit log.\n\n[The pilot notes](https://example.com/pilot)"

	var diagram string
	got := IssueSynopsis(content, func(mermaid string) string {
		diagram = mermaid
		return "DIAGRAM"
	})
	if want := prose + "\n\nDIAGRAM"; got != want {
		t.Fatalf("IssueSynopsis() = %q, want %q", got, want)
	}
	if diagram != "flowchart LR\n  A --> B" {
		t.Fatalf("diagram source = %q", diagram)
	}
	if got := IssueSynopsis(content, func(string) string { return "" }); got != prose {
		t.Fatalf("IssueSynopsis() without a diagram = %q, want %q", got, prose)
	}
}
