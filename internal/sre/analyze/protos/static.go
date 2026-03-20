package protos

import (
	"fmt"

	"github.com/project-init/devex/internal/sre/config/analyze"
)

func staticAnalysis(config analyze.ProtosConfiguration) error {
	err := validateCfg(config)
	if err != nil {
		return err
	}

	fmt.Printf("Static Analysis Checks:\n")
	fmt.Printf("\tBuild Tool:%s\n", config.BuildTool)
	return nil
}

func validateCfg(cfg analyze.ProtosConfiguration) error {
	switch cfg.BuildTool {
	case analyze.Buf:
		// Do Nothing
	default:
		return fmt.Errorf("unknown build tool: '%s', should be one of %+v", cfg.BuildTool, analyze.AllowableProtoBufBuildTools)
	}

	return nil
}
