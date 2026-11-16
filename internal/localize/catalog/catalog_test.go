package catalog

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLocaleDirsSkipsFilesAndHiddenDirs(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"es-US", "en-US", ".git"} {
		if err := os.Mkdir(filepath.Join(dir, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, ".DS_Store"), []byte("junk"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "es-US"), filepath.Join(dir, "es-MX")); err != nil {
		t.Fatal(err)
	}
	got, err := LocaleDirs(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{filepath.Join(dir, "en-US"), filepath.Join(dir, "es-MX"), filepath.Join(dir, "es-US")}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("LocaleDirs() = %v, want %v", got, want)
	}
}

func TestLocaleDirsRejectsEmptyDirectory(t *testing.T) {
	if _, err := LocaleDirs(t.TempDir()); err == nil || !strings.Contains(err.Error(), "no locale directories") {
		t.Errorf("LocaleDirs(empty) error = %v, want no locale directories", err)
	}
}

func TestLoadReadsBothIDForms(t *testing.T) {
	path := filepath.Join(t.TempDir(), MessagesFile)
	data := `{"language":"es-US","messages":[
		{"id":"Hello","message":"Hello","translation":"Hola"},
		{"id":["Bye {Name}","Bye {Name}"],"message":"Bye {Name}","translation":{"select":{}}}]}`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Messages[0].ID != "Hello" || got.Messages[1].ID != "Bye {Name}" {
		t.Errorf("ids = %q, %q", got.Messages[0].ID, got.Messages[1].ID)
	}
	if text, ok := got.Messages[0].Text(); !ok || text != "Hola" {
		t.Errorf("Text() = %q, %v; want Hola", text, ok)
	}
	if _, ok := got.Messages[1].Text(); ok {
		t.Error("Text() of a select message reported a string")
	}
}

func TestLoadNamesMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), OutFile)
	_, err := Load(path)
	if !errors.Is(err, os.ErrNotExist) || !strings.HasPrefix(err.Error(), "missing "+path) {
		t.Errorf("Load(missing) error = %v, want missing %s wrapping os.ErrNotExist", err, path)
	}
}
