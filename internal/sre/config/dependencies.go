package config

type DependenciesConfiguration struct {
	Go   *bool `yaml:"go"`
	Mise *bool `yaml:"mise"`
	Buf  *bool `yaml:"buf"`
}
