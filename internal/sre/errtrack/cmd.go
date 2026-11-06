package errtrack

import (
	"errors"

	"github.com/project-init/devex/internal/sre/config"
	"github.com/spf13/cobra"
)

func Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:         "errtrack <link>",
		Short:       "Pull Datadog error metadata down as JSON from an error link",
		Args:        cobra.ExactArgs(1),
		Annotations: map[string]string{config.OptionalAnnotation: "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			return errors.New("not-implemented")
		},
	}
	return cmd
}
