package html

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/project-init/devex/internal/localize/catalog"
	"github.com/project-init/devex/internal/ui"
)

func TestGenerate(t *testing.T) {
	root := t.TempDir()
	localesDir := filepath.Join(root, "locales")
	outputPath := filepath.Join(root, "report", "translations.html")

	writeTestFile(t, filepath.Join(localesDir, "en-US", catalog.MessagesFile), `{
  "language": "en-US",
  "messages": [
    {"id": "Sign in", "translation": ""},
    {"id": "Welcome, {Name}", "translation": ""}
  ]
}
`)
	writeTestFile(t, filepath.Join(localesDir, "es-US", catalog.MessagesFile), `{
  "language": "es-US",
  "messages": [
    {"id": "Sign in", "translation": "Iniciar sesión"},
    {"id": "Welcome, {Name}", "translation": ""}
  ]
}
`)
	writeTestFile(t, filepath.Join(localesDir, "fr-FR", catalog.MessagesFile), `{
  "language": "fr-FR",
  "messages": [
    {"id": "Sign in", "translation": "Se connecter"}
  ]
}
`)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := Generate(localesDir, outputPath, testUI(&stdout, &stderr))
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

	if !strings.Contains(stderr.String(), "Wrote "+outputPath+" (2 locales, 2 rows)") || strings.Contains(stderr.String(), "warning:") {
		t.Errorf("stderr = %q", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want nothing", stdout.String())
	}
}

func TestGenerateIncludesIDOnlyPresentInBaseLocale(t *testing.T) {
	root := t.TempDir()
	localesDir := filepath.Join(root, "locales")
	outputPath := filepath.Join(root, "translations.html")

	writeTestFile(t, filepath.Join(localesDir, "en-US", catalog.MessagesFile), `{
  "language": "en-US",
  "messages": [
    {"id": "Brand new", "translation": ""}
  ]
}
`)
	writeTestFile(t, filepath.Join(localesDir, "es-US", catalog.MessagesFile), `{
  "language": "es-US",
  "messages": []
}
`)

	var stdout, stderr bytes.Buffer
	if err := Generate(localesDir, outputPath, testUI(&stdout, &stderr)); err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	row := generatedRow(t, outputPath, "Brand new")
	if !strings.Contains(row, `<td class="missing"></td>`) {
		t.Errorf("base-only id should have a missing translation cell; row = %q", row)
	}
	if !strings.Contains(stderr.String(), "(1 locales, 1 rows)") || strings.Contains(stderr.String(), "warning:") {
		t.Errorf("stderr = %q", stderr.String())
	}
}

func TestGenerateMarksNonStringTranslation(t *testing.T) {
	root := t.TempDir()
	localesDir := filepath.Join(root, "locales")
	outputPath := filepath.Join(root, "translations.html")

	writeTestFile(t, filepath.Join(localesDir, "en-US", catalog.MessagesFile), `{
  "language": "en-US",
  "messages": []
}
`)
	writeTestFile(t, filepath.Join(localesDir, "fr-FR", catalog.MessagesFile), `{
  "language": "fr-FR",
  "messages": [
    {"id": "Unread messages", "translation": {"plural": {"one": "Un message", "other": "Des messages"}}}
  ]
}
`)

	var stdout, stderr bytes.Buffer
	if err := Generate(localesDir, outputPath, testUI(&stdout, &stderr)); err != nil {
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

	writeTestFile(t, filepath.Join(localesDir, "en-US", catalog.MessagesFile), `{
  "language": "en-US",
  "messages": [{"id": "Hello", "translation": ""}]
}
`)
	writeTestFile(t, filepath.Join(localesDir, "fr-CA", catalog.MessagesFile), `{
  "language": "fr-FR",
  "messages": [{"id": "Hello", "translation": "Allô"}]
}
`)
	writeTestFile(t, filepath.Join(localesDir, "fr-FR", catalog.MessagesFile), `{
  "language": "fr-FR",
  "messages": [{"id": "Hello", "translation": "Bonjour"}]
}
`)

	var stdout, stderr bytes.Buffer
	if err := Generate(localesDir, outputPath, testUI(&stdout, &stderr)); err != nil {
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
	if !strings.Contains(stderr.String(), "(2 locales, 1 rows)") {
		t.Errorf("stderr = %q", stderr.String())
	}
}

func TestGenerateMissingConfig(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := Generate("", "out.html", testUI(&stdout, &stderr)); err == nil {
		t.Fatal("expected error for missing localesDir")
	}
	if err := Generate("locales", "", testUI(&stdout, &stderr)); err == nil {
		t.Fatal("expected error for missing outputPath")
	}
}

func TestGenerateReportsCoverageOffline(t *testing.T) {
	root := t.TempDir()
	localesDir := filepath.Join(root, "locales")
	outputPath := filepath.Join(root, "translations.html")
	writeTestFile(t, filepath.Join(localesDir, "en-US", catalog.MessagesFile), `{"language": "en-US", "messages": [
  {"id": "One", "message": "One"}, {"id": "Two", "message": "Two"}, {"id": "Three", "message": "Three"}, {"id": "Four", "message": "Four"}]}`)
	writeTestFile(t, filepath.Join(localesDir, "es-US", catalog.MessagesFile), `{"language": "es-US", "messages": [
  {"id": "One", "translation": "Uno"}, {"id": "Two", "translation": "Dos"}, {"id": "Three", "translation": "Tres"}, {"id": "Four", "translation": ""}]}`)
	writeTestFile(t, filepath.Join(localesDir, ".DS_Store"), "junk")

	var stdout, stderr bytes.Buffer
	if err := Generate(localesDir, outputPath, testUI(&stdout, &stderr)); err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	html := string(data)
	for _, want := range []string{
		`<div class="tile low">`,
		"<b>75%</b>",
		"3 of 4 translated",
		`<tr data-missing>`,
		`id="search"`,
		`id="missing-only"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("output missing %q", want)
		}
	}
	if strings.Contains(html, "<link") || strings.Contains(html, "https://") {
		t.Error("report loads a remote resource; it must render offline")
	}
}

func TestGenerateCountsAbsentTranslationAsMissing(t *testing.T) {
	root := t.TempDir()
	localesDir := filepath.Join(root, "locales")
	outputPath := filepath.Join(root, "translations.html")
	writeTestFile(t, filepath.Join(localesDir, "en-US", catalog.MessagesFile), `{"language": "en-US", "messages": [
  {"id": "One", "message": "One"}, {"id": "Two", "message": "Two"}]}`)
	writeTestFile(t, filepath.Join(localesDir, "es-US", catalog.MessagesFile), `{"language": "es-US", "messages": [
  {"id": "One", "translation": "Uno"}, {"id": "Two"}]}`)
	writeTestFile(t, filepath.Join(localesDir, "fr-FR", catalog.MessagesFile), `{"language": "fr-FR", "messages": [
  {"id": "One", "translation": "Un"}, {"id": "Two", "translation": "Deux"}, {"id": "Removed", "translation": "Retiré"}]}`)

	var stdout, stderr bytes.Buffer
	if err := Generate(localesDir, outputPath, testUI(&stdout, &stderr)); err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if strings.Contains(stderr.String(), "non-string") {
		t.Errorf("stderr = %q, want no plural warning for an absent translation", stderr.String())
	}
	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	html := string(data)
	for _, want := range []string{"1 of 2 translated", "2 of 2 translated", "2 source strings"} {
		if !strings.Contains(html, want) {
			t.Errorf("output missing %q", want)
		}
	}
}

func testUI(stdout, stderr *bytes.Buffer) *ui.UI {
	return ui.NewWithStreams(nil, stdout, stderr, ui.Options{})
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
