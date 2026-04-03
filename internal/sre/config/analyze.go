package config

import (
	"github.com/project-init/devex/internal/sre/config/analyze"
)

type AnalysisDepth string

const (
	Diff         AnalysisDepth = "Diff"
	Repo         AnalysisDepth = "Repo"
	Organization AnalysisDepth = "Organization"
)

var AllowableAnalysisDepths = []AnalysisDepth{Diff, Repo, Organization}

type AnalyzeConfiguration struct {
	AIAgent      string                           `yaml:"aiAgent"`
	Depth        AnalysisDepth                    `yaml:"depth"`
	Protos       analyze.ProtosConfiguration      `yaml:"protos"`
	APIs         AnalyzeAPIsConfiguration         `yaml:"apis"`
	SQL          AnalyzeSQLConfiguration          `yaml:"sql"`
	Reliability  AnalyzeReliabilityConfiguration  `yaml:"reliability"`
	Dependencies AnalyzeDependenciesConfiguration `yaml:"dependencies"`
	Ownership    AnalyzeOwnershipConfiguration    `yaml:"ownership"`
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
