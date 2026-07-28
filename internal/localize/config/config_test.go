package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigMobileIOS(t *testing.T) {
	configDir := t.TempDir()
	path := filepath.Join(configDir, "localize.yaml")
	data := []byte(`localize:
  localesDir: internal/translations/locales
  mobile:
    sourceLanguage: en-US
    registryPath: internal/translations/mobileregistry/registry.go
    ios:
      sourceDir: ios/ProjectInit
      outputDir: ios/ProjectInit/Resources/l10n
`)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(configDir)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}

	mobile := cfg.Localize.Mobile
	if mobile.SourceLanguage != "en-US" {
		t.Errorf("SourceLanguage = %q, want %q", mobile.SourceLanguage, "en-US")
	}
	if mobile.RegistryPath != "internal/translations/mobileregistry/registry.go" {
		t.Errorf("RegistryPath = %q", mobile.RegistryPath)
	}
	if mobile.IOS.SourceDir != "ios/ProjectInit" {
		t.Errorf("IOS.SourceDir = %q", mobile.IOS.SourceDir)
	}
	if mobile.IOS.OutputDir != "ios/ProjectInit/Resources/l10n" {
		t.Errorf("IOS.OutputDir = %q", mobile.IOS.OutputDir)
	}
}
