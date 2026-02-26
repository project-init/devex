package config

import (
	"os"
	"time"

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
	Organization      string            `yaml:"organization"`
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

func (cfg *Config) CutoffDate() time.Time {
	year, month, day := time.Now().AddDate(0, 0, -cfg.NumLookBackDays).Date()
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}
