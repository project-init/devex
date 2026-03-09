package config

import (
	"context"
	"os"

	"gopkg.in/yaml.v3"
)

type Configuration struct {
	Postgres PostgresConfiguration `yaml:"postgres"`
}

type configKey struct{}

func WithConfig(ctx context.Context, cfg *Configuration) context.Context {
	return context.WithValue(ctx, configKey{}, cfg)
}

func GetConfig(ctx context.Context) (*Configuration, bool) {
	cfg, ok := ctx.Value(configKey{}).(*Configuration)
	return cfg, ok
}

func LoadConfig(configFilePath string) (*Configuration, error) {
	bytes, err := os.ReadFile(configFilePath)
	if err != nil {
		return nil, err
	}

	var config Configuration
	if err = yaml.Unmarshal(bytes, &config); err != nil {
		return nil, err
	}

	return &config, nil
}
