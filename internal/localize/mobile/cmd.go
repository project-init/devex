package mobile

import (
	"fmt"

	"github.com/project-init/devex/internal/localize/config"
	"github.com/project-init/devex/internal/localize/mobile/ios"
	"github.com/spf13/cobra"
)

func Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mobile",
		Short: "generate platform-specific mobile localization bundles from gotext catalogs",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, ok := config.GetConfig(cmd.Context())
			if !ok || cfg == nil {
				return fmt.Errorf("config not loaded")
			}

			mobileConfig := cfg.Localize.Mobile
			if mobileConfig.IOS.SourceDir == "" && mobileConfig.IOS.OutputDir == "" {
				return fmt.Errorf("no mobile platforms configured")
			}

			return ios.Generate(
				cfg.Localize.LocalesDir,
				mobileConfig.SourceLanguage,
				mobileConfig.RegistryPath,
				mobileConfig.IOS,
				cmd.OutOrStdout(),
				cmd.ErrOrStderr(),
			)
		},
	}
	return cmd
}
