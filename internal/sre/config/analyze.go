package config

type AnalysisDepth string

const (
	Diff         AnalysisDepth = "Diff"
	Repo         AnalysisDepth = "Repo"
	Organization AnalysisDepth = "Organization"
)

type AnalyzeConfiguration struct {
	AIAgent      string                           `yaml:"aiAgent"`
	Depth        AnalysisDepth                    `yaml:"depth"`
	Protos       AnalyzeProtosConfiguration       `yaml:"protos"`
	APIs         AnalyzeAPIsConfiguration         `yaml:"apis"`
	SQL          AnalyzeSQLConfiguration          `yaml:"sql"`
	Reliability  AnalyzeReliabilityConfiguration  `yaml:"reliability"`
	Dependencies AnalyzeDependenciesConfiguration `yaml:"dependencies"`
	Ownership    AnalyzeOwnershipConfiguration    `yaml:"ownership"`
}

type AnalyzeProtosConfiguration struct {
	Enabled *bool `yaml:"enabled"`
}

type AnalyzeAPIsConfiguration struct {
	Enabled *bool `yaml:"enabled"`
}

type AnalyzeSQLConfiguration struct {
	Enabled *bool `yaml:"enabled"`
}

type AnalyzeDependenciesConfiguration struct {
	Enabled *bool `yaml:"enabled"`
}

type AnalyzeReliabilityConfiguration struct {
	Enabled *bool `yaml:"enabled"`
}

type AnalyzeOwnershipConfiguration struct {
	Enabled *bool `yaml:"enabled"`
}
