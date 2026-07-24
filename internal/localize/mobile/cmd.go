package mobile

import (
	"fmt"

	"github.com/project-init/devex/internal/localize/config"
	"github.com/spf13/cobra"
)

func Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mobile",
		Short: "generate mobile localization bundles from gotext catalogs.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, ok := config.GetConfig(cmd.Context())
			if !ok || cfg == nil {
				return fmt.Errorf("config not loaded")
			}

			return generate(
				cfg.Localize.LocalesDir,
				cfg.Localize.Mobile,
				cmd.OutOrStdout(),
				cmd.ErrOrStderr(),
			)
		},
	}
	return cmd
}
