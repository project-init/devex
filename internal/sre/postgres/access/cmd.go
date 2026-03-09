package access

import (
	"fmt"

	"github.com/spf13/cobra"
)

func Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "access",
		Short: "Handle postgres access via psql",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "mycmd %s (%s)\n")
		},
	}
	return cmd
}
