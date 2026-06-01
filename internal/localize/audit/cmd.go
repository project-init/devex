package audit

import (
	"fmt"

	"github.com/project-init/devex/internal/localize/config"
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

			err := audit(cfg.Localize.LocalesDir)
			if err != nil {
				return err
			}

			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "localize audit passed\n")
			return nil
		},
	}
	return cmd
}
