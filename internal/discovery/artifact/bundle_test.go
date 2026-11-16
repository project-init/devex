package artifact

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/project-init/devex/internal/strictyaml"
)

func TestLoadSaysAnEmptyWorkBreakdownIsEmpty(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, WorkBreakdownFile), []byte("# filled in later\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); !errors.Is(err, strictyaml.ErrEmpty) {
		t.Errorf("Load(empty work breakdown) error = %v, want ErrEmpty", err)
	}
}
