package components

import (
	"os"

	"github.com/project-init/devex/internal/components/db"
	"gopkg.in/yaml.v3"
)

type Config struct {
	DB              db.Config `yaml:"db"`
	OutputDirectory string    `yaml:"outputDirectory"`
}

func LoadConfig(path string) (*Config, error) {
	bytes, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var config Config
	if err = yaml.Unmarshal(bytes, &config); err != nil {
		return nil, err
	}
	return &config, nil
}
