package translate

import (
	"fmt"

	"github.com/project-init/devex/internal/sre/config"
	"github.com/spf13/cobra"
)

func Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "translate",
		Short: "Ensure that all locales have translations for their given strings.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, ok := config.GetConfig(cmd.Context())
			if !ok || cfg == nil {
				return fmt.Errorf("config not loaded")
			}

			fmt.Printf("%+v\n", cfg)
			return nil
		},
	}
	return cmd
}
