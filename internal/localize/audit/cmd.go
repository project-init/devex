package audit

import (
	"fmt"

	"github.com/project-init/devex/internal/localize/config"
	"github.com/project-init/devex/internal/ui"
	"github.com/spf13/cobra"
)

func Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "audit",
		Short: "audit locale JSON files for empty localized strings.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, ok := config.GetConfig(cmd.Context())
			if !ok || cfg == nil {
				return fmt.Errorf("config not loaded")
			}
			return audit(cfg.Localize.LocalesDir, ui.New(cmd))
		},
	}
	return cmd
}
