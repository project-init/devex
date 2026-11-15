package components

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/project-init/devex/internal/components/db"
	"gopkg.in/yaml.v3"
)

type Config struct {
	DB              db.Config `yaml:"db"`
	OutputDirectory string    `yaml:"outputDirectory"`
}

// LoadConfig reads the configuration at path. An unknown key fails, so a misspelled one is not
// silently ignored.
func LoadConfig(path string) (*Config, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	var config Config
	decoder := yaml.NewDecoder(file)
	decoder.KnownFields(true)
	// An empty file decodes to the zero configuration, which validation then explains.
	if err := decoder.Decode(&config); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("decode components configuration %s: %w", path, err)
	}
	return &config, nil
}
