package protos

import (
	"fmt"

	"github.com/project-init/devex/internal/sre/config"
	"github.com/spf13/cobra"
)

func Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "protos",
		Short: "Handle proto analysis",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, ok := config.GetConfig(cmd.Context())
			if !ok || cfg == nil {
				return fmt.Errorf("config not loaded")
			}

			if cfg.Analyze.Protos.Enabled == nil || !*cfg.Analyze.Protos.Enabled {
				fmt.Println("protos analysis disabled")
				return nil
			}

			err := staticAnalysis(cfg.Analyze.Protos)
			if err != nil {
				return err
			}

			switch cfg.Analyze.Depth {
			case config.Diff:
				err = diffAnalysis(cfg.Analyze.Protos)
			case config.Repo:
				err = repoAnalysis(cfg.Analyze.Protos)
			case config.Organization:
				err = organizationAnalysis(cfg.Analyze.Protos)
			default:
				err = fmt.Errorf("unknown analyze depth: %s, must be in %+v", cfg.Analyze.Depth, config.AllowableAnalysisDepths)
			}
			return err
		},
	}
	return cmd
}
