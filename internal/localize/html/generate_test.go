package html

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerate(t *testing.T) {
	root := t.TempDir()
	localesDir := filepath.Join(root, "locales")
	outputPath := filepath.Join(root, "report", "translations.html")

	writeTestFile(t, filepath.Join(localesDir, "en-US", messagesCatalogName), `{
  "language": "en-US",
  "messages": [
    {"id": "Sign in", "translation": ""},
    {"id": "Welcome, {Name}", "translation": ""}
  ]
}
`)
	writeTestFile(t, filepath.Join(localesDir, "es-US", messagesCatalogName), `{
  "language": "es-US",
  "messages": [
    {"id": "Sign in", "translation": "Iniciar sesión"},
    {"id": "Welcome, {Name}", "translation": ""}
  ]
}
`)
	writeTestFile(t, filepath.Join(localesDir, "fr-FR", messagesCatalogName), `{
  "language": "fr-FR",
  "messages": [
    {"id": "Sign in", "translation": "Se connecter"}
  ]
}
`)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := Generate(localesDir, outputPath, &stdout, &stderr)
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	html := string(data)

	for _, want := range []string{
		"<th>es-US</th>",
		"<th>fr-FR</th>",
		"<td>Sign in</td>",
		"<td>Iniciar sesión</td>",
		"<td>Se connecter</td>",
		"<td>Welcome, {Name}</td>",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("output missing %q\noutput:\n%s", want, html)
		}
	}
	if strings.Contains(html, "en-US") {
		t.Errorf("output should not include the base locale as a column: %s", html)
	}

	if !strings.Contains(stdout.String(), "wrote "+outputPath+" (2 locales, 2 strings)") {
		t.Errorf("stdout = %q", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q", stderr.String())
	}
}

func TestGenerateIncludesIDOnlyPresentInBaseLocale(t *testing.T) {
	root := t.TempDir()
	localesDir := filepath.Join(root, "locales")
	outputPath := filepath.Join(root, "translations.html")

	writeTestFile(t, filepath.Join(localesDir, "en-US", messagesCatalogName), `{
  "language": "en-US",
  "messages": [
    {"id": "Brand new", "translation": ""}
  ]
}
`)
	writeTestFile(t, filepath.Join(localesDir, "es-US", messagesCatalogName), `{
  "language": "es-US",
  "messages": []
}
`)

	var stdout, stderr bytes.Buffer
	if err := Generate(localesDir, outputPath, &stdout, &stderr); err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	row := generatedRow(t, outputPath, "Brand new")
	if !strings.Contains(row, "<td></td>") {
		t.Errorf("base-only id should have an empty translation cell; row = %q", row)
	}
	if !strings.Contains(stdout.String(), "(1 locales, 1 strings)") {
		t.Errorf("stdout = %q", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q", stderr.String())
	}
}

func TestGenerateMarksNonStringTranslation(t *testing.T) {
	root := t.TempDir()
	localesDir := filepath.Join(root, "locales")
	outputPath := filepath.Join(root, "translations.html")

	writeTestFile(t, filepath.Join(localesDir, "en-US", messagesCatalogName), `{
  "language": "en-US",
  "messages": []
}
`)
	writeTestFile(t, filepath.Join(localesDir, "fr-FR", messagesCatalogName), `{
  "language": "fr-FR",
  "messages": [
    {"id": "Unread messages", "translation": {"plural": {"one": "Un message", "other": "Des messages"}}}
  ]
}
`)

	var stdout, stderr bytes.Buffer
	if err := Generate(localesDir, outputPath, &stdout, &stderr); err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	row := generatedRow(t, outputPath, "Unread messages")
	if !strings.Contains(row, "<td>"+nonStringTranslationMarker+"</td>") {
		t.Errorf("non-string translation should be marked explicitly; row = %q", row)
	}
	if !strings.Contains(stderr.String(), `non-string translation for "Unread messages"`) {
		t.Errorf("stderr = %q", stderr.String())
	}
}

func TestGenerateKeysLocalesByDirectoryName(t *testing.T) {
	root := t.TempDir()
	localesDir := filepath.Join(root, "locales")
	outputPath := filepath.Join(root, "translations.html")

	writeTestFile(t, filepath.Join(localesDir, "en-US", messagesCatalogName), `{
  "language": "en-US",
  "messages": [{"id": "Hello", "translation": ""}]
}
`)
	writeTestFile(t, filepath.Join(localesDir, "fr-CA", messagesCatalogName), `{
  "language": "fr-FR",
  "messages": [{"id": "Hello", "translation": "Allô"}]
}
`)
	writeTestFile(t, filepath.Join(localesDir, "fr-FR", messagesCatalogName), `{
  "language": "fr-FR",
  "messages": [{"id": "Hello", "translation": "Bonjour"}]
}
`)

	var stdout, stderr bytes.Buffer
	if err := Generate(localesDir, outputPath, &stdout, &stderr); err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	html := string(data)
	for _, want := range []string{"<th>fr-CA</th>", "<th>fr-FR</th>", "<td>Allô</td>", "<td>Bonjour</td>"} {
		if !strings.Contains(html, want) {
			t.Errorf("output missing %q\noutput:\n%s", want, html)
		}
	}
	for _, want := range []string{`declares language "fr-FR"`, `locale directory "fr-CA"`} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("warning missing %q: %q", want, stderr.String())
		}
	}
	if !strings.Contains(stdout.String(), "(2 locales, 1 strings)") {
		t.Errorf("stdout = %q", stdout.String())
	}
}

func TestGenerateMissingConfig(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := Generate("", "out.html", &stdout, &stderr); err == nil {
		t.Fatal("expected error for missing localesDir")
	}
	if err := Generate("locales", "", &stdout, &stderr); err == nil {
		t.Fatal("expected error for missing outputPath")
	}
}

func writeTestFile(t *testing.T, path string, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func generatedRow(t *testing.T, outputPath string, id string) string {
	t.Helper()
	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}

	html := string(data)
	idIndex := strings.Index(html, "<td>"+id+"</td>")
	if idIndex == -1 {
		t.Fatalf("output missing row for %q\noutput:\n%s", id, html)
	}
	rowStart := strings.LastIndex(html[:idIndex], "<tr>")
	rowEndOffset := strings.Index(html[idIndex:], "</tr>")
	if rowStart == -1 || rowEndOffset == -1 {
		t.Fatalf("could not isolate row for %q\noutput:\n%s", id, html)
	}
	return html[rowStart : idIndex+rowEndOffset+len("</tr>")]
}
