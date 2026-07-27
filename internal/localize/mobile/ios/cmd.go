package ios

import (
	"fmt"

	"github.com/project-init/devex/internal/localize/config"
	"github.com/spf13/cobra"
)

func Command() *cobra.Command {
	return &cobra.Command{
		Use:   "ios",
		Short: "generate iOS localization bundles from gotext catalogs",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, ok := config.GetConfig(cmd.Context())
			if !ok || cfg == nil {
				return fmt.Errorf("config not loaded")
			}

			return generate(
				cfg.Localize.LocalesDir,
				cfg.Localize.Mobile.IOS,
				cmd.OutOrStdout(),
				cmd.ErrOrStderr(),
			)
		},
	}
}
