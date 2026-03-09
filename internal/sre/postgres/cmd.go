package postgres

import (
	"github.com/project-init/devex/internal/sre/postgres/access"
	"github.com/spf13/cobra"
)

func Command() *cobra.Command {
	postgresCmd := &cobra.Command{
		Use:   "postgres",
		Short: "Postgres operations",
	}
	postgresCmd.AddCommand(access.Command())
	return postgresCmd
}
