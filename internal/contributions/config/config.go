package config

import (
	"os"

	"gopkg.in/yaml.v3"
)

type OutputDirectories struct {
	Prs     string `yaml:"prs"`
	Signals string `yaml:"signals"`
}

type Config struct {
	NumLookBackDays   int               `yaml:"numLookBackDays"`
	ReposToSkip       []string          `yaml:"reposToSkip"`
	ReposToCheck      []string          `yaml:"reposToCheck"`
	LastRepo          *string           `yaml:"lastRepo"`
	OutputDirectories OutputDirectories `yaml:"outputDirectories"`
}

func NewConfig(numLookBackDays int, reposToSkip []string, reposToCheck []string, lastRepo *string) *Config {
	return &Config{
		NumLookBackDays: numLookBackDays,
		ReposToSkip:     reposToSkip,
		ReposToCheck:    reposToCheck,
		LastRepo:        lastRepo,
	}
}

func NewConfigFromYaml(configFile string) (*Config, error) {
	bytes, err := os.ReadFile(configFile)
	if err != nil {
		return nil, err
	}

	var config Config
	if err = yaml.Unmarshal(bytes, &config); err != nil {
		return nil, err
	}

	return &config, nil
}
