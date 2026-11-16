package configdir

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type testConfig struct {
	Release struct {
		AllowMajor bool `yaml:"allowMajor"`
	} `yaml:"release"`
	Keygen struct {
		Length int `yaml:"length"`
	} `yaml:"keygen"`
}

func writeFiles(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestLoadMergesFilesInNameOrder(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"a.yaml":     "release:\n  allowMajor: true\nkeygen:\n  length: 16\n",
		"b.yaml":     "keygen:\n  length: 48\n",
		"empty.yaml": "",
		"notes.txt":  "ignored: true\n",
	})
	var cfg testConfig
	if err := Load(dir, &cfg); err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !cfg.Release.AllowMajor || cfg.Keygen.Length != 48 {
		t.Errorf("Load() = %+v, want allowMajor and length 48", cfg)
	}
}

func TestLoadRejectsUnknownKeyNamingFile(t *testing.T) {
	dir := writeFiles(t, map[string]string{"release.yaml": "release:\n  alowMajor: true\n"})
	var cfg testConfig
	err := Load(dir, &cfg)
	if err == nil {
		t.Fatal("Load() error = nil, want unknown key error")
	}
	for _, want := range []string{filepath.Join(dir, "release.yaml"), "alowMajor"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Load() error = %q, want it to contain %q", err, want)
		}
	}
}

func TestLoadSkipsFilesOwnedByOtherTools(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"jira-targets.yaml": "default_target: jira\ntargets:\n  jira: {}\n",
		"keygen.yaml":       "keygen:\n  length: 24\n",
	})
	var cfg testConfig
	if err := Load(dir, &cfg, "targets"); err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Keygen.Length != 24 {
		t.Errorf("Keygen.Length = %d, want 24", cfg.Keygen.Length)
	}
}

func TestResolveRejectsFile(t *testing.T) {
	dir := writeFiles(t, map[string]string{"release.yaml": "release: {}\n"})
	_, err := Resolve(filepath.Join(dir, "release.yaml"), ".sre")
	if err == nil || !strings.Contains(err.Error(), "is a file") {
		t.Fatalf("Resolve() error = %v, want a file error", err)
	}
}

func TestLoadReportsBadGlob(t *testing.T) {
	var cfg testConfig
	err := Load("bad[", &cfg)
	if err == nil || !strings.Contains(err.Error(), "list config files in bad[") {
		t.Fatalf("Load() error = %v, want glob error naming the directory", err)
	}
}
