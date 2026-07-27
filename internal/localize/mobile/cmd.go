package mobile

import (
	"github.com/project-init/devex/internal/localize/mobile/ios"
	"github.com/spf13/cobra"
)

func Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mobile <platform>",
		Short: "generate platform-specific mobile localization bundles from gotext catalogs",
		Args:  cobra.NoArgs,
	}
	cmd.AddCommand(ios.Command())
	return cmd
}
