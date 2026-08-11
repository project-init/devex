package config

import (
	"context"
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"
)

type Configuration struct {
	Localize LocalizeConfiguration `yaml:"localize"`
}

type LocalizeConfiguration struct {
	// LocalesDir is the root directory containing locale subdirectories (e.g., "en-US", "es-US")
	LocalesDir string `yaml:"localesDir"`

	// RubricPath is an optional path to a text/markdown file containing translation rules,
	// legal terms, and pre-defined translation strings to guide the LLM.
	RubricPath string `yaml:"rubricPath"`

	// Mobile configures generation of platform localization bundles from gotext catalogs.
	Mobile MobileConfiguration `yaml:"mobile"`

	// HTML configures generation of a combined English -> locale translation report.
	HTML HTMLConfiguration `yaml:"html"`
}

type HTMLConfiguration struct {
	// OutputPath is the file path the combined translation report is written to.
	OutputPath string `yaml:"outputPath"`
}

type MobileConfiguration struct {
	// SourceLanguage is the locale whose catalog must contain every registered mobile string.
	SourceLanguage string `yaml:"sourceLanguage"`

	// RegistryPath is the Go source file that declares mobile strings with message.Printer.Sprintf.
	RegistryPath string `yaml:"registryPath"`

	// IOS configures generation of iOS localization bundles from gotext catalogs.
	IOS IOSConfiguration `yaml:"ios"`
}

type IOSConfiguration struct {
	// SourceDir contains Swift source files whose translation keys are validated.
	SourceDir string `yaml:"sourceDir"`

	// OutputDir receives one l10n-<locale>.json bundle per locale.
	OutputDir string `yaml:"outputDir"`
}

type configKey struct{}

func WithConfig(ctx context.Context, cfg *Configuration) context.Context {
	return context.WithValue(ctx, configKey{}, cfg)
}

func GetConfig(ctx context.Context) (*Configuration, bool) {
	cfg, ok := ctx.Value(configKey{}).(*Configuration)
	return cfg, ok
}

func LoadConfig(configDirPath string) (*Configuration, error) {
	files, _ := filepath.Glob(filepath.Join(configDirPath, "*.yaml"))
	sort.Strings(files)

	var config Configuration
	for _, f := range files {
		bytes, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		if err = yaml.Unmarshal(bytes, &config); err != nil {
			return nil, err
		}
	}
	return &config, nil
}
