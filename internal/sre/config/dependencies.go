package config

type DependenciesConfiguration struct {
	// Upgrade lists the ecosystems `upgrade --all` runs: mise, go, and buf.
	Upgrade []string `yaml:"upgrade"`
	// Go configures how a Go upgrade keeps the repository's version pins in sync.
	Go GoDependenciesConfiguration `yaml:"go"`
}

type GoDependenciesConfiguration struct {
	// Target selects the upgrade version: patch (latest patch of the current minor) or latest.
	Target string `yaml:"target"`
	// Directive sets how the go directives in go.mod and go.work follow the toolchain: none,
	// minor, or exact.
	Directive string `yaml:"directive"`
	// Images lists the container images that carry the Go toolchain.
	Images []string `yaml:"images"`
	// Exclude lists globs skipped during pin discovery, on top of .gitignore.
	Exclude []string `yaml:"exclude"`
	// Pins declares version locations the built-in finders cannot recognize.
	Pins []GoPinConfiguration `yaml:"pins"`
}

type GoPinConfiguration struct {
	// Files lists the globs this pin applies to.
	Files []string `yaml:"files"`
	// Pattern is a regular expression whose "version" named group captures the version.
	Pattern string `yaml:"pattern"`
}
