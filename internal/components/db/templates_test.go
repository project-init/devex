package db

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"testing/fstest"
)

var valid = Config{SchemaName: "data_platform"}

func TestPlanRendersTheEmbeddedTemplates(t *testing.T) {
	files, err := Plan(valid, t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if filepath.Base(file.Path) == "structure.sql" {
			if !strings.Contains(string(file.content), "CREATE SCHEMA data_platform;") {
				t.Errorf("structure.sql lacks the schema:\n%s", file.content)
			}
			return
		}
	}
	t.Errorf("planned files %v lack structure.sql", files)
}

func TestPlanStopsOnABrokenTemplateBeforeWriting(t *testing.T) {
	source := fstest.MapFS{
		"templates/a.sql.tmpl": {Data: []byte("CREATE SCHEMA {{ .SchemaName }};")},
		"templates/b.sql.tmpl": {Data: []byte("{{ .Missing }}")},
	}
	out := filepath.Join(t.TempDir(), "db")
	if _, err := plan(source, "templates", valid, out, false); err == nil || !strings.Contains(err.Error(), "b.sql") {
		t.Errorf("plan() error = %v, want one naming b.sql", err)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Errorf("output directory exists (%v); want nothing written", err)
	}
}

func TestWriteMakesScriptsExecutable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows file modes carry no execute or group bits")
	}
	source := fstest.MapFS{
		"templates/init/000-init.local-only.sh": {Data: []byte("#!/bin/bash\n")},
		"templates/init/100-bootstrap.sql.tmpl": {Data: []byte("CREATE SCHEMA {{ .SchemaName }};")},
	}
	out := t.TempDir()
	files, err := plan(source, "templates", valid, out, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := Write(files); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]os.FileMode{"init/000-init.local-only.sh": umasked(t, 0o777), "init/100-bootstrap.sql": umasked(t, 0o666)} {
		info, err := os.Stat(filepath.Join(out, name))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != want {
			t.Errorf("%s mode = %v, want %v", name, info.Mode().Perm(), want)
		}
	}
}

func TestPlanKeepsTheTeamsFilesUnlessForced(t *testing.T) {
	source := fstest.MapFS{
		"templates/structure.sql.tmpl": {Data: []byte("CREATE SCHEMA {{ .SchemaName }};")},
		"templates/README.md":          {Data: []byte("generated")},
		"templates/doc/new.md":         {Data: []byte("generated")},
	}
	out := t.TempDir()
	for _, name := range []string{"structure.sql", "README.md"} {
		if err := os.WriteFile(filepath.Join(out, name), []byte("edited"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for force, want := range map[bool]string{
		false: "README.md:overwrite doc/new.md:create structure.sql:skip",
		true:  "README.md:overwrite doc/new.md:create structure.sql:overwrite",
	} {
		files, err := plan(source, "templates", valid, out, force)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, file := range files {
			relative, _ := filepath.Rel(out, file.Path)
			got = append(got, filepath.ToSlash(relative)+":"+string(file.Action))
		}
		if strings.Join(got, " ") != want {
			t.Errorf("force=%t: actions = %v, want %s", force, got, want)
		}
	}
}

func TestValidateRejectsSchemaNamesTheTemplatesCannotUse(t *testing.T) {
	for name, want := range map[string]string{
		"":                                   "required",
		"Data_Platform":                      "lowercase",
		"1platform":                          "lowercase",
		"drop schema x; --":                  "lowercase",
		"user":                               "reserved",
		"pg_data":                            "pg_",
		"left":                               "reserved",
		"public":                             "already has",
		"template1":                          "already has",
		strings.Repeat("a", maxSchemaName+1): "limit",
		"data_platform":                      "",
		strings.Repeat("a", maxSchemaName):   "",
	} {
		err := Config{SchemaName: name}.Validate()
		if (want == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), want)) {
			t.Errorf("Validate(%q) = %v, want %q", name, err, want)
		}
	}
}

func TestPlanRejectsAnInvalidSchemaNameFirst(t *testing.T) {
	if _, err := Plan(Config{SchemaName: "user"}, t.TempDir(), false); err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Errorf("Plan(user) error = %v, want the reserved-word error", err)
	}
}

func TestTheLongestSchemaNameKeepsEveryDerivedNameWithinPostgresLimit(t *testing.T) {
	schema := strings.Repeat("s", maxSchemaName)
	files, err := Plan(Config{SchemaName: schema}, t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	derived := regexp.MustCompile(`\w*` + schema + `\w*`)
	for _, file := range files {
		for _, name := range derived.FindAllString(string(file.content), -1) {
			if len(name) > 63 {
				t.Errorf("%s derives %s (%d bytes); PostgreSQL truncates names past 63", filepath.Base(file.Path), name, len(name))
			}
		}
	}
}

// umasked is the mode a file created with perm gets under this process's umask.
func umasked(t *testing.T, perm os.FileMode) os.FileMode {
	t.Helper()
	path := filepath.Join(t.TempDir(), "reference")
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		t.Fatal(err)
	}
	info, err := file.Stat()
	_ = file.Close()
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode().Perm()
}

func TestWriteKeepsAReplacedFilesMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows file modes carry no execute or group bits")
	}
	source := fstest.MapFS{
		"templates/README.md":                   {Data: []byte("generated")},
		"templates/init/000-init.local-only.sh": {Data: []byte("#!/bin/bash\n")},
	}
	out := t.TempDir()
	if err := os.MkdirAll(filepath.Join(out, "init"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A private README stays private; a script an older devex wrote 0644 becomes executable.
	existing := map[string]os.FileMode{"README.md": 0o600, "init/000-init.local-only.sh": 0o644}
	for name, mode := range existing {
		if err := os.WriteFile(filepath.Join(out, name), []byte("old"), mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(filepath.Join(out, name), mode); err != nil {
			t.Fatal(err)
		}
	}
	files, err := plan(source, "templates", valid, out, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := Write(files); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]os.FileMode{"README.md": 0o600, "init/000-init.local-only.sh": 0o755} {
		info, err := os.Stat(filepath.Join(out, name))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != want {
			t.Errorf("%s mode = %v, want %v", name, info.Mode().Perm(), want)
		}
	}
}
