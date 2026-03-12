package analyze

import (
	"github.com/project-init/devex/internal/sre/analyze/apis"
	"github.com/project-init/devex/internal/sre/analyze/dependencies"
	"github.com/project-init/devex/internal/sre/analyze/ownership"
	"github.com/project-init/devex/internal/sre/analyze/protos"
	"github.com/project-init/devex/internal/sre/analyze/reliability"
	"github.com/project-init/devex/internal/sre/analyze/sql"
	"github.com/spf13/cobra"
)

func Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "analyze",
		Short: "Analysis Operations",
	}
	cmd.AddCommand(apis.Command())
	cmd.AddCommand(dependencies.Command())
	cmd.AddCommand(ownership.Command())
	cmd.AddCommand(protos.Command())
	cmd.AddCommand(reliability.Command())
	cmd.AddCommand(sql.Command())
	return cmd
}
