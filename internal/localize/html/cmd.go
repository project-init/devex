package html

import (
	"fmt"

	"github.com/project-init/devex/internal/localize/config"
	"github.com/spf13/cobra"
)

func Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "html",
		Short: "generate a combined English -> locale translation report as HTML",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, ok := config.GetConfig(cmd.Context())
			if !ok || cfg == nil {
				return fmt.Errorf("config not loaded")
			}

			return Generate(
				cfg.Localize.LocalesDir,
				cfg.Localize.HTML.OutputPath,
				cmd.OutOrStdout(),
				cmd.ErrOrStderr(),
			)
		},
	}
	return cmd
}
