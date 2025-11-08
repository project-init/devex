package models

type Task struct {
	Summary     string   `yaml:"summary"`
	Description string   `yaml:"description"`
	Labels      []string `yaml:"labels,omitempty"`
	Type        string   `yaml:"type,omitempty"`
}
