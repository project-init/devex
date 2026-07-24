package mobile

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/project-init/devex/internal/localize/config"
)

func TestGenerate(t *testing.T) {
	root := t.TempDir()
	localesDir := filepath.Join(root, "locales")
	registryPath := filepath.Join(root, "registry.go")
	sourceDir := filepath.Join(root, "ios")
	outputDir := filepath.Join(root, "output")

	writeTestFile(t, registryPath, `package registry

func strings(p *message.Printer) {
	p.Sprintf("Sign in")
	var email string
	p.Sprintf("Welcome, %s", email) // id: Welcome, {Email}
}
`)
	writeTestFile(t, filepath.Join(sourceDir, "View.swift"), `
let title = translate("Sign in")
let welcome = translate("Welcome, {Email}")
`)
	writeTestFile(t, filepath.Join(localesDir, "en-US", outCatalogName), `{
  "language": "en-US",
  "messages": [
    {"id": "Welcome, {Email}", "translation": "Welcome, {Email}"},
    {"id": "Sign in", "translation": "Sign in"},
    {"id": "Web only", "translation": "Web only"}
  ]
}
`)
	writeTestFile(t, filepath.Join(localesDir, "es-US", outCatalogName), `{
  "language": "es-US",
  "messages": [
    {"id": "Sign in", "translation": "Iniciar sesión"},
    {"id": "Welcome, {Email}", "translation": ""}
  ]
}
`)
	writeTestFile(t, filepath.Join(outputDir, "l10n-stale.json"), "stale")
	writeTestFile(t, filepath.Join(outputDir, "keep.json"), "keep")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := generate(localesDir, config.MobileConfiguration{
		RegistryPath: registryPath,
		SourceDir:    sourceDir,
		OutputDir:    outputDir,
	}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("generate() error = %v", err)
	}

	assertTestFile(t, filepath.Join(outputDir, "l10n-en-US.json"), `{
  "language": "en-US",
  "messages": {
    "Welcome, {Email}": "Welcome, {Email}",
    "Sign in": "Sign in"
  }
}
`)
	assertTestFile(t, filepath.Join(outputDir, "l10n-es-US.json"), `{
  "language": "es-US",
  "messages": {
    "Sign in": "Iniciar sesión"
  }
}
`)
	if _, err := os.Stat(filepath.Join(outputDir, "l10n-stale.json")); !os.IsNotExist(err) {
		t.Errorf("stale bundle was not removed")
	}
	assertTestFile(t, filepath.Join(outputDir, "keep.json"), "keep")
	if !strings.Contains(stdout.String(), "l10n-en-US.json (2 messages)") {
		t.Errorf("stdout = %q", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q", stderr.String())
	}
}

func TestGenerateValidatesCatalogAndSource(t *testing.T) {
	tests := []struct {
		name        string
		registry    string
		catalog     string
		source      string
		wantError   string
		wantWarning string
	}{
		{
			name:      "missing registry id",
			registry:  `p.Sprintf("Missing")`,
			catalog:   `{"language":"en-US","messages":[]}`,
			source:    `let value = "Missing"`,
			wantError: "registry ids missing",
		},
		{
			name:     "plural translation",
			registry: `p.Sprintf("Items")`,
			catalog: `{"language":"en-US","messages":[{
  "id":"Items","translation":{"one":"Item","other":"Items"}
}]}`,
			source:    `let value = "Items"`,
			wantError: "contains plural/select translations",
		},
		{
			name:     "unexpected placeholder",
			registry: `p.Sprintf("Welcome")`,
			catalog: `{"language":"en-US","messages":[{
  "id":"Welcome","translation":"Welcome, {Name}"
}]}`,
			source:    `let value = "Welcome"`,
			wantError: "placeholders their id lacks",
		},
		{
			name:      "undeclared Swift key",
			registry:  `p.Sprintf("Known")`,
			catalog:   `{"language":"en-US","messages":[{"id":"Known","translation":"Known"}]}`,
			source:    `let value = translate("Unknown")`,
			wantError: "swift translate(...) keys missing",
		},
		{
			name:        "unused registry key",
			registry:    `p.Sprintf("Unused")`,
			catalog:     `{"language":"en-US","messages":[{"id":"Unused","translation":"Unused"}]}`,
			source:      `let value = "Other"`,
			wantWarning: "registry string not referenced by Swift code: Unused",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			localesDir := filepath.Join(root, "locales")
			registryPath := filepath.Join(root, "registry.go")
			sourceDir := filepath.Join(root, "ios")
			outputDir := filepath.Join(root, "output")

			writeTestFile(t, registryPath, tt.registry+"\n")
			writeTestFile(t, filepath.Join(localesDir, "en-US", outCatalogName), tt.catalog)
			writeTestFile(t, filepath.Join(sourceDir, "View.swift"), tt.source)

			var stderr bytes.Buffer
			err := generate(localesDir, config.MobileConfiguration{
				RegistryPath: registryPath,
				SourceDir:    sourceDir,
				OutputDir:    outputDir,
			}, &bytes.Buffer{}, &stderr)

			if tt.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantError) {
					t.Fatalf("generate() error = %v, want containing %q", err, tt.wantError)
				}
				return
			}
			if err != nil {
				t.Fatalf("generate() error = %v", err)
			}
			if !strings.Contains(stderr.String(), tt.wantWarning) {
				t.Errorf("stderr = %q, want containing %q", stderr.String(), tt.wantWarning)
			}
		})
	}
}

func writeTestFile(t *testing.T, path string, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

func assertTestFile(t *testing.T, path string, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Errorf("%s contents =\n%s\nwant:\n%s", path, got, want)
	}
}
