package config

import (
	"context"
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"
)

type Configuration struct {
	Keygen   KeygenConfiguration   `yaml:"keygen"`
	Postgres PostgresConfiguration `yaml:"postgres"`
	Release  ReleaseConfiguration  `yaml:"release"`
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
