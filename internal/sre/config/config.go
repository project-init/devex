package config

import (
	"context"
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"
)

type Configuration struct {
	Analyze      AnalyzeConfiguration      `yaml:"analyze"`
	Dependencies DependenciesConfiguration `yaml:"dependencies"`
	Keygen       KeygenConfiguration       `yaml:"keygen"`
	Postgres     PostgresConfiguration     `yaml:"postgres"`
	Release      ReleaseConfiguration      `yaml:"release"`
}

// OptionalAnnotation marks a command that runs without a config directory, applying its own
// defaults when GetConfig finds none. The sre root checks the command and each parent.
const OptionalAnnotation = "devex.sre/config-optional"

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
