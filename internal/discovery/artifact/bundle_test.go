package artifact

import (
	"crypto/sha256"
	"encoding/hex"
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

func TestDigestCoversTheSynopsisOnlyWhenPresent(t *testing.T) {
	bundle := Bundle{DiscoveryContent: []byte("discovery"), WorkBreakdownRaw: []byte("breakdown")}
	// The formula bundles without a synopsis have always used, so their existing plans stay fresh.
	hash := sha256.Sum256([]byte("discovery-document\x00discovery\x00work-breakdown\x00breakdown"))
	if want := "sha256:" + hex.EncodeToString(hash[:]); bundle.Digest() != want {
		t.Fatalf("Digest() without a synopsis = %s, want %s", bundle.Digest(), want)
	}
	bundle.SynopsisContent = []byte("# One")
	first := bundle.Digest()
	bundle.SynopsisContent = []byte("# Two")
	if first == bundle.Digest() {
		t.Fatal("Digest() ignores a synopsis edit")
	}
}
