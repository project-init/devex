package models

import "fmt"

type Epic struct {
	Summary     string   `yaml:"summary"`
	Description string   `yaml:"description"`
	Labels      []string `yaml:"labels,omitempty"`
	Tasks       []Task   `yaml:"tasks"`
}

func (e *Epic) CheckRequirements() error {
	if e.Summary == "" {
		return fmt.Errorf("epic summary is empty")
	}
	if e.Description == "" {
		return fmt.Errorf("epic description is empty")
	}
	if e.Tasks == nil {
		return fmt.Errorf("epic tasks are empty")
	}
	return nil
}
