package config

// SLOsConfiguration overrides the defaults of devex sre analyze slos. Every field is optional: the command runs
// from --group alone, in a repository with no .sre directory.
type SLOsConfiguration struct {
	// Module is the Go module whose release carries the SLO file. Its version comes from this repository's
	// go.mod, so the report reads the SLOs the code was built against. When empty, it is the module go.mod requires
	// that ships File.
	Module string `yaml:"module"`
	// Version is used only when go.mod does not require Module. It defaults to latest.
	Version string `yaml:"version"`
	// File is the SLO file's path inside Module. It defaults to sre/configuration/slos.gen.yaml.
	File string `yaml:"file"`
	// Group is the key under services in the SLO file, which --group overrides.
	Group string `yaml:"group"`
	// MetricPrefix comes before each RPC's metric name: <metricPrefix>.<metric>.response_time. It is read from
	// Datadog unless this or --metric-prefix pins it.
	MetricPrefix string                   `yaml:"metricPrefix"`
	Datadog      SLOsDatadogConfiguration `yaml:"datadog"`
}

type SLOsDatadogConfiguration struct {
	// Site defaults to datadoghq.com.
	Site string `yaml:"site"`
	// Scope is the tag filter on every query. It defaults to environment:production, because the same metric
	// carries uat.
	Scope string `yaml:"scope"`
}
