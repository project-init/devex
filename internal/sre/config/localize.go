package config

type LocalizeConfiguration struct {
	// LocalesDir is the root directory containing locale subdirectories (e.g., "en-US", "es-US")
	LocalesDir string `yaml:"localesDir"`

	// RubricPath is an optional path to a text/markdown file containing translation rules,
	// legal terms, and pre-defined translation strings to guide the LLM.
	RubricPath string `yaml:"rubricPath"`
}
