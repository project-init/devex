package models

type Workplan struct {
	Epics         []Epic   `yaml:"epics"`
	Investigation string   `yaml:"investigation"`
	Project       string   `yaml:"project"`
	Labels        []string `yaml:"labels,omitempty"`
}
