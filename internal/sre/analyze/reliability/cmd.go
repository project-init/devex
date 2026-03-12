package reliability

import (
	"fmt"

	"github.com/project-init/devex/internal/sre/config"
	"github.com/spf13/cobra"
)

func Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "reliability",
		Short: "Handle Reliability analysis",
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
