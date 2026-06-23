package postgres

import (
	"github.com/project-init/devex/internal/sre/postgres/access"
	"github.com/project-init/devex/internal/sre/postgres/migrate"
	"github.com/spf13/cobra"
)

func Command() *cobra.Command {
	postgresCmd := &cobra.Command{
		Use:   "postgres",
		Short: "Postgres operations",
	}
	postgresCmd.AddCommand(access.Command())
	postgresCmd.AddCommand(migrate.Command())
	return postgresCmd
}
