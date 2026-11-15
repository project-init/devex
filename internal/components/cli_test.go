package components

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/project-init/devex/internal/ui"
)

func TestDryRunWritesNothing(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "components.yaml")
	config := "outputDirectory: " + filepath.Join(dir, "gen") + "\ndb:\n  schemaName: data_platform\n"
	if err := os.WriteFile(configPath, []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := run(ui.NewWithStreams(nil, &out, &bytes.Buffer{}, ui.Options{}), configPath, true, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "gen")); !os.IsNotExist(err) {
		t.Errorf("gen exists (%v); want nothing written", err)
	}
	if !bytes.Contains(out.Bytes(), []byte("structure.sql")) {
		t.Errorf("output lists no structure.sql:\n%s", out.String())
	}
}

func TestAnEmptyConfigAsksForTheSchemaName(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "components.yaml")
	if err := os.WriteFile(configPath, []byte("# nothing yet\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := run(ui.NewWithStreams(nil, &bytes.Buffer{}, &bytes.Buffer{}, ui.Options{}), configPath, true, false)
	if err == nil || !strings.Contains(err.Error(), "db.schemaName is required") {
		t.Errorf("run() error = %v, want the missing schema name", err)
	}
}
