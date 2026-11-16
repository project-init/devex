package audit

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/project-init/devex/internal/localize/catalog"
	"github.com/project-init/devex/internal/ui"
)

func writeLocale(t *testing.T, dir, locale, messages, out string) {
	t.Helper()
	localeDir := filepath.Join(dir, locale)
	if err := os.MkdirAll(localeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(localeDir, catalog.MessagesFile), []byte(messages), 0o644); err != nil {
		t.Fatal(err)
	}
	if out != "" {
		if err := os.WriteFile(filepath.Join(localeDir, catalog.OutFile), []byte(out), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAuditReportsMissingAndUncopied(t *testing.T) {
	dir := t.TempDir()
	writeLocale(t, dir, "en-US",
		`{"language":"en-US","messages":[{"id":"Hello","translation":""},{"id":"Bye","translation":"Bye"}]}`,
		`{"language":"en-US","messages":[{"id":"Hello"},{"id":"Bye"},{"id":"New"}]}`)
	if err := os.WriteFile(filepath.Join(dir, ".DS_Store"), []byte("junk"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	err := audit(dir, ui.NewWithStreams(nil, &out, &errOut, ui.Options{}))
	if err == nil || !strings.Contains(err.Error(), "1 missing and 1 uncopied") {
		t.Fatalf("audit() error = %v, want 1 missing and 1 uncopied", err)
	}
	want := "Missing translations (1):\n  [en-US] Hello\nMessages in out.gotext.json not copied into messages.gotext.json (1):\n  [en-US] New\n"
	if out.String() != want {
		t.Errorf("stdout = %q, want %q", out.String(), want)
	}
}

func TestAuditPassesOnce(t *testing.T) {
	dir := t.TempDir()
	writeLocale(t, dir, "en-US", `{"language":"en-US","messages":[{"id":"Hello","translation":"Hello"}]}`, "")
	var out, errOut bytes.Buffer
	if err := audit(dir, ui.NewWithStreams(nil, &out, &errOut, ui.Options{})); err != nil {
		t.Fatal(err)
	}
	if out.String() != "" || strings.Count(errOut.String(), "All translations are present.") != 1 {
		t.Errorf("stdout = %q, stderr = %q; want one success message on stderr", out.String(), errOut.String())
	}
}

func TestAuditSkipsDirectoriesWithoutCatalogs(t *testing.T) {
	dir := t.TempDir()
	writeLocale(t, dir, "en-US", `{"language":"en-US","messages":[{"id":"Hello","translation":"Hello"}]}`, "")
	if err := os.MkdirAll(filepath.Join(dir, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := audit(dir, ui.NewWithStreams(nil, &bytes.Buffer{}, &bytes.Buffer{}, ui.Options{})); err != nil {
		t.Errorf("audit() error = %v, want helper directories skipped", err)
	}
}
