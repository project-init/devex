package catalog

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// gotextCatalog is a catalog as gotext writes it: json.MarshalIndent with a four-space indent,
// so HTML characters arrive escaped.
const gotextCatalog = `{
    "language": "es-US",
    "messages": [
        {
            "id": "Hello \u003cb\u003e{Name}\u003c/b\u003e",
            "message": "Hello \u003cb\u003e{Name}\u003c/b\u003e",
            "translation": "",
            "placeholders": [
                {
                    "id": "Name",
                    "string": "%[1]s"
                }
            ]
        },
        {
            "id": [
                "Bye",
                "Bye"
            ],
            "message": "Bye",
            "translation": "Adiós",
            "fuzzy": true
        }
    ]
}`

func writeCatalog(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), MessagesFile)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDocumentRoundTripsGotextOutput(t *testing.T) {
	// testdata/de.messages.gotext.golden is golang.org/x/text's own pipeline test catalog, copied
	// verbatim, including its duplicate id with two keys. Its one hand-written inline array is
	// the only thing Save changes: it reindents to gotext's json.MarshalIndent layout.
	original, err := os.ReadFile(filepath.Join("testdata", "de.messages.gotext.golden"))
	if err != nil {
		t.Fatal(err)
	}
	var compact, want bytes.Buffer
	if err := json.Compact(&compact, original); err != nil {
		t.Fatal(err)
	}
	if err := json.Indent(&want, compact.Bytes(), "", "    "); err != nil {
		t.Fatal(err)
	}
	want.WriteByte('\n')
	path := writeCatalog(t, string(original))
	doc, err := LoadDocument(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := doc.Save(path); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != want.String() {
		t.Errorf("round trip changed more than whitespace:\n%s", got)
	}
}

func TestDocumentRoundTripsUnchanged(t *testing.T) {
	path := writeCatalog(t, gotextCatalog)
	doc, err := LoadDocument(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := doc.Save(path); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != gotextCatalog {
		t.Errorf("round trip changed the file:\n%s", got)
	}
}

func TestDocumentSetsTranslationLosslessly(t *testing.T) {
	path := writeCatalog(t, gotextCatalog)
	doc, err := LoadDocument(path)
	if err != nil {
		t.Fatal(err)
	}
	found, err := doc.SetTranslation("Hello <b>{Name}</b>", "Hola <b>{Name}</b>")
	if err != nil || !found {
		t.Fatalf("SetTranslation() = %v, %v; want found", found, err)
	}
	if found, _ := doc.SetTranslation("Unknown", "x"); found {
		t.Error("SetTranslation(Unknown) reported an entry")
	}
	if err := doc.Save(path); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	want := strings.Replace(gotextCatalog, `"translation": "",`, `"translation": "Hola \u003cb\u003e{Name}\u003c/b\u003e",`, 1)
	if string(got) != want {
		t.Errorf("Save() wrote:\n%s\nwant:\n%s", got, want)
	}
}

func TestDocumentAppendsFromSource(t *testing.T) {
	source, err := LoadDocument(writeCatalog(t, gotextCatalog))
	if err != nil {
		t.Fatal(err)
	}
	path := writeCatalog(t, "{\n  \"language\": \"fr-FR\",\n  \"messages\": []\n}\n")
	doc, err := LoadDocument(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := doc.AppendFrom(source, "Hello <b>{Name}</b>", "Bonjour <b>{Name}</b>"); err != nil {
		t.Fatal(err)
	}
	if err := doc.AppendFrom(source, "Missing", "x"); err == nil {
		t.Error("AppendFrom(Missing) error = nil, want not found")
	}
	if err := doc.Save(path); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if len(loaded.Messages) != 1 || !strings.Contains(string(got), `"translation": "Bonjour \u003cb\u003e{Name}\u003c/b\u003e"`) ||
		!strings.Contains(string(got), `"string": "%[1]s"`) || !strings.Contains(string(got), "\n  \"language\"") {
		t.Errorf("Save() wrote:\n%s\nwant one entry with the source placeholders, two-space indent", got)
	}
}

func TestDocumentSaveKeepsPermissions(t *testing.T) {
	path := writeCatalog(t, gotextCatalog)
	if err := os.Chmod(path, 0o640); err != nil {
		t.Fatal(err)
	}
	doc, err := LoadDocument(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := doc.Save(path); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Errorf("mode = %v, want 0640", info.Mode().Perm())
	}
}

func TestDocumentAcceptsNullMessages(t *testing.T) {
	source, err := LoadDocument(writeCatalog(t, gotextCatalog))
	if err != nil {
		t.Fatal(err)
	}
	path := writeCatalog(t, `{"language": "fr-FR", "messages": null}`)
	doc, err := LoadDocument(path)
	if err != nil {
		t.Fatalf("LoadDocument(null messages) error = %v", err)
	}
	if err := doc.AppendFrom(source, "Bye", "Au revoir"); err != nil {
		t.Fatal(err)
	}
	if err := doc.Save(path); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil || len(loaded.Messages) != 1 {
		t.Errorf("Load() = %+v, %v; want one message", loaded, err)
	}
}
