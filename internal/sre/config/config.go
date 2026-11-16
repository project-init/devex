package config

import (
	"context"

	"github.com/project-init/devex/internal/configdir"
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

// discoveryKey is the top-level key every devex discovery configuration declares. Discovery
// keeps its file in .sre by default and loads it itself.
const discoveryKey = "targets"

func LoadConfig(configDirPath string) (*Configuration, error) {
	var config Configuration
	if err := configdir.Load(configDirPath, &config, discoveryKey); err != nil {
		return nil, err
	}
	return &config, nil
}
