package sre

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

func echoCmd() *cobra.Command {
	var upper bool
	var sep string

	cmd := &cobra.Command{
		Use:   "echo [args...]",
		Short: "Print arguments (optionally transform them)",
		Long: `Print arguments back to stdout.

This is a good template for new tools:
- define flags
- read args
- execute`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := strings.Join(args, sep)
			if upper {
				out = strings.ToUpper(out)
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), out)
			return nil
		},
	}

	cmd.Flags().BoolVar(&upper, "upper", false, "Uppercase the output")
	cmd.Flags().StringVar(&sep, "sep", " ", "Separator between args")

	return cmd
}
